package fundamentals

import "math"

// Item is one stock in the backtest.
type Item struct {
	Company *Company
	Prices  Prices
	Member  func(date string) bool // in the universe on date (nil = always)
}

type GroupStats struct {
	N         int     `json:"n"`
	Excess6m  float64 `json:"excess6m"`  // average 6-month return minus the universe average
	Excess12m float64 `json:"excess12m"` // same over 12 months
	Hit12m    float64 `json:"hit12m"`    // share that beat the universe over 12 months
	Trap12m   float64 `json:"trap12m"`   // share that fell a further 20%+ within 12 months
}

type Portfolio struct {
	Months      int       `json:"months"`     // months with at least one signal
	MeanExcess  float64   `json:"meanExcess"` // average monthly return over the universe
	TStat       float64   `json:"tStat"`      // on non-overlapping monthly returns
	HitRate     float64   `json:"hitRate"`    // months beating the universe
	CAGR        float64   `json:"cagr"`
	BenchCAGR   float64   `json:"benchCagr"`
	MaxDrawdown float64   `json:"maxDrawdown"`
	BenchMaxDD  float64   `json:"benchMaxDrawdown"`
	AvgHoldings float64   `json:"avgHoldings"`
	Equity      []float64 `json:"equity"`
	Bench       []float64 `json:"bench"`
	Dates       []string  `json:"dates"`
}

// Backtest is the historical record of the pattern.
type Backtest struct {
	From       string                `json:"from"`
	To         string                `json:"to"`
	Stocks     int                   `json:"stocks"`
	Months     int                   `json:"months"`
	Groups     map[string]GroupStats `json:"groups"` // signal | dip_only | all
	Portfolio  Portfolio             `json:"portfolio"`
	DipOnly    Portfolio             `json:"dipOnly"` // same portfolio from the price drop alone
	Status     string                `json:"status"`
	StatusNote string                `json:"statusNote"`
	Rules      Rules                 `json:"rules"`
	Notes      []string              `json:"notes"`
}

// RunBacktest evaluates the rules at every month end, point in time.
func RunBacktest(items []Item, r Rules, from string) *Backtest {
	bt := &Backtest{Groups: map[string]GroupStats{}, Rules: r}
	// Month ends from the longest price history.
	var ref Prices
	for _, it := range items {
		if len(it.Prices.Dates) > len(ref.Dates) {
			ref = it.Prices
		}
	}
	var months []string
	for i := 1; i < len(ref.Dates); i++ {
		if ref.Dates[i][:7] != ref.Dates[i-1][:7] && ref.Dates[i-1] >= from {
			months = append(months, ref.Dates[i-1])
		}
	}
	type obs struct {
		signal, dip bool
		f1, f6, f12 float64
		ok6, ok12   bool
	}
	acc := map[string]*struct {
		n                 int
		e6, e12, hit, trp float64
		n6, n12           int
	}{"signal": {}, "dip_only": {}, "all": {}}
	var sigRets, dipRets, benchRets []float64
	var holdings float64
	stocks := map[string]bool{}
	for _, d := range months {
		var rows []obs
		for _, it := range items {
			if it.Member != nil && !it.Member(d) {
				continue
			}
			_, t := it.Prices.At(d)
			if t < 0 || dayGap(it.Prices.Dates[t], d) > 5 || t+21 >= len(it.Prices.Closes) {
				continue
			}
			c := Compare(it.Company, it.Prices, t, r)
			if c == nil {
				continue
			}
			p := it.Prices.Closes
			o := obs{signal: c.Signal, dip: c.FromPeak <= -r.MinDrop, f1: p[t+21]/p[t] - 1}
			if t+126 < len(p) {
				o.f6, o.ok6 = p[t+126]/p[t]-1, true
			}
			if t+252 < len(p) {
				o.f12, o.ok12 = p[t+252]/p[t]-1, true
			}
			rows = append(rows, o)
			stocks[it.Company.Symbol] = true
		}
		if len(rows) < 20 {
			continue
		}
		bt.Months++
		var u1, u6, u12 float64
		var n6, n12 int
		for _, o := range rows {
			u1 += o.f1
			if o.ok6 {
				u6 += o.f6
				n6++
			}
			if o.ok12 {
				u12 += o.f12
				n12++
			}
		}
		u1 /= float64(len(rows))
		if n6 > 0 {
			u6 /= float64(n6)
		}
		if n12 > 0 {
			u12 /= float64(n12)
		}
		var s1, d1 float64
		var ns, nd int
		for _, o := range rows {
			groups := []string{"all"}
			if o.signal {
				groups = append(groups, "signal")
				s1 += o.f1
				ns++
			}
			if o.dip {
				d1 += o.f1
				nd++
				if !o.signal {
					groups = append(groups, "dip_only")
				}
			}
			for _, g := range groups {
				a := acc[g]
				a.n++
				if o.ok6 {
					a.e6 += o.f6 - u6
					a.n6++
				}
				if o.ok12 {
					a.e12 += o.f12 - u12
					a.n12++
					if o.f12 > u12 {
						a.hit++
					}
					if o.f12 < -0.20 {
						a.trp++
					}
				}
			}
		}
		benchRets = append(benchRets, u1)
		sigRets = append(sigRets, nanIfZero(s1, ns))
		dipRets = append(dipRets, nanIfZero(d1, nd))
		holdings += float64(ns)
		if bt.From == "" {
			bt.From = d
		}
		bt.To = d
	}
	bt.Stocks = len(stocks)
	for g, a := range acc {
		gs := GroupStats{N: a.n}
		if a.n6 > 0 {
			gs.Excess6m = a.e6 / float64(a.n6)
		}
		// 12-month figures use the observations that have 12 months of future.
		if a.n12 > 0 {
			gs.Excess12m, gs.Hit12m, gs.Trap12m = a.e12/float64(a.n12), a.hit/float64(a.n12), a.trp/float64(a.n12)
		}
		bt.Groups[g] = gs
	}
	bt.Portfolio = portfolio(sigRets, benchRets, months)
	if bt.Portfolio.Months > 0 {
		bt.Portfolio.AvgHoldings = holdings / float64(bt.Portfolio.Months)
	}
	bt.DipOnly = portfolio(dipRets, benchRets, months)
	p := bt.Portfolio
	switch {
	case p.Months >= 36 && p.TStat >= 2 && p.MeanExcess > 0 && p.CAGR > p.BenchCAGR:
		bt.Status = "validated"
		bt.StatusNote = "Stocks matching the pattern beat the rest of the index out-of-sample, with statistical significance."
	case p.MeanExcess > 0 && p.CAGR > p.BenchCAGR:
		bt.Status = "weak"
		bt.StatusNote = "Stocks matching the pattern did better than the rest on average, but not reliably enough to rule out luck."
	default:
		bt.Status = "no_edge"
		bt.StatusNote = "Stocks matching the pattern did not beat the rest of the index."
	}
	return bt
}

