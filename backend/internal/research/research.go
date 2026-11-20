// Package research builds an on-demand due-diligence report on a coin, token
// or stock. It gathers verified data first (our own reports, CoinGecko's
// project profile, GitHub activity), then has Claude search and read the web
// (website, docs, whitepaper, tokenomics, team, audits, Reddit, YouTube,
// news) and write a sourced report. Each report is saved with the price at
// the time, and the next one is told what the last one found so it can say
// what changed. It runs only when asked, under a monthly budget.
package research

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/sodiq/financial-market/backend/internal/engine"
	"github.com/sodiq/financial-market/backend/internal/news"
	"github.com/sodiq/financial-market/backend/internal/token"
)

// Store persists reports and Claude usage (implemented by store.Store).
type Store interface {
	SaveResearch(ctx context.Context, r *Report) error
	ResearchHistory(ctx context.Context, subject string, limit int) ([]*Report, error)
	ResearchSpend(ctx context.Context, since time.Time) (float64, error)
	RecordUsage(ctx context.Context, symbol string, u news.Usage) error
}

// Source is one web page or data feed the report relies on.
type Source struct {
	ID        int    `json:"id"`
	URL       string `json:"url"`
	Title     string `json:"title,omitempty"`
	Retrieved bool   `json:"retrieved"` // seen in Claude's search or fetch results (not only cited)
}

type Finding struct {
	Text    string `json:"text"`
	Tone    string `json:"tone"` // positive | negative | neutral
	Sources []int  `json:"sources"`
}

type Section struct {
	Key      string    `json:"key"`
	Title    string    `json:"title"`
	Summary  string    `json:"summary"`
	Findings []Finding `json:"findings"`
	Gaps     []string  `json:"gaps"` // what couldn't be found or verified
}

type Cost struct {
	Model    string  `json:"model"`
	Searches int64   `json:"searches"`
	Fetches  int64   `json:"fetches"`
	Input    int64   `json:"input"`
	Output   int64   `json:"output"`
	USD      float64 `json:"usd"`
	Seconds  int     `json:"seconds"`
}

// Report is one saved research run.
type Report struct {
	ID        int64     `json:"id"`
	Subject   string    `json:"subject"` // ticker or contract address
	Kind      string    `json:"kind"`    // stock | crypto | token
	Name      string    `json:"name"`
	Symbol    string    `json:"symbol"`
	CreatedAt time.Time `json:"createdAt"`
	Price     float64   `json:"price"` // price when the report was made

	Headline           string    `json:"headline,omitempty"` // the one-sentence answer (reports from 19 Nov 2026 on)
	Overview           string    `json:"overview"`
	Fundamentals       string    `json:"fundamentals"` // strong | mixed | weak | insufficient
	FundamentalsReason string    `json:"fundamentalsReason"`
	Sections           []Section `json:"sections"`
	BullCase           []Finding `json:"bullCase"`
	BearCase           []Finding `json:"bearCase"`
	WouldChangeView    []string  `json:"wouldChangeView"`
	HypeCheck          string    `json:"hypeCheck"`
	Changes            []Finding `json:"changes"` // since the previous report
	PreviousID         int64     `json:"previousId,omitempty"`
	PreviousAt         string    `json:"previousAt,omitempty"`
	Sources            []Source  `json:"sources"`
	Facts              string    `json:"facts"` // the verified data given to Claude
	Notes              string    `json:"notes"` // Claude's raw research notes
	Cost               Cost      `json:"cost"`
}

// Job is a research run in progress.
type Job struct {
	Subject  string    `json:"subject"`
	Started  time.Time `json:"started"`
	Stage    string    `json:"stage"`
	Running  bool      `json:"running"`
	Err      string    `json:"error,omitempty"`
	ReportID int64     `json:"reportId,omitempty"`
}

