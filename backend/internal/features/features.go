// Package features turns raw price history (plus benchmark, macro and
// sentiment series) into a time-aligned feature matrix for the models.
//
// Every feature at bar t uses only information that was public at the close
// of bar t. External series from markets that close later in UTC than the
// asset's market are lagged one day to prevent look-ahead leakage.
package features

import (
	"math"
	"sort"
	"time"

	ind "github.com/sodiq/financial-market/backend/internal/indicators"
	"github.com/sodiq/financial-market/backend/internal/market"
)

// Def describes one model input.
type Def struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Group string `json:"group"`
}

const (
	GroupMomentum   = "Momentum"
	GroupTrend      = "Trend"
	GroupOscillator = "Mean reversion"
	GroupVolatility = "Volatility"
	GroupVolume     = "Volume / flows"
	GroupRange      = "52-week range"
	GroupMarket     = "Market & relative strength"
	GroupMacro      = "Macro"
	GroupSentiment  = "Sentiment"
	GroupEarnings   = "Earnings"
)

// Matrix is the aligned feature set for one asset.
type Matrix struct {
	Dates    []string
	Close    []float64
	Defs     []Def
	X        [][]float64 // X[t][j]; rows with NaN are unusable
	VolDaily []float64   // 63-bar stdev of daily log returns, for scaling
	Raw      map[string][]float64
	// Start is the first row where every kept feature is defined.
	Start int
	// Dropped lists optional features excluded for insufficient history.
	Dropped []string
	// All holds every candidate input (kept or dropped) by key, for the
	// cross-asset models, which need a fixed input set across assets.
	All     map[string][]float64
	AllDefs []Def
}

// Inputs bundles everything the builder can use. Only Asset is required.
type Inputs struct {
	Asset     *market.Series
	Benchmark *market.Series
	Macro     map[string]*market.Series // keyed by symbol (^VIX, ^TNX, DX-Y.NYB)
	FearGreed map[string]float64        // crypto only, keyed by UTC date
	Earnings  *EarningsInput            // US-listed companies only
	Funding   map[string]float64        // crypto only: daily perpetual funding by UTC date
}

// EarningsEvent is the part of an earnings report the features need.
type EarningsEvent struct {
	Date     string
	Time     string  // pre | after | unknown
	Surprise float64 // % EPS surprise; NaN if unknown or not yet reported
}

// EarningsInput carries reports plus the date from which the report list is
// complete. Before that date earnings features are NaN, never zero, because
// "no report known" is not the same as "no report happened".
type EarningsInput struct {
	Events      []EarningsEvent
	CoveredFrom string
}

type column struct {
	def      Def
	vals     []float64
	optional bool
}

