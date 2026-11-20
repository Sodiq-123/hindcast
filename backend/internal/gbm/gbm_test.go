package gbm

import (
	"math"
	"math/rand/v2"
	"testing"
)

// data draws x uniform in [-1,1]^4. With interaction=true the label depends
// on x0*x1 (an interaction no linear model can see); otherwise it is noise.
func data(n int, interaction bool, seed uint64) ([][]float64, []float64) {
	rng := rand.New(rand.NewPCG(seed, 3))
	X := make([][]float64, n)
	y := make([]float64, n)
	for i := range X {
		x := []float64{rng.Float64()*2 - 1, rng.Float64()*2 - 1, rng.Float64()*2 - 1, rng.Float64()*2 - 1}
		p := 0.5
		if interaction {
			p = 1 / (1 + math.Exp(-3*x[0]*x[1]))
		}
		if rng.Float64() < p {
			y[i] = 1
		}
		X[i] = x
	}
	return X, y
}

func accuracy(m *Model, X [][]float64, y []float64) float64 {
	hit := 0.0
	for i := range X {
		if (m.Prob(X[i]) > 0.5) == (y[i] == 1) {
			hit++
		}
	}
	return hit / float64(len(X))
}

func TestLearnsInteraction(t *testing.T) {
	X, y := data(30000, true, 1)
	vX, vy := data(5000, true, 2)
	tX, ty := data(10000, true, 3)
	m := Fit(X, y, vX, vy, DefaultParams())
	acc := accuracy(m, tX, ty)
	t.Logf("interaction: %d trees, test accuracy %.3f, importance %.2f", len(m.Trees), acc, m.Importance)
	if acc < 0.62 {
		t.Fatalf("failed to learn x0*x1 interaction: %.3f", acc)
	}
	if m.Importance[0]+m.Importance[1] < 0.8 {
		t.Fatalf("importance should concentrate on x0, x1: %v", m.Importance)
	}
}

// On pure noise, early stopping should keep the model close to the base rate.
func TestNoiseStaysFlat(t *testing.T) {
	X, y := data(30000, false, 4)
	vX, vy := data(5000, false, 5)
	m := Fit(X, y, vX, vy, DefaultParams())
	maxDev := 0.0
	tX, _ := data(2000, false, 6)
	for _, x := range tX {
		maxDev = math.Max(maxDev, math.Abs(m.Prob(x)-0.5))
	}
	t.Logf("noise: %d trees kept, max |p-0.5| = %.3f", len(m.Trees), maxDev)
	if maxDev > 0.05 {
		t.Fatalf("model overfits noise: max deviation %.3f", maxDev)
	}
}
