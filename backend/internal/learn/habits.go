package learn

import (
	"fmt"
	"math"
	"math/rand"
	"strings"

	"github.com/sodiq/financial-market/backend/internal/market"
)

// Trade is a closed paper trade, as the habits see it.
type Trade struct {
	Name   string
	Kind   string // asset | token
	Symbol string
	Stop   *float64
	Exit   float64
	PnL    float64 // after fees
	PnLPct float64
	Days   int
	Edge   string // tested | untested | ""
}

// Class groups trades for the facts: meme tokens, crypto or stocks and funds.
func Class(kind, symbol string) string {
	switch {
	case kind == "token":
		return "token"
	case market.IsCrypto(symbol):
		return "crypto"
	}
	return "stock"
}

var classPlural = map[string]string{"token": "meme tokens", "crypto": "crypto coins", "stock": "stocks or funds"}

// ClassLabel names a class for the "Not on … trades" switch.
var ClassLabel = map[string]string{"token": "token", "crypto": "crypto", "stock": "stock and fund"}

// Fact is something the record shows outright (a count, not a prediction).
type Fact struct {
	Key    string `json:"key"`
	Title  string `json:"title"`
	Detail string `json:"detail,omitempty"`
	Count  string `json:"count"`
}

// Pattern is a habit that needs enough trades, and a clear enough result,
// before the app names it.
type Pattern struct {
	Key      string  `json:"key"`
	Title    string  `json:"title"`
	Need     string  `json:"need"`     // what it needs, and how far along
	Progress float64 `json:"progress"` // 0..1 towards enough trades
	Ready    bool    `json:"ready"`    // enough trades to check
	Found    bool    `json:"found"`    // checked, and the record clearly shows it
	Text     string  `json:"text,omitempty"`
	Detail   string  `json:"detail,omitempty"`
	Count    string  `json:"count,omitempty"`
}

// Habits is what the journal shows about a person's trading.
type Habits struct {
	Closed   int       `json:"closed"`
	Facts    []Fact    `json:"facts"`
	Patterns []Pattern `json:"patterns"`
	// The worst gap past a stop, so a new plan can be shown what the same gap would cost.
	GapPct  float64 `json:"gapPct"`
	GapName string  `json:"gapName,omitempty"`
	// The class every closed trade so far belongs to ("" if mixed or too few).
	OnlyClass string `json:"onlyClass,omitempty"`
}

// Thresholds for the patterns.
const (
	EachSide    = 8    // trades on each side before comparing tested and untested signals
	MinLosers   = 5    // losing and winning trades before comparing how long each is held
	MinStopHits = 5    // trades that reached the stop before judging how they were closed
	GapCut      = 0.05 // closing this far below the stop counts as "well past it"
	Luck        = 0.05 // the chance of luck below which a difference is named
)

func money(x float64) string {
	s := fmt.Sprintf("$%.2f", math.Abs(x))
	if x < 0 {
		return "−" + s
	}
	return s
}

