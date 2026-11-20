package news

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// Analyst uses Claude to judge each headline's relevance and likely price
// impact, and to write the plain-English narrative for the report.
type Analyst struct {
	client anthropic.Client
	model  string
	effort anthropic.OutputConfigEffort
}

// NewAnalyst returns nil when no API key is configured; callers then fall
// back to lexicon scoring.
func NewAnalyst(apiKey, model, effort string) *Analyst {
	if apiKey == "" {
		return nil
	}
	if model == "" {
		model = "claude-opus-5-5"
	}
	if effort == "" {
		effort = "high"
	}
	return &Analyst{
		client: anthropic.NewClient(option.WithAPIKey(apiKey), option.WithRequestTimeout(4*time.Minute)),
		model:  model,
		effort: anthropic.OutputConfigEffort(effort),
	}
}

func (a *Analyst) Model() string { return a.model }

// Judgment is the model's assessment of one article.
type Judgment struct {
	ID        string  `json:"id"`
	Relevance float64 `json:"relevance"`
	Direction float64 `json:"direction"`
	Impact    string  `json:"impact"`
	Horizon   string  `json:"horizon"`
	Reasoning string  `json:"reasoning"`
}

// Analysis is the full structured response.
type Analysis struct {
	Articles  []Judgment `json:"articles"`
	Narrative string     `json:"narrative"`
	BullCase  []string   `json:"bull_case"`
	BearCase  []string   `json:"bear_case"`
	Catalysts []string   `json:"upcoming_catalysts"`
	Risks     []string   `json:"key_risks"`
	Model     string     `json:"model"`
}

var analysisSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"articles": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id":        map[string]any{"type": "string", "description": "Article id exactly as given"},
					"relevance": map[string]any{"type": "number", "description": "0 to 1: how much this headline bears on THIS asset's price"},
					"direction": map[string]any{"type": "number", "description": "-1 (strongly bearish) to 1 (strongly bullish) for this asset; 0 if neutral or already priced in"},
					"impact":    map[string]any{"type": "string", "enum": []string{"low", "medium", "high"}},
					"horizon":   map[string]any{"type": "string", "enum": []string{"days", "weeks", "months"}},
					"reasoning": map[string]any{"type": "string", "description": "One or two sentences: the causal chain from this news to this asset's price"},
				},
				"required":             []string{"id", "relevance", "direction", "impact", "horizon", "reasoning"},
				"additionalProperties": false,
			},
		},
		"narrative":          map[string]any{"type": "string"},
		"bull_case":          map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"bear_case":          map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"upcoming_catalysts": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"key_risks":          map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
	},
	"required":             []string{"articles", "narrative", "bull_case", "bear_case", "upcoming_catalysts", "key_risks"},
	"additionalProperties": false,
}

const systemPrompt = `You are the news and narrative analyst inside a quantitative market-forecasting system. A statistical model has already produced probability forecasts from price, volume, cross-asset and macro data; your job is to assess the news flow and explain the overall picture to a trader who will risk their own money on it.

How to judge each article:
- Relevance is about this specific asset. Generic market commentary that barely mentions it is low relevance; company-specific earnings, guidance, regulation, lawsuits, product or partnership news is high. Macro news (rates, inflation, tariffs, geopolitics, oil) matters in proportion to the asset's real sensitivity to it.
- Direction is the expected effect on this asset's price from here. Markets react fast: news more than a day or two old that the price has already reacted to is largely priced in, so pull its direction toward zero. Contradictory or speculative headlines ("could", "might", opinion pieces, listicles) deserve small magnitudes.
- Be calibrated, not dramatic. Most headlines are noise; reserve |direction| above 0.6 and "high" impact for genuinely material, new information.
- Return a judgment for every article listed under TO JUDGE, and only those ids. Articles under ALREADY JUDGED were assessed in earlier runs (their verdicts are shown); do not judge them again, but use them as context for the narrative.
- For an article with relevance below 0.3, return an empty reasoning string; spend your explanation on the articles that matter.

How to write the narrative:
- 2-4 short paragraphs in plain English. Start with the bottom line across horizons, then what the quantitative evidence says, then what the news adds or contradicts, then what would invalidate the view.
- Use only facts present in the data and headlines provided. Cite headlines by their id in square brackets, e.g. [a1b2c3]. Do not invent numbers, events, dates or earnings figures.
- Respect the model's validation status: if a horizon has no demonstrated out-of-sample edge, say plainly that its forecast is close to a coin flip regardless of how the news reads.
- Bull case, bear case, catalysts and risks: concise bullet-style strings grounded in the provided data. For catalysts, only list events implied by the headlines or generic known schedules (e.g. "next earnings report", "next FOMC meeting") without inventing dates.`

// AnalysisRequest is one asset's analysis job.
type AnalysisRequest struct {
	Symbol       string
	Name         string
	QuantSummary string
	New          []Article // headlines to judge
	Known        []Article // headlines judged in earlier runs (context only)
}

