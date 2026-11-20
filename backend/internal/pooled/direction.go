package pooled

import (
	"errors"
	"math"
	"runtime"
	"sort"
	"sync"
	"time"

	"github.com/sodiq/financial-market/backend/internal/features"
	"github.com/sodiq/financial-market/backend/internal/market"
	"github.com/sodiq/financial-market/backend/internal/model"
)

// Pool names group asset classes that share a direction model.
const (
	PoolEquity      = "equity"
	PoolCrypto      = "crypto"
	PoolCommodities = "commodities"
)

func PoolFor(class market.AssetClass) string {
	switch class {
	case market.ClassCrypto:
		return PoolCrypto
	case market.ClassFuture:
		return PoolCommodities
	}
	return PoolEquity
}

var baseKeys = []string{
	"ret_1", "ret_5", "ret_21", "ret_63", "mom_12_1", "dist_sma20", "dist_sma50", "dist_sma200", "sma50_200",
	"adx_dir", "rsi14", "bb_z20", "stoch14", "macd_hist", "vol_21", "vol_ratio", "atr_pct", "dist_52w_high",
	"dist_52w_low", "volume_z", "obv_flow", "bench_ret_21", "bench_trend", "rel_strength_63",
	"vix_z", "vix_chg_5", "tnx_chg_21", "dxy_chg_21", "vix_term", "skew_z", "vvix_z",
}

// DirKeys is the fixed input list for each pool. Inputs an asset lacks
// entirely (an ETF has no earnings, an index no volume) enter as 0, the
// "nothing unusual" value in z-space.
func DirKeys(pool string) []string {
	k := append([]string(nil), baseKeys...)
	switch pool {
	case PoolEquity:
		k = append(k, "earn_soon", "pead", "earn_react", "beat4")
	case PoolCrypto:
		k = append(k, "fng", "fng_chg_7", "funding_7d", "funding_z")
	}
	return k
}

// DirModel is a pooled direction model for one pool and horizon.
type DirModel struct {
	Pool       string                `json:"pool"`
	Horizon    string                `json:"horizon"`
	Bars       int                   `json:"bars"`
	Keys       []string              `json:"keys"`
	Labels     []string              `json:"labels"`
	Folds      []Fold                `json:"folds"`
	Final      Fold                  `json:"final"`
	Lambda     float64               `json:"lambda"`
	Assets     []string              `json:"assets"`
	Rows       int                   `json:"rows"`
	Validation model.Metrics         `json:"validation"`
	PerAsset   map[string]AssetScore `json:"perAsset"`
	TestFrom   string                `json:"testFrom"`
	TestTo     string                `json:"testTo"`
	TrainedAt  time.Time             `json:"trainedAt"`
}

type AssetScore struct {
	N        int     `json:"n"`
	Accuracy float64 `json:"accuracy"`
}

// Inputs whose history starts late (earnings from 2010, alt-coin funding
// from 2023): unknown counts as neutral rather than discarding the row.
var earningsKey = map[string]bool{"earn_soon": true, "pead": true, "earn_react": true, "beat4": true, "funding_7d": true, "funding_z": true}

// dirRow extracts the input vector for asset a at bar t. ok=false if any
// input the asset has is undefined there.
func dirRow(a *Asset, keys []string, t int) ([]float64, bool) {
	x := make([]float64, len(keys))
	for j, k := range keys {
		col, has := a.Z[k]
		if !has {
			continue // absent input → 0
		}
		v := col[t]
		if math.IsNaN(v) {
			if earningsKey[k] {
				continue // outside the earnings history's coverage → neutral
			}
			return nil, false
		}
		x[j] = v
	}
	return x, true
}

// DirConfig controls pooled direction training.
type DirConfig struct {
	Years    int // training window, years back from today
	MinYears int // minimum history before the first out-of-sample forecast
	Blocks   int
	RankCost float64 // ranking strategy cost per trade, each side (0 = 0.1%)
}

func DefaultDirConfig() DirConfig { return DirConfig{Years: 16, MinYears: 4, Blocks: 20} }

