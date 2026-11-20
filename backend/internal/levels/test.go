package levels

import "math"

// TestLevels are the retracement levels compared: Fibonacci ones and
// non-Fibonacci ones in between.
var TestLevels = []float64{0.382, 0.45, 0.5, 0.55, 0.618, 0.68, 0.75, 0.786}

// Test is the cross-stock record of every level.
type Test struct {
	Stocks     int         `json:"stocks"`
	From       string      `json:"from"`
	To         string      `json:"to"`
	Levels     []LevelStat `json:"levels"`
	BreakEven  float64     `json:"breakEven"`
	Neighbours LevelStat   `json:"neighbours"` // 0.55 and 0.68 pooled
	Z          float64     `json:"z"`          // 0.618 bounce rate vs neighbours (two-proportion z)
	Status     string      `json:"status"`     // special | not_special | worse
	Note       string      `json:"note"`
}

// eventsAll is Events for several levels, computing each week's swing once.
func eventsAll(b []Bar, lv []float64) map[float64][]Event {
	out := map[float64][]Event{}
	used := map[float64]map[string]bool{}
	for _, l := range lv {
		used[l] = map[string]bool{}
	}
	for w := 1; w < len(b); w++ {
		s := swingAt(b, w)
		if s == nil {
			continue
		}
		r, prev := s.ratio(b[w].Close), s.ratio(b[w-1].Close)
		for _, level := range lv {
			if used[level][s.HighDate] || r < level-band || r > level+band || prev >= level-band {
				continue
			}
			used[level][s.HighDate] = true
			e := Event{Date: b[w].Date, Entry: b[w].Close, Target: s.price(level - upMove), Stop: s.price(level + downMove), Outcome: "open",
				Ret13w: math.NaN(), Ret26w: math.NaN()}
			for k := w + 1; k < len(b) && k <= w+maxHold; k++ {
				if b[k].Low <= e.Stop {
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
			out[level] = append(out[level], e)
		}
	}
	return out
}

// RunTest replays every level touch across many stocks.
func RunTest(series [][]Bar) *Test {
	t := &Test{BreakEven: BreakEven}
	all := map[float64][]Event{}
	for _, b := range series {
		if len(b) < 160 {
			continue
		}
		t.Stocks++
		if t.From == "" || b[0].Date < t.From {
			t.From = b[0].Date
		}
		if last := b[len(b)-1].Date; last > t.To {
			t.To = last
		}
		for l, ev := range eventsAll(b, TestLevels) {
			all[l] = append(all[l], ev...)
		}
	}
	for _, l := range TestLevels {
		t.Levels = append(t.Levels, summarise(l, all[l]))
	}
	var nb []Event
	nb = append(nb, all[0.55]...)
	nb = append(nb, all[0.68]...)
	t.Neighbours = summarise(0, nb)
	f := summarise(0.618, all[0.618])
	n1, n2 := float64(f.Bounces+f.Stopped), float64(t.Neighbours.Bounces+t.Neighbours.Stopped)
	if n1 > 0 && n2 > 0 {
		p := (float64(f.Bounces) + float64(t.Neighbours.Bounces)) / (n1 + n2)
		if se := math.Sqrt(p * (1 - p) * (1/n1 + 1/n2)); se > 0 {
			t.Z = (f.BounceRate - t.Neighbours.BounceRate) / se
		}
	}
	switch {
	case t.Z >= 2:
		t.Status = "special"
		t.Note = "Prices bounced at the 0.618 level significantly more often than at the nearby non-Fibonacci levels."
	case t.Z <= -2:
		t.Status = "worse"
		t.Note = "Prices bounced at the 0.618 level significantly LESS often than at the nearby non-Fibonacci levels."
	default:
		t.Status = "not_special"
		t.Note = "The 0.618 level behaved like the nearby non-Fibonacci levels: no evidence it is special."
	}
	return t
}
