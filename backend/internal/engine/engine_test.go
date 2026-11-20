package engine

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"math"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	"github.com/sodiq/financial-market/backend/internal/market"
	"github.com/sodiq/financial-market/backend/internal/model"
	"github.com/sodiq/financial-market/backend/internal/news"
)

type fakeProvider struct{ series map[string]*market.Series }

func (f fakeProvider) Name() string { return "fake" }
func (f fakeProvider) Fetch(_ context.Context, sym string) (*market.Series, error) {
	if s, ok := f.series[sym]; ok {
		return s, nil
	}
	return nil, market.ErrNotFound
}

func gen(sym string, n int, drift, vol float64, seed uint64, class market.AssetClass) *market.Series {
	rng := rand.New(rand.NewPCG(seed, 99))
	s := &market.Series{Meta: market.Meta{Symbol: sym, Name: sym + " Inc", Class: class, Currency: "USD", Provider: "fake", GMTOffset: -14400}, FetchedAt: time.Now()}
	p := 100.0
	d := time.Now().AddDate(0, 0, -n*7/5)
	for i := 0; i < n; i++ {
		o := p
		p *= math.Exp(drift + vol*rng.NormFloat64())
		s.Bars = append(s.Bars, market.Bar{Date: d.Format("2006-01-02"), Open: o, High: math.Max(o, p) * 1.003, Low: math.Min(o, p) * 0.997, Close: p, Volume: 1e6 + 1e5*rng.Float64()})
		d = d.AddDate(0, 0, 1)
		for d.Weekday() == time.Saturday || d.Weekday() == time.Sunday {
			d = d.AddDate(0, 0, 1)
		}
	}
	return s
}

