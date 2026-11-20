package levels

import (
	"math"
	"math/rand/v2"
	"testing"
	"time"
)

func walk(n int, seed uint64, bounceAt float64) []Bar {
	rng := rand.New(rand.NewPCG(seed, 9))
	t0 := time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC)
	var b []Bar
	p := 100.0
	for i := 0; i < n; i++ {
		r := 0.04 * rng.NormFloat64()
		if bounceAt > 0 && len(b) > 60 {
			if s := swingAt(b, len(b)); s != nil {
				if x := s.ratio(p); math.Abs(x-bounceAt) < 0.04 {
					r = math.Abs(r) + 0.08 // planted: price jumps off this level
				}
			}
		}
		o := p
		p *= math.Exp(r)
		b = append(b, Bar{Date: t0.AddDate(0, 0, 7*i).Format("2006-01-02"), Open: o, High: math.Max(o, p) * 1.01, Low: math.Min(o, p) * 0.99, Close: p})
	}
	return b
}

func TestRandomWalkHasNoSpecialLevel(t *testing.T) {
	var s [][]Bar
	for i := 0; i < 150; i++ {
		s = append(s, walk(700, uint64(i), 0))
	}
	res := RunTest(s)
	if res.Status == "special" {
		t.Errorf("random walks should show no special 0.618 level: z=%.2f %+v", res.Z, res.Levels)
	}
}

func TestPlantedLevelIsFound(t *testing.T) {
	var s [][]Bar
	for i := 0; i < 150; i++ {
		s = append(s, walk(700, uint64(1000+i), 0.618))
	}
	res := RunTest(s)
	if res.Status != "special" {
		t.Errorf("planted 0.618 bounce should be detected: z=%.2f %+v", res.Z, res.Levels)
	}
}

func TestAnalysePlan(t *testing.T) {
	b := walk(400, 7, 0)
	v := Analyse(b)
	if v.Swing != nil && v.Plan != nil && (v.Plan.Stop >= v.Plan.ZoneLow || v.Plan.ZoneLow >= v.Plan.ZoneHigh) {
		t.Errorf("stop must sit below the entry zone: %+v", v.Plan)
	}
}

func TestWeekly(t *testing.T) {
	d := []string{"2026-09-28", "2026-09-29", "2026-10-02", "2026-10-05"}
	w := Weekly(d, []float64{1, 2, 3, 4}, []float64{2, 5, 3, 4}, []float64{1, 1, 0.5, 4}, []float64{1, 2, 3, 4})
	if len(w) != 2 || w[0].High != 5 || w[0].Low != 0.5 || w[0].Close != 3 || w[0].Date != "2026-10-02" {
		t.Errorf("weekly %+v", w)
	}
}
