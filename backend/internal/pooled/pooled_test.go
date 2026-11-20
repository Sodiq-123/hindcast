package pooled

import (
	"fmt"
	"math"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/sodiq/financial-market/backend/internal/features"
	"github.com/sodiq/financial-market/backend/internal/market"
)

// universe builds n synthetic assets on a shared weekday calendar ending
// today. Each has a slowly wandering drift; with signal=true momentum is
// therefore informative across assets, with signal=false drifts are zero.
func universe(n, bars int, signal bool, seed uint64) []*Asset {
	rng := rand.New(rand.NewPCG(seed, 7))
	var dates []string
	d := time.Now().UTC()
	for len(dates) < bars {
		if d.Weekday() != time.Saturday && d.Weekday() != time.Sunday {
			dates = append(dates, d.Format("2006-01-02"))
		}
		d = d.AddDate(0, 0, -1)
	}
	for i, j := 0, len(dates)-1; i < j; i, j = i+1, j-1 {
		dates[i], dates[j] = dates[j], dates[i]
	}
	var out []*Asset
	for a := 0; a < n; a++ {
		s := &market.Series{Meta: market.Meta{Symbol: fmt.Sprintf("S%02d", a), Class: market.ClassEquity}}
		p, mu := 100.0, 0.0
		for _, dt := range dates {
			if signal {
				mu = 0.995*mu + 0.00012*rng.NormFloat64()
			}
			o := p
			p *= math.Exp(mu + 0.015*rng.NormFloat64())
			s.Bars = append(s.Bars, market.Bar{Date: dt, Open: o, High: math.Max(o, p) * 1.002, Low: math.Min(o, p) * 0.998, Close: p, Volume: 1e6 * (1 + rng.Float64())})
		}
		m := features.Build(features.Inputs{Asset: s})
		out = append(out, FromMatrix(s.Meta.Symbol, s.Meta.Symbol, market.ClassEquity, m))
	}
	return out
}

var month = market.Horizon{Key: "1m", Label: "Next month", Bars: 21}

func TestRankFindsPlantedMomentum(t *testing.T) {
	m, err := TrainRank("test", month, universe(16, 3200, true, 1), DefaultDirConfig())
	if err != nil {
		t.Fatal(err)
	}
	v := m.Validation
	t.Logf("signal: IC=%.3f t=%.2f top=%.4f status=%s cagr=%.3f bench=%.3f", v.MeanIC, v.ICTStat, v.TopExcess, v.Status, v.Strategy.CAGR, v.Strategy.BenchCAGR)
	if v.MeanIC < 0.05 || v.ICTStat < 2 {
		t.Fatalf("ranking failed to find planted momentum: %+v", v)
	}
	if len(m.Live) != 16 || m.Live[0].Rank != 1 || m.Live[0].POutperform < m.Live[15].POutperform {
		t.Fatalf("bad live ranking: %+v", m.Live)
	}
}

func TestRankNoFalseEdge(t *testing.T) {
	bad := 0
	for seed := uint64(2); seed <= 4; seed++ {
		m, err := TrainRank("test", month, universe(16, 3200, false, seed), DefaultDirConfig())
		if err != nil {
			t.Fatal(err)
		}
		v := m.Validation
		t.Logf("noise %d: IC=%.3f t=%.2f status=%s", seed, v.MeanIC, v.ICTStat, v.Status)
		if v.Status == "validated" {
			bad++
		}
	}
	if bad > 0 {
		t.Fatalf("ranking validated an edge on pure noise %d times", bad)
	}
}

