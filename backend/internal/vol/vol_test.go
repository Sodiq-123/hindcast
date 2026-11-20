package vol

import (
	"math"
	"math/rand/v2"
	"testing"
)

func simulate(n int, garch bool, seed uint64) []float64 {
	rng := rand.New(rand.NewPCG(seed, 1))
	c := make([]float64, n)
	c[0] = 100
	v := 0.0001 // daily variance (1% moves)
	r := 0.0
	for i := 1; i < n; i++ {
		if garch {
			v = 0.000002 + 0.10*r*r + 0.88*v
		}
		r = math.Sqrt(v) * rng.NormFloat64()
		c[i] = c[i-1] * math.Exp(r)
	}
	return c
}

func TestHARBeatsNaiveOnClusteredVolatility(t *testing.T) {
	c := simulate(6000, true, 7)
	for _, h := range []int{1, 5, 21} {
		f := Model(c, nil, h)
		if f == nil {
			t.Fatalf("h=%d: nil forecast", h)
		}
		e := f.Eval
		t.Logf("h=%d qlike %.4f naive %.4f r2 %.3f naive %.3f cov %.3f naive %.3f better %v", h, e.QLike, e.NaiveQLike, e.R2, e.NaiveR2, e.Coverage, e.NaiveCoverage, e.Better)
		if !e.Better {
			t.Errorf("h=%d: HAR should beat 63-day volatility on GARCH data", h)
		}
		if e.Coverage < 0.72 || e.Coverage > 0.88 {
			t.Errorf("h=%d: 80%% range coverage %.3f", h, e.Coverage)
		}
	}
}

// With constant volatility the 63-day estimate is just noise around the true
// level; HAR's intercept shrinks towards the long-run level, so it should be
// close to the truth and keep honest ranges.
func TestConstantVolatility(t *testing.T) {
	c := simulate(6000, false, 3)
	f := Model(c, nil, 5)
	if math.Abs(f.Daily-0.01) > 0.0015 {
		t.Errorf("daily σ forecast %.4f, want ≈ 0.01", f.Daily)
	}
	if math.Abs(f.Eval.Coverage-0.8) > 0.05 {
		t.Errorf("coverage %.3f", f.Eval.Coverage)
	}
}

func TestShortHistory(t *testing.T) {
	if Model(simulate(500, true, 1), nil, 5) != nil {
		t.Error("expected nil for short history")
	}
}
