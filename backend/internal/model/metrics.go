package model

import (
	"math"
	"sort"
)

// Metrics summarise out-of-sample performance of probability forecasts.
type Metrics struct {
	N            int     `json:"n"`            // OOS predictions scored
	NEffective   int     `json:"nEffective"`   // non-overlapping (independent) periods
	Accuracy     float64 `json:"accuracy"`     // hit rate of p>0.5 ⇒ up
	BaseAccuracy float64 `json:"baseAccuracy"` // always guessing the training majority
	Brier        float64 `json:"brier"`        // mean squared probability error (lower is better)
	BaseBrier    float64 `json:"baseBrier"`    // Brier of always predicting the training base rate
	BrierSkill   float64 `json:"brierSkill"`   // 1 - Brier/BaseBrier; > 0 means real skill
	LogLoss      float64 `json:"logLoss"`
	AUC          float64 `json:"auc"`    // ranking ability; 0.5 = coin flip
	UpRate       float64 `json:"upRate"` // realised fraction of up periods
	PValue       float64 `json:"pValue"` // chance the accuracy edge over baseline is luck
}

// Bucket is one row of a calibration / confidence table.
type Bucket struct {
	Lo        float64 `json:"lo"`
	Hi        float64 `json:"hi"`
	N         int     `json:"n"`
	MeanPred  float64 `json:"meanPred"`
	UpRate    float64 `json:"upRate"`
	AvgReturn float64 `json:"avgReturn"` // mean forward simple return in bucket
}

// ConfidenceRow answers: "when the model was at least this confident, how
// often was it right?"
type ConfidenceRow struct {
	MinEdge  float64 `json:"minEdge"` // |p - 0.5| threshold
	N        int     `json:"n"`
	Accuracy float64 `json:"accuracy"`
	Coverage float64 `json:"coverage"` // share of days that met the threshold
}

func ComputeMetrics(p, y, base []float64, horizon int) Metrics {
	return ComputeMetricsN(p, y, base, len(p)/max(1, horizon))
}

// ComputeMetricsN takes the effective (independent) sample size explicitly.
// Pooled cross-asset forecasts need this: 80 assets on the same day are not
// 80 independent observations.
func ComputeMetricsN(p, y, base []float64, nEff int) Metrics {
	n := len(p)
	m := Metrics{N: n, NEffective: nEff}
	if n == 0 {
		return m
	}
	var hit, baseHit, brier, bbrier, ll, up float64
	for i := range p {
		if (p[i] > 0.5) == (y[i] == 1) {
			hit++
		}
		if (base[i] > 0.5) == (y[i] == 1) {
			baseHit++
		}
		brier += (p[i] - y[i]) * (p[i] - y[i])
		bbrier += (base[i] - y[i]) * (base[i] - y[i])
		pc := math.Max(1e-6, math.Min(1-1e-6, p[i]))
		ll -= y[i]*math.Log(pc) + (1-y[i])*math.Log(1-pc)
		up += y[i]
	}
	fn := float64(n)
	m.Accuracy, m.BaseAccuracy = hit/fn, baseHit/fn
	m.Brier, m.BaseBrier = brier/fn, bbrier/fn
	if m.BaseBrier > 0 {
		m.BrierSkill = 1 - m.Brier/m.BaseBrier
	}
	m.LogLoss = ll / fn
	m.UpRate = up / fn
	m.AUC = AUC(p, y)
	// One-sided z-test of accuracy vs the majority baseline using the
	// effective (non-overlapping) sample size, which is what matters for
	// overlapping multi-day labels.
	ne := float64(max(1, m.NEffective))
	b := math.Max(0.01, math.Min(0.99, m.BaseAccuracy))
	z := (m.Accuracy - m.BaseAccuracy) / math.Sqrt(b*(1-b)/ne)
	m.PValue = 0.5 * math.Erfc(z/math.Sqrt2)
	return m
}

// AUC via the rank-sum (Mann-Whitney) formulation with tie handling.
func AUC(p, y []float64) float64 {
	type pair struct{ p, y float64 }
	ps := make([]pair, len(p))
	for i := range p {
		ps[i] = pair{p[i], y[i]}
	}
	sort.Slice(ps, func(i, j int) bool { return ps[i].p < ps[j].p })
	var pos, neg, rankSum float64
	for i := 0; i < len(ps); {
		j := i
		for j < len(ps) && ps[j].p == ps[i].p {
			j++
		}
		avgRank := float64(i+j+1) / 2
		for k := i; k < j; k++ {
			if ps[k].y == 1 {
				rankSum += avgRank
				pos++
			} else {
				neg++
			}
		}
		i = j
	}
	if pos == 0 || neg == 0 {
		return 0.5
	}
	return (rankSum - pos*(pos+1)/2) / (pos * neg)
}

var bucketEdges = []float64{0, 0.35, 0.4, 0.45, 0.5, 0.55, 0.6, 0.65, 1.0001}

func Calibration(p, y, ret []float64) []Bucket {
	out := make([]Bucket, len(bucketEdges)-1)
	for k := range out {
		out[k].Lo, out[k].Hi = bucketEdges[k], math.Min(1, bucketEdges[k+1])
	}
	for i := range p {
		for k := range out {
			if p[i] >= bucketEdges[k] && p[i] < bucketEdges[k+1] {
				out[k].N++
				out[k].MeanPred += p[i]
				out[k].UpRate += y[i]
				out[k].AvgReturn += math.Exp(ret[i]) - 1
				break
			}
		}
	}
	for k := range out {
		if out[k].N > 0 {
			fn := float64(out[k].N)
			out[k].MeanPred /= fn
			out[k].UpRate /= fn
			out[k].AvgReturn /= fn
		}
	}
	return out
}

