package plain

import (
	"strings"
	"testing"

	"github.com/sodiq/financial-market/backend/internal/engine"
	"github.com/sodiq/financial-market/backend/internal/model"
)

func horizon(key, status, signal string, p, base, acc, baseAcc float64) engine.HorizonForecast {
	h := engine.HorizonForecast{Key: key, Bars: map[string]int{"1d": 1, "1w": 5, "1m": 21, "1y": 252}[key], TargetDate: "2026-12-16", Available: true,
		PUp: p, BaseRate: base, Status: status, Signal: signal, PLogistic: 0.61, PAnalogs: 0.40}
	h.Validation.Ensemble = model.Metrics{N: 3386, Accuracy: acc, BaseAccuracy: baseAcc, UpRate: 0.65, BrierSkill: -0.0044, AUC: 0.501}
	h.Analogs.K, h.Analogs.UpRate, h.Analogs.MedianReturn = 64, 0.33, -0.055
	h.Drivers = []engine.Driver{
		{Label: "3-month return", Z: -1.9, Text: "Past 3 months -28.5% — far below normal."},
		{Label: "OBV", Z: 2.1, Text: "On-balance volume shows net selling over 20 days (-0.63) — far below normal for this asset."},
		{Label: "1-month return", Z: -2.9, Text: "Past month -28.4% — far below normal for this asset (-2.9σ). The model learned this has had little effect."},
	}
	return h
}

func report(hs ...engine.HorizonForecast) *engine.Report {
	return &engine.Report{Name: "NVIDIA Corporation", Symbol: "NVDA", Horizons: hs}
}

func TestNoEdgeMonth(t *testing.T) {
	a := Explain(report(horizon("1m", "no_edge", "NO EDGE", 0.647, 0.619, 0.630, 0.632)), "beginner")[0]
	if a.Say != "Too close to call for the next month." {
		t.Errorf("say = %q", a.Say)
	}
	if !strings.HasPrefix(a.More, "65% chance it's higher by 16 Dec, about the usual 62%.") || !strings.Contains(a.More, "mostly followed by falls") {
		t.Errorf("more = %q", a.More)
	}
	if a.Tested != "not_proven" || a.TestLabel != "Not proven" || a.Tests != 3386 || a.GuessWord != "up" {
		t.Errorf("tested = %+v", a)
	}
	leads := []string{}
	for _, r := range a.Reasons {
		leads = append(leads, r.Lead)
	}
	want := []string{"No proven edge.", "Days like today leaned lower.", "Most unusual right now.", "The two models disagree.", "News barely moves this forecast."}
	if strings.Join(leads, "|") != strings.Join(want, "|") || a.Shown != 3 {
		t.Errorf("leads = %v, shown %d", leads, a.Shown)
	}
	if got := a.Reasons[0].Text; got != " In 3,386 past tests on NVIDIA this method was right 63.0% of the time; always guessing “up” was right 63.2%." {
		t.Errorf("tests text = %q", got)
	}
	if got := a.Reasons[2].Text; got != " Past month -28.4%, far below normal for this asset." {
		t.Errorf("standout = %q (should pick the largest |z| and drop σ for beginners)", got)
	}
}

func TestAdvancedKeepsNumbers(t *testing.T) {
	a := Explain(report(horizon("1m", "no_edge", "NO EDGE", 0.647, 0.619, 0.630, 0.632)), "advanced")[0]
	if !strings.Contains(a.Reasons[0].Text, "Brier skill -0.44%, AUC 0.501") || !strings.Contains(a.Reasons[2].Text, "(-2.9σ)") {
		t.Errorf("advanced reasons = %+v", a.Reasons[:3])
	}
}

