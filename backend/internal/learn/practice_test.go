package learn

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/sodiq/financial-market/backend/internal/market"
	"github.com/sodiq/financial-market/backend/internal/store"
)

func bar(open, high, low, close float64) market.Bar {
	return market.Bar{Open: open, High: high, Low: low, Close: close}
}

func TestOutcome(t *testing.T) {
	cases := []struct {
		name   string
		after  []market.Bar
		result string
		bar    int
		exit   float64
	}{
		{"target", []market.Bar{bar(100, 104, 98, 103), bar(103, 116, 102, 114)}, "target", 2, 115},
		{"stop", []market.Bar{bar(100, 101, 95, 96), bar(96, 97, 91, 92)}, "stop", 2, 92},
		{"gap below the stop fills at the open", []market.Bar{bar(85, 88, 80, 82)}, "stop", 1, 85},
		{"stop and target in one day: the stop, to be safe", []market.Bar{bar(100, 120, 90, 110)}, "stop", 1, 92},
		{"neither: the last close", []market.Bar{bar(100, 105, 95, 101), bar(101, 106, 97, 104)}, "time", 2, 104},
	}
	for _, c := range cases {
		res, b, exit := Outcome(100, c.after, 0.08, 0.15)
		if res != c.result || b != c.bar || math.Abs(exit-c.exit) > 1e-9 {
			t.Errorf("%s: got %s on bar %d at %.2f, want %s on bar %d at %.2f", c.name, res, b, exit, c.result, c.bar, c.exit)
		}
	}
	// $1,000 at 100 with 0.1% a side, sold at 115.
	if got := PnL(100, 115, 0.001); math.Abs(got-147.70) > 0.01 {
		t.Fatalf("PnL = %.2f, want about 147.70", got)
	}
}

func TestAppSaid(t *testing.T) {
	a := AppSaidFrom(true, "BUY", "weak", 0.56, 0.54)
	if a.Lean != "up" || a.Tested || a.Text != "◐ weak evidence · buy call · 56% chance higher in a month (usually 54%)." {
		t.Fatalf("got %+v", a)
	}
	if b := AppSaidFrom(true, "BUY", "validated", 0.6, 0.54); !b.Tested {
		t.Fatal("a validated buy should count as tested")
	}
	if d := AppSaidFrom(true, "HOLD", "weak", 0.4958, 0.54); d.Lean != "" {
		t.Fatalf("49.58%% shows as 50%%, so it isn't a lean: %+v", d)
	}
	if c := AppSaidFrom(false, "", "", 0, 0); c.Available || c.Text == "" {
		t.Fatalf("got %+v", c)
	}
}

// walk makes a price history: a gentle random walk with a big jump every 40 bars.
func walk(n int, seed int64) []market.Bar {
	rng := rand.New(rand.NewSource(seed))
	p := 50.0
	start := time.Date(2010, 1, 4, 0, 0, 0, 0, time.UTC)
	out := make([]market.Bar, n)
	for i := range out {
		r := rng.NormFloat64() * 0.015
		if i%40 == 39 {
			r += 0.12
		}
		o := p
		p *= math.Exp(r)
		out[i] = market.Bar{Date: start.AddDate(0, 0, i).Format("2006-01-02"), Open: o, High: math.Max(o, p) * 1.01, Low: math.Min(o, p) * 0.99, Close: p}
	}
	return out
}

func TestSetupFrom(t *testing.T) {
	bars := walk(400, 3)
	st := SetupFrom(bars, 39+5*40, false) // the day of a big jump
	a := st.Answers()
	if !st.HasBars || !a["bigup"] || a["bigdown"] {
		t.Fatalf("a 12%% jump should read as a big week up: %+v", st)
	}
	if SetupFrom(bars, 10, false).HasBars {
		t.Fatal("too little history should leave the price part unknown")
	}
	if i := BarIndex(bars, bars[100].Date); i != 100 {
		t.Fatalf("BarIndex = %d", i)
	}
}

func sample(i int, win bool, a map[string]bool) Sample {
	return Sample{At: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, i), Win: win, Answers: a}
}