func ageOf(x Article) string {
	if x.Published.IsZero() {
		return "unknown age"
	}
	return fmt.Sprintf("%.0fh ago", time.Since(x.Published).Hours())
}

func (a *Analyst) params(r AnalysisRequest) anthropic.MessageNewParams {
	var b strings.Builder
	fmt.Fprintf(&b, "ASSET: %s (%s)\nCURRENT TIME (UTC): %s\n\nQUANTITATIVE MODEL SUMMARY:\n%s\n\nTO JUDGE (newest first):\n", r.Symbol, r.Name, time.Now().UTC().Format(time.RFC1123), r.QuantSummary)
	if len(r.New) == 0 {
		b.WriteString("(none: no new headlines since the last run; return an empty articles array)\n")
	}
	for _, x := range r.New {
		fmt.Fprintf(&b, "- id=%s | %s | %s | scope=%s | %s", x.ID, ageOf(x), x.Source, x.Scope, x.Title)
		if x.Summary != "" {
			fmt.Fprintf(&b, " — %s", x.Summary)
		}
		b.WriteString("\n")
	}
	if len(r.Known) > 0 {
		b.WriteString("\nALREADY JUDGED (context only, do not return these):\n")
		for _, x := range r.Known {
			fmt.Fprintf(&b, "- id=%s | %s | %s | relevance %.1f, direction %+.2f, %s impact\n", x.ID, ageOf(x), x.Title, x.Relevance, x.Direction, x.Impact)
		}
	}
	return anthropic.MessageNewParams{
		Model:     anthropic.Model(a.model),
		MaxTokens: 16000,
		System:    []anthropic.TextBlockParam{{Text: systemPrompt}},
		Messages:  []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock(b.String()))},
		OutputConfig: anthropic.OutputConfigParam{
			Effort: a.effort,
			Format: anthropic.JSONOutputFormatParam{Schema: analysisSchema},
		},
	}
}

// Analyze runs one analysis synchronously (someone is waiting on it).
func (a *Analyst) Analyze(ctx context.Context, r AnalysisRequest) (*Analysis, Usage, error) {
	resp, err := a.client.Messages.New(ctx, a.params(r),
		// Server-side fallback: if a safety classifier declines (rare for
		// financial news, but possible), Anthropic re-serves the request on
		// its recommended fallback model instead of returning a refusal.
		option.WithHeaderAdd("anthropic-beta", "server-side-fallback-2026-07-01"),
		option.WithJSONSet("fallbacks", "default"),
	)
	if err != nil {
		var apiErr *anthropic.Error
		if errors.As(err, &apiErr) {
			return nil, Usage{}, fmt.Errorf("claude api %d: %w", apiErr.StatusCode, err)
		}
		return nil, Usage{}, fmt.Errorf("claude: %w", err)
	}
	u := usageOf(resp, false)
	an, err := parseMessage(resp)
	return an, u, err
}

func parseMessage(resp *anthropic.Message) (*Analysis, error) {
	if resp.StopReason == anthropic.StopReasonRefusal {
		return nil, fmt.Errorf("claude declined: %s", resp.StopDetails.Explanation)
	}
	if resp.StopReason == anthropic.StopReasonMaxTokens {
		return nil, errors.New("claude response truncated (max_tokens)")
	}
	var text string
	for _, blk := range resp.Content {
		if t, ok := blk.AsAny().(anthropic.TextBlock); ok {
			text = t.Text
		}
	}
	var out Analysis
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		return nil, fmt.Errorf("decode analysis: %w", err)
	}
	out.Model = string(resp.Model)
	return &out, nil
}

// ------------------------------------------------------------------ batch

// BatchOutcome is one request's result from a batch.
type BatchOutcome struct {
	Analysis *Analysis
	Usage    Usage
	Err      error
}