type Service struct {
	Store        Store
	Tokens       *token.Service
	Assets       func(ctx context.Context, symbol string) (*engine.Report, error) // latest saved analysis
	CoinGeckoKey string
	Budget       float64 // USD per calendar month
	Log          *slog.Logger

	client anthropic.Client
	model  string
	limits limits
	http   *http.Client
	mu     sync.Mutex
	jobs   map[string]*Job
}

// limits cap one research run (tests use tiny ones).
type limits struct{ searches, fetches, tokens int64 }

// EstimateUSD is the typical cost of one report, used for the budget check.
const EstimateUSD = 2.5

// New returns nil when no Anthropic key is configured.
func New(apiKey, model string) *Service {
	if apiKey == "" {
		return nil
	}
	if model == "" {
		model = "claude-opus-5-5"
	}
	return &Service{
		client: anthropic.NewClient(option.WithAPIKey(apiKey), option.WithRequestTimeout(15*time.Minute)),
		model:  model, http: &http.Client{Timeout: 25 * time.Second}, jobs: map[string]*Job{},
		limits: limits{searches: 15, fetches: 12, tokens: 40000},
	}
}

func (s *Service) Model() string { return s.model }

// MonthSpend is this calendar month's research spend.
func (s *Service) MonthSpend(ctx context.Context) float64 {
	t := time.Now().UTC()
	v, _ := s.Store.ResearchSpend(ctx, time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC))
	return v
}

// Job returns the current or last job for a subject.
func (s *Service) Job(subject string) *Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	if j := s.jobs[subject]; j != nil {
		c := *j
		return &c
	}
	return nil
}

func (s *Service) stage(subject, stage string) {
	s.mu.Lock()
	if j := s.jobs[subject]; j != nil {
		j.Stage = stage
	}
	s.mu.Unlock()
}

// Start begins a research run in the background.
func (s *Service) Start(ctx context.Context, subject string) (*Job, error) {
	subject = normalise(subject)
	if subject == "" {
		return nil, errors.New("nothing to research")
	}
	if spent := s.MonthSpend(ctx); spent+EstimateUSD > s.Budget {
		return nil, fmt.Errorf("monthly research budget reached: $%.2f of $%.2f spent this month (a report costs about $%.2f). Raise RESEARCH_BUDGET_USD in .env to allow more", spent, s.Budget, EstimateUSD)
	}
	s.mu.Lock()
	if j := s.jobs[subject]; j != nil && j.Running {
		s.mu.Unlock()
		return nil, errors.New("research on this is already running")
	}
	j := &Job{Subject: subject, Started: time.Now().UTC(), Stage: "gathering verified data", Running: true}
	s.jobs[subject] = j
	s.mu.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		rep, err := s.run(ctx, subject)
		s.mu.Lock()
		defer s.mu.Unlock()
		j.Running = false
		if err != nil {
			j.Err = err.Error()
			s.Log.Warn("research failed", "subject", subject, "err", err)
			return
		}
		j.ReportID, j.Stage = rep.ID, "done"
	}()
	c := *j
	return &c, nil
}

func normalise(subject string) string {
	subject = strings.TrimSpace(subject)
	if IsCoinID(subject) {
		return strings.ToLower(subject)
	}
	if token.LooksLikeAddress(subject) {
		return subject
	}
	return strings.ToUpper(subject)
}

// ------------------------------------------------------------------ the run

func (s *Service) run(ctx context.Context, subject string) (*Report, error) {
	t0 := time.Now()
	rep := &Report{Subject: subject, CreatedAt: time.Now().UTC()}
	facts, err := s.gather(ctx, rep)
	if err != nil {
		return nil, err
	}
	rep.Facts = facts
	var prev *Report
	if hist, err := s.Store.ResearchHistory(ctx, subject, 1); err == nil && len(hist) > 0 {
		prev = hist[0]
		rep.PreviousID, rep.PreviousAt = prev.ID, prev.CreatedAt.Format("2006-01-02")
	}

	s.stage(subject, "searching and reading the web (usually 2–6 minutes)")
	notes, sources, cost, err := s.investigate(ctx, rep, prev)
	s.record(ctx, subject, cost)
	rep.Cost = cost
	if err != nil {
		return nil, err
	}
	rep.Notes = notes

	s.stage(subject, "writing the report")
	if err := s.structure(ctx, rep, prev, sources); err != nil {
		return nil, err
	}
	rep.Cost.Seconds = int(time.Since(t0).Seconds())
	if err := s.Store.SaveResearch(ctx, rep); err != nil {
		return nil, err
	}
	return rep, nil
}

