package today

import (
	"strings"
	"testing"
	"time"

	"github.com/sodiq/financial-market/backend/internal/engine"
	"github.com/sodiq/financial-market/backend/internal/plain"
	"github.com/sodiq/financial-market/backend/internal/token"
)

var now = time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)

func rep(name string, chg float64, earnings string) *engine.Report {
	r := &engine.Report{Name: name, Price: 100, Change1d: chg, GeneratedAt: now.Add(-2 * time.Hour)}
	for _, k := range Horizons {
		r.Horizons = append(r.Horizons, engine.HorizonForecast{Key: k, Available: true, PUp: 0.55, BaseRate: 0.53, Status: "no_edge", Signal: "NO EDGE",
			ReturnRange: map[string]float64{"p10": -0.05, "p50": 0.01, "p90": 0.07}})
	}
	if earnings != "" {
		r.Earnings = engine.EarningsInfo{Next: &engine.EarningsNext{Date: earnings}, Reactions: 67, AvgAbsMove: 0.061, NormalMove: 0.02}
	}
	return r
}

func base() Input {
	return Input{
		Now: now, Level: "beginner", Horizon: "1m", Profile: "done",
		Assets: []Asset{
			{Symbol: "NVDA", Report: rep("NVIDIA Corporation", -0.008, "2026-11-18")},
			{Symbol: "AAPL", Report: rep("Apple Inc.", 0.015, "")},
			{Symbol: "SPY", Report: rep("State Street SPDR S&P 500 ETF Trust", -0.004, "")},
		},
		Tokens: []token.Brief{{Address: "pumpaddr", Symbol: "PUMP", Verdict: "Very high risk", Change24h: -0.05, Top10: 0.6}},
		Steps:  map[string]StepState{},
	}
}

func TestBriefingLines(t *testing.T) {
	got := Build(base())
	if got.Title != "Today at a glance" {
		t.Errorf("title without a previous visit = %q", got.Title)
	}
	tags := []string{}
	for _, l := range got.Lines {
		tags = append(tags, l.Tag)
	}
	if strings.Join(tags, ",") != "EARNINGS,RISK,MOVES,SIGNALS" {
		t.Fatalf("tags = %v", tags)
	}
	l := got.Lines
	if l[0].Strong != "NVIDIA reports tomorrow." || !strings.Contains(l[0].Text, "67 reports") || !strings.Contains(l[0].Text, "6.1%") {
		t.Errorf("earnings line = %+v", l[0])
	}
	if l[1].Strong != "PUMP fell 5% and still rates very high risk." || !strings.Contains(l[1].Text, "60%") {
		t.Errorf("risk line = %+v", l[1])
	}
	if l[2].Text != "Apple rose 1.5% on the last trading day. Everything else moved less than 1%." {
		t.Errorf("moves line = %q", l[2].Text)
	}
	if !strings.Contains(l[3].Text, "No reliable signals") {
		t.Errorf("signals line = %q", l[3].Text)
	}
	seen := now.Add(-48 * time.Hour)
	in := base()
	in.LastSeen = &seen
	if Build(in).Title != "Since you last looked" {
		t.Error("title with a previous visit")
	}
}

func TestAdvancedVoice(t *testing.T) {
	in := base()
	in.Level = "advanced"
	l := Build(in).Lines
	if !strings.Contains(l[0].Text, "±6.1%") || !strings.Contains(l[0].Text, "ordinary") {
		t.Errorf("advanced earnings text = %q", l[0].Text)
	}
	if l[3].Text != "No validated signals on any horizon." {
		t.Errorf("advanced signals text = %q", l[3].Text)
	}
}

func TestStaleDataLine(t *testing.T) {
	in := base()
	for i := range in.Assets {
		in.Assets[i].Report.GeneratedAt = now.Add(-19 * 24 * time.Hour)
	}
	l := Build(in).Lines
	last := l[len(l)-1]
	if last.Tag != "DATA" || !strings.Contains(last.Text, "29 October") {
		t.Errorf("stale line = %+v", last)
	}
}

func TestEarningsStepDependsOnHolding(t *testing.T) {
	s := Build(base()).Step
	if s == nil || s.ID != "earnings:NVDA:2026-11-18" || s.Title != "See how NVIDIA usually moves after its reports." || !strings.HasPrefix(s.Body, "It reports tomorrow. Over its last 67") || !strings.HasSuffix(s.Link, "#earnings") {
		t.Fatalf("not held: %+v", s)
	}
	in := base()
	in.Open = []Position{{ID: 4, Symbol: "NVDA"}}
	s = Build(in).Step
	if !strings.HasPrefix(s.Title, "Set a plan for your NVIDIA position before its report tomorrow") || s.Link != "/journal" {
		t.Errorf("held without stop: %+v", s)
	}
	in.Open[0].HasStop = true
	if s = Build(in).Step; !strings.HasPrefix(s.Title, "Check your NVIDIA plan") {
		t.Errorf("held with stop: %+v", s)
	}
}

