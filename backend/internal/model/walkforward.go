package model

import (
	"errors"
	"math"
	"runtime"
	"sync"

	"github.com/sodiq/financial-market/backend/internal/features"
)

// Dataset is the labelled sample for one horizon.
type Dataset struct {
	Idx     []int // bar index in the feature matrix
	X       [][]float64
	Y       []float64 // 1 if forward return > 0
	Ret     []float64 // forward log return over the horizon
	RetNorm []float64 // Ret / (daily vol at t × √h)
}

func BuildDataset(m *features.Matrix, h int) *Dataset {
	ds := &Dataset{}
	n := len(m.Close)
	for t := m.Start; t+h < n; t++ {
		if !m.RowOK(t) || !(m.VolDaily[t] > 0) {
			continue
		}
		r := math.Log(m.Close[t+h] / m.Close[t])
		if math.IsNaN(r) || math.IsInf(r, 0) {
			continue
		}
		y := 0.0
		if r > 0 {
			y = 1
		}
		ds.Idx = append(ds.Idx, t)
		ds.X = append(ds.X, m.X[t])
		ds.Y = append(ds.Y, y)
		ds.Ret = append(ds.Ret, r)
		ds.RetNorm = append(ds.RetNorm, r/(m.VolDaily[t]*math.Sqrt(float64(h))))
	}
	return ds
}

// Config controls validation.
type Config struct {
	MaxTrainRows int     // cap on training window (most recent rows)
	MinTrainRows int     // minimum before the first out-of-sample prediction
	Blocks       int     // approximate number of refits across the test span
	TradeCost    float64 // per-trade cost for the strategy simulation
	SignalEdge   float64 // |p-0.5| needed to go long in the simulation
}

func DefaultConfig() Config {
	return Config{MaxTrainRows: 5000, MinTrainRows: 750, Blocks: 40, TradeCost: 0.001, SignalEdge: 0.03}
}

// OOS holds every out-of-sample prediction from the walk-forward.
type OOS struct {
	Idx  []int
	PLR  []float64
	PKNN []float64
	PEns []float64
	Base []float64
	Y    []float64
	Ret  []float64
}

// Validation is the honest track record of the method on this asset.
type Validation struct {
	Ensemble Metrics `json:"ensemble"`
	Logistic Metrics `json:"logistic"`
	Analogs  Metrics `json:"analogs"`
	// External is the cross-asset model's record on this asset (only on
	// dates where its own out-of-sample forecasts exist).
	External    *Metrics        `json:"external,omitempty"`
	Calibration []Bucket        `json:"calibration"`
	Confidence  []ConfidenceRow `json:"confidence"`
	Strategy    Strategy        `json:"strategy"`
	TestFrom    string          `json:"testFrom"`
	TestTo      string          `json:"testTo"`
	TrainRows   int             `json:"trainRows"`
	Lambda      float64         `json:"lambda"`
}

// Live is the forecast for the most recent bar.
type Live struct {
	PLogistic     float64            `json:"pLogistic"`
	PAnalogs      float64            `json:"pAnalogs"`
	PExternal     float64            `json:"pExternal"`   // cross-asset model (NaN if unavailable)
	PRaw          float64            `json:"pRaw"`        // equal-weight blend
	PCalibrated   float64            `json:"pCalibrated"` // after Platt shrinkage
	BaseRate      float64            `json:"baseRate"`    // historical up-rate for this horizon
	Z             []float64          `json:"-"`
	Contributions []float64          `json:"-"`
	Neighbors     []Neighbor         `json:"-"`
	Weights       []float64          `json:"-"`
	Intercept     float64            `json:"-"`
	RetQuantiles  map[string]float64 `json:"retQuantiles"` // conditional forward log-return quantiles
	UncondQuant   map[string]float64 `json:"uncondQuantiles"`
	ExpectedRet   float64            `json:"expectedReturn"` // mean simple return of vol-scaled analogs
}

type Result struct {
	Horizon    int
	Validation Validation
	OOS        OOS
	Live       *Live
	Platt      Platt
}

// External is a forecast produced elsewhere (the cross-asset model) that
// joins the blend. OOS must be aligned to matrix rows and contain only
// genuinely out-of-sample forecasts; NaN where unavailable.
type External struct {
	OOS  []float64
	Live float64
}

// Run performs walk-forward validation then fits on all data for the live
// forecast.
func Run(m *features.Matrix, h int, barsPerYear float64, cfg Config) (*Result, error) {
	return RunWith(m, h, barsPerYear, cfg, nil)
}

// blend is the fixed equal-weight ensemble. Weights are deliberately not
// learned: learning them on the same out-of-sample data used for reporting
// would inflate the reported accuracy.
func blend(lr, knn, ext float64) float64 {
	if math.IsNaN(ext) {
		return (lr + knn) / 2
	}
	return (lr + knn + ext) / 3
}

