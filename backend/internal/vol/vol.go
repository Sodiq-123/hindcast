// Package vol forecasts volatility. Direction is mostly unpredictable but the
// size of moves is not: volatility clusters, so recent turbulence predicts
// near-term turbulence. The model is a HAR (heterogeneous autoregressive)
// regression of future realised variance on daily, weekly and quarterly
// realised variance, fitted directly for each horizon and scored walk-forward
// against the engine's previous scaling (63-day historical volatility).
package vol

import (
	"math"
	"sort"
)

const (
	eps       = 1e-10
	minTrain  = 3 * 252 // bars of fitted rows before the first test point
	testBars  = 5 * 252 // walk-forward test window (most recent years)
	refitStep = 63      // refit quarterly inside the test window
)

var windows = [3]int{5, 22, 66}

// Eval is the walk-forward record of the HAR forecast against the naive one.
type Eval struct {
	N             int     `json:"n"`             // test forecasts
	Independent   int     `json:"independent"`   // non-overlapping forecasts (N / horizon)
	QLike         float64 `json:"qlike"`         // mean QLIKE loss (lower is better)
	NaiveQLike    float64 `json:"naiveQlike"`    // same for 63-day historical volatility
	R2            float64 `json:"r2"`            // R² of log realised variance
	NaiveR2       float64 `json:"naiveR2"`       // same for the naive forecast
	Coverage      float64 `json:"coverage"`      // share of outcomes inside the 10–90% range (target 80%)
	NaiveCoverage float64 `json:"naiveCoverage"` // same with naive scaling
	Better        bool    `json:"better"`        // HAR beat naive on loss without worse range coverage
	TestFrom      string  `json:"testFrom,omitempty"`
}

// Forecast is the volatility outlook for one horizon.
type Forecast struct {
	Horizon      int     `json:"horizon"`      // bars
	Daily        float64 `json:"daily"`        // forecast typical daily move (σ of daily log return)
	Over         float64 `json:"over"`         // forecast σ of the log return over the whole horizon
	NaiveDaily   float64 `json:"naiveDaily"`   // 63-day historical daily σ
	LongRunDaily float64 `json:"longRunDaily"` // average daily σ over the full history
	Eval         Eval    `json:"eval"`
}

type series struct {
	r2   []float64 // cumulative sum of squared log returns: r2[i] = Σ r_k², k ≤ i
	logp []float64
	n    int
}

func newSeries(closes []float64) *series {
	s := &series{n: len(closes), r2: make([]float64, len(closes)), logp: make([]float64, len(closes))}
	for i, c := range closes {
		s.logp[i] = math.Log(c)
		if i > 0 {
			r := s.logp[i] - s.logp[i-1]
			if math.IsNaN(r) || math.IsInf(r, 0) {
				r = 0
			}
			s.r2[i] = s.r2[i-1] + r*r
		}
	}
	return s
}

// mean squared return over bars (a, b].
func (s *series) rv(a, b int) float64 { return (s.r2[b] - s.r2[a]) / float64(b-a) }

func (s *series) x(t int) [4]float64 {
	return [4]float64{1, math.Log(s.rv(t-windows[0], t) + eps), math.Log(s.rv(t-windows[1], t) + eps), math.Log(s.rv(t-windows[2], t) + eps)}
}

// naive is the engine's previous scaling: 63-bar historical variance.
func (s *series) naive(t int) float64 { return s.rv(t-63, t) }

type fit struct {
	beta  [4]float64
	smear float64 // mean exp(residual): bias correction from log to level
	q10   float64 // quantiles of standardised outcomes in the training rows
	q90   float64
	nq10  float64 // same with naive scaling
	nq90  float64
}

func (f *fit) predict(x [4]float64) float64 { return math.Exp(logDot(f.beta, x)) * f.smear }

// train fits the rows t in [lo, hi] (each needs bars up to t+h).
func (s *series) train(lo, hi, h int) *fit {
	var xtx [4][4]float64
	var xty [4]float64
	for t := lo; t <= hi; t++ {
		x := s.x(t)
		y := math.Log(s.rv(t, t+h) + eps)
		for i := range 4 {
			xty[i] += x[i] * y
			for j := range 4 {
				xtx[i][j] += x[i] * x[j]
			}
		}
	}
	for i := 1; i < 4; i++ {
		xtx[i][i] += 1e-6 * float64(hi-lo+1)
	}
	f := &fit{beta: solve4(xtx, xty), smear: 1}
	var sm float64
	z, nz := make([]float64, 0, hi-lo+1), make([]float64, 0, hi-lo+1)
	for t := lo; t <= hi; t++ {
		sm += (s.rv(t, t+h) + eps) / math.Exp(logDot(f.beta, s.x(t)))
	}
	f.smear = sm / float64(hi-lo+1)
	for t := lo; t <= hi; t++ {
		ret := s.logp[t+h] - s.logp[t]
		z = append(z, ret/math.Sqrt(f.predict(s.x(t))*float64(h)))
		nz = append(nz, ret/math.Sqrt((s.naive(t)+eps)*float64(h)))
	}
	f.q10, f.q90 = quantile(z, 0.10), quantile(z, 0.90)
	f.nq10, f.nq90 = quantile(nz, 0.10), quantile(nz, 0.90)
	return f
}

