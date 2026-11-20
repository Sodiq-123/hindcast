// Package levels finds Fibonacci retracement levels and support zones on the
// weekly ("higher time frame") chart, proposes an entry zone and stop the way
// many swing traders do, and tests whether those levels have actually
// mattered: every past touch is replayed with only the data available then.
package levels

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"time"
)

// Bar is one weekly bar.
type Bar struct {
	Date                   string // last trading day of the week
	Open, High, Low, Close float64
}

// Weekly groups daily bars into weeks ending on the last trading day.
func Weekly(dates []string, open, high, low, close []float64) []Bar {
	var out []Bar
	var cur *Bar
	var curWeek string
	for i, d := range dates {
		t, err := time.Parse("2006-01-02", d)
		if err != nil {
			continue
		}
		y, w := t.ISOWeek()
		key := fmt.Sprintf("%d-%02d", y, w)
		if cur == nil || key != curWeek {
			out = append(out, Bar{Date: d, Open: open[i], High: high[i], Low: low[i], Close: close[i]})
			cur, curWeek = &out[len(out)-1], key
			continue
		}
		cur.Date, cur.Close = d, close[i]
		cur.High, cur.Low = math.Max(cur.High, high[i]), math.Min(cur.Low, low[i])
	}
	return out
}

// Swing is the major up-leg the retracement is measured on.
type Swing struct {
	LowDate  string  `json:"lowDate"`
	Low      float64 `json:"low"`
	HighDate string  `json:"highDate"`
	High     float64 `json:"high"`
	lowI     int
	highI    int
}

const (
	lookback = 156  // weeks (3 years)
	minLeg   = 0.30 // the up-leg must be at least +30%
)

// swingAt finds the leg as it looked at week w: the highest high of the last
// three years (before this week) and the lowest low before it.
func swingAt(b []Bar, w int) *Swing {
	start := max(0, w-lookback)
	hi := -1
	for i := start; i < w; i++ {
		if hi < 0 || b[i].High > b[hi].High {
			hi = i
		}
	}
	if hi < 0 {
		return nil
	}
	lo := -1
	for i := start; i < hi; i++ {
		if lo < 0 || b[i].Low < b[lo].Low {
			lo = i
		}
	}
	if lo < 0 || hi-lo < 4 || b[lo].Low <= 0 || b[hi].High/b[lo].Low-1 < minLeg {
		return nil
	}
	return &Swing{LowDate: b[lo].Date, Low: b[lo].Low, HighDate: b[hi].Date, High: b[hi].High, lowI: lo, highI: hi}
}

func (s *Swing) price(r float64) float64 { return s.High - r*(s.High-s.Low) }
func (s *Swing) ratio(p float64) float64 { return (s.High - p) / (s.High - s.Low) }

// Level is a retracement level.
type Level struct {
	Ratio float64 `json:"ratio"`
	Price float64 `json:"price"`
	Fib   bool    `json:"fib"`
}

// Zone is a support area made of weekly pivot lows.
type Zone struct {
	Low     float64 `json:"low"`
	High    float64 `json:"high"`
	Touches int     `json:"touches"`
	Last    string  `json:"last"`
}

// supports clusters weekly pivot lows (lower than the 4 weeks either side)
// within 3% of each other.
func supports(b []Bar, from, to int) []Zone {
	var lows []struct {
		p float64
		d string
	}
	for i := max(from, 4); i <= to-4; i++ {
		pivot := true
		for k := i - 4; k <= i+4; k++ {
			if k != i && b[k].Low < b[i].Low {
				pivot = false
				break
			}
		}
		if pivot {
			lows = append(lows, struct {
				p float64
				d string
			}{b[i].Low, b[i].Date})
		}
	}
	sort.Slice(lows, func(i, j int) bool { return lows[i].p < lows[j].p })
	var zones []Zone
	for _, l := range lows {
		if n := len(zones); n > 0 && l.p <= zones[n-1].Low*1.03 {
			z := &zones[n-1]
			z.High, z.Touches = math.Max(z.High, l.p), z.Touches+1
			if l.d > z.Last {
				z.Last = l.d
			}
			continue
		}
		zones = append(zones, Zone{Low: l.p, High: l.p, Touches: 1, Last: l.d})
	}
	return zones
}