func nanIfZero(sum float64, n int) float64 {
	if n == 0 {
		return math.NaN()
	}
	return sum / float64(n)
}

func dayGap(a, b string) int {
	d := days(a, b)
	if d < 0 {
		return -d
	}
	return d
}

// portfolio holds the signal stocks for one month at a time (equal weight),
// in cash-equivalent "universe" months when there are none, and compares it
// with holding the whole universe.
func portfolio(rets, bench []float64, months []string) Portfolio {
	var p Portfolio
	eq, bq, peak, bpeak := 1.0, 1.0, 1.0, 1.0
	var ex []float64
	for i := range rets {
		r := rets[i]
		if math.IsNaN(r) {
			r = bench[i] // no signal this month: hold the universe
		} else {
			ex = append(ex, rets[i]-bench[i])
		}
		eq *= 1 + r
		bq *= 1 + bench[i]
		peak, bpeak = math.Max(peak, eq), math.Max(bpeak, bq)
		p.MaxDrawdown = math.Min(p.MaxDrawdown, eq/peak-1)
		p.BenchMaxDD = math.Min(p.BenchMaxDD, bq/bpeak-1)
		p.Equity, p.Bench = append(p.Equity, eq), append(p.Bench, bq)
		if i < len(months) {
			p.Dates = append(p.Dates, months[i])
		}
	}
	p.Months = len(ex)
	if n := float64(len(rets)); n > 0 {
		p.CAGR = math.Pow(eq, 12/n) - 1
		p.BenchCAGR = math.Pow(bq, 12/n) - 1
	}
	if len(ex) > 1 {
		m, s := meanStd(ex)
		p.MeanExcess = m
		if s > 0 {
			p.TStat = m / s * math.Sqrt(float64(len(ex)))
		}
		hit := 0
		for _, x := range ex {
			if x > 0 {
				hit++
			}
		}
		p.HitRate = float64(hit) / float64(len(ex))
	}
	return p
}

func meanStd(x []float64) (float64, float64) {
	m := 0.0
	for _, v := range x {
		m += v
	}
	m /= float64(len(x))
	s := 0.0
	for _, v := range x {
		s += (v - m) * (v - m)
	}
	return m, math.Sqrt(s / float64(len(x)-1))
}
