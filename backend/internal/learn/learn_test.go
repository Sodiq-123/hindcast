package learn

import (
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 11, 19, 18, 0, 0, 0, time.Local)

func visits(sym string, n int, start time.Time) []Event {
	var out []Event
	for i := range n {
		out = append(out, Event{"visit", sym, start.Add(time.Duration(i) * 20 * time.Hour)})
	}
	return out
}

func kinds(cs []Candidate) string {
	var k []string
	for _, c := range cs {
		k = append(k, c.Kind+":"+c.Subject)
	}
	return strings.Join(k, ",")
}

func TestAddRule(t *testing.T) {
	st := State{Now: now, Watch: map[string]time.Time{"NVDA": now.AddDate(0, -2, 0)}, Horizon: "1d", Detail: "full",
		Names: map[string]string{"SOL-USD": "Solana"}}
	st.Events = append(visits("SOL-USD", 3, now.AddDate(0, 0, -6)), visits("NVDA", 9, now.AddDate(0, 0, -6))...)
	if got := kinds(Candidates(st)); got != "" {
		t.Fatalf("3 visits shouldn't suggest anything (NVDA is already watched): %q", got)
	}
	st.Events = append(st.Events, visits("SOL-USD", 3, now.AddDate(0, 0, -2))...)
	cs := Candidates(st)
	if kinds(cs) != "add:SOL-USD" {
		t.Fatalf("want add:SOL-USD, got %q", kinds(cs))
	}
	if !strings.Contains(cs[0].Card.Say, "Solana 6 times") {
		t.Errorf("wording: %q", cs[0].Card.Say)
	}
	// Visits older than a week don't count.
	st.Events = visits("SOL-USD", 6, now.AddDate(0, 0, -20))
	if kinds(Candidates(st)) != "" {
		t.Error("old visits counted")
	}
}

func TestBlockedBySnoozeAndPending(t *testing.T) {
	st := State{Now: now, Watch: map[string]time.Time{}, Horizon: "1d", Detail: "full", Events: visits("ETH-USD", 5, now.AddDate(0, 0, -5))}
	later := now.Add(48 * time.Hour)
	st.History = []Past{{Kind: KindAdd, Subject: "ETH-USD", Answer: "no", SnoozeUntil: &later}}
	if kinds(Candidates(st)) != "" {
		t.Error("a snoozed suggestion came back early")
	}
	st.History = []Past{{Kind: KindAdd, Subject: "ETH-USD", Answer: ""}}
	if kinds(Candidates(st)) != "" {
		t.Error("a suggestion waiting for an answer was made again")
	}
	before := now.Add(-time.Hour)
	st.History = []Past{{Kind: KindAdd, Subject: "ETH-USD", Answer: "no", SnoozeUntil: &before}}
	if kinds(Candidates(st)) != "add:ETH-USD" {
		t.Error("an expired snooze should allow asking again")
	}
}

func TestRemoveRuleWaitsUntilItKnows(t *testing.T) {
	added := now.AddDate(0, -3, 0)
	st := State{Now: now, Watch: map[string]time.Time{"AAPL": added, "NVDA": added}, Horizon: "1d", Detail: "full",
		Events: visits("NVDA", 2, now.AddDate(0, 0, -3))}
	recent := now.AddDate(0, 0, -10)
	st.Since = &recent
	if kinds(Candidates(st)) != "" {
		t.Fatal("suggested a remove before 30 days of noticing")
	}
	long := now.AddDate(0, 0, -40)
	st.Since = &long
	if got := kinds(Candidates(st)); got != "remove:AAPL" {
		t.Fatalf("want remove:AAPL, got %q", got)
	}
	// Something added recently isn't suggested for removal.
	st.Watch["AAPL"] = now.AddDate(0, 0, -5)
	if kinds(Candidates(st)) != "" {
		t.Error("suggested removing something just added")
	}
}

func TestHorizonRule(t *testing.T) {
	st := State{Now: now, Watch: map[string]time.Time{}, Horizon: "1d", Detail: "full"}
	for i := range 10 {
		v := "1w"
		if i == 3 {
			v = "1m"
		}
		st.Events = append(st.Events, Event{"view", v, now.Add(-time.Duration(10-i) * time.Hour)})
	}
	cs := Candidates(st)
	if kinds(cs) != "horizon:1w" || !strings.Contains(cs[0].Card.Say, "week view on 9 of your last 10") {
		t.Fatalf("got %q %q", kinds(cs), cs)
	}
	st.Horizon = "1w"
	if kinds(Candidates(st)) != "" {
		t.Error("suggested the horizon that's already the default")
	}
}

