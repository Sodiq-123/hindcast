package indicators

import (
	"math"
	"testing"
)

func approx(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

func TestSMA(t *testing.T) {
	got := SMA([]float64{1, 2, 3, 4, 5}, 3)
	if !math.IsNaN(got[1]) || got[2] != 2 || got[4] != 4 {
		t.Fatalf("SMA wrong: %v", got)
	}
}

func TestSMARecoversAfterNaN(t *testing.T) {
	got := SMA([]float64{NaN, NaN, 1, 2, 3, 4}, 2)
	if !math.IsNaN(got[2]) || got[3] != 1.5 || got[5] != 3.5 {
		t.Fatalf("SMA must resume after leading NaN: %v", got)
	}
}

func TestRSIBounds(t *testing.T) {
	up := make([]float64, 50)
	for i := range up {
		up[i] = float64(i + 1)
	}
	if r := RSI(up, 14); r[49] != 100 {
		t.Fatalf("monotonic up series must have RSI 100, got %v", r[49])
	}
	zig := make([]float64, 200)
	for i := range zig {
		zig[i] = 100 + float64(i%2)
	}
	if r := RSI(zig, 14); !approx(r[199], 50, 2) {
		t.Fatalf("alternating series RSI should be ~50, got %v", r[199])
	}
}

// Indicators must be causal: changing a future value must not change any
// past output. This is the single most important property for honest
// backtests.
func TestCausality(t *testing.T) {
	n := 300
	c := make([]float64, n)
	h := make([]float64, n)
	l := make([]float64, n)
	for i := range c {
		c[i] = 100 + 10*math.Sin(float64(i)/7) + float64(i)*0.1
		h[i], l[i] = c[i]+1, c[i]-1
	}
	base := func(c, h, l []float64) [][]float64 {
		_, _, hist := MACD(c, 12, 26, 9)
		adx, _, _ := ADX(h, l, c, 14)
		return [][]float64{RSI(c, 14), hist, adx, BollingerZ(c, 20), ATR(h, l, c, 14), StochK(h, l, c, 14), Slope(c, 20)}
	}
	before := base(c, h, l)
	c2 := append([]float64(nil), c...)
	h2 := append([]float64(nil), h...)
	l2 := append([]float64(nil), l...)
	c2[n-1] *= 1.5
	h2[n-1] *= 1.5
	l2[n-1] *= 1.5
	after := base(c2, h2, l2)
	for k := range before {
		for i := 0; i < n-1; i++ {
			a, b := before[k][i], after[k][i]
			if !(math.IsNaN(a) && math.IsNaN(b)) && a != b {
				t.Fatalf("indicator %d leaked future data at %d: %v vs %v", k, i, a, b)
			}
		}
	}
}