// Build computes the feature matrix.
func Build(in Inputs) *Matrix {
	a := in.Asset
	n := len(a.Bars)
	c, h, l, v := make([]float64, n), make([]float64, n), make([]float64, n), make([]float64, n)
	dates := make([]string, n)
	hasVolume := false
	for i, b := range a.Bars {
		c[i], h[i], l[i], v[i], dates[i] = b.Close, b.High, b.Low, b.Volume, b.Date
		if b.Volume > 0 {
			hasVolume = true
		}
	}
	bpy := market.BarsPerYear(a.Meta.Class)

	lr := ind.LogReturns(c)
	sma20, sma50, sma200 := ind.SMA(c, 20), ind.SMA(c, 50), ind.SMA(c, 200)
	rsi := ind.RSI(c, 14)
	macd, macdSig, macdHist := ind.MACD(c, 12, 26, 9)
	bbz := ind.BollingerZ(c, 20)
	atr := ind.ATR(h, l, c, 14)
	adx, pdi, mdi := ind.ADX(h, l, c, 14)
	stoch := ind.StochK(h, l, c, 14)
	vol21, vol63, vol126 := ind.RollingStd(lr, 21), ind.RollingStd(lr, 63), ind.RollingStd(lr, 126)
	hi52, lo52 := ind.RollingMax(h, 252), ind.RollingMin(l, 252)

	r1, r5, r21, r63, r252 := ind.LogReturnN(c, 1), ind.LogReturnN(c, 5), ind.LogReturnN(c, 21), ind.LogReturnN(c, 63), ind.LogReturnN(c, 252)

	f := func(fn func(i int) float64) []float64 {
		out := make([]float64, n)
		for i := range out {
			out[i] = fn(i)
		}
		return out
	}
	ratio := func(x, y []float64) []float64 {
		return f(func(i int) float64 {
			if y[i] > 0 && !math.IsNaN(x[i]) {
				return x[i]/y[i] - 1
			}
			return math.NaN()
		})
	}

	cols := []column{
		{Def{"ret_1", "1-bar return", GroupMomentum}, r1, false},
		{Def{"ret_5", "1-week return", GroupMomentum}, r5, false},
		{Def{"ret_21", "1-month return", GroupMomentum}, r21, false},
		{Def{"ret_63", "3-month return", GroupMomentum}, r63, false},
		{Def{"mom_12_1", "12-month momentum (ex last month)", GroupMomentum}, f(func(i int) float64 { return r252[i] - r21[i] }), false},
		{Def{"dist_sma20", "Distance from 20-day average", GroupTrend}, ratio(c, sma20), false},
		{Def{"dist_sma50", "Distance from 50-day average", GroupTrend}, ratio(c, sma50), false},
		{Def{"dist_sma200", "Distance from 200-day average", GroupTrend}, ratio(c, sma200), false},
		{Def{"sma50_200", "50-day vs 200-day average (golden/death cross)", GroupTrend}, ratio(sma50, sma200), false},
		{Def{"adx_dir", "Directional trend strength (ADX × sign of DI)", GroupTrend}, f(func(i int) float64 {
			if math.IsNaN(adx[i]) {
				return math.NaN()
			}
			if pdi[i] >= mdi[i] {
				return adx[i] / 100
			}
			return -adx[i] / 100
		}), false},
		{Def{"rsi14", "RSI (14)", GroupOscillator}, f(func(i int) float64 { return (rsi[i] - 50) / 50 }), false},
		{Def{"bb_z20", "Bollinger z-score (20)", GroupOscillator}, bbz, false},
		{Def{"stoch14", "Stochastic %K (14)", GroupOscillator}, f(func(i int) float64 { return (stoch[i] - 50) / 50 }), false},
		{Def{"macd_hist", "MACD histogram (ATR-scaled)", GroupOscillator}, f(func(i int) float64 {
			if atr[i] > 0 {
				return macdHist[i] / atr[i]
			}
			return math.NaN()
		}), false},
		{Def{"vol_21", "Realised volatility (1 month, annualised)", GroupVolatility}, f(func(i int) float64 { return vol21[i] * math.Sqrt(bpy) }), false},
		{Def{"vol_ratio", "Volatility regime (1m vs 6m vol)", GroupVolatility}, f(func(i int) float64 {
			if vol126[i] > 0 {
				return math.Log(vol21[i] / vol126[i])
			}
			return math.NaN()
		}), false},
		{Def{"atr_pct", "ATR as % of price", GroupVolatility}, f(func(i int) float64 { return atr[i] / c[i] }), false},
		{Def{"dist_52w_high", "Distance from 52-week high", GroupRange}, ratio(c, hi52), false},
		{Def{"dist_52w_low", "Distance from 52-week low", GroupRange}, ratio(c, lo52), false},
	}

	raw := map[string][]float64{
		"close": c, "sma20": sma20, "sma50": sma50, "sma200": sma200, "rsi": rsi,
		"macd": macd, "macd_signal": macdSig, "macd_hist": macdHist, "bbz": bbz, "atr": atr,
		"adx": adx, "plus_di": pdi, "minus_di": mdi, "stoch": stoch,
		"vol21": f(func(i int) float64 { return vol21[i] * math.Sqrt(bpy) }),
		"hi52":  hi52, "lo52": lo52, "ret_5": r5, "ret_21": r21, "ret_63": r63, "ret_252": r252,
	}

	if hasVolume {
		lv := f(func(i int) float64 {
			if v[i] > 0 {
				return math.Log(v[i])
			}
			return math.NaN()
		})
		obv := ind.OBV(c, v)
		volSum20 := ind.SMA(v, 20)
		cols = append(cols,
			column{Def{"volume_z", "Volume vs normal (z-score)", GroupVolume}, ind.ZScore(lv, 20), true},
			column{Def{"obv_flow", "On-balance volume flow (20)", GroupVolume}, f(func(i int) float64 {
				if i < 20 || !(volSum20[i] > 0) {
					return math.NaN()
				}
				return (obv[i] - obv[i-20]) / (volSum20[i] * 20)
			}), true},
		)
		raw["volume"] = v
		raw["volume_avg20"] = volSum20
		raw["volume_z"] = ind.ZScore(lv, 20)
	}

	lagFor := func(ext *market.Series) int {
		if a.Meta.Class == market.ClassCrypto {
			return 0 // crypto daily bars close at 24:00 UTC, after every major market
		}
		if a.Meta.GMTOffset > ext.Meta.GMTOffset {
			return 1 // asset market closes earlier in UTC than the external one
		}
		return 0
	}

	if b := in.Benchmark; b != nil && len(b.Bars) > 0 && b.Meta.Symbol != a.Meta.Symbol {
		bc := Align(dates, b, lagFor(b))
		br21, br63 := ind.LogReturnN(bc, 21), ind.LogReturnN(bc, 63)
		bsma200 := ind.SMA(bc, 200)
		cols = append(cols,
			column{Def{"bench_ret_21", "Benchmark 1-month return (" + b.Meta.Symbol + ")", GroupMarket}, br21, true},
			column{Def{"bench_trend", "Benchmark vs its 200-day average", GroupMarket}, ratio(bc, bsma200), true},
			column{Def{"rel_strength_63", "3-month return relative to benchmark", GroupMarket}, f(func(i int) float64 { return r63[i] - br63[i] }), true},
		)
		raw["bench_close"] = bc
		raw["bench_ret_21"] = br21
		raw["bench_trend"] = ratio(bc, bsma200)
	}

	if s := in.Macro["^VIX"]; s != nil && len(s.Bars) > 0 {
		vx := Align(dates, s, lagFor(s))
		lvx := f(func(i int) float64 { return math.Log(vx[i]) })
		cols = append(cols,
			column{Def{"vix_z", "VIX fear gauge vs its 1-year norm", GroupMacro}, ind.ZScore(lvx, 252), true},
			column{Def{"vix_chg_5", "VIX 1-week change", GroupMacro}, ind.LogReturnN(vx, 5), true},
		)
		raw["vix"] = vx
	}
	if s := in.Macro["^TNX"]; s != nil && len(s.Bars) > 0 {
		tn := Align(dates, s, lagFor(s))
		cols = append(cols, column{Def{"tnx_chg_21", "US 10-year yield 1-month change (pts)", GroupMacro}, f(func(i int) float64 {
			if i < 21 {
				return math.NaN()
			}
			return tn[i] - tn[i-21]
		}), true})
		raw["tnx"] = tn
	}
	if s := in.Macro["DX-Y.NYB"]; s != nil && len(s.Bars) > 0 {
		dx := Align(dates, s, lagFor(s))
		cols = append(cols, column{Def{"dxy_chg_21", "US dollar index 1-month change", GroupMacro}, ind.LogReturnN(dx, 21), true})
		raw["dxy"] = dx
	}
	// Options-market indices (CBOE). Term structure: VIX above its 3-month
	// counterpart (backwardation) marks acute stress; SKEW prices crash
	// protection; VVIX is the volatility of volatility.
	if vs, v3 := in.Macro["^VIX"], in.Macro["^VIX3M"]; vs != nil && v3 != nil && len(vs.Bars) > 0 && len(v3.Bars) > 0 {
		vx, vx3 := Align(dates, vs, lagFor(vs)), Align(dates, v3, lagFor(v3))
		cols = append(cols, column{Def{"vix_term", "VIX term structure (1-month vs 3-month implied vol)", GroupMacro}, f(func(i int) float64 {
			if vx3[i] > 0 && vx[i] > 0 {
				return math.Log(vx[i] / vx3[i])
			}
			return math.NaN()
		}), true})
		raw["vix3m"] = vx3
	}
	if s := in.Macro["^SKEW"]; s != nil && len(s.Bars) > 0 {
		sk := Align(dates, s, lagFor(s))
		cols = append(cols, column{Def{"skew_z", "SKEW index (demand for crash protection) vs 1-year norm", GroupMacro}, ind.ZScore(sk, 252), true})
		raw["skew"] = sk
	}
	if s := in.Macro["^VVIX"]; s != nil && len(s.Bars) > 0 {
		vv := Align(dates, s, lagFor(s))
		lvv := f(func(i int) float64 { return math.Log(vv[i]) })
		cols = append(cols, column{Def{"vvix_z", "VVIX (volatility of volatility) vs 1-year norm", GroupMacro}, ind.ZScore(lvv, 252), true})
		raw["vvix"] = vv
	}
	if len(in.Funding) > 0 {
		fd := alignMapMaxGap(dates, in.Funding, 3)
		f7 := f(func(i int) float64 {
			if i < 6 {
				return math.NaN()
			}
			sum := 0.0
			for k := i - 6; k <= i; k++ {
				if math.IsNaN(fd[k]) {
					return math.NaN()
				}
				sum += fd[k]
			}
			return sum / 7 * 365 // annualised cost of holding a leveraged long
		})
		cols = append(cols,
			column{Def{"funding_7d", "Perpetual futures funding, 1-week average (annualised)", GroupSentiment}, f7, true},
			column{Def{"funding_z", "Funding vs its 3-month norm (leverage crowding)", GroupSentiment}, ind.ZScore(f7, 90), true},
		)
		raw["funding_7d"] = f7
	}
	if len(in.FearGreed) > 0 {
		fg := alignMap(dates, in.FearGreed)
		cols = append(cols,
			column{Def{"fng", "Crypto Fear & Greed index", GroupSentiment}, f(func(i int) float64 { return (fg[i] - 50) / 50 }), true},
			column{Def{"fng_chg_7", "Fear & Greed 1-week change", GroupSentiment}, f(func(i int) float64 {
				if i < 7 {
					return math.NaN()
				}
				return (fg[i] - fg[i-7]) / 50
			}), true},
		)
		raw["fng"] = fg
	}

	if e := in.Earnings; e != nil && e.CoveredFrom != "" {
		soon, pead, react, beat4 := EarningsColumns(dates, c, vol63, e)
		cols = append(cols,
			column{Def{"earn_soon", "Earnings report approaching (next ~month)", GroupEarnings}, soon, true},
			column{Def{"pead", "Post-earnings drift: last EPS surprise (decaying)", GroupEarnings}, pead, true},
			column{Def{"earn_react", "Post-earnings drift: last earnings-day price reaction (decaying)", GroupEarnings}, react, true},
			column{Def{"beat4", "Earnings beat streak (share of last 4 reports above consensus)", GroupEarnings}, beat4, true},
		)
	}

	// Core features define the earliest usable row. Optional features with
	// poor coverage after that point are dropped rather than shrinking
	// the training set.
	start := 0
	for _, col := range cols {
		if col.optional {
			continue
		}
		if fv := firstValid(col.vals); fv > start {
			start = fv
		}
	}
	m := &Matrix{Dates: dates, Close: c, Raw: raw}
	var kept []column
	for _, col := range cols {
		if !col.optional {
			kept = append(kept, col)
			continue
		}
		valid := 0
		for i := start; i < n; i++ {
			if !math.IsNaN(col.vals[i]) && !math.IsInf(col.vals[i], 0) {
				valid++
			}
		}
		if n-start > 0 && float64(valid)/float64(n-start) >= 0.6 && !math.IsNaN(col.vals[n-1]) {
			kept = append(kept, col)
		} else if col.def.Group != GroupEarnings {
			// Earnings inputs only cover ~2010 onwards, so long-history assets
			// drop them here by design; the cross-asset models still use them.
			m.Dropped = append(m.Dropped, col.def.Label)
		}
	}
	for _, col := range kept {
		if fv := firstValid(col.vals); fv > start {
			start = fv
		}
	}
	m.Start = start
	m.Defs = make([]Def, len(kept))
	for j, col := range kept {
		m.Defs[j] = col.def
	}
	m.X = make([][]float64, n)
	for i := 0; i < n; i++ {
		row := make([]float64, len(kept))
		for j, col := range kept {
			x := col.vals[i]
			if math.IsInf(x, 0) {
				x = math.NaN()
			}
			row[j] = x
		}
		m.X[i] = row
	}
	m.VolDaily = vol63
	m.All = make(map[string][]float64, len(cols))
	for _, col := range cols {
		vals := col.vals
		for i, x := range vals {
			if math.IsInf(x, 0) {
				vals[i] = math.NaN()
			}
		}
		m.All[col.def.Key] = vals
		m.AllDefs = append(m.AllDefs, col.def)
	}
	return m
}