func TestDetailRule(t *testing.T) {
	st := State{Now: now, Watch: map[string]time.Time{}, Horizon: "1d", Detail: "simple"}
	for i, sym := range []string{"A", "B", "C", "D", "E", "F", "G", "H"} {
		at := now.Add(-time.Duration(i+1) * 25 * time.Hour)
		st.Events = append(st.Events, Event{"visit", sym, at})
		if i < 6 {
			st.Events = append(st.Events, Event{"detail", sym, at.Add(time.Minute)})
		}
	}
	if got := kinds(Candidates(st)); got != "detail:full" {
		t.Fatalf("6 of 8 should suggest full detail, got %q", got)
	}
	st.Detail = "full"
	if strings.Contains(kinds(Candidates(st)), "detail:") {
		t.Error("suggested full detail to someone already on it")
	}
}

func TestLevelRule(t *testing.T) {
	base := func() State { return State{Now: now, Watch: map[string]time.Time{}, Horizon: "1d", Detail: "simple", Level: "beginner"} }
	pageDays := func(st *State, n int) {
		for i := range n {
			st.Events = append(st.Events, Event{"page", "track-record", now.Add(-time.Duration(i+1) * 26 * time.Hour)})
		}
	}
	st := base()
	pageDays(&st, 4)
	cs := Candidates(st)
	if kinds(cs) != "level:advanced" {
		t.Fatalf("Track record on 4 days should offer the numbers, got %q", kinds(cs))
	}
	if !strings.Contains(cs[0].Card.More, "Today switches to the precise wording") || !strings.Contains(cs[0].Card.More, "5-question check") {
		t.Errorf("a beginner's card should mention Today and the quiz: %q", cs[0].Card.More)
	}
	st = base()
	pageDays(&st, 3)
	if kinds(Candidates(st)) != "" {
		t.Error("3 days is not enough")
	}
	for name, change := range map[string]func(*State){
		"already advanced": func(s *State) { s.Level = "advanced" },
		"chose a wording":  func(s *State) { s.LevelChose = true },
		"quiz this month":  func(s *State) { q := now.AddDate(0, 0, -10); s.QuizAt = &q },
	} {
		st = base()
		pageDays(&st, 5)
		change(&st)
		if got := kinds(Candidates(st)); got != "" {
			t.Errorf("%s: should stay quiet, got %q", name, got)
		}
	}
	// On full detail, opening the deeper tabs on half of 8+ asset pages counts too.
	deep := func(n int) State {
		st := base()
		st.Detail, st.Level = "full", "intermediate"
		for i, sym := range []string{"A", "B", "C", "D", "E", "F", "G", "H"} {
			at := now.Add(-time.Duration(i+1) * 25 * time.Hour)
			st.Events = append(st.Events, Event{"visit", sym, at})
			if i < n {
				st.Events = append(st.Events, Event{"detail", sym, at.Add(time.Minute)})
			}
		}
		return st
	}
	if got := kinds(Candidates(deep(4))); got != "level:advanced" {
		t.Errorf("4 of 8 on full detail: %q", got)
	}
	if got := kinds(Candidates(deep(3))); got != "" {
		t.Errorf("3 of 8 is under half: %q", got)
	}
	st = base()
	pageDays(&st, 2)
	found := false
	for _, n := range Notice(st) {
		found = found || n.Key == "page"
	}
	if !found {
		t.Error("Track record visits on 2 days should be listed as noticed")
	}
}

func TestPicksAndRank(t *testing.T) {
	if e := Estimate(0.5, 0, 0); e != 0.5 {
		t.Fatalf("no answers should give the shared rate, got %v", e)
	}
	hist := []Past{{Kind: KindAdd, Answer: "yes"}, {Kind: KindAdd, Answer: "yes"}, {Kind: KindAdd, Answer: "no"},
		{Kind: KindDetail, Answer: "no"}, {Kind: KindDetail, Answer: "undone"}, {Kind: KindDetail, Answer: "no", ShownAt: now.AddDate(0, 0, -5)}}
	picks := Picks(map[string][2]int{KindAdd: {6, 10}}, hist)
	by := map[string]Pick{}
	for _, p := range picks {
		by[p.Kind] = p
	}
	if p := by[KindAdd]; p.Yes != 2 || p.Answered != 3 || p.Estimate <= p.Everyone {
		t.Fatalf("add pick wrong: %+v", p)
	}
	cands := []Candidate{{Kind: KindDetail, Subject: "full"}, {Kind: KindHorizon, Subject: "1w"}, {Kind: KindAdd, Subject: "X"}}
	ranked := Rank(cands, picks, hist, now)
	// Detail was declined 3 times and asked within 60 days, so it's held back.
	if kinds(ranked) != "add:X,horizon:1w" {
		t.Fatalf("rank: %q", kinds(ranked))
	}
	if !strings.Contains(ranked[0].Card.Why[0], "2 of 3") {
		t.Errorf("why line: %q", ranked[0].Card.Why)
	}
}