// TrainDir fits and walk-forward validates a pooled direction model.
func TrainDir(pool string, h market.Horizon, assets []*Asset, cfg DirConfig) (*DirModel, error) {
	keys := DirKeys(pool)
	windowStart := addYears(time.Now().UTC().Format("2006-01-02"), -cfg.Years)
	stride := max(1, h.Bars/5)
	var rows []row
	var syms []string
	for ai, a := range assets {
		syms = append(syms, a.Symbol)
		start := sort.SearchStrings(a.Dates, windowStart)
		for t := start; t+h.Bars < len(a.Dates); t += stride {
			x, ok := dirRow(a, keys, t)
			if !ok {
				continue
			}
			y := 0.0
			if a.Close[t+h.Bars] > a.Close[t] {
				y = 1
			}
			rows = append(rows, row{asset: ai, t: t, date: a.Dates[t], labelEnd: a.Dates[t+h.Bars], x: x, y: y, fwd: a.Close[t+h.Bars]/a.Close[t] - 1})
		}
	}
	dates := uniqueDates(rows)
	if len(rows) < 2000 || len(dates) < 400 {
		return nil, errors.New("not enough pooled history")
	}
	first := sort.SearchStrings(dates, addYears(dates[0], cfg.MinYears))
	blocks := dateBlocks(dates, first, cfg.Blocks)
	if len(blocks) == 0 {
		return nil, errors.New("not enough pooled history for validation")
	}
	m := &DirModel{Pool: pool, Horizon: h.Key, Bars: h.Bars, Keys: keys, Assets: syms, Rows: len(rows), TrainedAt: time.Now().UTC()}
	for _, k := range keys {
		m.Labels = append(m.Labels, labelFor(k))
	}
	m.Lambda = chooseLambda(rows, blocks[0][0])

	type out struct {
		fold Fold
		idx  []int
		p    []float64
		base float64
	}
	outs := make([]out, len(blocks))
	var wg sync.WaitGroup
	sem := make(chan struct{}, runtime.GOMAXPROCS(0))
	var ferr error
	var mu sync.Mutex
	for bi, b := range blocks {
		wg.Add(1)
		sem <- struct{}{}
		go func(bi int, from, to string) {
			defer wg.Done()
			defer func() { <-sem }()
			f, base, err := fitRows(rows, func(r *row) bool { return r.labelEnd <= from }, m.Lambda)
			if err != nil {
				mu.Lock()
				ferr = err
				mu.Unlock()
				return
			}
			f.From, f.To = from, to
			o := out{fold: *f, base: base}
			for i := range rows {
				if rows[i].date >= from && rows[i].date <= to {
					o.idx = append(o.idx, i)
					o.p = append(o.p, f.Prob(rows[i].x))
				}
			}
			outs[bi] = o
		}(bi, b[0], b[1])
	}
	wg.Wait()
	if ferr != nil {
		return nil, ferr
	}
	var p, y, base []float64
	testDates := map[string]bool{}
	per := map[int][2]int{}
	for _, o := range outs {
		m.Folds = append(m.Folds, o.fold)
		for i, ri := range o.idx {
			r := rows[ri]
			p, y, base = append(p, o.p[i]), append(y, r.y), append(base, o.base)
			testDates[r.date] = true
			c := per[r.asset]
			c[0]++
			if (o.p[i] > 0.5) == (r.y == 1) {
				c[1]++
			}
			per[r.asset] = c
		}
	}
	// Independent periods are distinct dates (not asset-dates), divided by the
	// horizon and the sampling stride already applied.
	nEff := len(testDates) * stride / max(1, h.Bars)
	m.Validation = model.ComputeMetricsN(p, y, base, nEff)
	m.TestFrom, m.TestTo = blocks[0][0], blocks[len(blocks)-1][1]
	m.PerAsset = map[string]AssetScore{}
	for ai, c := range per {
		m.PerAsset[assets[ai].Symbol] = AssetScore{N: c[0], Accuracy: float64(c[1]) / float64(c[0])}
	}
	final, _, err := fitRows(rows, func(*row) bool { return true }, m.Lambda)
	if err != nil {
		return nil, err
	}
	final.From = m.TestTo
	m.Final = *final
	return m, nil
}