// ReactionBars locates an earnings event in a daily series: b0 is the last
// bar before the news could be priced, r the first bar that reflects it.
// Pre-market reports move the report day; after-hours ones the next day;
// unknown timing uses the two-day window that covers both.
func ReactionBars(dates []string, ev EarningsEvent) (b0, r int, ok bool) {
	n := len(dates)
	ge := sort.SearchStrings(dates, ev.Date)
	gt := ge
	if ge < n && dates[ge] == ev.Date {
		gt = ge + 1
	}
	switch ev.Time {
	case "pre":
		r, b0 = ge, ge-1
	case "after":
		r, b0 = gt, gt-1
	default:
		r, b0 = gt, ge-1
	}
	return b0, r, b0 >= 0 && r < n && r > b0
}

// EarningsColumns builds the three earnings inputs.
//   - earn_soon: rises from 0 to 1 over the 21 bars before a report (the
//     "earnings announcement premium"). Uses the scheduled date, which is
//     public weeks ahead; historically it is the actual date, a slight
//     look-ahead that is documented in DECISIONS.md.
//   - pead: last EPS surprise, clipped to ±50%, scaled to ±1, decaying with
//     a 21-bar time constant and cut off after 63 bars.
//   - earn_react: last earnings-window return in volatility units (±4σ →
//     ±1), same decay. Price reaction is the best-documented drift signal.
//   - beat4: share of the last four reported quarters that beat consensus,
//     centred (-1 = four misses, +1 = four beats). Persistent beaters keep
//     beating, a documented pattern in analyst forecast errors.
func EarningsColumns(dates []string, c, volDaily []float64, e *EarningsInput) (soon, pead, react, beat4 []float64) {
	n := len(dates)
	soon, pead, react, beat4 = make([]float64, n), make([]float64, n), make([]float64, n), make([]float64, n)
	type ev struct {
		r, b0  int // r may be ≥ n for future reports
		inData bool
		surp   float64
	}
	var evs []ev
	for _, x := range e.Events {
		b0, r, ok := ReactionBars(dates, x)
		if ok {
			evs = append(evs, ev{r: r, b0: b0, inData: true, surp: x.Surprise})
			continue
		}
		if n > 0 && x.Date > dates[n-1] {
			// Future report: estimate its bar index by counting weekdays.
			k := tradingDaysBetween(dates[n-1], x.Date)
			if x.Time != "pre" {
				k++
			}
			evs = append(evs, ev{r: n - 1 + k, b0: -1, surp: math.NaN()})
		}
	}
	sort.Slice(evs, func(i, j int) bool { return evs[i].r < evs[j].r })
	// Coverage starts a quarter after CoveredFrom so "last report" is known.
	start := sort.SearchStrings(dates, addDays(e.CoveredFrom, 100))
	next, prev := 0, -1
	for i := 0; i < n; i++ {
		if i < start {
			soon[i], pead[i], react[i], beat4[i] = math.NaN(), math.NaN(), math.NaN(), math.NaN()
			continue
		}
		for next < len(evs) && evs[next].r <= i {
			if evs[next].inData {
				prev = next
			}
			next++
		}
		if next < len(evs) {
			if k := evs[next].r - i; k >= 1 && k <= 21 {
				soon[i] = float64(22-k) / 21
			}
		}
		if prev >= 0 {
			// Beat streak over the last four reports with a known surprise.
			beats, known := 0, 0
			for k := prev; k >= 0 && known < 4; k-- {
				if evs[k].inData && !math.IsNaN(evs[k].surp) {
					known++
					if evs[k].surp > 0 {
						beats++
					}
				}
			}
			if known > 0 {
				beat4[i] = (float64(beats)/float64(known) - 0.5) * 2
			}
			p := evs[prev]
			s := i - p.r
			if s <= 63 {
				decay := math.Exp(-float64(s) / 21)
				if !math.IsNaN(p.surp) {
					pead[i] = math.Max(-50, math.Min(50, p.surp)) / 50 * decay
				}
				if v := volDaily[p.b0]; v > 0 && !math.IsNaN(v) {
					ret := math.Log(c[p.r] / c[p.b0])
					z := ret / (v * math.Sqrt(float64(p.r-p.b0)))
					react[i] = math.Max(-4, math.Min(4, z)) / 4 * decay
				}
			}
		}
	}
	return
}