func f(x float64) *float64 { return &x }

func TestHabitsFromRealJournal(t *testing.T) {
	// The four closed paper trades on 19 Nov 2026 (after fees).
	ts := []Trade{
		{Name: "PAID", Kind: "token", Symbol: "addr1", Stop: f(0.0039), Exit: 0.004738, PnL: 11.89, PnLPct: 0.118, Days: 6},
		{Name: "baton", Kind: "token", Symbol: "addr2", Stop: f(0.005), Exit: 0.01677, PnL: 187.44, PnLPct: 1.26, Days: 6},
		{Name: "ZIG", Kind: "token", Symbol: "addr3", Stop: f(0.05), Exit: 0.04113, PnL: -55.48, PnLPct: -0.37, Days: 21},
		{Name: "Fartcoin", Kind: "token", Symbol: "addr4", Stop: f(0.05), Exit: 0.1691, PnL: 4.1, PnLPct: 0.04, Days: 5},
	}
	h := HabitsFrom(ts)
	want := []string{"All 4 of your closed trades were meme tokens.", "Your one loss, ZIG, closed 18% below its stop.", "One trade, baton, made more than your whole profit"}
	if len(h.Facts) != 3 {
		t.Fatalf("facts: %+v", h.Facts)
	}
	for i, w := range want {
		if !strings.HasPrefix(h.Facts[i].Title, w) {
			t.Errorf("fact %d = %q, want prefix %q", i, h.Facts[i].Title, w)
		}
	}
	for _, p := range h.Patterns {
		if p.Ready || p.Found {
			t.Errorf("pattern %s shouldn't be ready with 4 trades: %+v", p.Key, p)
		}
	}
	if h.Patterns[0].Need != "needs 8 trades on each side · you have 0 and 4" {
		t.Errorf("tested need: %q", h.Patterns[0].Need)
	}
	rs := Reminders(h, "token")
	if len(rs) != 2 || !strings.Contains(rs[0].Detail, "This is the 5th") || rs[1].Key != "gap" {
		t.Fatalf("token reminders: %+v", rs)
	}
	if rs := Reminders(h, "stock"); len(rs) != 1 || rs[0].Key != "gap" {
		t.Fatalf("a stock trade should only get the gap reminder: %+v", rs)
	}
}

func TestPatternsNeedEvidence(t *testing.T) {
	var ts []Trade
	// 10 tested trades that make money, 10 untested that lose: a clear difference.
	for i := range 10 {
		ts = append(ts, Trade{Name: "T", Kind: "asset", Symbol: "SPY", Exit: 1, PnL: 20 + float64(i), PnLPct: 0.02 + float64(i)/1000, Days: 4, Edge: "tested"})
		ts = append(ts, Trade{Name: "U", Kind: "asset", Symbol: "QQQ", Exit: 1, PnL: -30 - float64(i), PnLPct: -0.03 - float64(i)/1000, Days: 15, Edge: "untested"})
	}
	h := HabitsFrom(ts)
	tested, holding := h.Patterns[0], h.Patterns[1]
	if !tested.Found || !strings.Contains(tested.Text, "Your 10 trades on signals without a tested edge lost") {
		t.Fatalf("tested pattern: %+v", tested)
	}
	if !holding.Found || !strings.HasPrefix(holding.Text, "You held losing trades for 15 days") {
		t.Fatalf("holding pattern: %+v", holding)
	}
	// The same number of trades with no real difference must not be named.
	for i := range ts {
		ts[i].PnLPct = []float64{0.01, -0.01}[i%4/2]
		ts[i].Days = 7
	}
	h = HabitsFrom(ts)
	if h.Patterns[0].Found || h.Patterns[1].Found {
		t.Fatalf("named a pattern from noise: %+v", h.Patterns)
	}
}

func TestPermutationIsRepeatable(t *testing.T) {
	a, b := []float64{1, 2, 3, 4}, []float64{2, 3, 4, 5}
	if permutationP(a, b) != permutationP(a, b) {
		t.Fatal("same data gave different answers")
	}
}

func TestNotice(t *testing.T) {
	st := State{Now: now, Names: map[string]string{"NVDA": "NVIDIA"}}
	st.Events = append(visits("NVDA", 4, now.AddDate(0, 0, -8)), visits("BTC-USD", 2, now.AddDate(0, 0, -3))...)
	for i := range 6 {
		st.Events = append(st.Events, Event{"view", "1w", now.Add(-time.Duration(i) * time.Hour)})
	}
	n := Notice(st)
	if len(n) != 3 || n[0].Text != "You look at the week view most." || n[2].Text != "Most visited: NVIDIA, BTC-USD." {
		t.Fatalf("noticed: %+v", n)
	}
}