func ConfidenceTable(p, y []float64) []ConfidenceRow {
	var out []ConfidenceRow
	for _, e := range []float64{0, 0.025, 0.05, 0.075, 0.1, 0.15, 0.2} {
		row := ConfidenceRow{MinEdge: e}
		hit := 0.0
		for i := range p {
			if math.Abs(p[i]-0.5) >= e && p[i] != 0.5 {
				row.N++
				if (p[i] > 0.5) == (y[i] == 1) {
					hit++
				}
			}
		}
		if row.N > 0 {
			row.Accuracy = hit / float64(row.N)
			row.Coverage = float64(row.N) / float64(len(p))
		}
		out = append(out, row)
	}
	return out
}

// Strategy is a simple, honest trading simulation of the signals:
// non-overlapping holding periods, long when p > 0.5+threshold, otherwise in
// cash, with a per-trade cost. It shows whether the forecasts would have
// made money, which accuracy alone does not.
type Strategy struct {
	Threshold     float64   `json:"threshold"`
	CostPerTrade  float64   `json:"costPerTrade"`
	Periods       int       `json:"periods"`
	Trades        int       `json:"trades"`
	Exposure      float64   `json:"exposure"`
	WinRate       float64   `json:"winRate"`
	TotalReturn   float64   `json:"totalReturn"`
	BuyHoldReturn float64   `json:"buyHoldReturn"`
	CAGR          float64   `json:"cagr"`
	BuyHoldCAGR   float64   `json:"buyHoldCagr"`
	Sharpe        float64   `json:"sharpe"`
	BuyHoldSharpe float64   `json:"buyHoldSharpe"`
	MaxDrawdown   float64   `json:"maxDrawdown"`
	BuyHoldMaxDD  float64   `json:"buyHoldMaxDrawdown"`
	Equity        []float64 `json:"equity"`
	BuyHold       []float64 `json:"buyHold"`
	Dates         []string  `json:"dates"`
}

func Simulate(dates []string, p, ret []float64, horizon int, barsPerYear, threshold, cost float64) Strategy {
	s := Strategy{Threshold: threshold, CostPerTrade: cost}
	eq, bh := 1.0, 1.0
	peak, bhPeak := 1.0, 1.0
	pos := 0.0
	var rs, brs []float64
	wins := 0
	for i := 0; i < len(p); i += horizon {
		want := 0.0
		if p[i] > 0.5+threshold {
			want = 1
		}
		r := math.Exp(ret[i]) - 1
		pr := want * r
		if want != pos {
			pr -= cost
			if want == 1 {
				s.Trades++
			}
		}
		pos = want
		if want == 1 {
			s.Exposure++
			if r > 0 {
				wins++
			}
		}
		eq *= 1 + pr
		bh *= 1 + r
		peak, bhPeak = math.Max(peak, eq), math.Max(bhPeak, bh)
		s.MaxDrawdown = math.Min(s.MaxDrawdown, eq/peak-1)
		s.BuyHoldMaxDD = math.Min(s.BuyHoldMaxDD, bh/bhPeak-1)
		rs, brs = append(rs, pr), append(brs, r)
		s.Equity, s.BuyHold, s.Dates = append(s.Equity, eq), append(s.BuyHold, bh), append(s.Dates, dates[i])
		s.Periods++
	}
	if s.Periods == 0 {
		return s
	}
	if s.Exposure > 0 {
		s.WinRate = float64(wins) / s.Exposure
	}
	s.Exposure /= float64(s.Periods)
	s.TotalReturn, s.BuyHoldReturn = eq-1, bh-1
	years := float64(s.Periods*horizon) / barsPerYear
	if years > 0 {
		s.CAGR = math.Pow(math.Max(eq, 1e-9), 1/years) - 1
		s.BuyHoldCAGR = math.Pow(math.Max(bh, 1e-9), 1/years) - 1
	}
	ppy := barsPerYear / float64(horizon)
	s.Sharpe, s.BuyHoldSharpe = sharpe(rs, ppy), sharpe(brs, ppy)
	return s
}

func sharpe(r []float64, periodsPerYear float64) float64 {
	if len(r) < 2 {
		return 0
	}
	m, v := 0.0, 0.0
	for _, x := range r {
		m += x
	}
	m /= float64(len(r))
	for _, x := range r {
		v += (x - m) * (x - m)
	}
	sd := math.Sqrt(v / float64(len(r)-1))
	if sd == 0 {
		return 0
	}
	return m / sd * math.Sqrt(periodsPerYear)
}

// Quantile of a sample (linear interpolation). Sorts a copy.
func Quantile(x []float64, q float64) float64 {
	if len(x) == 0 {
		return math.NaN()
	}
	s := append([]float64(nil), x...)
	sort.Float64s(s)
	pos := q * float64(len(s)-1)
	lo := int(math.Floor(pos))
	hi := min(lo+1, len(s)-1)
	f := pos - float64(lo)
	return s[lo]*(1-f) + s[hi]*f
}