// Plan is the entry zone and stop for today, in the style described: start
// buying around the 0.618 retracement, exit if the next major support below
// it breaks.
type Plan struct {
	ZoneLow     float64 `json:"zoneLow"`  // 0.65 retracement
	ZoneHigh    float64 `json:"zoneHigh"` // 0.58 retracement
	Stop        float64 `json:"stop"`
	StopReason  string  `json:"stopReason"`
	RiskFromMid float64 `json:"riskFromMid"` // loss from the middle of the zone to the stop
	State       string  `json:"state"`       // above | in_zone | below
}

// View is everything the chart panel shows for one stock.
type View struct {
	Price       float64   `json:"price"`
	Date        string    `json:"date"`
	Swing       *Swing    `json:"swing,omitempty"`
	Retracement float64   `json:"retracement"` // where the price sits in the leg (0 = high, 1 = low)
	Levels      []Level   `json:"levels"`
	Supports    []Zone    `json:"supports"`
	Plan        *Plan     `json:"plan,omitempty"`
	History     []Event   `json:"history"` // this stock's past 0.618 touches
	Summary     LevelStat `json:"summary"`
	Note        string    `json:"note,omitempty"`
}

var fibRatios = []float64{0.236, 0.382, 0.5, 0.618, 0.786}

// Analyse builds the view for the latest week.
func Analyse(b []Bar) *View {
	if len(b) < 60 {
		return &View{Note: "Not enough weekly history (needs about a year)."}
	}
	w := len(b) - 1
	v := &View{Price: b[w].Close, Date: b[w].Date}
	s := swingAt(b, w)
	v.History = Events(b, 0.618)
	v.Summary = summarise(0.618, v.History)
	if s == nil {
		v.Note = "No major up-leg (+30% or more) in the last three years, so there is no retracement to measure."
		return v
	}
	v.Swing, v.Retracement = s, s.ratio(v.Price)
	for _, r := range fibRatios {
		v.Levels = append(v.Levels, Level{r, s.price(r), true})
	}
	for _, z := range supports(b, max(0, w-lookback), w) {
		if z.High < v.Price*1.25 {
			v.Supports = append(v.Supports, z)
		}
	}
	p := &Plan{ZoneHigh: s.price(0.58), ZoneLow: s.price(0.65)}
	// Next major support below the zone: the highest zone touched at least
	// twice; otherwise the 0.786 level; never below the swing low.
	p.Stop, p.StopReason = s.price(0.786), "0.786 retracement (no support zone with 2+ touches below the 0.618 level)"
	for i := len(v.Supports) - 1; i >= 0; i-- {
		z := v.Supports[i]
		if z.High < p.ZoneLow && z.Touches >= 2 && z.Low > s.Low*0.97 {
			p.Stop, p.StopReason = z.Low*0.99, fmt.Sprintf("1%% below the support zone at %.2f–%.2f (%d weekly lows)", z.Low, z.High, z.Touches)
			break
		}
	}
	mid := (p.ZoneLow + p.ZoneHigh) / 2
	p.RiskFromMid = p.Stop/mid - 1
	switch {
	case v.Price > p.ZoneHigh:
		p.State = "above"
	case v.Price >= p.ZoneLow:
		p.State = "in_zone"
	default:
		p.State = "below"
	}
	v.Plan = p
	return v
}

// Event is one past touch of a retracement level, replayed causally.
type Event struct {
	Date    string  `json:"date"`
	Entry   float64 `json:"entry"`
	Target  float64 `json:"target"`
	Stop    float64 `json:"stop"`
	Outcome string  `json:"outcome"` // bounce | stopped | open
	Weeks   int     `json:"weeks"`
	Ret13w  float64 `json:"ret13w"`
	Ret26w  float64 `json:"ret26w"`
}

const (
	band     = 0.03  // a touch is a weekly close within ±0.03 of the level
	upMove   = 0.236 // "bounce": back up by 0.236 of the leg (0.618 → 0.382)
	downMove = 0.168 // "broken": down by 0.168 of the leg (0.618 → 0.786)
	maxHold  = 52
)