func TestDirectionPooled(t *testing.T) {
	week := market.Horizon{Key: "1w", Label: "Next week", Bars: 5}
	noise, err := TrainDir(PoolEquity, week, universe(12, 3000, false, 9), DefaultDirConfig())
	if err != nil {
		t.Fatal(err)
	}
	nv := noise.Validation
	t.Logf("noise: acc=%.3f bss=%.4f auc=%.3f p=%.3f", nv.Accuracy, nv.BrierSkill, nv.AUC, nv.PValue)
	if nv.BrierSkill > 0.005 && nv.PValue < 0.01 {
		t.Fatalf("pooled direction claims an edge on noise: %+v", nv)
	}
	// Applying to a member asset gives out-of-sample scores only inside fold windows.
	u := universe(12, 3000, false, 9)
	s := &market.Series{Meta: market.Meta{Symbol: "S00", Class: market.ClassEquity}}
	_ = s
	a := u[0]
	mat := &features.Matrix{Dates: a.Dates, Close: a.Close, All: a.Raw}
	ap := ApplyDir(noise, "S00", market.ClassEquity, mat)
	covered := 0
	for t2, p := range ap.OOS {
		if !math.IsNaN(p) {
			covered++
			if a.Dates[t2] < noise.Folds[0].From {
				t.Fatalf("OOS score before first test window at %s", a.Dates[t2])
			}
		}
	}
	if covered == 0 || math.IsNaN(ap.Live) || !ap.InSample {
		t.Fatalf("apply failed: covered=%d live=%v inSample=%v", covered, ap.Live, ap.InSample)
	}
}

func TestRollingZ(t *testing.T) {
	x := make([]float64, 300)
	for i := range x {
		x[i] = float64(i % 10)
	}
	x[5] = math.NaN()
	z := rollingZ(x, 100, 20)
	if !math.IsNaN(z[5]) || math.IsNaN(z[250]) || math.Abs(z[250]) > 3 {
		t.Fatalf("rollingZ wrong: z5=%v z250=%v", z[5], z[250])
	}
}

// A rising market where assets differ only in beta: a raw "beat the median"
// model would happily pick the high-beta names. Beta-neutral labels must not
// call that skill.
func TestBetaIsNotAlpha(t *testing.T) {
	rng := rand.New(rand.NewPCG(11, 7))
	var dates []string
	d := time.Now().UTC()
	for len(dates) < 3200 {
		if d.Weekday() != time.Saturday && d.Weekday() != time.Sunday {
			dates = append(dates, d.Format("2006-01-02"))
		}
		d = d.AddDate(0, 0, -1)
	}
	for i, j := 0, len(dates)-1; i < j; i, j = i+1, j-1 {
		dates[i], dates[j] = dates[j], dates[i]
	}
	mkt := make([]float64, len(dates))
	for i := range mkt {
		mkt[i] = 0.0008 + 0.01*rng.NormFloat64() // strong bull market
	}
	var assets []*Asset
	for a := 0; a < 16; a++ {
		beta := 0.4 + 0.12*float64(a)
		s := &market.Series{Meta: market.Meta{Symbol: fmt.Sprintf("B%02d", a), Class: market.ClassEquity}}
		p := 100.0
		for i, dt := range dates {
			o := p
			p *= math.Exp(beta*mkt[i] + 0.012*rng.NormFloat64())
			s.Bars = append(s.Bars, market.Bar{Date: dt, Open: o, High: math.Max(o, p) * 1.002, Low: math.Min(o, p) * 0.998, Close: p, Volume: 1e6})
		}
		assets = append(assets, FromMatrix(s.Meta.Symbol, s.Meta.Symbol, market.ClassEquity, features.Build(features.Inputs{Asset: s})))
	}
	m, err := TrainRank("beta", month, assets, DefaultDirConfig())
	if err != nil {
		t.Fatal(err)
	}
	v := m.Validation
	t.Logf("beta-only: IC=%.3f t=%.2f topExcess=%.4f topAlpha=%.4f topBeta=%.2f status=%s", v.MeanIC, v.ICTStat, v.TopExcess, v.TopAlpha, v.TopBeta, v.Status)
	if v.Status == "validated" {
		t.Fatal("picking high-beta names in a bull market must not count as a validated edge")
	}
}

func TestMembershipFilter(t *testing.T) {
	a := &Asset{Member: [][2]string{{"2020-01-01", "2020-12-31"}, {"2023-01-01", ""}}}
	for date, want := range map[string]bool{"2019-06-01": false, "2020-06-01": true, "2021-06-01": false, "2024-06-01": true} {
		if a.IsMember(date) != want {
			t.Errorf("IsMember(%s) = %v", date, !want)
		}
	}
}
