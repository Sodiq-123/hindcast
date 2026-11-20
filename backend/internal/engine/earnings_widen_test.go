package engine

import (
	"math"
	"testing"
)

// Pins the calibrated widening (see cmd/earncheck): variance of the horizon
// plus the full rms of past reactions.
func TestApplyEarningsWidening(t *testing.T) {
	q := map[string]float64{"p10": -0.05, "p50": 0, "p90": 0.05}
	info := EarningsInfo{Reactions: 60, RMSMove: 0.078, Next: &EarningsNext{Date: "2026-11-18", BarsAway: 1}}
	for _, bars := range []int{1, 21} {
		hf := &HorizonForecast{Bars: bars}
		out := applyEarnings(hf, info, q, 0.033)
		want := math.Sqrt(1 + 0.078*0.078/(0.033*0.033*float64(bars)))
		if got := out["p90"] / 0.05; math.Abs(got-want) > 1e-9 || !hf.EarningsInside {
			t.Errorf("bars %d: widened ×%.3f, want ×%.3f (inside=%v)", bars, got, want, hf.EarningsInside)
		}
	}
	far := info
	far.Next = &EarningsNext{Date: "2026-12-20", BarsAway: 25}
	if out := applyEarnings(&HorizonForecast{Bars: 21}, far, q, 0.033); out["p90"] != 0.05 {
		t.Errorf("report outside the horizon changed the range: %v", out)
	}
}