func (s *Service) record(ctx context.Context, subject string, c Cost) {
	if s.Store == nil || (c.Input == 0 && c.Output == 0) {
		return
	}
	u := news.Usage{Model: c.Model, Input: c.Input, Output: c.Output, CostUSD: c.USD}
	if err := s.Store.RecordUsage(ctx, "RESEARCH:"+subject, u); err != nil {
		s.Log.Warn("record research usage", "err", err)
	}
}

const researchSystem = `You are a meticulous, sceptical investment research analyst doing due diligence for a beginner investor.
Your job is to find out what is TRUE about this project or company, from the best sources available, and to separate evidence from marketing and hype.

How to work:
- Use web_search and web_fetch. Prefer primary sources: the official website, documentation, whitepaper, token or investor pages, GitHub, audit reports, regulatory filings, official blog and announcements. Then reputable news, then community sources (Reddit, forums, YouTube, X/Twitter posts that search can see).
- Check claims against evidence. Promised features vs what has actually shipped; claimed partners vs whether the partner confirms it; "audited" vs an actual audit report; team names vs evidence they are real.
- Look actively for the negative: hacks, exploits, lawsuits, regulatory action, insider selling, large token unlocks, broken promises, paid promotion, anonymous teams, fake volume, delistings.
- For crypto: total and circulating supply, who received tokens at launch (team, investors, airdrop), vesting and unlock schedule, and what the token is actually used for.
- For companies: what they sell and to whom, revenue and profit trend, debt, management, recent guidance versus results, and pending legal or regulatory issues.
- Note how excited people are versus how much real use or revenue there is (hype check).
- After EVERY factual claim put the source URL in square brackets, e.g. "The supply is capped at 21M [https://example.com/page]". Never invent a URL; only cite pages you actually found. If you could not find or verify something, say so plainly.
- Do not give buy or sell advice and do not predict the price. Describe what is true, what is uncertain and what to watch.
Write thorough plain-English research notes organised under the requested headings.`