func TestSkipAndDone(t *testing.T) {
	in := base()
	in.Steps["earnings:NVDA:2026-11-18"] = StepState{SkippedToday: true}
	s := Build(in).Step
	if s == nil || s.ID != "earnings:NVDA:2026-11-18" || !s.Skipped {
		t.Fatalf("a skipped step should come back marked, not be replaced: %+v", s)
	}
	in.Steps["earnings:NVDA:2026-11-18"] = StepState{Done: true}
	s = Build(in).Step
	if s == nil || s.ID != "learn:no-signal" {
		t.Fatalf("after done, the next suggestion should show: %+v", s)
	}
	in.Steps["learn:no-signal"] = StepState{Done: true}
	if s = Build(in).Step; s != nil {
		t.Errorf("nothing left should mean no step, got %+v", s)
	}
}

func TestEmptyWatchlist(t *testing.T) {
	got := Build(Input{Now: now, Level: "beginner", Profile: "done", Steps: map[string]StepState{}})
	if got.Step == nil || got.Step.ID != "watch:first" {
		t.Errorf("step = %+v", got.Step)
	}
	if len(got.Lines) != 0 || got.Rows == nil || got.Tokens == nil || got.Lines == nil {
		t.Errorf("an empty list should give empty, non-nil sections: %+v", got)
	}
}

func TestEarningsWindow(t *testing.T) {
	in := base()
	in.Assets[0].Report.Earnings.Next.Date = "2026-11-21" // Saturday, 4 days out
	got := Build(in)
	if got.Lines[0].Strong != "NVIDIA reports on Saturday." {
		t.Errorf("4 days out should be in the briefing: %+v", got.Lines[0])
	}
	if got.Step != nil && strings.HasPrefix(got.Step.ID, "earnings:") {
		t.Errorf("4 days out is too early for the step: %+v", got.Step)
	}
	in.Assets[0].Report.Earnings.Next.Date = "2026-11-16"
	if Build(in).Lines[0].Tag == "EARNINGS" {
		t.Error("past dates should be ignored")
	}
}

func TestRowsAndTokens(t *testing.T) {
	in := base()
	in.Assets = append(in.Assets, Asset{Symbol: "NEW"})
	got := Build(in)
	if len(got.Rows) != 4 || got.Rows[3].Analysed || len(got.Rows[0].Ranges) != 4 {
		t.Fatalf("rows = %+v", got.Rows)
	}
	if l := got.Lines[len(got.Lines)-1]; l.Tag != "NEW" || l.Text != "NEW hasn't been analysed yet. It takes a minute or two after adding." {
		t.Errorf("waiting line = %+v", l)
	}
	r := got.Rows[0].Ranges[2]
	if r.Key != "1m" || r.P10 != -0.05 || r.P90 != 0.07 || r.Base != 0.53 || r.Verdict != "No edge over the usual" {
		t.Errorf("range = %+v", r)
	}
	if got.Rows[0].Name != "NVIDIA" || got.Rows[1].Name != "Apple" || got.Rows[2].Name != "SPY" {
		t.Errorf("short names = %q, %q", got.Rows[0].Name, got.Rows[1].Name)
	}
	if tk := got.Tokens[0]; tk.Level != 4 || tk.Label != "Very high risk" {
		t.Errorf("token = %+v", tk)
	}
}

func TestEarningsTiming(t *testing.T) {
	in := base()
	in.Assets[0].Report.Earnings.Next.Time = "after"
	got := Build(in)
	if got.Lines[0].Strong != "NVIDIA reports tomorrow, after the market closes." || !strings.HasPrefix(got.Step.Body, "It reports tomorrow, after the market closes. ") {
		t.Errorf("timing: %q / %q", got.Lines[0].Strong, got.Step.Body)
	}
}

func TestShortNames(t *testing.T) {
	for in, want := range map[string]string{"Bitcoin USD": "Bitcoin", "NVIDIA Corporation": "NVIDIA", "Apple Inc.": "Apple", "Alphabet Inc. Class A": "Alphabet Inc. Class A", "State Street SPDR S&P 500 ETF Trust": "X"} {
		if got := plain.Short(in, "X"); got != want {
			t.Errorf("shortName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTokenDropStep(t *testing.T) {
	in := base()
	in.Assets = nil
	in.Tokens[0].Change24h = -0.22
	s := Build(in).Step
	if s == nil || s.ID != "token-drop:pumpaddr:2026-11-17" || !strings.Contains(s.Body, "22%") {
		t.Errorf("step = %+v", s)
	}
}