// Events replays every first touch of a level from above, using only data
// available at that week, and records what happened next.
func Events(b []Bar, level float64) []Event {
	var out []Event
	used := map[string]bool{} // one touch per leg
	for w := 1; w < len(b); w++ {
		s := swingAt(b, w)
		if s == nil || used[s.HighDate] {
			continue
		}
		r, prev := s.ratio(b[w].Close), s.ratio(b[w-1].Close)
		if r < level-band || r > level+band || prev >= level-band {
			continue // must arrive from above, into the band
		}
		used[s.HighDate] = true
		e := Event{Date: b[w].Date, Entry: b[w].Close, Target: s.price(level - upMove), Stop: s.price(level + downMove), Outcome: "open",
			Ret13w: math.NaN(), Ret26w: math.NaN()}
		for k := w + 1; k < len(b) && k <= w+maxHold; k++ {
			if b[k].Low <= e.Stop { // a week touching both counts as stopped
				e.Outcome, e.Weeks = "stopped", k-w
				break
			}
			if b[k].High >= e.Target {
				e.Outcome, e.Weeks = "bounce", k-w
				break
			}
		}
		if w+13 < len(b) {
			e.Ret13w = b[w+13].Close/e.Entry - 1
		}
		if w+26 < len(b) {
			e.Ret26w = b[w+26].Close/e.Entry - 1
		}
		out = append(out, e)
	}
	return out
}

// LevelStat summarises touches of one level.
type LevelStat struct {
	Ratio      float64 `json:"ratio"`
	Fib        bool    `json:"fib"`
	Touches    int     `json:"touches"`
	Bounces    int     `json:"bounces"`
	Stopped    int     `json:"stopped"`
	BounceRate float64 `json:"bounceRate"` // bounces ÷ (bounces + stopped)
	Expectancy float64 `json:"expectancy"` // average result in units of the risk to the stop
	Avg13w     float64 `json:"avg13w"`
	Avg26w     float64 `json:"avg26w"`
}

// BreakEven is the bounce rate at which the reward (0.236 of the leg) and the
// risk (0.168) balance.
const BreakEven = downMove / (upMove + downMove)

func summarise(level float64, ev []Event) LevelStat {
	st := LevelStat{Ratio: level, Fib: isFib(level), Touches: len(ev)}
	var s13, s26 float64
	var n13, n26 int
	for _, e := range ev {
		switch e.Outcome {
		case "bounce":
			st.Bounces++
		case "stopped":
			st.Stopped++
		}
		if !math.IsNaN(e.Ret13w) {
			s13 += e.Ret13w
			n13++
		}
		if !math.IsNaN(e.Ret26w) {
			s26 += e.Ret26w
			n26++
		}
	}
	if d := st.Bounces + st.Stopped; d > 0 {
		st.BounceRate = float64(st.Bounces) / float64(d)
		st.Expectancy = st.BounceRate*upMove/downMove - (1 - st.BounceRate)
	}
	if n13 > 0 {
		st.Avg13w = s13 / float64(n13)
	}
	if n26 > 0 {
		st.Avg26w = s26 / float64(n26)
	}
	return st
}

func isFib(r float64) bool {
	for _, f := range fibRatios {
		if math.Abs(f-r) < 1e-9 {
			return true
		}
	}
	return false
}

// MarshalJSON writes missing returns (not enough weeks yet) as null.
func (e Event) MarshalJSON() ([]byte, error) {
	type alias Event
	nf := func(x float64) *float64 {
		if math.IsNaN(x) {
			return nil
		}
		return &x
	}
	return json.Marshal(struct {
		alias
		Ret13w *float64 `json:"ret13w"`
		Ret26w *float64 `json:"ret26w"`
	}{alias(e), nf(e.Ret13w), nf(e.Ret26w)})
}

func (e *Event) UnmarshalJSON(b []byte) error {
	type alias Event
	var v struct {
		alias
		Ret13w *float64 `json:"ret13w"`
		Ret26w *float64 `json:"ret26w"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	*e = Event(v.alias)
	e.Ret13w, e.Ret26w = math.NaN(), math.NaN()
	if v.Ret13w != nil {
		e.Ret13w = *v.Ret13w
	}
	if v.Ret26w != nil {
		e.Ret26w = *v.Ret26w
	}
	return nil
}
