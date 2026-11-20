// Package model contains the statistical learners, walk-forward validation
// and accuracy metrics. Everything is pure Go with no external dependencies so
// the maths is auditable line by line.
package model

import (
	"errors"
	"math"
)

// Standardizer z-scores features using statistics from the training rows
// only, and clips extreme values so a single outlier day cannot dominate.
type Standardizer struct {
	Mean, Std []float64
}

const zClip = 5.0

func FitStandardizer(X [][]float64) Standardizer {
	d := len(X[0])
	s := Standardizer{Mean: make([]float64, d), Std: make([]float64, d)}
	n := float64(len(X))
	for _, r := range X {
		for j, v := range r {
			s.Mean[j] += v / n
		}
	}
	for _, r := range X {
		for j, v := range r {
			s.Std[j] += (v - s.Mean[j]) * (v - s.Mean[j])
		}
	}
	for j := range s.Std {
		s.Std[j] = math.Sqrt(s.Std[j] / math.Max(n-1, 1))
		if s.Std[j] < 1e-12 {
			s.Std[j] = 1
		}
	}
	return s
}

func (s Standardizer) Transform(r []float64) []float64 {
	out := make([]float64, len(r))
	for j, v := range r {
		z := (v - s.Mean[j]) / s.Std[j]
		out[j] = math.Max(-zClip, math.Min(zClip, z))
	}
	return out
}

func (s Standardizer) TransformAll(X [][]float64) [][]float64 {
	out := make([][]float64, len(X))
	for i, r := range X {
		out[i] = s.Transform(r)
	}
	return out
}

// Logistic is an L2-regularised logistic regression fitted by Newton/IRLS.
// Inputs must already be standardised. The intercept is not penalised.
type Logistic struct {
	W      []float64 // weights per feature
	B      float64   // intercept
	Lambda float64
}

func sigmoid(z float64) float64 {
	if z >= 0 {
		return 1 / (1 + math.Exp(-z))
	}
	e := math.Exp(z)
	return e / (1 + e)
}

func logit(p float64) float64 {
	p = math.Max(1e-6, math.Min(1-1e-6, p))
	return math.Log(p / (1 - p))
}

// FitLogistic fits with sample weights w (nil = all 1).
func FitLogistic(Z [][]float64, y []float64, w []float64, lambda float64) (*Logistic, error) {
	n := len(Z)
	if n == 0 {
		return nil, errors.New("no rows")
	}
	d := len(Z[0])
	p := d + 1 // last param is intercept
	theta := make([]float64, p)
	// Start the intercept at the base rate.
	sw, sy := 0.0, 0.0
	for i := range y {
		wi := 1.0
		if w != nil {
			wi = w[i]
		}
		sw += wi
		sy += wi * y[i]
	}
	theta[d] = logit(sy / sw)

	H := make([][]float64, p)
	for k := range H {
		H[k] = make([]float64, p)
	}
	g := make([]float64, p)
	for iter := 0; iter < 25; iter++ {
		for k := range H {
			clear(H[k])
		}
		clear(g)
		for i, r := range Z {
			wi := 1.0
			if w != nil {
				wi = w[i]
			}
			z := theta[d]
			for j, v := range r {
				z += theta[j] * v
			}
			pi := sigmoid(z)
			res := wi * (y[i] - pi)
			q := wi * pi * (1 - pi)
			for a := 0; a < p; a++ {
				xa := 1.0
				if a < d {
					xa = r[a]
				}
				g[a] += res * xa
				qa := q * xa
				for b := a; b < p; b++ {
					xb := 1.0
					if b < d {
						xb = r[b]
					}
					H[a][b] += qa * xb
				}
			}
		}
		for a := 0; a < p; a++ {
			for b := 0; b < a; b++ {
				H[a][b] = H[b][a]
			}
		}
		for j := 0; j < d; j++ {
			g[j] -= lambda * theta[j]
			H[j][j] += lambda
		}
		H[d][d] += 1e-9
		step, err := solve(H, g)
		if err != nil {
			return nil, err
		}
		maxStep := 0.0
		for k := range theta {
			theta[k] += step[k]
			maxStep = math.Max(maxStep, math.Abs(step[k]))
		}
		if maxStep < 1e-7 {
			break
		}
	}
	return &Logistic{W: theta[:d], B: theta[d], Lambda: lambda}, nil
}

func (m *Logistic) Logit(z []float64) float64 {
	s := m.B
	for j, v := range z {
		s += m.W[j] * v
	}
	return s
}

func (m *Logistic) Prob(z []float64) float64 { return sigmoid(m.Logit(z)) }

// Contributions returns each feature's additive effect on the log-odds
// relative to an average day (z = 0). This is exact for a linear model, which
// is the main reason logistic regression is the explainable backbone.
func (m *Logistic) Contributions(z []float64) []float64 {
	out := make([]float64, len(z))
	for j, v := range z {
		out[j] = m.W[j] * v
	}
	return out
}

// solve does Gaussian elimination with partial pivoting on a copy.
func solve(A [][]float64, b []float64) ([]float64, error) {
	n := len(b)
	M := make([][]float64, n)
	for i := range A {
		M[i] = append(append([]float64(nil), A[i]...), b[i])
	}
	for col := 0; col < n; col++ {
		piv := col
		for r := col + 1; r < n; r++ {
			if math.Abs(M[r][col]) > math.Abs(M[piv][col]) {
				piv = r
			}
		}
		if math.Abs(M[piv][col]) < 1e-14 {
			return nil, errors.New("singular matrix")
		}
		M[col], M[piv] = M[piv], M[col]
		for r := col + 1; r < n; r++ {
			f := M[r][col] / M[col][col]
			if f == 0 {
				continue
			}
			for k := col; k <= n; k++ {
				M[r][k] -= f * M[col][k]
			}
		}
	}
	x := make([]float64, n)
	for i := n - 1; i >= 0; i-- {
		s := M[i][n]
		for k := i + 1; k < n; k++ {
			s -= M[i][k] * x[k]
		}
		x[i] = s / M[i][i]
	}
	return x, nil
}

// Platt holds a 2-parameter calibration map p' = σ(a + b·logit(p)).
type Platt struct {
	A, B float64
}

func (c Platt) Apply(p float64) float64 { return sigmoid(c.A + c.B*logit(p)) }

// FitPlatt fits the calibration map on out-of-sample predictions. The slope
// is clamped to [0, 1] so calibration can only shrink overconfident models
// toward the base rate, never amplify them.
func FitPlatt(preds, y []float64) Platt {
	if len(preds) < 50 {
		return Platt{A: 0, B: 1}
	}
	Z := make([][]float64, len(preds))
	for i, p := range preds {
		Z[i] = []float64{logit(p)}
	}
	m, err := FitLogistic(Z, y, nil, 1e-6)
	if err != nil {
		return Platt{A: 0, B: 1}
	}
	b := math.Max(0, math.Min(1, m.W[0]))
	// Refit the intercept for the clamped slope.
	a := m.B
	if b != m.W[0] {
		a = 0
		for iter := 0; iter < 50; iter++ {
			g, h := 0.0, 0.0
			for i, p := range preds {
				pi := sigmoid(a + b*logit(p))
				g += y[i] - pi
				h += pi * (1 - pi)
			}
			if h == 0 {
				break
			}
			a += g / h
		}
	}
	return Platt{A: a, B: b}
}
