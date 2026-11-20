package tracker

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/sodiq/financial-market/backend/internal/engine"
	"github.com/sodiq/financial-market/backend/internal/market"
	"github.com/sodiq/financial-market/backend/internal/store"
)

type fixed struct{ s *market.Series }

func (f fixed) Name() string                                          { return "fixed" }
func (f fixed) Fetch(context.Context, string) (*market.Series, error) { return f.s, nil }

func TestResolveAndSummarise(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	st := store.OpenTest(t)

	// A BUY made on 2024-01-02 for 2 bars; the price then rises 10%.
	ser := &market.Series{Meta: market.Meta{Symbol: "X"}, FetchedAt: time.Now(), Bars: []market.Bar{
		{Date: "2024-01-01", Close: 90}, {Date: "2024-01-02", Close: 100}, {Date: "2024-01-03", Close: 105}, {Date: "2024-01-04", Close: 110},
	}}
	rep := &engine.Report{Symbol: "X", Class: "equity", AsOf: "2024-01-02", Price: 100, GeneratedAt: time.Now(),
		Horizons: []engine.HorizonForecast{{Key: "2d", Bars: 2, TargetDate: "2024-01-04", Available: true, PUp: 0.6, PModel: 0.58, Signal: "BUY", Status: "validated"}}}
	if err := st.SaveReport(ctx, rep); err != nil {
		t.Fatal(err)
	}
	cache, _ := market.NewDiskCache(filepath.Join(dir, "c"))
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	tr := &Tracker{Store: st, Market: market.NewServiceWithProviders(cache, time.Hour, log, fixed{ser}), Log: log}
	n, err := tr.Resolve(ctx)
	if err != nil || n != 1 {
		t.Fatalf("resolve: n=%d err=%v", n, err)
	}
	rec, err := tr.Summary(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Overall.N != 1 || rec.Overall.HitRate != 1 || rec.Overall.AvgReturn < 0.099 || rec.Overall.AvgReturn > 0.101 {
		t.Fatalf("bad summary: %+v", rec.Overall)
	}
}
