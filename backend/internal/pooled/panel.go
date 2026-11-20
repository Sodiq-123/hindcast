// Package pooled trains models across many assets at once:
//
//   - Direction models (one per asset class and horizon) answer "will this
//     go up?" using ~80 assets' history instead of one, then join each
//     asset's own forecast as a third ensemble member.
//   - Ranking models (one per universe and horizon) answer "which of these
//     will beat the others?", the question behind "what should I buy next".
//
// Both are walk-forward validated with purged training windows, exactly like
// the per-asset models, and persisted to disk so analyses can use them
// instantly.
package pooled

import (
	"math"
	"sort"

	"github.com/sodiq/financial-market/backend/internal/features"
	"github.com/sodiq/financial-market/backend/internal/market"
)

// Asset is one instrument prepared for pooled training.
type Asset struct {
	Symbol string
	Name   string
	Class  market.AssetClass
	Dates  []string
	Close  []float64
	Raw    map[string][]float64 // raw feature values by key
	Z      map[string][]float64 // per-asset rolling z-scores (direction models)
	// Member holds [start, end] date ranges when the asset belonged to its
	// universe ("" end = still a member); nil = always a member. Ranking only
	// uses an asset on dates it was actually in the index, so today's
	// winners aren't ranked against a past they weren't part of.
	Member  [][2]string
	beta    []float64 // trailing 1-year beta to the universe, aligned to Dates
	dateIdx map[string]int
}

func (a *Asset) IsMember(date string) bool {
	if a.Member == nil {
		return true
	}
	for _, iv := range a.Member {
		if date >= iv[0] && (iv[1] == "" || date <= iv[1]) {
			return true
		}
	}
	return false
}

// noRollZ lists inputs that are already bounded/normalised; rolling
// z-scores of mostly-zero event features would explode.
var noRollZ = map[string]bool{"earn_soon": true, "pead": true, "earn_react": true, "beat4": true, "vix_z": true, "fng": true,
	"skew_z": true, "vvix_z": true, "funding_z": true}

const (
	zWindow = 504 // two years
	zMinObs = 126
	zClip   = 5.0
)

// FromMatrix converts a feature matrix into a pooled-model asset.
func FromMatrix(symbol, name string, class market.AssetClass, m *features.Matrix) *Asset {
	a := &Asset{Symbol: symbol, Name: name, Class: class, Dates: m.Dates, Close: m.Close, Raw: m.All, Z: map[string][]float64{}}
	for k, v := range m.All {
		if noRollZ[k] {
			a.Z[k] = v
		} else {
			a.Z[k] = rollingZ(v, zWindow, zMinObs)
		}
	}
	// Derived: risk-adjusted momentum, a cleaner cross-sectional signal than
	// raw momentum because it doesn't just pick the most volatile names.
	if mom, vol := a.Raw["mom_12_1"], a.Raw["vol_21"]; mom != nil && vol != nil {
		mv := make([]float64, len(mom))
		for i := range mv {
			if vol[i] > 0 {
				mv[i] = mom[i] / vol[i]
			} else {
				mv[i] = math.NaN()
			}
		}
		a.Raw["mom_vol"] = mv
	}
	a.dateIdx = make(map[string]int, len(a.Dates))
	for i, d := range a.Dates {
		a.dateIdx[d] = i
	}
	return a
}

// FromMatrixCompact keeps only what the ranking model needs (raw values of
// the ranking inputs, from `from` onwards) so hundreds of stocks fit in
// memory. Features were computed on the full history, so slicing is safe.
func FromMatrixCompact(symbol, name string, class market.AssetClass, m *features.Matrix, from string) *Asset {
	start := sort.SearchStrings(m.Dates, from)
	keep := append([]string{"ret_1", "mom_12_1", "vol_21"}, RankKeys...)
	a := &Asset{Symbol: symbol, Name: name, Class: class, Raw: map[string][]float64{}}
	a.Dates = append([]string(nil), m.Dates[start:]...)
	a.Close = append([]float64(nil), m.Close[start:]...)
	for _, k := range keep {
		if col, ok := m.All[k]; ok {
			a.Raw[k] = append([]float64(nil), col[start:]...)
		}
	}
	if mom, vol := a.Raw["mom_12_1"], a.Raw["vol_21"]; mom != nil && vol != nil {
		mv := make([]float64, len(mom))
		for i := range mv {
			mv[i] = math.NaN()
			if vol[i] > 0 {
				mv[i] = mom[i] / vol[i]
			}
		}
		a.Raw["mom_vol"] = mv
	}
	a.dateIdx = make(map[string]int, len(a.Dates))
	for i, d := range a.Dates {
		a.dateIdx[d] = i
	}
	return a
}

// rollingZ is a causal rolling z-score that skips NaNs and never lets one
// poison later windows.
func rollingZ(x []float64, win, minObs int) []float64 {
	out := make([]float64, len(x))
	var sum, sq float64
	cnt := 0
	for i, v := range x {
		if !math.IsNaN(v) {
			sum += v
			sq += v * v
			cnt++
		}
		if i >= win {
			if o := x[i-win]; !math.IsNaN(o) {
				sum -= o
				sq -= o * o
				cnt--
			}
		}
		out[i] = math.NaN()
		if math.IsNaN(v) || cnt < minObs {
			continue
		}
		mean := sum / float64(cnt)
		vr := (sq - float64(cnt)*mean*mean) / float64(cnt-1)
		if vr <= 1e-18 {
			out[i] = 0
			continue
		}
		out[i] = math.Max(-zClip, math.Min(zClip, (v-mean)/math.Sqrt(vr)))
	}
	return out
}

// Fold is one fitted model plus the test window it was evaluated on.
// Pooled inputs are already scale-free (rolling z-scores, cross-sectional
// ranks, bounded event scores), so no standardiser is stored.
type Fold struct {
	From string    `json:"from"`
	To   string    `json:"to"`
	W    []float64 `json:"w"`
	B    float64   `json:"b"`
}

func (f *Fold) Prob(x []float64) float64 {
	z := f.B
	for j, v := range x {
		z += f.W[j] * v
	}
	return 1 / (1 + math.Exp(-z))
}

// row is one (asset, date) training example.
type row struct {
	asset    int
	t        int
	date     string
	labelEnd string
	x        []float64
	y        float64
	fwd      float64 // simple forward return
	resid    float64 // forward return minus beta × universe return (ranking)
	beta     float64
}

// dateBlocks splits sorted unique dates from index `first` into ~n blocks.
func dateBlocks(dates []string, first, n int) [][2]string {
	if first >= len(dates) {
		return nil
	}
	span := len(dates) - first
	step := max(1, (span+n-1)/n)
	var out [][2]string
	for s := first; s < len(dates); s += step {
		e := min(s+step, len(dates)) - 1
		out = append(out, [2]string{dates[s], dates[e]})
	}
	return out
}

func uniqueDates(rows []row) []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range rows {
		if !seen[r.date] {
			seen[r.date] = true
			out = append(out, r.date)
		}
	}
	sort.Strings(out)
	return out
}

func addYears(d string, y int) string {
	if len(d) < 4 {
		return d
	}
	yr := 0
	for _, c := range d[:4] {
		yr = yr*10 + int(c-'0')
	}
	yr += y
	s := []byte(d)
	for i := 3; i >= 0; i-- {
		s[i] = byte('0' + yr%10)
		yr /= 10
	}
	return string(s)
}