func (s *Service) investigate(ctx context.Context, rep *Report, prev *Report) (string, map[string]Source, Cost, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "SUBJECT: %s (%s)\nTYPE: %s\nTODAY: %s\n\n", rep.Name, rep.Symbol, kindLabel(rep.Kind), time.Now().UTC().Format("2 January 2006"))
	fmt.Fprintf(&b, "VERIFIED DATA (from APIs; trust these numbers, use them to check claims):\n%s\n\n", rep.Facts)
	if prev != nil {
		fmt.Fprintf(&b, "PREVIOUS RESEARCH (%s, price then %.6g). Check what has changed since:\n%s\n\n", prev.CreatedAt.Format("2 Jan 2006"), prev.Price, previousSummary(prev))
	}
	b.WriteString("Research and write notes under these headings:\n")
	for _, h := range headings(rep.Kind) {
		fmt.Fprintf(&b, "- %s\n", h[1])
	}
	b.WriteString("- Hype check\n- What would change the view\n")
	if prev != nil {
		b.WriteString("- What changed since the previous research\n")
	}

	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(s.model),
		MaxTokens: s.limits.tokens,
		// Server-side web tools run several model steps per request; caching
		// lets each step re-read earlier pages at 5% of the input price
		// instead of paying full price again.
		System:       []anthropic.TextBlockParam{{Text: researchSystem, CacheControl: anthropic.NewCacheControlEphemeralParam()}},
		CacheControl: anthropic.NewCacheControlEphemeralParam(),
		Messages:     []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock(b.String()))},
		Tools: []anthropic.ToolUnionParam{
			{OfWebSearchTool20260209: &anthropic.WebSearchTool20260209Param{MaxUses: anthropic.Int(s.limits.searches)}},
			{OfWebFetchTool20260209: &anthropic.WebFetchTool20260209Param{MaxUses: anthropic.Int(s.limits.fetches), MaxContentTokens: anthropic.Int(20000)}},
		},
		OutputConfig: anthropic.OutputConfigParam{Effort: anthropic.OutputConfigEffortHigh},
	}
	cost := Cost{Model: s.model}
	sources := map[string]Source{}
	var notes strings.Builder
	// The server pauses long tool loops ("pause_turn"); resend to resume.
	for round := 0; round < 6; round++ {
		msg, err := s.stream(ctx, params)
		if msg != nil {
			addUsage(&cost, msg)
		}
		if err != nil {
			return "", nil, cost, err
		}
		collect(msg, sources, &notes)
		if s.Log != nil {
			s.Log.Info("research round", "subject", rep.Subject, "round", round, "stop", msg.StopReason, "input", msg.Usage.InputTokens,
				"cacheRead", msg.Usage.CacheReadInputTokens, "cacheWrite", msg.Usage.CacheCreationInputTokens, "output", msg.Usage.OutputTokens,
				"searches", msg.Usage.ServerToolUse.WebSearchRequests, "fetches", msg.Usage.ServerToolUse.WebFetchRequests, "costSoFar", fmt.Sprintf("%.3f", cost.USD))
		}
		switch msg.StopReason {
		case anthropic.StopReasonPauseTurn:
			params.Messages = append(params.Messages, msg.ToParam())
			continue
		case anthropic.StopReasonRefusal:
			return "", nil, cost, fmt.Errorf("Claude declined to research this: %s", msg.StopDetails.Explanation)
		case anthropic.StopReasonMaxTokens:
			// Keep what was written; the notes are long enough to use.
		}
		break
	}
	if strings.TrimSpace(notes.String()) == "" {
		return "", nil, cost, errors.New("the research step returned no notes")
	}
	return notes.String(), sources, cost, nil
}

func (s *Service) stream(ctx context.Context, p anthropic.MessageNewParams) (*anthropic.Message, error) {
	st := s.client.Messages.NewStreaming(ctx, p)
	msg := anthropic.Message{}
	for st.Next() {
		if err := msg.Accumulate(st.Current()); err != nil {
			return &msg, err
		}
	}
	if err := st.Err(); err != nil {
		var apiErr *anthropic.Error
		if errors.As(err, &apiErr) {
			return &msg, fmt.Errorf("claude api %d: %w", apiErr.StatusCode, err)
		}
		return &msg, fmt.Errorf("claude: %w", err)
	}
	return &msg, nil
}

func addUsage(c *Cost, m *anthropic.Message) {
	u := news.UsageOf(m)
	c.Input += u.Input + u.CacheRead + u.CacheWrite
	c.Output += u.Output
	c.Searches += m.Usage.ServerToolUse.WebSearchRequests
	c.Fetches += m.Usage.ServerToolUse.WebFetchRequests
	// Web search is billed at $10 per 1,000 searches on top of tokens;
	// fetched pages are billed only as input tokens.
	c.USD += u.CostUSD + 0.01*float64(m.Usage.ServerToolUse.WebSearchRequests)
}

// collect appends the text and records every page seen in search and fetch
// results, so sources can be marked as actually retrieved.
func collect(m *anthropic.Message, sources map[string]Source, notes *strings.Builder) {
	for _, blk := range m.Content {
		switch v := blk.AsAny().(type) {
		case anthropic.TextBlock:
			notes.WriteString(v.Text)
			for _, c := range v.Citations {
				if w, ok := c.AsAny().(anthropic.CitationsWebSearchResultLocation); ok && w.URL != "" {
					addSource(sources, w.URL, w.Title, true)
				}
			}
		case anthropic.WebSearchToolResultBlock:
			for _, r := range v.Content.AsWebSearchResultBlockArray() {
				addSource(sources, r.URL, r.Title, true)
			}
		case anthropic.WebFetchToolResultBlock:
			if f := v.Content.AsResponseWebFetchResultBlock(); f.URL != "" {
				addSource(sources, f.URL, f.Content.Title, true)
			}
		}
	}
	notes.WriteString("\n")
}