func TestModel(t *testing.T) {
	if r := Model(make([]Sample, 9)); r.Stage != "collecting" || r.Finding != nil {
		t.Fatalf("9 trades: %+v", r)
	}
	// 36 trades: those after a big week mostly lose, the rest mostly win,
	// spread evenly through time.
	var ss []Sample
	for i := range 36 {
		big := i%3 == 0
		win := !big && i%4 != 1 || big && i%12 == 0
		ss = append(ss, sample(i, win, map[string]bool{"bigup": big, "nostop": false, "stock": true}))
	}
	r := Model(ss)
	if r.Stage != "full" || r.Finding == nil || r.Finding.Key != "bigup" || !r.Finding.Worse {
		t.Fatalf("want a full read on big weeks, got %+v %+v", r, r.Finding)
	}
	if r.Finding.Luck >= PassLuck || r.Test == nil || !r.Test.Passed || !r.Clear {
		t.Fatalf("a strong split should pass: luck %.3f test %+v", r.Finding.Luck, r.Test)
	}
	if !strings.HasPrefix(r.Finding.Title, "Your trades bought after an unusually big rise that week tend to lose") {
		t.Fatalf("title: %q", r.Finding.Title)
	}
	if r.Finding.Lo > r.Finding.Rate || r.Finding.Hi < r.Finding.Rate {
		t.Fatalf("the best guess should sit inside its range: %+v", r.Finding)
	}

	// Pure noise: wins unrelated to any question rarely look clear, and
	// never pass the full test.
	rng := rand.New(rand.NewSource(7))
	clear, passed := 0, 0
	for k := range 20 {
		var noise []Sample
		for i := range 30 {
			noise = append(noise, sample(i, rng.Intn(2) == 0, map[string]bool{
				"bigup": rng.Intn(3) == 0, "dip": rng.Intn(3) == 0, "jumpy": rng.Intn(2) == 0, "applean": rng.Intn(2) == 0,
			}))
		}
		r := Model(noise)
		if r.Clear {
			clear++
		}
		if r.Test != nil && r.Test.Passed {
			passed++
		}
		_ = k
	}
	if clear > 7 || passed > 2 {
		t.Fatalf("noise looked clear %d of 20 times and passed %d times", clear, passed)
	}

	// The same trades give the same read.
	a, b := Model(ss), Model(ss)
	if a.Finding.Luck != b.Finding.Luck {
		t.Fatal("the luck check should be repeatable")
	}
}