func fitRows(rows []row, use func(*row) bool, lambda float64) (*Fold, float64, error) {
	var X [][]float64
	var y []float64
	base := 0.0
	for i := range rows {
		if use(&rows[i]) {
			X = append(X, rows[i].x)
			y = append(y, rows[i].y)
			base += rows[i].y
		}
	}
	if len(X) < 500 {
		return nil, 0, errors.New("too few training rows")
	}
	lr, err := model.FitLogistic(X, y, nil, lambda*float64(len(X))/10000)
	if err != nil {
		return nil, 0, err
	}
	return &Fold{W: lr.W, B: lr.B}, base / float64(len(X)), nil
}

// chooseLambda picks the ridge penalty (per 10,000 rows) using only data
// before the first test block: the last 20% of those dates, purged, are the
// validation fold.
func chooseLambda(rows []row, firstTest string) float64 {
	var pre []row
	for _, r := range rows {
		if r.labelEnd <= firstTest {
			pre = append(pre, r)
		}
	}
	ds := uniqueDates(pre)
	if len(ds) < 100 {
		return 100
	}
	cut := ds[len(ds)*4/5]
	best, bestLL := 100.0, math.Inf(1)
	for _, lam := range []float64{1, 10, 100, 1000, 10000} {
		f, _, err := fitRows(pre, func(r *row) bool { return r.labelEnd <= cut }, lam)
		if err != nil {
			continue
		}
		ll := 0.0
		for _, r := range pre {
			if r.date > cut {
				p := math.Max(1e-6, math.Min(1-1e-6, f.Prob(r.x)))
				ll -= r.y*math.Log(p) + (1-r.y)*math.Log(1-p)
			}
		}
		if ll < bestLL {
			best, bestLL = lam, ll
		}
	}
	return best
}

// Skilled reports whether the pooled model earned a place in the ensemble:
// it must have beaten the base rate out-of-sample with p < 0.10. A no-skill
// model would only dilute the per-asset forecasts toward the base rate.
func (m *DirModel) Skilled() bool {
	return m.Validation.BrierSkill > 0 && m.Validation.PValue < 0.10
}

// DirApplied is a pooled model applied to one asset.
type DirApplied struct {
	OOS      []float64 // aligned to the asset's matrix rows; NaN where unavailable
	Live     float64   // NaN if the latest row is unusable
	Drivers  []Contribution
	Model    *DirModel
	InSample bool // asset was part of the training pool
}

type Contribution struct {
	Key          string  `json:"key"`
	Label        string  `json:"label"`
	Z            float64 `json:"z"`
	Weight       float64 `json:"weight"`
	Contribution float64 `json:"contribution"`
	ProbImpact   float64 `json:"probImpact"`
}

// ApplyDir scores every row of an asset with the fold model whose test
// window covers that row's date (so each score is out-of-sample) and the
// latest row with the final model.
func ApplyDir(m *DirModel, symbol string, class market.AssetClass, mat *features.Matrix) *DirApplied {
	a := FromMatrix(symbol, "", class, mat)
	n := len(a.Dates)
	res := &DirApplied{OOS: make([]float64, n), Live: math.NaN(), Model: m}
	for _, s := range m.Assets {
		if s == symbol {
			res.InSample = true
		}
	}
	fi := 0
	for t := 0; t < n; t++ {
		res.OOS[t] = math.NaN()
		d := a.Dates[t]
		for fi < len(m.Folds) && m.Folds[fi].To < d {
			fi++
		}
		if fi >= len(m.Folds) || d < m.Folds[fi].From {
			continue
		}
		if x, ok := dirRow(a, m.Keys, t); ok {
			res.OOS[t] = m.Folds[fi].Prob(x)
		}
	}
	if x, ok := dirRow(a, m.Keys, n-1); ok {
		p := m.Final.Prob(x)
		res.Live = p
		for j, k := range m.Keys {
			c := m.Final.W[j] * x[j]
			res.Drivers = append(res.Drivers, Contribution{Key: k, Label: m.Labels[j], Z: x[j], Weight: m.Final.W[j], Contribution: c, ProbImpact: p * (1 - p) * c})
		}
		sort.Slice(res.Drivers, func(i, j int) bool {
			return math.Abs(res.Drivers[i].Contribution) > math.Abs(res.Drivers[j].Contribution)
		})
	}
	return res
}