func addSource(m map[string]Source, url, title string, retrieved bool) {
	url = strings.TrimRight(strings.TrimSpace(url), ".,);]")
	if url == "" {
		return
	}
	cur, ok := m[url]
	if !ok {
		m[url] = Source{URL: url, Title: title, Retrieved: retrieved}
		return
	}
	if cur.Title == "" {
		cur.Title = title
	}
	cur.Retrieved = cur.Retrieved || retrieved
	m[url] = cur
}

var urlRe = regexp.MustCompile(`https?://[^\s\]\)"'<>]+`)

// structure turns the notes into the report. Structured output can't be
// combined with citations, so this second call has no tools: it only
// reorganises the notes, citing sources by number from a fixed list.
func (s *Service) structure(ctx context.Context, rep *Report, prev *Report, seen map[string]Source) error {
	for _, u := range urlRe.FindAllString(rep.Notes, -1) {
		addSource(seen, u, "", false)
	}
	urls := make([]string, 0, len(seen))
	for u := range seen {
		urls = append(urls, u)
	}
	sort.Strings(urls)
	// Source 0 is the platform's own verified data.
	rep.Sources = []Source{{ID: 0, URL: "", Title: "Verified data from this platform (CoinGecko, GitHub, on-chain checks, price history)", Retrieved: true}}
	var list strings.Builder
	list.WriteString("[0] Verified data block (platform APIs)\n")
	cited := map[string]bool{}
	for _, u := range urlRe.FindAllString(rep.Notes, -1) {
		cited[strings.TrimRight(u, ".,);]")] = true
	}
	for _, u := range urls {
		if !cited[u] {
			continue // only list pages the notes actually rely on
		}
		src := seen[u]
		src.ID = len(rep.Sources)
		rep.Sources = append(rep.Sources, src)
		fmt.Fprintf(&list, "[%d] %s %s\n", src.ID, u, src.Title)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "SUBJECT: %s (%s), %s\n\nVERIFIED DATA:\n%s\n\nRESEARCH NOTES:\n%s\n\nSOURCES (cite by number):\n%s\n",
		rep.Name, rep.Symbol, kindLabel(rep.Kind), rep.Facts, rep.Notes, list.String())
	if prev != nil {
		fmt.Fprintf(&b, "\nPREVIOUS RESEARCH (%s):\n%s\n", prev.CreatedAt.Format("2 Jan 2006"), previousSummary(prev))
	}
	b.WriteString("\nSection keys to use, in this order: ")
	for i, h := range headings(rep.Kind) {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%s (%s)", h[0], h[1])
	}

	p := anthropic.MessageNewParams{
		Model:     anthropic.Model(s.model),
		MaxTokens: 16000,
		System: []anthropic.TextBlockParam{{Text: `Turn research notes into a structured due-diligence report for a beginner investor.
Rules: use ONLY facts in the notes or the verified data; do not add knowledge of your own. Every finding cites the numbers of the sources it comes from (use 0 for the verified data block); a finding with no source must say it is unverified. Write in plain English and explain any jargon in a few words. Tone: positive = good for the project, negative = a risk or problem, neutral = context.
"headline" is the short answer: one plain sentence of at most 16 words that sums up the project or business and its biggest strength and problem, for example "A real, working privacy coin, but 2026 has been rough behind the scenes." It must not mention the price or what to do.
"fundamentals" rates the evidence about the project or business itself (not the price): strong, mixed, weak, or insufficient when too little could be verified. "changes" lists what is different since the previous research (empty if there is none). Never recommend buying or selling and never predict the price.`}},
		Messages: []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock(b.String()))},
		OutputConfig: anthropic.OutputConfigParam{
			Effort: anthropic.OutputConfigEffortMedium,
			Format: anthropic.JSONOutputFormatParam{Schema: reportSchema(rep.Kind)},
		},
	}
	msg, err := s.stream(ctx, p)
	if msg != nil {
		addUsage(&rep.Cost, msg)
		var c2 Cost
		addUsage(&c2, msg)
		c2.Model = s.model
		s.record(ctx, rep.Subject, c2)
	}
	if err != nil {
		return err
	}
	if msg.StopReason == anthropic.StopReasonRefusal {
		return fmt.Errorf("Claude declined to write the report: %s", msg.StopDetails.Explanation)
	}
	if msg.StopReason == anthropic.StopReasonMaxTokens {
		return errors.New("the report was cut off (max_tokens)")
	}
	var text string
	for _, blk := range msg.Content {
		if t, ok := blk.AsAny().(anthropic.TextBlock); ok {
			text += t.Text
		}
	}
	var out struct {
		Headline           string    `json:"headline"`
		Overview           string    `json:"overview"`
		Fundamentals       string    `json:"fundamentals"`
		FundamentalsReason string    `json:"fundamentals_reason"`
		Sections           []Section `json:"sections"`
		BullCase           []Finding `json:"bull_case"`
		BearCase           []Finding `json:"bear_case"`
		WouldChangeView    []string  `json:"would_change_view"`
		HypeCheck          string    `json:"hype_check"`
		Changes            []Finding `json:"changes"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		return fmt.Errorf("decode report: %w", err)
	}
	rep.Headline, rep.Overview, rep.Fundamentals, rep.FundamentalsReason = strings.TrimSpace(out.Headline), out.Overview, out.Fundamentals, out.FundamentalsReason
	rep.Sections, rep.BullCase, rep.BearCase = out.Sections, out.BullCase, out.BearCase
	rep.WouldChangeView, rep.HypeCheck, rep.Changes = out.WouldChangeView, out.HypeCheck, out.Changes
	titles := map[string]string{}
	for _, h := range headings(rep.Kind) {
		titles[h[0]] = h[1]
	}
	valid := len(rep.Sources)
	clean := func(fs []Finding) []Finding {
		for i := range fs {
			var ok []int
			for _, id := range fs[i].Sources {
				if id >= 0 && id < valid {
					ok = append(ok, id)
				}
			}
			fs[i].Sources = ok
		}
		return fs
	}
	for i := range rep.Sections {
		if t, ok := titles[rep.Sections[i].Key]; ok {
			rep.Sections[i].Title = t
		}
		rep.Sections[i].Findings = clean(rep.Sections[i].Findings)
	}
	rep.BullCase, rep.BearCase, rep.Changes = clean(rep.BullCase), clean(rep.BearCase), clean(rep.Changes)
	return nil
}

func kindLabel(kind string) string {
	switch kind {
	case "token":
		return "crypto token traded on decentralised exchanges"
	case "crypto":
		return "cryptocurrency"
	}
	return "listed company stock or fund"
}

// headings are the report sections, by subject type: key and title.
func headings(kind string) [][2]string {
	if kind == "stock" {
		return [][2]string{
			{"business", "What the company does"},
			{"financials", "Revenue, profit and debt"},
			{"management", "Management and ownership"},
			{"delivery", "Promises vs delivery (guidance and results)"},
			{"sentiment", "News and what people are saying"},
			{"red_flags", "Red flags (lawsuits, investigations, accounting, dilution)"},
		}
	}
	return [][2]string{
		{"what_it_is", "What it is, in plain English"},
		{"team", "Team, backers and partners"},
		{"token", "The token: supply, who holds it, unlocks"},
		{"usage", "Real use and development activity"},
		{"delivery", "Roadmap: promises vs delivery"},
		{"sentiment", "What people are saying (Reddit, YouTube, news)"},
		{"red_flags", "Red flags (hacks, lawsuits, insiders, paid promotion)"},
	}
}

func previousSummary(p *Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Overview: %s\nFundamentals: %s (%s)\n", p.Overview, p.Fundamentals, p.FundamentalsReason)
	for _, s := range p.Sections {
		fmt.Fprintf(&b, "%s: %s\n", s.Title, s.Summary)
		for _, f := range s.Findings {
			if f.Tone == "negative" {
				fmt.Fprintf(&b, "  - risk: %s\n", f.Text)
			}
		}
	}
	return b.String()
}

func reportSchema(kind string) map[string]any {
	var keys []any
	for _, h := range headings(kind) {
		keys = append(keys, h[0])
	}
	finding := map[string]any{
		"type": "object", "additionalProperties": false,
		"required": []any{"text", "tone", "sources"},
		"properties": map[string]any{
			"text":    map[string]any{"type": "string"},
			"tone":    map[string]any{"type": "string", "enum": []any{"positive", "negative", "neutral"}},
			"sources": map[string]any{"type": "array", "items": map[string]any{"type": "integer"}},
		},
	}
	findings := map[string]any{"type": "array", "items": finding}
	strs := map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
	return map[string]any{
		"type": "object", "additionalProperties": false,
		"required": []any{"headline", "overview", "fundamentals", "fundamentals_reason", "sections", "bull_case", "bear_case", "would_change_view", "hype_check", "changes"},
		"properties": map[string]any{
			"headline":            map[string]any{"type": "string"},
			"overview":            map[string]any{"type": "string"},
			"fundamentals":        map[string]any{"type": "string", "enum": []any{"strong", "mixed", "weak", "insufficient"}},
			"fundamentals_reason": map[string]any{"type": "string"},
			"sections": map[string]any{"type": "array", "items": map[string]any{
				"type": "object", "additionalProperties": false,
				"required": []any{"key", "title", "summary", "findings", "gaps"},
				"properties": map[string]any{
					"key":      map[string]any{"type": "string", "enum": keys},
					"title":    map[string]any{"type": "string"},
					"summary":  map[string]any{"type": "string"},
					"findings": findings,
					"gaps":     strs,
				},
			}},
			"bull_case":         findings,
			"bear_case":         findings,
			"would_change_view": strs,
			"hype_check":        map[string]any{"type": "string"},
			"changes":           findings,
		},
	}
}

var lookalikeRe = regexp.MustCompile(`Identity WARNING: not the CoinGecko-listed ([^;]+);`)

// IdentityWarning is a short warning when a researched token isn't the coin
// its name suggests, read from the verified data the report was given:
// "Not the listed ZEC" or "Not listed on CoinGecko". Empty when there's
// nothing to warn about (or for stocks and coins).
func (r *Report) IdentityWarning() string {
	if r.Kind != "token" {
		return ""
	}
	if m := lookalikeRe.FindStringSubmatch(r.Facts); m != nil {
		return "Not the listed " + strings.TrimSpace(m[1])
	}
	if !strings.Contains(r.Facts, "Identity: CoinGecko lists this exact address") {
		return "Not listed on CoinGecko"
	}
	return ""
}

// IsCoinID reports whether a subject names a coin by its CoinGecko id
// ("cg:quantus"), for coins on blockchains the token checks don't cover.
func IsCoinID(subject string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(subject)), "cg:") && len(strings.TrimSpace(subject)) > 3
}

// CoinPrice is a CoinGecko coin's current USD price.
func (s *Service) CoinPrice(ctx context.Context, id string) (float64, error) {
	var r map[string]map[string]float64
	if _, err := s.getJSON(ctx, "https://api.coingecko.com/api/v3/simple/price?vs_currencies=usd&ids="+id, &r, s.cgHeader()); err != nil {
		return 0, err
	}
	return r[id]["usd"], nil
}