func TestPracticeFlow(t *testing.T) {
	st := store.OpenTest(t)
	ctx := context.Background()
	u, err := st.CreateAccount(ctx, store.Signup{Email: "practice@example.com", PasswordHash: "x"})
	if err != nil {
		t.Fatal(err)
	}
	views := 0
	svc := &Service{Store: st, Seed: 11, Sync: true,
		Bars: func(_ context.Context, sym string) (*market.Series, error) {
			if sym == "GLD" {
				return nil, errors.New("offline")
			}
			seed := int64(len(sym)*31 + int(sym[0]))
			return &market.Series{Meta: market.Meta{Symbol: sym, Name: sym + " Inc"}, Bars: walk(1600, seed)}, nil
		},
		PastView: func(_ context.Context, sym, day string) (AppSaid, error) {
			views++
			return AppSaidFrom(true, "BUY", "weak", 0.56, 0.54), nil
		},
	}

	st0, err := svc.Practice(ctx, u.ID)
	if err != nil || st0.Set != 0 || st0.Current != nil || st0.Summary != nil {
		t.Fatalf("before any practice: %+v %v", st0, err)
	}
	ps, err := svc.StartPractice(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ps.Set != 1 || ps.Current == nil || ps.Current.N != 1 || views != RoundsPerSet {
		t.Fatalf("want round 1 of set 1 with every day analysed, got set %d current %+v views %d", ps.Set, ps.Current, views)
	}
	cur := ps.Current
	if cur.Symbol != "" || cur.Day != "" || cur.Entry != 0 || len(cur.After) != 0 || cur.Name != "" {
		t.Fatalf("the name, date and outcome must stay hidden before deciding: %+v", cur)
	}
	if len(cur.Before) != beforeBars || cur.Before[len(cur.Before)-1] != 1 || cur.App == nil || cur.Working {
		t.Fatalf("the chart should end at 1 (the entry) and the app's view be ready: %+v", cur)
	}
	// Starting again returns the unfinished set.
	again, _ := svc.StartPractice(ctx, u.ID)
	if again.Set != 1 || again.Current.ID != cur.ID {
		t.Fatalf("an unfinished set should be returned, got %+v", again.Current)
	}

	rows, _ := st.PracticeRounds(ctx, u.ID)
	if _, err := svc.DecideRound(ctx, u.ID, rows[1].ID, "buy", 0.08, 0.15); !errors.Is(err, ErrPractice) {
		t.Fatalf("only the waiting round can be decided, got %v", err)
	}
	if _, err := svc.DecideRound(ctx, u.ID, cur.ID, "maybe", 0.08, 0.15); err == nil {
		t.Fatal("a call other than buy or skip was accepted")
	}
	syms := map[string]bool{}
	for i := range RoundsPerSet {
		call := "buy"
		if i%3 == 2 {
			call = "skip"
		}
		ps, _ := svc.Practice(ctx, u.ID)
		rd, err := svc.DecideRound(ctx, u.ID, ps.Current.ID, call, StopChoices[i%3], TargetChoices[i%3])
		if err != nil {
			t.Fatal(err)
		}
		if rd.Symbol == "" || rd.Day == "" || rd.Entry <= 0 || len(rd.After) == 0 || rd.Result == "" || rd.Headline == "" || rd.AppLine == "" {
			t.Fatalf("a decided round should show the answer: %+v", rd)
		}
		if rd.Symbol == "GLD" {
			t.Fatal("a name without history was used")
		}
		syms[rd.Symbol] = true
		if call == "skip" && !strings.HasPrefix(rd.Headline, "You skipped.") {
			t.Fatalf("skip headline: %q", rd.Headline)
		}
		if _, err := svc.DecideRound(ctx, u.ID, rd.ID, "buy", 0.08, 0.15); !errors.Is(err, ErrPractice) {
			t.Fatal("a round was decided twice")
		}
	}
	if len(syms) < 8 {
		t.Fatalf("a set should mix names, got %v", syms)
	}
	done, _ := svc.Practice(ctx, u.ID)
	if !done.Finished || done.Summary == nil || done.Summary.Rounds != RoundsPerSet || done.Summary.Bought != 7 || done.Done != RoundsPerSet {
		t.Fatalf("want a summary of 10 rounds with 7 buys, got %+v", done.Summary)
	}
	sm := done.Summary
	if sm.Target+sm.Stop+sm.Time != sm.Bought || !strings.HasPrefix(sm.Headline, "You bought 7 of 10 days.") {
		t.Fatalf("summary: %+v", sm)
	}

	// Practice buys count towards the personal model, marked as practice.
	o, err := svc.Overview(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if o.Model.Practice != 7 || o.Model.Paper != 0 || o.Model.Trades != 7 || o.Model.Stage != "collecting" {
		t.Fatalf("model: %+v", o.Model)
	}

	// A new set, then clearing practice.
	next, err := svc.StartPractice(ctx, u.ID)
	if err != nil || next.Set != 2 || next.Current == nil || next.Current.N != 1 || len(next.Rounds) != 1 {
		t.Fatalf("second set: %+v %v", next, err)
	}
	if err := svc.Forget(ctx, u.ID, "practice"); err != nil {
		t.Fatal(err)
	}
	if left, _ := st.PracticeRounds(ctx, u.ID); len(left) != 0 {
		t.Fatalf("%d rounds left after clearing", len(left))
	}
}

func TestSharedByLosers(t *testing.T) {
	mk := func(pnl float64, big bool) store.PracticeRound {
		b := fmt.Sprintf(`{"setup":{"class":"stock","hasBars":true,"week":%v,"weekMove":0.02,"fromHigh":-0.08,"jumpy":1}}`, map[bool]float64{true: 0.12, false: 0.01}[big])
		return store.PracticeRound{Call: "buy", PnL: pnl, StopPct: 0.08, Body: []byte(b)}
	}
	w := sharedByLosers([]store.PracticeRound{mk(-80, true), mk(-80, true), mk(150, false), mk(150, false), mk(20, true)})
	if w == nil || w.Key != "bigup" || !strings.HasPrefix(w.Title, "Both losing buys came after an unusually big rise that week.") {
		t.Fatalf("got %+v", w)
	}
	if sharedByLosers([]store.PracticeRound{mk(-80, true), mk(-80, true)}) != nil {
		t.Fatal("with no winning buys there's nothing to compare")
	}
}

func TestCapFirst(t *testing.T) {
	for in, want := range map[string]string{"15% below": "15% below", "on meme tokens": "On meme tokens", "": "", "éclair": "Éclair"} {
		if got := capFirst(in); got != want {
			t.Errorf("capFirst(%q) = %q, want %q", in, got, want)
		}
	}
}