// RunBatch submits every request as one Message Batch (50% off all tokens)
// and waits for it. Used for scheduled runs, where nobody is waiting; batches
// usually finish within minutes, and anything not back by `timeout` is
// returned as an error so the caller can fall back. Server-side refusal
// fallbacks are not used in batches: a refused request comes back as an
// error and that asset is scored with the lexicon instead.
func (a *Analyst) RunBatch(ctx context.Context, reqs map[string]AnalysisRequest, poll, timeout time.Duration) (map[string]BatchOutcome, error) {
	keys := make([]string, 0, len(reqs))
	for k := range reqs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	// custom_id must match ^[a-zA-Z0-9_-]{1,64}$; symbols like ^GSPC or GC=F don't.
	var items []anthropic.MessageBatchNewParamsRequest
	for i, k := range keys {
		p := a.params(reqs[k])
		items = append(items, anthropic.MessageBatchNewParamsRequest{
			CustomID: fmt.Sprintf("r%d", i),
			Params: anthropic.MessageBatchNewParamsRequestParams{
				Model: p.Model, MaxTokens: p.MaxTokens, System: p.System, Messages: p.Messages, OutputConfig: p.OutputConfig,
			},
		})
	}
	b, err := a.client.Messages.Batches.New(ctx, anthropic.MessageBatchNewParams{Requests: items})
	if err != nil {
		return nil, fmt.Errorf("submit batch: %w", err)
	}
	deadline := time.Now().Add(timeout)
	for b.ProcessingStatus != anthropic.MessageBatchProcessingStatusEnded {
		if time.Now().After(deadline) {
			_, _ = a.client.Messages.Batches.Cancel(context.Background(), b.ID, anthropic.MessageBatchCancelParams{})
			return nil, fmt.Errorf("batch %s not finished after %s; cancelled", b.ID, timeout)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(poll):
		}
		if nb, err := a.client.Messages.Batches.Get(ctx, b.ID, anthropic.MessageBatchGetParams{}); err == nil {
			b = nb
		}
	}
	out := map[string]BatchOutcome{}
	stream := a.client.Messages.Batches.ResultsStreaming(ctx, b.ID, anthropic.MessageBatchResultsParams{})
	for stream.Next() {
		r := stream.Current()
		var idx int
		if _, err := fmt.Sscanf(r.CustomID, "r%d", &idx); err != nil || idx >= len(keys) {
			continue
		}
		key := keys[idx]
		switch r.Result.Type {
		case "succeeded":
			msg := r.Result.Message
			an, perr := parseMessage(&msg)
			out[key] = BatchOutcome{Analysis: an, Usage: usageOf(&msg, true), Err: perr}
		default:
			out[key] = BatchOutcome{Err: fmt.Errorf("batch request %s", r.Result.Type)}
		}
	}
	if err := stream.Err(); err != nil {
		return out, fmt.Errorf("batch results: %w", err)
	}
	return out, nil
}

// ------------------------------------------------------------------ usage

// Usage is the token count and cost of one Claude call.
type Usage struct {
	Model      string  `json:"model"`
	Input      int64   `json:"input"`
	CacheWrite int64   `json:"cacheWrite"`
	CacheRead  int64   `json:"cacheRead"`
	Output     int64   `json:"output"` // includes thinking tokens
	Batch      bool    `json:"batch"`
	CostUSD    float64 `json:"costUsd"`
}

// rates are USD per million tokens: input, output, 5-minute cache write,
// cache read. From https://platform.claude.com/docs/en/about-claude/pricing
// (fetched 2026-10-05). Update when prices change.
var rates = map[string][4]float64{
	"claude-opus-5-5":   {4, 20, 5, 0.20},
	"claude-sonnet-5-5": {2, 10, 2.5, 0.20},
	"claude-haiku-4-5":  {1, 5, 1.25, 0.10},
	"claude-fable-5-1":  {10, 50, 12.5, 0.25},
}

// UsageOf prices a synchronous response's token usage.
func UsageOf(m *anthropic.Message) Usage { return usageOf(m, false) }

func usageOf(m *anthropic.Message, batch bool) Usage {
	u := Usage{Model: string(m.Model), Input: m.Usage.InputTokens, CacheWrite: m.Usage.CacheCreationInputTokens,
		CacheRead: m.Usage.CacheReadInputTokens, Output: m.Usage.OutputTokens, Batch: batch}
	r, ok := rates[u.Model]
	if !ok {
		for k, v := range rates { // tolerate suffixed ids
			if strings.HasPrefix(u.Model, k) {
				r, ok = v, true
			}
		}
	}
	if ok {
		c := (float64(u.Input)*r[0] + float64(u.Output)*r[1] + float64(u.CacheWrite)*r[2] + float64(u.CacheRead)*r[3]) / 1e6
		if batch {
			c /= 2 // Batch API: 50% off every token, including cache reads and writes
		}
		u.CostUSD = c
	}
	return u
}

// Apply merges LLM judgments into the articles. Values are clamped because
// the schema cannot enforce numeric ranges.
func (an *Analysis) Apply(arts []Article) {
	byID := map[string]Judgment{}
	for _, j := range an.Articles {
		byID[j.ID] = j
	}
	for i := range arts {
		j, ok := byID[arts[i].ID]
		if !ok {
			continue
		}
		arts[i].Relevance = clamp(j.Relevance, 0, 1)
		arts[i].Direction = clamp(j.Direction, -1, 1)
		arts[i].Impact = j.Impact
		arts[i].Horizon = j.Horizon
		arts[i].Reasoning = j.Reasoning
		arts[i].ScoredBy = "llm"
	}
}

func clamp(x, lo, hi float64) float64 {
	if x < lo {
		return lo
	}
	if x > hi {
		return hi
	}
	return x
}