func TestYearWhereGuessingWins(t *testing.T) {
	h := horizon("1y", "no_edge", "NO EDGE", 0.845, 0.804, 0.634, 0.845)
	h.TargetDate = "2027-11-04"
	a := Explain(report(h), "beginner")[0]
	if a.Say != "Usually higher a year on, and this model can't add to that." || !strings.Contains(a.More, "guessing “up” beat this model") || !strings.Contains(a.More, "by 4 Nov 2027") {
		t.Errorf("year: %q / %q", a.Say, a.More)
	}
}

func TestValidatedAndWeak(t *testing.T) {
	cases := []struct {
		status, signal string
		p, base        float64
		want           string
	}{
		{"validated", "BUY", 0.70, 0.55, "Likely higher for the next week."},
		{"validated", "STRONG SELL", 0.35, 0.55, "Likely lower for the next week."},
		{"validated", "HOLD", 0.56, 0.55, "No clear lean for the next week, from a model that has passed testing."},
		{"weak", "LEAN BUY", 0.62, 0.55, "Leaning higher for the next week, but the evidence is weak."},
		{"weak", "HOLD", 0.56, 0.55, "Too close to call for the next week."},
	}
	for _, c := range cases {
		a := Explain(report(horizon("1w", c.status, c.signal, c.p, c.base, 0.6, 0.55)), "beginner")[0]
		if a.Say != c.want {
			t.Errorf("%s %s: %q, want %q", c.status, c.signal, a.Say, c.want)
		}
	}
	a := Explain(report(horizon("1w", "validated", "BUY", 0.70, 0.55, 0.6, 0.55)), "beginner")[0]
	if !strings.Contains(a.More, "above the usual 55%") || a.TestLabel != "Tested edge" || a.Reasons[0].Lead != "A tested edge." {
		t.Errorf("validated: %+v", a)
	}
}

func TestEarningsAndOptions(t *testing.T) {
	h := horizon("1d", "no_edge", "NO EDGE", 0.54, 0.527, 0.514, 0.529)
	h.EarningsInside = true
	h.Options = &engine.OptionsView{Expiry: "2026-11-18", ImpliedMove: 0.082, ModelMove: 0.103}
	rep := report(h)
	rep.Earnings = engine.EarningsInfo{Reactions: 67, AvgAbsMove: 0.062, NormalMove: 0.018, Next: &engine.EarningsNext{Date: "2026-11-17"}}
	a := Explain(rep, "beginner")[0]
	if !strings.Contains(a.More, "at the next close") || !strings.Contains(a.More, "NVIDIA's earnings report on 17 Nov makes this less predictable") {
		t.Errorf("more = %q", a.More)
	}
	found := map[string]bool{}
	for _, r := range a.Reasons {
		found[r.Lead] = true
	}
	if !found["Earnings widen the range."] || !found["Options traders expect a similar size of move."] {
		t.Errorf("reasons = %+v", a.Reasons)
	}
}

func TestStandoutDropsRawReadingForBeginners(t *testing.T) {
	h := horizon("1m", "no_edge", "NO EDGE", 0.6, 0.6, 0.6, 0.6)
	h.Drivers = h.Drivers[1:2] // only the OBV driver
	if got := Explain(report(h), "beginner")[0].Reasons[2].Text; got != " On-balance volume shows net selling over 20 days, far below normal for this asset." {
		t.Errorf("standout = %q", got)
	}
	h.Key, h.Bars, h.EarningsInside = "1y", 252, true
	rep := report(h)
	rep.Earnings = engine.EarningsInfo{Reactions: 67, AvgAbsMove: 0.062, NormalMove: 0.018, Next: &engine.EarningsNext{Date: "2026-11-17"}}
	for _, r := range Explain(rep, "beginner")[0].Reasons {
		if r.Lead == "Earnings widen the range." {
			t.Error("a single report shouldn't be a headline reason for the one-year view")
		}
	}
}

func TestUnavailableSkipped(t *testing.T) {
	h := horizon("1y", "", "", 0, 0, 0, 0)
	h.Available = false
	if got := Explain(report(h), "beginner"); len(got) != 0 {
		t.Errorf("unavailable horizon explained: %+v", got)
	}
}