func logDot(b, x [4]float64) float64 {
	v := 0.0
	for j := range x {
		v += b[j] * x[j]
	}
	return v
}

// Model fits the HAR forecast for an h-bar horizon on a daily close series,
// scores it walk-forward and returns today's forecast. It returns nil when the
// history is too short to fit.
func Model(closes []float64, dates []string, h int) *Forecast {
	s := newSeries(closes)
	first := windows[2] + 1
	last := s.n - 1
	if h < 1 || last-h-first < minTrain {
		return nil
	}
	fc := &Forecast{Horizon: h}

	// Walk-forward evaluation over the most recent years, refitting quarterly
	// on rows whose outcome was fully known at the time (purged by h bars).
	start := max(first+minTrain+h, last-h-testBars)
	var ql, nql, ss, nss, tss, inside, ninside float64
	var ly []float64
	var lf, nlf []float64
	for b := start; b+h <= last; b += refitStep {
		f := s.train(first, b-h, h)
		for t := b; t < b+refitStep && t+h <= last; t++ {
			y := s.rv(t, t+h) + eps
			p := f.predict(s.x(t))
			np := s.naive(t) + eps
			ql += y/p - math.Log(y/p) - 1
			nql += y/np - math.Log(y/np) - 1
			ly, lf, nlf = append(ly, math.Log(y)), append(lf, math.Log(p)), append(nlf, math.Log(np))
			ret := s.logp[t+h] - s.logp[t]
			if z := ret / math.Sqrt(p*float64(h)); z >= f.q10 && z <= f.q90 {
				inside++
			}
			if z := ret / math.Sqrt(np*float64(h)); z >= f.nq10 && z <= f.nq90 {
				ninside++
			}
		}
	}
	n := len(ly)
	if n > 0 {
		m := 0.0
		for _, v := range ly {
			m += v
		}
		m /= float64(n)
		for i := range ly {
			ss += (ly[i] - lf[i]) * (ly[i] - lf[i])
			nss += (ly[i] - nlf[i]) * (ly[i] - nlf[i])
			tss += (ly[i] - m) * (ly[i] - m)
		}
		e := &fc.Eval
		e.N, e.Independent = n, n/h
		e.QLike, e.NaiveQLike = ql/float64(n), nql/float64(n)
		if tss > 0 {
			e.R2, e.NaiveR2 = 1-ss/tss, 1-nss/tss
		}
		e.Coverage, e.NaiveCoverage = inside/float64(n), ninside/float64(n)
		if start < len(dates) {
			e.TestFrom = dates[start]
		}
		// HAR must lower the forecast loss (QLIKE, the standard proper loss
		// for variance forecasts) by at least 2%, be judged on at least 20
		// non-overlapping periods, and not make the 80% ranges measurably
		// worse calibrated (more than 1.5 standard errors of a coverage rate).
		se := math.Sqrt(0.8 * 0.2 / float64(max(1, e.Independent)))
		e.Better = e.Independent >= 20 && e.QLike < 0.98*e.NaiveQLike &&
			math.Abs(e.Coverage-0.8) <= math.Abs(e.NaiveCoverage-0.8)+1.5*se
	}

	f := s.train(first, last-h, h)
	fc.Daily = math.Sqrt(f.predict(s.x(last)))
	fc.Over = fc.Daily * math.Sqrt(float64(h))
	fc.NaiveDaily = math.Sqrt(s.naive(last))
	fc.LongRunDaily = math.Sqrt(s.rv(0, last))
	return fc
}

func quantile(x []float64, q float64) float64 {
	if len(x) == 0 {
		return math.NaN()
	}
	c := append([]float64(nil), x...)
	sort.Float64s(c)
	pos := q * float64(len(c)-1)
	i := int(pos)
	if i+1 >= len(c) {
		return c[i]
	}
	return c[i] + (pos-float64(i))*(c[i+1]-c[i])
}

// solve4 solves a 4×4 linear system by Gaussian elimination with pivoting.
func solve4(a [4][4]float64, b [4]float64) [4]float64 {
	for c := range 4 {
		p := c
		for r := c + 1; r < 4; r++ {
			if math.Abs(a[r][c]) > math.Abs(a[p][c]) {
				p = r
			}
		}
		a[c], a[p] = a[p], a[c]
		b[c], b[p] = b[p], b[c]
		if a[c][c] == 0 {
			continue
		}
		for r := c + 1; r < 4; r++ {
			k := a[r][c] / a[c][c]
			for j := c; j < 4; j++ {
				a[r][j] -= k * a[c][j]
			}
			b[r] -= k * b[c]
		}
	}
	var x [4]float64
	for r := 3; r >= 0; r-- {
		if a[r][r] == 0 {
			continue
		}
		v := b[r]
		for j := r + 1; j < 4; j++ {
			v -= a[r][j] * x[j]
		}
		x[r] = v / a[r][r]
	}
	return x
}
