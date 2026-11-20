// Package indicators implements causal technical indicators. Every output
// slice is aligned with the input; value i only uses data at or before i.
// Warm-up positions are NaN.
package indicators

import "math"

var NaN = math.NaN()

func nanSlice(n int) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = NaN
	}
	return out
}

// SMA is the simple moving average. Windows containing NaN yield NaN, and a
// NaN never contaminates later windows (series like VIX start mid-history).
func SMA(x []float64, n int) []float64 {
	out := nanSlice(len(x))
	sum, nans := 0.0, 0
	for i, v := range x {
		if math.IsNaN(v) {
			nans++
		} else {
			sum += v
		}
		if i >= n {
			if old := x[i-n]; math.IsNaN(old) {
				nans--
			} else {
				sum -= old
			}
		}
		if i >= n-1 && nans == 0 {
			out[i] = sum / float64(n)
		}
	}
	return out
}

func EMA(x []float64, n int) []float64 {
	out := nanSlice(len(x))
	if len(x) < n {
		return out
	}
	k := 2.0 / float64(n+1)
	seed := 0.0
	for i := 0; i < n; i++ {
		seed += x[i]
	}
	out[n-1] = seed / float64(n)
	for i := n; i < len(x); i++ {
		out[i] = x[i]*k + out[i-1]*(1-k)
	}
	return out
}

// wilder smooths with alpha = 1/n, seeded with a simple mean.
func wilder(x []float64, n, start int) []float64 {
	out := nanSlice(len(x))
	if len(x) < start+n {
		return out
	}
	s := 0.0
	for i := start; i < start+n; i++ {
		s += x[i]
	}
	out[start+n-1] = s / float64(n)
	for i := start + n; i < len(x); i++ {
		out[i] = (out[i-1]*float64(n-1) + x[i]) / float64(n)
	}
	return out
}

// RSI is Wilder's Relative Strength Index (0-100).
func RSI(close []float64, n int) []float64 {
	gains := make([]float64, len(close))
	losses := make([]float64, len(close))
	for i := 1; i < len(close); i++ {
		d := close[i] - close[i-1]
		if d > 0 {
			gains[i] = d
		} else {
			losses[i] = -d
		}
	}
	ag, al := wilder(gains, n, 1), wilder(losses, n, 1)
	out := nanSlice(len(close))
	for i := range close {
		if math.IsNaN(ag[i]) {
			continue
		}
		if al[i] == 0 {
			out[i] = 100
			continue
		}
		rs := ag[i] / al[i]
		out[i] = 100 - 100/(1+rs)
	}
	return out
}

// MACD returns the MACD line, signal line and histogram.
func MACD(close []float64, fast, slow, signal int) (line, sig, hist []float64) {
	ef, es := EMA(close, fast), EMA(close, slow)
	line = nanSlice(len(close))
	first := -1
	for i := range close {
		if !math.IsNaN(ef[i]) && !math.IsNaN(es[i]) {
			line[i] = ef[i] - es[i]
			if first < 0 {
				first = i
			}
		}
	}
	sig = nanSlice(len(close))
	hist = nanSlice(len(close))
	if first < 0 {
		return
	}
	s := EMA(line[first:], signal)
	for i, v := range s {
		sig[first+i] = v
		if !math.IsNaN(v) {
			hist[first+i] = line[first+i] - v
		}
	}
	return
}

// RollingStd is the sample standard deviation over n points.
func RollingStd(x []float64, n int) []float64 {
	out := nanSlice(len(x))
	for i := n - 1; i < len(x); i++ {
		m := 0.0
		ok := true
		for j := i - n + 1; j <= i; j++ {
			if math.IsNaN(x[j]) {
				ok = false
				break
			}
			m += x[j]
		}
		if !ok {
			continue
		}
		m /= float64(n)
		v := 0.0
		for j := i - n + 1; j <= i; j++ {
			v += (x[j] - m) * (x[j] - m)
		}
		out[i] = math.Sqrt(v / float64(n-1))
	}
	return out
}

// BollingerZ is (close - SMA(n)) / StdDev(n): how many standard deviations
// price sits from its moving average.
func BollingerZ(close []float64, n int) []float64 {
	m, s := SMA(close, n), RollingStd(close, n)
	out := nanSlice(len(close))
	for i := range close {
		if s[i] > 0 {
			out[i] = (close[i] - m[i]) / s[i]
		}
	}
	return out
}

func trueRange(high, low, close []float64) []float64 {
	tr := make([]float64, len(close))
	for i := range close {
		if i == 0 {
			tr[i] = high[i] - low[i]
			continue
		}
		tr[i] = math.Max(high[i]-low[i], math.Max(math.Abs(high[i]-close[i-1]), math.Abs(low[i]-close[i-1])))
	}
	return tr
}

// ATR is Wilder's Average True Range.
func ATR(high, low, close []float64, n int) []float64 {
	return wilder(trueRange(high, low, close), n, 1)
}

