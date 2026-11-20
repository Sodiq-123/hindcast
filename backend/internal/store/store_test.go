package store

import (
	"context"
	"testing"
	"time"

	"github.com/sodiq/financial-market/backend/internal/engine"
	"github.com/sodiq/financial-market/backend/internal/market"
	"github.com/sodiq/financial-market/backend/internal/news"
)

func TestReportsAndPredictions(t *testing.T) {
	st := OpenTest(t)
	ctx := context.Background()
	rep := &engine.Report{Symbol: "AAPL", AsOf: "2024-01-02", Headline: "h", GeneratedAt: time.Now().UTC(), UsedLLM: true,
		Horizons: []engine.HorizonForecast{
			{Key: "1d", Bars: 1, TargetDate: "2024-01-03", Available: true, PUp: 0.6, Signal: "BUY", Status: "validated"},
			{Key: "1y", Available: false},
		}}
	if err := st.SaveReport(ctx, rep); err != nil {
		t.Fatal(err)
	}
	if rep.ID == 0 {
		t.Fatal("report id not set")
	}
	got, err := st.LatestReport(ctx, "aapl")
	if err != nil || got.ID != rep.ID || got.Horizons[0].Signal != "BUY" {
		t.Fatalf("latest report: %+v %v", got, err)
	}
	hist, _ := st.ReportHistory(ctx, "AAPL", 5)
	if len(hist) != 1 || hist[0].AsOf != "2024-01-02" || len(hist[0].CreatedAt) != 20 {
		t.Fatalf("history: %+v", hist)
	}
	due, err := st.DuePredictions(ctx, "2024-01-05")
	if err != nil || len(due) != 1 || !due[0].UsedLLM || due[0].TargetDate != "2024-01-03" {
		t.Fatalf("due: %+v %v", due, err)
	}
	if err := st.ResolvePrediction(ctx, due[0].ID, "2024-01-03", 0.02); err != nil {
		t.Fatal(err)
	}
	res, _ := st.ResolvedPredictions(ctx)
	if len(res) != 1 || *res[0].ExitDate != "2024-01-03" || *res[0].RealizedReturn != 0.02 {
		t.Fatalf("resolved: %+v", res)
	}
}

func TestWatchlistScansJudgmentsUsage(t *testing.T) {
	st := OpenTest(t)
	ctx := context.Background()
	u := testUser(t, st, "a@example.com")
	_ = st.AddWatch(ctx, u, "btc-usd")
	_ = st.AddWatch(ctx, u, "BTC-USD") // duplicate ignored
	if wl, _ := st.Watchlist(ctx, u); len(wl) != 1 || wl[0] != "BTC-USD" {
		t.Fatalf("watchlist: %v", wl)
	}
	sr := &engine.ScanResult{Horizon: "1w", Scanned: 3}
	if err := st.SaveScan(ctx, sr); err != nil || sr.ID == 0 {
		t.Fatalf("scan: %v id=%d", err, sr.ID)
	}
	if got, err := st.LatestScan(ctx); err != nil || got.Scanned != 3 {
		t.Fatalf("latest scan: %+v %v", got, err)
	}
	j := news.Judgment{ID: "a1", Relevance: 0.7, Direction: 0.4, Impact: "high", Horizon: "days", Reasoning: "r"}
	_ = st.SaveJudgments(ctx, "X", "claude-opus-5-5", []news.Judgment{j})
	j.Direction = -0.2
	_ = st.SaveJudgments(ctx, "X", "claude-opus-5-5", []news.Judgment{j}) // upsert
	if js, _ := st.LoadJudgments(ctx, "x"); js["a1"].Direction != -0.2 {
		t.Fatalf("judgments: %+v", js)
	}
	_ = st.RecordUsage(ctx, "X", news.Usage{Model: "claude-opus-5-5", Input: 100, Output: 50, Batch: true, CostUSD: 0.5})
	if u, err := st.UsageSummary(ctx); err != nil || u.Calls30 != 1 || u.TodayUSD != 0.5 || u.BatchShare30 != 1 {
		t.Fatalf("usage: %+v %v", u, err)
	}
}

func TestPriceCacheAndModels(t *testing.T) {
	st := OpenTest(t)
	ctx := context.Background()
	c := st.PriceCache()
	ser := &market.Series{Meta: market.Meta{Symbol: "^GSPC", Provider: "yahoo"}, FetchedAt: time.Now().Add(-time.Hour),
		Bars: []market.Bar{{Date: "2024-01-02", Close: 1}, {Date: "2024-01-03", Close: 2}}}
	if err := c.Put("^GSPC", ser); err != nil {
		t.Fatal(err)
	}
	if got, fresh := c.Get("^GSPC", 2*time.Hour); !fresh || len(got.Bars) != 2 || got.Meta.Provider != "yahoo" {
		t.Fatalf("fresh get: %+v %v", got, fresh)
	}
	if got, fresh := c.Get("^GSPC", 30*time.Minute); fresh || got == nil {
		t.Fatal("stale series must come back with fresh=false")
	}
	if _, ok := c.Get("NOPE", time.Hour); ok {
		t.Fatal("missing symbol must miss")
	}
	if err := st.SaveModel(ctx, "dir-equity-1d", "dir", time.Now(), map[string]any{"pool": "equity", "horizon": "1d"}); err != nil {
		t.Fatal(err)
	}
	if m, err := st.LoadModels(ctx); err != nil || len(m["dir"]) != 1 {
		t.Fatalf("models: %v %v", m, err)
	}
}

func TestWatchTokens(t *testing.T) {
	st := OpenTest(t)
	ctx := context.Background()
	addr := "pumpCmXqMfrsAkQ5r49WcJnRayYRqmXz6ae8H7H9Dfn"
	u := testUser(t, st, "a@example.com")
	if err := st.AddWatch(ctx, u, "pump"); err != nil { // the NYSE stock
		t.Fatal(err)
	}
	if err := st.AddWatchToken(ctx, u, addr, "solana"); err != nil {
		t.Fatal(err)
	}
	syms, _ := st.Watchlist(ctx, u)
	if len(syms) != 1 || syms[0] != "PUMP" {
		t.Errorf("assets: %v", syms)
	}
	ts, _ := st.WatchTokens(ctx, u)
	if len(ts) != 1 || ts[0].Address != addr {
		t.Fatalf("tokens: %+v (case must be kept)", ts)
	}
	if err := st.RemoveWatch(ctx, u, addr); err != nil {
		t.Fatal(err)
	}
	ts, _ = st.WatchTokens(ctx, u)
	syms, _ = st.Watchlist(ctx, u)
	if len(ts) != 0 || len(syms) != 1 {
		t.Errorf("remove token only: tokens %v assets %v", ts, syms)
	}
}