func addDays(d string, n int) string {
	t, err := time.Parse("2006-01-02", d)
	if err != nil {
		return d
	}
	return t.AddDate(0, 0, n).Format("2006-01-02")
}

func tradingDaysBetween(from, to string) int {
	a, err1 := time.Parse("2006-01-02", from)
	b, err2 := time.Parse("2006-01-02", to)
	if err1 != nil || err2 != nil {
		return 0
	}
	k := 0
	for d := a.AddDate(0, 0, 1); !d.After(b); d = d.AddDate(0, 0, 1) {
		if d.Weekday() != time.Saturday && d.Weekday() != time.Sunday {
			k++
		}
	}
	return k
}

// RowOK reports whether every feature at row i is defined.
func (m *Matrix) RowOK(i int) bool {
	for _, x := range m.X[i] {
		if math.IsNaN(x) {
			return false
		}
	}
	return true
}

func firstValid(x []float64) int {
	for i, v := range x {
		if !math.IsNaN(v) && !math.IsInf(v, 0) {
			return i
		}
	}
	return len(x)
}

// Align maps an external series onto the asset's dates, carrying the last
// known value forward. lag=1 uses only external bars strictly before the
// asset's date.
func Align(dates []string, ext *market.Series, lag int) []float64 {
	out := make([]float64, len(dates))
	j := -1
	for i, d := range dates {
		for j+1 < len(ext.Bars) && (ext.Bars[j+1].Date < d || (lag == 0 && ext.Bars[j+1].Date == d)) {
			j++
		}
		if j >= 0 {
			out[i] = ext.Bars[j].Close
		} else {
			out[i] = math.NaN()
		}
	}
	return out
}

// alignMapMaxGap is alignMap that refuses to carry a value forward more than
// maxDays calendar days: a feed that stopped updating must read as missing,
// not as frozen.
func alignMapMaxGap(dates []string, m map[string]float64, maxDays int) []float64 {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]float64, len(dates))
	j := -1
	for i, d := range dates {
		for j+1 < len(keys) && keys[j+1] <= d {
			j++
		}
		out[i] = math.NaN()
		if j >= 0 && addDays(keys[j], maxDays) >= d {
			out[i] = m[keys[j]]
		}
	}
	return out
}

func alignMap(dates []string, m map[string]float64) []float64 {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]float64, len(dates))
	j := -1
	for i, d := range dates {
		for j+1 < len(keys) && keys[j+1] <= d {
			j++
		}
		if j >= 0 {
			out[i] = m[keys[j]]
		} else {
			out[i] = math.NaN()
		}
	}
	return out
}