// ADX measures trend strength (0-100) regardless of direction. Also returns
// +DI and -DI so callers can tell direction.
func ADX(high, low, close []float64, n int) (adx, plusDI, minusDI []float64) {
	l := len(close)
	pdm, mdm := make([]float64, l), make([]float64, l)
	for i := 1; i < l; i++ {
		up, down := high[i]-high[i-1], low[i-1]-low[i]
		if up > down && up > 0 {
			pdm[i] = up
		}
		if down > up && down > 0 {
			mdm[i] = down
		}
	}
	atr := wilder(trueRange(high, low, close), n, 1)
	sp, sm := wilder(pdm, n, 1), wilder(mdm, n, 1)
	plusDI, minusDI = nanSlice(l), nanSlice(l)
	dx := nanSlice(l)
	first := -1
	for i := 0; i < l; i++ {
		if math.IsNaN(atr[i]) || atr[i] == 0 {
			continue
		}
		plusDI[i] = 100 * sp[i] / atr[i]
		minusDI[i] = 100 * sm[i] / atr[i]
		if s := plusDI[i] + minusDI[i]; s > 0 {
			dx[i] = 100 * math.Abs(plusDI[i]-minusDI[i]) / s
			if first < 0 {
				first = i
			}
		}
	}
	adx = nanSlice(l)
	if first < 0 {
		return
	}
	a := wilder(dx[first:], n, 0)
	copy(adx[first:], a)
	return
}

// StochK is the fast stochastic %K (0-100): where the close sits in the
// recent high-low range.
func StochK(high, low, close []float64, n int) []float64 {
	out := nanSlice(len(close))
	for i := n - 1; i < len(close); i++ {
		hh, ll := math.Inf(-1), math.Inf(1)
		for j := i - n + 1; j <= i; j++ {
			hh = math.Max(hh, high[j])
			ll = math.Min(ll, low[j])
		}
		if hh > ll {
			out[i] = 100 * (close[i] - ll) / (hh - ll)
		} else {
			out[i] = 50
		}
	}
	return out
}

// OBV is On-Balance Volume.
func OBV(close, volume []float64) []float64 {
	out := make([]float64, len(close))
	for i := 1; i < len(close); i++ {
		switch {
		case close[i] > close[i-1]:
			out[i] = out[i-1] + volume[i]
		case close[i] < close[i-1]:
			out[i] = out[i-1] - volume[i]
		default:
			out[i] = out[i-1]
		}
	}
	return out
}

// LogReturns returns r[i] = ln(x[i]/x[i-1]); r[0] = NaN.
func LogReturns(x []float64) []float64 {
	out := nanSlice(len(x))
	for i := 1; i < len(x); i++ {
		if x[i] > 0 && x[i-1] > 0 {
			out[i] = math.Log(x[i] / x[i-1])
		}
	}
	return out
}

// LogReturnN returns ln(x[i]/x[i-n]).
func LogReturnN(x []float64, n int) []float64 {
	out := nanSlice(len(x))
	for i := n; i < len(x); i++ {
		if x[i] > 0 && x[i-n] > 0 {
			out[i] = math.Log(x[i] / x[i-n])
		}
	}
	return out
}

func RollingMax(x []float64, n int) []float64 {
	out := nanSlice(len(x))
	for i := n - 1; i < len(x); i++ {
		m := math.Inf(-1)
		for j := i - n + 1; j <= i; j++ {
			m = math.Max(m, x[j])
		}
		out[i] = m
	}
	return out
}

func RollingMin(x []float64, n int) []float64 {
	out := nanSlice(len(x))
	for i := n - 1; i < len(x); i++ {
		m := math.Inf(1)
		for j := i - n + 1; j <= i; j++ {
			m = math.Min(m, x[j])
		}
		out[i] = m
	}
	return out
}

// ZScore is (x - rolling mean) / rolling std over n.
func ZScore(x []float64, n int) []float64 {
	m, s := SMA(x, n), RollingStd(x, n)
	out := nanSlice(len(x))
	for i := range x {
		if s[i] > 0 && !math.IsNaN(m[i]) {
			out[i] = (x[i] - m[i]) / s[i]
		}
	}
	return out
}

// Slope is the OLS slope of x over the last n points, divided by the
// absolute mean so it is comparable across scales (fraction per bar).
func Slope(x []float64, n int) []float64 {
	out := nanSlice(len(x))
	xm := float64(n-1) / 2
	den := 0.0
	for k := 0; k < n; k++ {
		den += (float64(k) - xm) * (float64(k) - xm)
	}
	for i := n - 1; i < len(x); i++ {
		ym := 0.0
		for j := i - n + 1; j <= i; j++ {
			ym += x[j]
		}
		ym /= float64(n)
		num := 0.0
		for k := 0; k < n; k++ {
			num += (float64(k) - xm) * (x[i-n+1+k] - ym)
		}
		scale := math.Abs(ym)
		if scale == 0 {
			continue
		}
		out[i] = num / den / scale
	}
	return out
}