func TestAnalyzeEndToEnd(t *testing.T) {
	cache, err := market.NewDiskCache(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fp := fakeProvider{series: map[string]*market.Series{
		"TEST":     gen("TEST", 3000, 0.0003, 0.015, 1, market.ClassEquity),
		"^GSPC":    gen("^GSPC", 3200, 0.0003, 0.01, 2, market.ClassIndex),
		"^VIX":     gen("^VIX", 3200, 0, 0.05, 3, market.ClassIndex),
		"^TNX":     gen("^TNX", 3200, 0, 0.02, 4, market.ClassIndex),
		"DX-Y.NYB": gen("DX-Y.NYB", 3200, 0, 0.004, 5, market.ClassIndex),
	}}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	e := &Engine{
		Market: market.NewServiceWithProviders(cache, time.Hour, log, fp),
		News:   news.NewCollector(),
		Config: model.DefaultConfig(),
		Log:    log,
	}
	rep, err := e.Analyze(context.Background(), "TEST", Options{NoNews: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Horizons) != 4 {
		t.Fatalf("want 4 horizons, got %d", len(rep.Horizons))
	}
	for _, h := range rep.Horizons {
		t.Logf("%s avail=%v %s p=%.3f status=%s conv=%d reason=%s", h.Key, h.Available, h.Signal, h.PUp, h.Status, h.Conviction, h.Reason)
		if h.Available && (len(h.Why) < 5 || len(h.Drivers) == 0) {
			t.Fatalf("%s missing explanation", h.Key)
		}
	}
	if !rep.Horizons[0].Available || !rep.Horizons[1].Available {
		t.Fatal("short horizons should be available with 3000 bars")
	}
	if len(rep.Features.Used) < 20 {
		t.Fatalf("expected macro & benchmark features to be used, got %v (dropped %v)", rep.Features.Used, rep.Features.Dropped)
	}
	b, err := json.Marshal(rep)
	if err != nil {
		t.Fatalf("report must serialise (NaN leak?): %v", err)
	}
	if strings.Contains(string(b), "NaN") {
		t.Fatal("NaN in JSON")
	}
	// A random walk with tiny drift: the engine must not hand out strong
	// signals for it.
	for _, h := range rep.Horizons {
		if strings.HasPrefix(h.Signal, "STRONG") && h.Status != "validated" {
			t.Fatalf("strong signal without validation on %s", h.Key)
		}
	}
}

func TestFDRDemotesMarginalResults(t *testing.T) {
	// One genuinely strong result, one marginal "validated" one, and noise.
	items := []ScanItem{{Signal: "STRONG BUY", Status: "validated", PValue: 0.0001}, {Signal: "BUY", Status: "validated", PValue: 0.08}}
	for i := 0; i < 18; i++ {
		items = append(items, ScanItem{Signal: "HOLD", Status: "no_edge", PValue: 0.3 + 0.035*float64(i)})
	}
	applyFDR(items, 0.10)
	if !items[0].FDRPass || items[0].Signal != "STRONG BUY" || items[1].Signal != "LEAN UP" {
		t.Fatalf("strong result should survive: %+v", items[0])
	}
	if items[1].FDRPass || items[1].Status != "weak" {
		t.Fatalf("p=0.08 among 20 tests should be demoted: %+v", items[1])
	}
}

type memMemory struct{ j map[string]news.Judgment }

func (m *memMemory) LoadJudgments(context.Context, string) (map[string]news.Judgment, error) {
	return m.j, nil
}
func (m *memMemory) SaveJudgments(_ context.Context, _, _ string, js []news.Judgment) error {
	for _, j := range js {
		m.j[j.ID] = j
	}
	return nil
}
func (m *memMemory) RecordUsage(context.Context, string, news.Usage) error { return nil }

// A headline judged in an earlier run is reused (not re-sent for judgment)
// but still given to Claude as context for the narrative.
func TestJudgmentReuse(t *testing.T) {
	mem := &memMemory{j: map[string]news.Judgment{
		"old": {ID: "old", Relevance: 0.9, Direction: -0.5, Impact: "high", Reasoning: "earlier verdict"},
	}}
	e := &Engine{Analyst: news.NewAnalyst("test-key", "", ""), Memory: mem}
	p := &pending{opt: Options{UseLLM: true}, rep: &Report{Symbol: "X", Name: "X"},
		arts: []news.Article{{ID: "old", Title: "seen before"}, {ID: "new", Title: "fresh headline"}}}
	req, ok := e.llmRequest(context.Background(), p)
	if !ok {
		t.Fatal("expected a request")
	}
	if len(req.New) != 1 || req.New[0].ID != "new" || len(req.Known) != 1 || req.Known[0].ID != "old" {
		t.Fatalf("wrong split: new=%v known=%v", req.New, req.Known)
	}
	if a := p.arts[0]; a.ScoredBy != "llm" || a.Direction != -0.5 || a.Reasoning != "earlier verdict" {
		t.Fatalf("stored judgment not applied: %+v", a)
	}
}

// A replay must only use data up to its date, and must report what came next.
func TestReplayNoLookahead(t *testing.T) {
	cache, _ := market.NewDiskCache(t.TempDir())
	full := gen("TEST", 3200, 0.0003, 0.015, 7, market.ClassEquity)
	fp := fakeProvider{series: map[string]*market.Series{"TEST": full}}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	e := &Engine{Market: market.NewServiceWithProviders(cache, time.Hour, log, fp), News: news.NewCollector(), Config: model.DefaultConfig(), Log: log}
	asOf := full.Bars[2700].Date
	rep, err := e.Analyze(context.Background(), "TEST", Options{AsOf: asOf})
	if err != nil {
		t.Fatal(err)
	}
	if rep.AsOf != asOf || rep.HistoryBars != 2701 || rep.Replay == nil {
		t.Fatalf("replay not cut at %s: asOf=%s bars=%d", asOf, rep.AsOf, rep.HistoryBars)
	}
	for _, h := range rep.Horizons {
		if h.Available && h.Validation.TestTo > asOf {
			t.Fatalf("%s validation used data after the replay date: %s", h.Key, h.Validation.TestTo)
		}
	}
	o := rep.Replay.Outcomes["1w"]
	want := full.Bars[2705].Close/full.Bars[2700].Close - 1
	if !o.Complete || math.Abs(o.Return-want) > 1e-12 || o.ExitDate != full.Bars[2705].Date {
		t.Fatalf("1w outcome wrong: %+v want return %.5f", o, want)
	}
	if len(rep.News.Articles) != 0 {
		t.Fatal("replays must not use today's news")
	}
}
