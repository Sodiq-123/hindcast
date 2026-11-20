// Package news collects headlines about an asset and the macro backdrop,
// scores them, and turns them into a bounded adjustment to the quantitative
// forecast.
package news

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Article is one headline with every score attached to it.
type Article struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Source    string    `json:"source"`
	URL       string    `json:"url"`
	Published time.Time `json:"published"`
	Summary   string    `json:"summary,omitempty"`
	Scope     string    `json:"scope"` // "asset" | "market"
	Query     string    `json:"query"`

	// Lexicon scoring (always present).
	LexScore float64  `json:"lexScore"` // -1..1
	LexHits  []string `json:"lexHits,omitempty"`

	// LLM analysis (present when an API key is configured).
	Relevance float64 `json:"relevance"` // 0..1
	Direction float64 `json:"direction"` // -1..1 expected price impact
	Impact    string  `json:"impact"`    // low | medium | high
	Horizon   string  `json:"horizon"`   // days | weeks | months
	Reasoning string  `json:"reasoning,omitempty"`
	ScoredBy  string  `json:"scoredBy"` // "llm" | "lexicon"
}

// Collector fetches RSS feeds.
type Collector struct {
	http *http.Client
}

func NewCollector() *Collector {
	return &Collector{http: &http.Client{Timeout: 20 * time.Second}}
}

type rss struct {
	Channel struct {
		Items []struct {
			Title       string `xml:"title"`
			Link        string `xml:"link"`
			PubDate     string `xml:"pubDate"`
			Description string `xml:"description"`
			Source      struct {
				Name string `xml:",chardata"`
			} `xml:"source"`
		} `xml:"item"`
	} `xml:"channel"`
}