// HabitsFrom works out the facts and patterns from closed trades.
func HabitsFrom(ts []Trade) Habits {
	h := Habits{Closed: len(ts), Facts: []Fact{}, Patterns: []Pattern{}}
	n := len(ts)
	of := func(k int) string { return fmt.Sprintf("%d of %d trades", k, n) }

	// All one class.
	if n >= 3 {
		c := Class(ts[0].Kind, ts[0].Symbol)
		same := true
		for _, t := range ts {
			same = same && Class(t.Kind, t.Symbol) == c
		}
		if same {
			h.OnlyClass = c
			h.Facts = append(h.Facts, Fact{Key: "class", Title: fmt.Sprintf("All %d of your closed trades were %s.", n, classPlural[c]), Count: of(n)})
		}
	}

	// Gaps past the stop.
	var gaps []Trade
	losers := 0
	for _, t := range ts {
		if t.PnL <= 0 {
			losers++
		}
		if t.Stop != nil && *t.Stop > 0 && t.Exit < *t.Stop*(1-GapCut) {
			gaps = append(gaps, t)
		}
	}
	for _, g := range gaps {
		if gp := 1 - g.Exit / *g.Stop; gp > h.GapPct {
			h.GapPct, h.GapName = gp, g.Name
		}
	}
	switch {
	case len(gaps) == 1 && losers == 1 && gaps[0].PnL <= 0:
		h.Facts = append(h.Facts, Fact{Key: "gap", Title: fmt.Sprintf("Your one loss, %s, closed %.0f%% below its stop.", h.GapName, h.GapPct*100), Count: of(1)})
	case len(gaps) == 1:
		h.Facts = append(h.Facts, Fact{Key: "gap", Title: fmt.Sprintf("%s closed %.0f%% below its stop.", h.GapName, h.GapPct*100), Count: of(1)})
	case len(gaps) > 1:
		h.Facts = append(h.Facts, Fact{Key: "gap", Title: fmt.Sprintf("%d trades closed well below their stops, by up to %.0f%%.", len(gaps), h.GapPct*100), Count: of(len(gaps))})
	}

	// One trade carrying the total.
	total, best := 0.0, -1
	for i, t := range ts {
		total += t.PnL
		if best < 0 || t.PnL > ts[best].PnL {
			best = i
		}
	}
	if n >= 3 && total > 0 && ts[best].PnL > total {
		h.Facts = append(h.Facts, Fact{Key: "carry", Title: fmt.Sprintf("One trade, %s, made more than your whole profit (%s of %s).", ts[best].Name, money(ts[best].PnL), money(total)), Count: of(1)})
	}

	h.Patterns = append(h.Patterns, testedPattern(ts), holdingPattern(ts), stopPattern(ts))
	return h
}

func testedPattern(ts []Trade) Pattern {
	var tested, untested []float64
	var tSum, uSum float64
	for _, t := range ts {
		edge := t.Edge
		if edge == "" && t.Kind == "token" {
			edge = "untested" // tokens never have a tested forecast
		}
		switch edge {
		case "tested":
			tested = append(tested, t.PnLPct)
			tSum += t.PnL
		case "untested":
			untested = append(untested, t.PnLPct)
			uSum += t.PnL
		}
	}
	p := Pattern{Key: "tested", Title: "Tested signals against untested ones",
		Need:     fmt.Sprintf("needs %d trades on each side · you have %d and %d", EachSide, len(tested), len(untested)),
		Progress: math.Min(1, float64(min(len(tested), len(untested)))/EachSide)}
	if len(tested) < EachSide || len(untested) < EachSide {
		return p
	}
	p.Ready = true
	pv := permutationP(tested, untested)
	if pv < Luck {
		p.Found = true
		p.Text = fmt.Sprintf("Your %d trades on signals without a tested edge %s %s in total. Your %d on tested signals %s %s.",
			len(untested), madeLost(uSum), money(math.Abs(uSum)), len(tested), madeLost(tSum), money(math.Abs(tSum)))
		p.Detail = " A gap that size would come from luck less than 1 time in 20."
		p.Count = fmt.Sprintf("%d trades", len(tested)+len(untested))
	}
	return p
}

func madeLost(x float64) string {
	if x < 0 {
		return "lost"
	}
	return "made"
}

func holdingPattern(ts []Trade) Pattern {
	var win, lose []float64
	for _, t := range ts {
		if t.PnL > 0 {
			win = append(win, float64(t.Days))
		} else {
			lose = append(lose, float64(t.Days))
		}
	}
	p := Pattern{Key: "holding", Title: "Holding losers longer than winners",
		Need:     fmt.Sprintf("needs %d losing and %d winning trades · you have %d and %d", MinLosers, MinLosers, len(lose), len(win)),
		Progress: math.Min(1, float64(min(len(lose), len(win)))/MinLosers)}
	if len(win) < MinLosers || len(lose) < MinLosers {
		return p
	}
	p.Ready = true
	if mean(lose) > mean(win) && permutationP(lose, win) < Luck {
		p.Found = true
		p.Text = fmt.Sprintf("You held losing trades for %.0f days on average, and winners for %.0f.", mean(lose), mean(win))
		p.Detail = " Cutting losers sooner is usually the quickest fix."
		p.Count = fmt.Sprintf("%d trades", len(win)+len(lose))
	}
	return p
}

