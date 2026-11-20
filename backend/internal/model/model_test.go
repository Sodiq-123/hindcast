package model

import (
	"math"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/sodiq/financial-market/backend/internal/features"
	"github.com/sodiq/financial-market/backend/internal/market"
)

// synth builds a price series whose daily returns follow
// r_t = phi * r_{t-1} + sigma * e_t. phi = 0 is a pure random walk.
func synth(n int, phi float64, seed uint64) *market.Series {
	rng := rand.New(rand.NewPCG(seed, seed*7+1))
	s := &market.Series{Meta: market.Meta{Symbol: "SYN", Class: market.ClassEquity}}
	price, prev := 100.0, 0.0
	d := time.Date(1995, 1, 2, 0, 0, 0, 0, time.UTC)
	for i := 0; i < n; i++ {
		r := phi*prev + 0.012*rng.NormFloat64()
		prev = r
		o := price
		price *= math.Exp(r)
		hi := math.Max(o, price) * (1 + 0.004*math.Abs(rng.NormFloat64()))
		lo := math.Min(o, price) * (1 - 0.004*math.Abs(rng.NormFloat64()))
		s.Bars = append(s.Bars, market.Bar{Date: d.Format("2006-01-02"), Open: o, High: hi, Low: lo, Close: price, Volume: 1e6 * (1 + rng.Float64())})
		d = d.AddDate(0, 0, 1)
		for d.Weekday() == time.Saturday || d.Weekday() == time.Sunday {
			d = d.AddDate(0, 0, 1)
		}
	}
	return s
}

func TestLogisticRecoversSignal(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	var Z [][]float64
	var y []float64
	for i := 0; i < 4000; i++ {
		x := []float64{rng.NormFloat64(), rng.NormFloat64()}
		p := sigmoid(1.5*x[0] - 0.0*x[1])
		yi := 0.0
		if rng.Float64() < p {
			yi = 1
		}
		Z, y = append(Z, x), append(y, yi)
	}
	m, err := FitLogistic(Z, y, nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(m.W[0]-1.5) > 0.2 || math.Abs(m.W[1]) > 0.15 {
		t.Fatalf("bad coefficients %v", m.W)
	}
}

// On a pure random walk there is nothing to predict. The validation must
// not report a statistically significant edge — if it did, every "edge" it
// reports on real data would be suspect.
func TestNoFalseEdgeOnRandomWalk(t *testing.T) {
	falsePositives := 0
	for seed := uint64(1); seed <= 4; seed++ {
		m := features.Build(features.Inputs{Asset: synth(3500, 0, seed)})
		res, err := Run(m, 1, 252, DefaultConfig())
		if err != nil {
			t.Fatal(err)
		}
		e := res.Validation.Ensemble
		t.Logf("seed %d: acc=%.3f base=%.3f bss=%.4f auc=%.3f p=%.3f", seed, e.Accuracy, e.BaseAccuracy, e.BrierSkill, e.AUC, e.PValue)
		if e.BrierSkill > 0.005 && e.PValue < 0.01 {
			falsePositives++
		}
	}
	if falsePositives > 0 {
		t.Fatalf("model claimed a significant edge on %d random walks", falsePositives)
	}
}

// With genuine short-term mean reversion planted, the validation must find it.
func TestFindsRealSignal(t *testing.T) {
	m := features.Build(features.Inputs{Asset: synth(3500, -0.25, 9)})
	res, err := Run(m, 1, 252, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	e := res.Validation.Ensemble
	t.Logf("acc=%.3f bss=%.4f auc=%.3f p=%.4f", e.Accuracy, e.BrierSkill, e.AUC, e.PValue)
	if e.AUC < 0.56 || e.BrierSkill <= 0 || e.PValue > 0.01 {
		t.Fatalf("failed to detect planted signal: %+v", e)
	}
	if res.Live == nil || res.Live.PCalibrated <= 0 || res.Live.PCalibrated >= 1 {
		t.Fatalf("bad live forecast: %+v", res.Live)
	}
}

func TestPurgedTrainingRows(t *testing.T) {
	ds := &Dataset{Idx: make([]int, 100)}
	for i := range ds.Idx {
		ds.Idx[i] = i + 300
	}
	rows := trainRows(ds, 50, 5, 1000)
	if last := ds.Idx[rows[len(rows)-1]]; last+5 > ds.Idx[50] {
		t.Fatalf("training label window overlaps test bar: %d+5 > %d", last, ds.Idx[50])
	}
}
