package learn

import (
	"math"
	"sort"
	"strings"

	"github.com/sodiq/financial-market/backend/internal/market"
)

// Setup is what a trade's starting point looked like: the price's recent
// path, the plan and what the app said. Unknown parts are left out of the
// comparisons rather than guessed.
type Setup struct {
	Class     string  `json:"class"` // token | crypto | stock
	HasBars   bool    `json:"hasBars"`
	Week      float64 `json:"week"`      // change over the week before
	WeekMove  float64 `json:"weekMove"`  // a typical week's move in the past year (standard deviation)
	FromHigh  float64 `json:"fromHigh"`  // change from the 3-month high (0 at the high, negative below)
	Jumpy     float64 `json:"jumpy"`     // the last month's daily swings ÷ the past year's
	StopPct   float64 `json:"stopPct"`   // how far below the entry the stop was (0: no stop)
	AppKnown  bool    `json:"appKnown"`  // whether what the app said is known
	AppLean   string  `json:"appLean"`   // up | down | "" (no lean)
	AppTested bool    `json:"appTested"` // the app had a tested buy signal
}

// SetupFrom works out the price part of a setup from the bars up to and
// including bar i.
func SetupFrom(bars []market.Bar, i int, crypto bool) Setup {
	st := Setup{Class: "stock"}
	if crypto {
		st.Class = "crypto"
	}
	week, month := 5, 21
	if crypto {
		week, month = 7, 30
	}
	if i < 0 || i >= len(bars) || i < 63 || bars[i].Close <= 0 {
		return st
	}
	c := bars[i].Close
	st.HasBars = true
	if p := bars[i-week].Close; p > 0 {
		st.Week = c/p - 1
	}
	hi := 0.0
	for k := i - 62; k <= i; k++ {
		hi = math.Max(hi, bars[k].Close)
	}
	st.FromHigh = c/hi - 1
	// A typical week and how jumpy the last month was, against the past year.
	from := max(week, i-252)
	var weeks, days, recent []float64
	for k := from; k <= i; k++ {
		if p := bars[k-week].Close; p > 0 && (i-k)%week == 0 {
			weeks = append(weeks, bars[k].Close/p-1)
		}
		if p := bars[k-1].Close; p > 0 {
			r := math.Log(bars[k].Close / p)
			days = append(days, r)
			if k > i-month {
				recent = append(recent, r)
			}
		}
	}
	st.WeekMove = stdev(weeks)
	if all := stdev(days); all > 0 {
		st.Jumpy = stdev(recent) / all
	}
	return st
}

// BarIndex is the last bar on or before a date (-1 if none).
func BarIndex(bars []market.Bar, day string) int {
	return sort.Search(len(bars), func(i int) bool { return bars[i].Date > day }) - 1
}

func stdev(xs []float64) float64 {
	if len(xs) < 2 {
		return 0
	}
	m := 0.0
	for _, x := range xs {
		m += x
	}
	m /= float64(len(xs))
	v := 0.0
	for _, x := range xs {
		v += (x - m) * (x - m)
	}
	return math.Sqrt(v / float64(len(xs)-1))
}

// Cond is a yes-or-no question about a setup the model may compare trades on.
type Cond struct {
	Key     string
	Subject string // "Your trades bought after an unusually big rise that week"
	Short   string // "after an unusually big rise that week"
}

// Conds are the questions, in the order ties are broken.
var Conds = []Cond{
	{"bigup", "Your trades bought after an unusually big rise that week", "after an unusually big rise that week"},
	{"bigdown", "Your trades bought after an unusually big fall that week", "after an unusually big fall that week"},
	{"nearhigh", "Your trades bought near a 3-month high", "near a 3-month high"},
	{"dip", "Your trades bought 15% or more below a 3-month high", "15% or more below a 3-month high"},
	{"jumpy", "Your trades bought when prices were jumpier than usual", "when prices were jumpier than usual"},
	{"tight", "Your trades with a stop 5% or less away", "with a stop 5% or less away"},
	{"nostop", "Your trades with no stop", "with no stop"},
	{"applean", "Your trades bought when the app said buy", "when the app said buy"},
	{"apptested", "Your trades on a tested buy signal", "on a tested buy signal"},
	{"token", "Your meme token trades", "on meme tokens"},
	{"crypto", "Your crypto coin trades", "on crypto coins"},
	{"stock", "Your stock and fund trades", "on stocks or funds"},
}

func condByKey(k string) Cond {
	for _, c := range Conds {
		if c.Key == k {
			return c
		}
	}
	return Cond{Key: k, Subject: k, Short: k}
}

// Answers gives the setup's answer to each question it can answer.
func (st Setup) Answers() map[string]bool {
	a := map[string]bool{}
	if st.HasBars {
		big := math.Max(0.05, 1.5*st.WeekMove)
		a["bigup"] = st.Week >= big
		a["bigdown"] = st.Week <= -big
		a["nearhigh"] = st.FromHigh >= -0.03
		a["dip"] = st.FromHigh <= -0.15
		if st.Jumpy > 0 {
			a["jumpy"] = st.Jumpy >= 1.3
		}
	}
	if st.StopPct > 0 {
		a["tight"] = st.StopPct <= 0.05+1e-9
	}
	a["nostop"] = st.StopPct <= 0
	if st.AppKnown {
		a["applean"] = st.AppLean == "up"
		a["apptested"] = st.AppTested
	}
	for _, c := range []string{"token", "crypto", "stock"} {
		a[c] = st.Class == c
	}
	return a
}

// LeanOf reads a signal like "1 month: BUY" as up, down or no lean.
func LeanOf(signal string) string {
	s := strings.ToUpper(signal)
	switch {
	case strings.Contains(s, "BUY"):
		return "up"
	case strings.Contains(s, "SELL"):
		return "down"
	}
	return ""
}