func stopPattern(ts []Trade) Pattern {
	reached, past := 0, 0
	for _, t := range ts {
		if t.Stop == nil || *t.Stop <= 0 {
			continue
		}
		if t.Exit <= *t.Stop {
			reached++
			if t.Exit < *t.Stop*(1-GapCut) {
				past++
			}
		}
	}
	p := Pattern{Key: "stop", Title: "Closing well past your stop",
		Need:     fmt.Sprintf("needs %d trades that reached the stop · you have %d", MinStopHits, reached),
		Progress: math.Min(1, float64(reached)/MinStopHits)}
	if reached < MinStopHits {
		return p
	}
	p.Ready = true
	if past*2 > reached {
		p.Found = true
		p.Text = fmt.Sprintf("%d of your %d trades that reached their stop closed more than %.0f%% below it.", past, reached, GapCut*100)
		p.Detail = " Either the stops aren’t being followed, or these markets jump past them; smaller positions help with both."
		p.Count = fmt.Sprintf("%d trades", reached)
	}
	return p
}

func mean(x []float64) float64 {
	if len(x) == 0 {
		return 0
	}
	s := 0.0
	for _, v := range x {
		s += v
	}
	return s / float64(len(x))
}

// permutationP is the two-sided chance that shuffling the labels gives a
// difference in means at least as large as the one seen. The seed is fixed,
// so the same record always gives the same answer.
func permutationP(a, b []float64) float64 {
	obs := math.Abs(mean(a) - mean(b))
	all := append(append([]float64{}, a...), b...)
	rng := rand.New(rand.NewSource(1))
	const rounds = 5000
	hits := 0
	for range rounds {
		rng.Shuffle(len(all), func(i, j int) { all[i], all[j] = all[j], all[i] })
		if math.Abs(mean(all[:len(a)])-mean(all[len(a):])) >= obs-1e-12 {
			hits++
		}
	}
	return float64(hits+1) / float64(rounds+1)
}

// Reminder is one "Worth a look" line in the trade form.
type Reminder struct {
	Key    string `json:"key"`
	Title  string `json:"title"`
	Detail string `json:"detail,omitempty"`
	Count  string `json:"count"`
}

// Reminders picks what's worth a look before a new trade of this class.
func Reminders(h Habits, class string) []Reminder {
	out := []Reminder{}
	for _, f := range h.Facts {
		switch f.Key {
		case "class":
			if h.OnlyClass == class {
				out = append(out, Reminder{f.Key, f.Title, fmt.Sprintf(" This is the %s. Your record can’t show yet how your plan does on anything else.", ordinal(h.Closed+1)), f.Count})
			}
		case "gap":
			out = append(out, Reminder{f.Key, f.Title, "", f.Count}) // the trade form adds what the same gap would cost
		}
	}
	for _, p := range h.Patterns {
		if p.Found {
			out = append(out, Reminder{p.Key, p.Text, p.Detail, p.Count})
		}
	}
	return out
}

func ordinal(n int) string {
	suf := "th"
	if n%100 < 11 || n%100 > 13 {
		switch n % 10 {
		case 1:
			suf = "st"
		case 2:
			suf = "nd"
		case 3:
			suf = "rd"
		}
	}
	return fmt.Sprintf("%d%s", n, suf)
}

// Muted reports whether reminders are off for a class.
func Muted(muted []string, class string) bool {
	for _, m := range muted {
		if strings.EqualFold(m, class) {
			return true
		}
	}
	return false
}