// RunWith is Run with an optional external component.
func RunWith(m *features.Matrix, h int, barsPerYear float64, cfg Config, ext *External) (*Result, error) {
	extAt := func(t int) float64 {
		if ext == nil || t >= len(ext.OOS) {
			return math.NaN()
		}
		return ext.OOS[t]
	}
	ds := BuildDataset(m, h)
	minTrain := max(cfg.MinTrainRows, 3*h)
	if len(ds.Y) < minTrain+h+60 {
		return nil, errors.New("not enough history for this horizon")
	}
	res := &Result{Horizon: h}

	// Test span: everything after the minimum training window plus a purge gap.
	first := minTrain + h
	testN := len(ds.Y) - first
	step := max(21, (testN+cfg.Blocks-1)/cfg.Blocks)

	lambda := chooseLambda(ds, first, h, cfg)
	res.Validation.Lambda = lambda

	type blockOut struct {
		start int
		plr   []float64
		pknn  []float64
		base  []float64
	}
	var blocks []int
	for s := first; s < len(ds.Y); s += step {
		blocks = append(blocks, s)
	}
	outs := make([]blockOut, len(blocks))
	var wg sync.WaitGroup
	sem := make(chan struct{}, runtime.GOMAXPROCS(0))
	var firstErr error
	var errMu sync.Mutex
	for bi, s := range blocks {
		wg.Add(1)
		sem <- struct{}{}
		go func(bi, s int) {
			defer wg.Done()
			defer func() { <-sem }()
			end := min(s+step, len(ds.Y))
			tr := trainRows(ds, s, h, cfg.MaxTrainRows)
			fit, err := fitAll(ds, tr, lambda*float64(len(tr))/float64(first))
			if err != nil {
				errMu.Lock()
				firstErr = err
				errMu.Unlock()
				return
			}
			bo := blockOut{start: s}
			for k := s; k < end; k++ {
				z := fit.std.Transform(ds.X[k])
				bo.plr = append(bo.plr, fit.lr.Prob(z))
				bo.pknn = append(bo.pknn, fit.an.Prob(fit.an.Neighbors(z)))
				bo.base = append(bo.base, fit.base)
			}
			outs[bi] = bo
		}(bi, s)
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	o := &res.OOS
	var pe, ye, be []float64
	for _, bo := range outs {
		for i := range bo.plr {
			k := bo.start + i
			o.Idx = append(o.Idx, ds.Idx[k])
			o.PLR = append(o.PLR, bo.plr[i])
			o.PKNN = append(o.PKNN, bo.pknn[i])
			x := extAt(ds.Idx[k])
			o.PEns = append(o.PEns, blend(bo.plr[i], bo.pknn[i], x))
			o.Base = append(o.Base, bo.base[i])
			if !math.IsNaN(x) {
				pe, ye, be = append(pe, x), append(ye, ds.Y[k]), append(be, bo.base[i])
			}
			o.Y = append(o.Y, ds.Y[k])
			o.Ret = append(o.Ret, ds.Ret[k])
		}
	}

	v := &res.Validation
	v.Ensemble = ComputeMetrics(o.PEns, o.Y, o.Base, h)
	v.Logistic = ComputeMetrics(o.PLR, o.Y, o.Base, h)
	v.Analogs = ComputeMetrics(o.PKNN, o.Y, o.Base, h)
	if len(pe) >= 100 {
		em := ComputeMetrics(pe, ye, be, h)
		v.External = &em
	}
	v.Calibration = Calibration(o.PEns, o.Y, o.Ret)
	v.Confidence = ConfidenceTable(o.PEns, o.Y)
	dates := make([]string, len(o.Idx))
	for i, t := range o.Idx {
		dates[i] = m.Dates[t]
	}
	v.Strategy = Simulate(dates, o.PEns, o.Ret, h, barsPerYear, cfg.SignalEdge, cfg.TradeCost)
	v.Strategy.Equity, v.Strategy.BuyHold, v.Strategy.Dates = thin(v.Strategy.Equity, v.Strategy.BuyHold, v.Strategy.Dates, 400)
	if len(dates) > 0 {
		v.TestFrom, v.TestTo = dates[0], dates[len(dates)-1]
	}
	res.Platt = FitPlatt(o.PEns, o.Y)

	// Final model on all labelled history for the live forecast.
	last := len(m.Close) - 1
	if !m.RowOK(last) {
		return res, nil
	}
	all := trainRows(ds, len(ds.Y), 0, cfg.MaxTrainRows)
	v.TrainRows = len(all)
	fit, err := fitAll(ds, all, lambda*float64(len(all))/float64(first))
	if err != nil {
		return nil, err
	}
	z := fit.std.Transform(m.X[last])
	nb := fit.an.Neighbors(z)
	live := &Live{
		PLogistic:     fit.lr.Prob(z),
		PAnalogs:      fit.an.Prob(nb),
		BaseRate:      fit.base,
		Z:             z,
		Contributions: fit.lr.Contributions(z),
		Neighbors:     nb,
		Weights:       fit.lr.W,
		Intercept:     fit.lr.B,
	}
	live.PExternal = math.NaN()
	if ext != nil {
		live.PExternal = ext.Live
	}
	live.PRaw = blend(live.PLogistic, live.PAnalogs, live.PExternal)
	live.PCalibrated = res.Platt.Apply(live.PRaw)

	// Forward-return distribution: analog outcomes re-scaled to today's
	// volatility, so a calm-period analog doesn't understate today's risk.
	scale := m.VolDaily[last] * math.Sqrt(float64(h))
	cond := make([]float64, len(nb))
	mean := 0.0
	for i, n := range nb {
		cond[i] = n.RetNorm * scale
		mean += math.Exp(cond[i]) - 1
	}
	live.ExpectedRet = mean / float64(len(nb))
	live.RetQuantiles = quantiles(cond)
	uncond := make([]float64, len(all))
	for i, k := range all {
		uncond[i] = ds.RetNorm[k] * scale
	}
	live.UncondQuant = quantiles(uncond)
	res.Live = live
	return res, nil
}

func quantiles(x []float64) map[string]float64 {
	return map[string]float64{
		"p05": Quantile(x, 0.05), "p10": Quantile(x, 0.10), "p25": Quantile(x, 0.25),
		"p50": Quantile(x, 0.50), "p75": Quantile(x, 0.75), "p90": Quantile(x, 0.90), "p95": Quantile(x, 0.95),
	}
}

// trainRows returns dataset rows usable to predict row s: their forward
// label window must end at or before bar Idx[s] (purging), capped to the most
// recent maxRows.
func trainRows(ds *Dataset, s, h, maxRows int) []int {
	var limitBar int
	if s < len(ds.Idx) {
		limitBar = ds.Idx[s]
	} else {
		limitBar = math.MaxInt
	}
	var rows []int
	for k := 0; k < len(ds.Idx) && k < s; k++ {
		if h == 0 || ds.Idx[k]+h <= limitBar {
			rows = append(rows, k)
		}
	}
	if len(rows) > maxRows {
		rows = rows[len(rows)-maxRows:]
	}
	return rows
}

type fitted struct {
	std  Standardizer
	lr   *Logistic
	an   *Analogs
	base float64
}

func fitAll(ds *Dataset, rows []int, lambda float64) (*fitted, error) {
	X := make([][]float64, len(rows))
	y := make([]float64, len(rows))
	base := 0.0
	for i, k := range rows {
		X[i], y[i] = ds.X[k], ds.Y[k]
		base += y[i]
	}
	base /= float64(len(rows))
	std := FitStandardizer(X)
	Z := std.TransformAll(X)
	lr, err := FitLogistic(Z, y, nil, lambda)
	if err != nil {
		return nil, err
	}
	an := &Analogs{Z: Z, Y: y, K: KFor(len(rows)), Prior: base}
	an.Ret = make([]float64, len(rows))
	an.RetNorm = make([]float64, len(rows))
	an.Index = make([]int, len(rows))
	for i, k := range rows {
		an.Ret[i], an.RetNorm[i], an.Index[i] = ds.Ret[k], ds.RetNorm[k], ds.Idx[k]
	}
	return &fitted{std: std, lr: lr, an: an, base: base}, nil
}

// chooseLambda picks the ridge penalty using only the initial training
// window: the last 25% of it (after a purge gap) is a validation fold.
func chooseLambda(ds *Dataset, first, h int, cfg Config) float64 {
	trainEnd := first - h
	valStart := trainEnd * 3 / 4
	tr := trainRows(ds, valStart, h, cfg.MaxTrainRows)
	if len(tr) < 200 {
		return 100
	}
	best, bestLL := 100.0, math.Inf(1)
	for _, lam := range []float64{1, 10, 100, 1000, 10000} {
		fit, err := fitAll(ds, tr, lam)
		if err != nil {
			continue
		}
		ll := 0.0
		for k := valStart; k < trainEnd; k++ {
			p := math.Max(1e-6, math.Min(1-1e-6, fit.lr.Prob(fit.std.Transform(ds.X[k]))))
			ll -= ds.Y[k]*math.Log(p) + (1-ds.Y[k])*math.Log(1-p)
		}
		if ll < bestLL {
			best, bestLL = lam, ll
		}
	}
	return best * float64(first) / float64(len(tr))
}

func thin(a, b []float64, d []string, n int) ([]float64, []float64, []string) {
	if len(a) <= n {
		return a, b, d
	}
	step := float64(len(a)-1) / float64(n-1)
	oa, ob, od := make([]float64, n), make([]float64, n), make([]string, n)
	for i := 0; i < n; i++ {
		k := int(math.Round(float64(i) * step))
		oa[i], ob[i], od[i] = a[k], b[k], d[k]
	}
	return oa, ob, od
}
