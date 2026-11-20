package tracker

import (
	"fmt"
	"testing"

	"github.com/sodiq/financial-market/backend/internal/store"
)

func preds(n, bars int, p float64, up func(i int) bool) []store.Prediction {
	var out []store.Prediction
	base := 0.55
	for i := range n {
		r := -0.01
		if up(i) {
			r = 0.01
		}
		day := fmt.Sprintf("2026-%02d-%02d", 1+i/28, 1+i%28)
		out = append(out, store.Prediction{Symbol: "X", Horizon: "1d", Bars: bars, AsOf: day, PUp: p, RealizedReturn: &r, BaseRate: &base},
			store.Prediction{Symbol: "X", Horizon: "1d", Bars: bars, AsOf: day, PUp: p, RealizedReturn: &r, BaseRate: &base}) // twice-daily duplicate
	}
	return out
}

func TestDrift(t *testing.T) {
	// Confidently bullish while the asset fell 70% of days: worse than base rate.
	bad := computeDrift(preds(60, 1, 0.65, func(i int) bool { return i%10 < 3 }))
	d := bad["X|1d"]
	if d == nil || d.N != 60 {
		t.Fatalf("expected drift over 60 deduplicated days, got %+v", d)
	}
	// Bullish and right most of the time: no drift.
	if good := computeDrift(preds(60, 1, 0.65, func(i int) bool { return i%10 < 7 })); len(good) != 0 {
		t.Errorf("unexpected drift %+v", good["X|1d"])
	}
	// Too few independent periods for a 1-month horizon.
	if few := computeDrift(preds(60, 21, 0.65, func(i int) bool { return i%10 < 3 })); len(few) != 0 {
		t.Error("judged a monthly horizon on 3 independent periods")
	}
}