func (c *Collector) fetchRSS(ctx context.Context, u, scope, query string, limit int) ([]Article, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0 Safari/537.36")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("rss %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	var feed rss
	if err := xml.Unmarshal(body, &feed); err != nil {
		return nil, err
	}
	var out []Article
	for _, it := range feed.Channel.Items {
		title := html.UnescapeString(strings.TrimSpace(it.Title))
		src := strings.TrimSpace(it.Source.Name)
		// Google News appends " - Source" to titles.
		if src != "" && strings.HasSuffix(title, " - "+src) {
			title = strings.TrimSuffix(title, " - "+src)
		}
		pub, _ := parseDate(it.PubDate)
		a := Article{
			Title: title, Source: src, URL: it.Link, Published: pub,
			Summary: truncate(stripHTML(it.Description), 300), Scope: scope, Query: query,
		}
		if a.Summary == title || strings.HasPrefix(a.Summary, title) {
			a.Summary = ""
		}
		a.ID = articleID(title)
		out = append(out, a)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func googleNewsURL(q string) string {
	return "https://news.google.com/rss/search?hl=en-US&gl=US&ceid=US:en&q=" + url.QueryEscape(q)
}

// Request describes what to collect.
type Request struct {
	Symbol    string
	Name      string
	AssetType string // equity | crypto | etf | ...
	Country   string // rough region hint from exchange
	MaxAge    time.Duration
}

// Collect gathers asset-specific and market-wide headlines in parallel,
// de-duplicates them and sorts newest first.
func (c *Collector) Collect(ctx context.Context, r Request) []Article {
	if r.MaxAge == 0 {
		r.MaxAge = 7 * 24 * time.Hour
	}
	name := cleanName(r.Name)
	base := strings.SplitN(r.Symbol, ".", 2)[0]
	base = strings.TrimSuffix(base, "-USD")
	type q struct {
		url, scope, query string
		limit             int
	}
	var qs []q
	if name != "" && !strings.EqualFold(name, base) {
		qs = append(qs, q{googleNewsURL(fmt.Sprintf("%q when:7d", name)), "asset", name, 25})
	}
	switch r.AssetType {
	case "crypto":
		qs = append(qs,
			q{googleNewsURL(base + " crypto when:3d"), "asset", base + " crypto", 20},
			q{googleNewsURL("crypto market OR bitcoin price when:2d"), "market", "crypto market", 12},
			q{googleNewsURL("crypto regulation OR SEC crypto OR stablecoin when:5d"), "market", "crypto regulation", 8},
		)
	default:
		qs = append(qs,
			q{googleNewsURL(base + " stock when:7d"), "asset", base + " stock", 20},
			q{"https://feeds.finance.yahoo.com/rss/2.0/headline?region=US&lang=en-US&s=" + url.QueryEscape(r.Symbol), "asset", "yahoo:" + r.Symbol, 20},
			q{googleNewsURL("stock market today when:2d"), "market", "stock market", 12},
		)
	}
	qs = append(qs,
		q{googleNewsURL("Federal Reserve OR inflation OR interest rates when:3d"), "market", "rates & inflation", 8},
		q{googleNewsURL("geopolitical OR tariffs OR sanctions markets when:3d"), "market", "geopolitics", 8},
		q{googleNewsURL("oil prices OR recession OR jobs report when:3d"), "market", "macro data", 6},
	)

	var mu sync.Mutex
	var all []Article
	var wg sync.WaitGroup
	for _, x := range qs {
		wg.Add(1)
		go func(x q) {
			defer wg.Done()
			arts, err := c.fetchRSS(ctx, x.url, x.scope, x.query, x.limit)
			if err != nil {
				return
			}
			mu.Lock()
			all = append(all, arts...)
			mu.Unlock()
		}(x)
	}
	wg.Wait()

	cutoff := time.Now().Add(-r.MaxAge)
	seen := map[string]bool{}
	var out []Article
	sort.SliceStable(all, func(i, j int) bool { return all[i].Scope == "asset" && all[j].Scope != "asset" })
	for _, a := range all {
		if a.Title == "" || seen[a.ID] || (!a.Published.IsZero() && a.Published.Before(cutoff)) {
			continue
		}
		seen[a.ID] = true
		out = append(out, a)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Published.After(out[j].Published) })
	if len(out) > 60 {
		out = out[:60]
	}
	return out
}

var tagRe = regexp.MustCompile(`<[^>]*>`)
var spaceRe = regexp.MustCompile(`\s+`)

func stripHTML(s string) string {
	s = html.UnescapeString(tagRe.ReplaceAllString(s, " "))
	return strings.TrimSpace(spaceRe.ReplaceAllString(s, " "))
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

var nonAlnumRe = regexp.MustCompile(`[^a-zA-Z0-9 ]`)

func articleID(title string) string {
	norm := strings.ToLower(spaceRe.ReplaceAllString(nonAlnumRe.ReplaceAllString(title, ""), " "))
	sum := sha1.Sum([]byte(strings.TrimSpace(norm)))
	return hex.EncodeToString(sum[:])[:10]
}

func parseDate(s string) (time.Time, error) {
	for _, layout := range []string{time.RFC1123Z, time.RFC1123, "Mon, 2 Jan 2006 15:04:05 -0700", "Mon, 2 Jan 2006 15:04:05 MST"} {
		if t, err := time.Parse(layout, strings.TrimSpace(s)); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("bad date %q", s)
}

var corpSuffix = regexp.MustCompile(`(?i)[,.]?\s+(public limited company|inc|incorporated|corp|corporation|co|company|plc|ltd|limited|holdings|group|sa|ag|nv|se|asa|ab|oyj|spa|s\.p\.a|class [a-z])\.?$`)

func cleanName(n string) string {
	n = strings.TrimSpace(n)
	for i := 0; i < 3; i++ {
		n = strings.TrimSpace(corpSuffix.ReplaceAllString(n, ""))
	}
	return n
}

// Memory persists Claude's per-headline judgments (so a headline is judged
// once, not on every run) and the cost of every call. The store implements it.
type Memory interface {
	LoadJudgments(ctx context.Context, symbol string) (map[string]Judgment, error)
	SaveJudgments(ctx context.Context, symbol, model string, js []Judgment) error
	RecordUsage(ctx context.Context, symbol string, u Usage) error
}
