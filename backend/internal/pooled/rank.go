package pooled

import (
	"errors"
	"math"
	"runtime"
	"sort"
	"sync"
	"time"

	"github.com/sodiq/financial-market/backend/internal/gbm"
	"github.com/sodiq/financial-market/backend/internal/market"
	"github.com/sodiq/financial-market/backend/internal/model"
)

// RankKeys are asset-specific inputs. Market-wide inputs (VIX, rates) are
// identical for every asset on a date, so they carry no cross-sectional
// information and are excluded.
var RankKeys = []string{
	"ret_1", "ret_5", "ret_21", "ret_63", "mom_12_1", "mom_vol", "dist_sma20", "dist_sma50", "dist_sma200",
	"sma50_200", "adx_dir", "rsi14", "bb_z20", "stoch14", "macd_hist", "vol_21", "vol_ratio", "atr_pct",
	"dist_52w_high", "dist_52w_low", "volume_z", "obv_flow", "earn_soon", "pead", "earn_react", "beat4",
	"funding_7d", "funding_z",
}

// coreRankKeys must be defined for an asset to be ranked on a date.
var coreRankKeys = []string{"ret_21", "ret_63", "vol_21"}

const minCrossSection = 5

// RankModel ranks a universe of assets by their chance of beating the
// universe median over the horizon.
type RankModel struct {
	Universe   string         `json:"universe"`
	Label      string         `json:"label"`    // what "beat the median" is measured on
	Deployed   string         `json:"deployed"` // "logistic" or "blend" (logistic + boosted trees)
	GBM        *gbm.Model     `json:"gbm,omitempty"`
	Horizon    string         `json:"horizon"`
	Bars       int            `json:"bars"`
	Keys       []string       `json:"keys"`
	Labels     []string       `json:"labels"`
	Final      Fold           `json:"final"`
	Lambda     float64        `json:"lambda"`
	Assets     []string       `json:"assets"`
	Validation RankValidation `json:"validation"`
	AsOf       string         `json:"asOf"`
	Live       []RankItem     `json:"live"`
	TrainedAt  time.Time      `json:"trainedAt"`
}

type RankValidation struct {
	Status     string        `json:"status"` // validated | weak | no_edge
	StatusNote string        `json:"statusNote"`
	TestFrom   string        `json:"testFrom"`
	TestTo     string        `json:"testTo"`
	Rows       int           `json:"rows"`
	MeanIC     float64       `json:"meanIc"`    // average Spearman rank correlation, forecast vs outcome
	ICTStat    float64       `json:"icTStat"`   // on non-overlapping periods
	ICHitRate  float64       `json:"icHitRate"` // share of periods with IC > 0
	Periods    int           `json:"periods"`   // non-overlapping rebalances
	HitRate    float64       `json:"hitRate"`   // P>0.5 ⇔ beat the median
	TopExcess  float64       `json:"topExcess"` // avg top-quintile return minus universe average, per period
	BotExcess  float64       `json:"botExcess"`
	Deciles    []DecileStat  `json:"deciles"`
	Strategy   RankStrategy  `json:"strategy"`
	Metrics    model.Metrics `json:"metrics"`
	// Beta-neutral view: how much of the top fifth's edge survives after
	// removing what its market exposure alone would have earned.
	TopAlpha   float64                  `json:"topAlpha"`
	TopBeta    float64                  `json:"topBeta"`
	Components map[string]ComponentStat `json:"components"` // logistic / gbm / blend scored separately
}

type ComponentStat struct {
	MeanIC  float64 `json:"meanIc"`
	ICTStat float64 `json:"icTStat"`
}

type DecileStat struct {
	Decile    int     `json:"decile"` // 1 = lowest forecast, 10 = highest
	N         int     `json:"n"`
	AvgExcess float64 `json:"avgExcess"`
	BeatRate  float64 `json:"beatRate"`
}

type RankStrategy struct {
	CostPerTrade float64 `json:"costPerTrade"`
	CAGR         float64 `json:"cagr"`
	BenchCAGR    float64 `json:"benchCagr"`
	Sharpe       float64 `json:"sharpe"`
	BenchSharpe  float64 `json:"benchSharpe"`
	MaxDrawdown  float64 `json:"maxDrawdown"`
	BenchMaxDD   float64 `json:"benchMaxDrawdown"`
	Turnover     float64 `json:"turnover"` // average share of the basket replaced per rebalance
	// BreakEvenCost is the cost per trade (each side) at which the top
	// fifth's edge over the universe would be used up by trading.
	BreakEvenCost float64   `json:"breakEvenCost"`
	Equity        []float64 `json:"equity"`
	Bench         []float64 `json:"bench"`
	Dates         []string  `json:"dates"`
}

type RankItem struct {
	Symbol         string       `json:"symbol"`
	Name           string       `json:"name"`
	Price          float64      `json:"price"`
	LastDate       string       `json:"lastDate"`
	POutperform    float64      `json:"pOutperform"`
	Rank           int          `json:"rank"`
	Of             int          `json:"of"`
	ExpectedExcess float64      `json:"expectedExcess"` // historical avg excess of this forecast decile
	Drivers        []RankDriver `json:"drivers"`
}

type RankDriver struct {
	Key          string  `json:"key"`
	Label        string  `json:"label"`
	Value        float64 `json:"value"`      // raw input value
	Percentile   float64 `json:"percentile"` // 0..1 within the universe today
	Contribution float64 `json:"contribution"`
}

// crossSection is the set of rankable assets on one date.
type crossSection struct {
	date   string
	assets []int
	ts     []int
	x      [][]float64 // rank-normalised inputs
	raw    [][]float64
	pct    [][]float64
}

// detail=false skips the raw values and percentiles that only the live
// ranking displays, which matters at S&P 500 scale.
func buildCrossSection(date string, assets []*Asset, keys []string, needLabel int, detail bool) *crossSection {
	cs := &crossSection{date: date}
	for ai, a := range assets {
		t, ok := a.dateIdx[date]
		if !ok || t+needLabel >= len(a.Dates) || !a.IsMember(date) {
			continue
		}
		good := true
		for _, k := range coreRankKeys {
			if c := a.Raw[k]; c == nil || math.IsNaN(c[t]) {
				good = false
				break
			}
		}
		if good {
			cs.assets, cs.ts = append(cs.assets, ai), append(cs.ts, t)
		}
	}
	n := len(cs.assets)
	if n < minCrossSection {
		return nil
	}
	cs.x = make([][]float64, n)
	flat := make([]float64, n*len(keys))
	for i := range cs.x {
		cs.x[i] = flat[i*len(keys) : (i+1)*len(keys) : (i+1)*len(keys)]
	}
	if detail {
		cs.raw, cs.pct = make([][]float64, n), make([][]float64, n)
		for i := range cs.raw {
			cs.raw[i], cs.pct[i] = make([]float64, len(keys)), make([]float64, len(keys))
		}
	}
	col := make([]float64, n)
	vals := make([]float64, n)
	idx := make([]int, n)
	ord := make([]int, n)
	for j, k := range keys {
		m := 0
		c := assets[0].Raw[k]
		for i, ai := range cs.assets {
			v := math.NaN()
			if c = assets[ai].Raw[k]; c != nil {
				v = c[cs.ts[i]]
			}
			col[i] = v
			if !math.IsNaN(v) {
				vals[m], idx[m] = v, i
				m++
			}
		}
		// Rank among assets that have this input; others stay at 0 (median).
		o := ord[:m]
		for q := range o {
			o[q] = q
		}
		sort.Slice(o, func(a, b int) bool { return vals[o[a]] < vals[o[b]] })
		for r := 0; r < m; {
			e := r
			for e+1 < m && vals[o[e+1]] == vals[o[r]] {
				e++
			}
			avg := float64(r+e) / 2
			for q := r; q <= e; q++ {
				i := idx[o[q]]
				pc := 0.5
				if m > 1 {
					pc = avg / float64(m-1)
					cs.x[i][j] = 2*pc - 1
				}
				if detail {
					cs.pct[i][j] = pc
				}
			}
			r = e + 1
		}
		if detail {
			for i := range cs.assets {
				cs.raw[i][j] = col[i]
				if math.IsNaN(col[i]) {
					cs.pct[i][j] = math.NaN()
				}
			}
		}
	}
	return cs
}

// TrainRank fits and validates a ranking model for one universe.
func TrainRank(universe string, h market.Horizon, assets []*Asset, cfg DirConfig) (*RankModel, error) {
	keys := RankKeys
	windowStart := addYears(time.Now().UTC().Format("2006-01-02"), -cfg.Years)
	computeBetas(assets)
	dateSet := map[string]bool{}
	for _, a := range assets {
		for _, d := range a.Dates {
			if d >= windowStart {
				dateSet[d] = true
			}
		}
	}
	var allDates []string
	for d := range dateSet {
		allDates = append(allDates, d)
	}
	sort.Strings(allDates)
	stride := max(1, h.Bars/5)

	var rows []row
	for di := 0; di < len(allDates); di += stride {
		cs := buildCrossSection(allDates[di], assets, keys, h.Bars, false)
		if cs == nil {
			continue
		}
		fwd := make([]float64, len(cs.assets))
		mkt := 0.0
		for i, ai := range cs.assets {
			a := assets[ai]
			fwd[i] = a.Close[cs.ts[i]+h.Bars]/a.Close[cs.ts[i]] - 1
			mkt += fwd[i]
		}
		mkt /= float64(len(fwd))
		// Beta-neutral label: beating the median after removing what the
		// asset's market exposure alone explains. Otherwise, in a rising
		// market, the model learns to pick high-beta names, which is a
		// leveraged market bet rather than skill.
		resid := make([]float64, len(fwd))
		betas := make([]float64, len(fwd))
		for i, ai := range cs.assets {
			betas[i] = 1
			if b := assets[ai].beta; b != nil {
				betas[i] = b[cs.ts[i]]
			}
			resid[i] = fwd[i] - betas[i]*mkt
		}
		med := model.Quantile(resid, 0.5)
		for i, ai := range cs.assets {
			y := 0.0
			if resid[i] > med {
				y = 1
			}
			a := assets[ai]
			rows = append(rows, row{asset: ai, t: cs.ts[i], date: cs.date, labelEnd: a.Dates[cs.ts[i]+h.Bars], x: cs.x[i], y: y,
				fwd: fwd[i], resid: resid[i], beta: betas[i]})
		}
	}
	dates := uniqueDates(rows)
	if len(dates) < 300 {
		return nil, errors.New("not enough cross-sectional history")
	}
	first := sort.SearchStrings(dates, addYears(dates[0], cfg.MinYears))
	blocks := dateBlocks(dates, first, cfg.Blocks)
	if len(blocks) == 0 {
		return nil, errors.New("not enough cross-sectional history for validation")
	}
	m := &RankModel{Universe: universe, Horizon: h.Key, Bars: h.Bars, Keys: keys, TrainedAt: time.Now().UTC(),
		Label: "beta-neutral excess return vs the universe median"}
	for _, k := range keys {
		m.Labels = append(m.Labels, labelFor(k))
	}
	for _, a := range assets {
		m.Assets = append(m.Assets, a.Symbol)
	}
	m.Lambda = chooseLambda(rows, blocks[0][0])

	pred, predLR, predGB := make([]float64, len(rows)), make([]float64, len(rows)), make([]float64, len(rows))
	for i := range pred {
		pred[i], predLR[i], predGB[i] = math.NaN(), math.NaN(), math.NaN()
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, runtime.GOMAXPROCS(0))
	var ferr error
	var mu sync.Mutex
	for _, b := range blocks {
		wg.Add(1)
		sem <- struct{}{}
		go func(from, to string) {
			defer wg.Done()
			defer func() { <-sem }()
			f, _, err := fitRows(rows, func(r *row) bool { return r.labelEnd <= from }, m.Lambda)
			if err != nil {
				mu.Lock()
				ferr = err
				mu.Unlock()
				return
			}
			gb := fitGBM(rows, from)
			for i := range rows {
				if rows[i].date >= from && rows[i].date <= to {
					predLR[i] = f.Prob(rows[i].x)
					predGB[i] = predLR[i]
					if gb != nil {
						predGB[i] = gb.Prob(rows[i].x)
					}
					pred[i] = (predLR[i] + predGB[i]) / 2
				}
			}
		}(b[0], b[1])
	}
	wg.Wait()
	if ferr != nil {
		return nil, ferr
	}
	step := max(1, h.Bars/stride)
	comps := map[string]ComponentStat{
		"blend":    icStats(rows, pred, step),
		"logistic": icStats(rows, predLR, step),
		"gbm":      icStats(rows, predGB, step),
	}
	// The trees join only if they earned it out-of-sample (same rule as the
	// cross-asset direction model). On the first real training they never
	// beat the linear model, and blending them in diluted it. Validation
	// always describes the model that is actually deployed.
	useGBM := comps["gbm"].MeanIC >= comps["logistic"].MeanIC
	if useGBM {
		m.Validation = evaluateRank(rows, pred, h, stride, blocks, cfg.RankCost)
	} else {
		m.Validation = evaluateRank(rows, predLR, h, stride, blocks, cfg.RankCost)
	}
	m.Validation.Components = comps
	m.Deployed = "logistic"
	if useGBM {
		m.Deployed = "blend"
	}
	final, _, err := fitRows(rows, func(*row) bool { return true }, m.Lambda)
	if err != nil {
		return nil, err
	}
	m.Final = *final
	if useGBM {
		m.GBM = fitGBM(rows, "9999-12-31")
	}
	m.liveRanking(assets)
	return m, nil
}

// evaluateRank turns out-of-sample forecasts into cross-sectional metrics
// and a long-only top-quintile strategy versus the equal-weight universe.
func evaluateRank(rows []row, pred []float64, h market.Horizon, stride int, blocks [][2]string, cost float64) RankValidation {
	if cost <= 0 {
		cost = 0.001
	}
	v := RankValidation{TestFrom: blocks[0][0], TestTo: blocks[len(blocks)-1][1], Strategy: RankStrategy{CostPerTrade: cost}}
	byDate := map[string][]int{}
	var p, y, base []float64
	for i := range rows {
		if math.IsNaN(pred[i]) {
			continue
		}
		byDate[rows[i].date] = append(byDate[rows[i].date], i)
		p, y, base = append(p, pred[i]), append(y, rows[i].y), append(base, 0.5)
	}
	var dates []string
	for d := range byDate {
		dates = append(dates, d)
	}
	sort.Strings(dates)
	v.Rows = len(p)
	if len(dates) == 0 {
		v.Status = "no_edge"
		return v
	}
	// Periods are non-overlapping: dates are already sampled every `stride`
	// bars, so take every (h/stride)-th one.
	step := max(1, h.Bars/stride)
	v.Metrics = model.ComputeMetricsN(p, y, base, len(dates)/step)
	hit := 0.0
	for i := range p {
		if (p[i] > 0.5) == (y[i] == 1) {
			hit++
		}
	}
	v.HitRate = hit / float64(len(p))

	dec := make([]DecileStat, 10)
	for i := range dec {
		dec[i].Decile = i + 1
	}
	var ics []float64
	eq, bench := 1.0, 1.0
	peak, bpeak := 1.0, 1.0
	var srets, brets []float64
	var prevTop map[int]bool
	turn, nTurn := 0.0, 0
	for di, d := range dates {
		ids := byDate[d]
		n := len(ids)
		ps, fw := make([]float64, n), make([]float64, n)
		mean := 0.0
		for k, i := range ids {
			ps[k], fw[k] = pred[i], rows[i].fwd
			mean += fw[k]
		}
		mean /= float64(n)
		// Deciles of the forecast within the day's cross-section.
		pr := ranks(ps)
		for k := range ids {
			dk := min(9, int(pr[k]*10/float64(n)))
			dec[dk].N++
			dec[dk].AvgExcess += fw[k] - mean
			if rows[ids[k]].y == 1 {
				dec[dk].BeatRate++
			}
		}
		if di%step != 0 {
			continue
		}
		res := make([]float64, n)
		for k, i := range ids {
			res[k] = rows[i].resid
		}
		ics = append(ics, spearman(ps, res)) // skill measured on beta-neutral outcomes
		order := make([]int, n)
		for k := range order {
			order[k] = k
		}
		sort.Slice(order, func(a, b int) bool { return ps[order[a]] > ps[order[b]] })
		q := max(1, n/5)
		top, bot, topRes, topBeta, allRes := 0.0, 0.0, 0.0, 0.0, 0.0
		cur := map[int]bool{}
		for k := 0; k < q; k++ {
			top += fw[order[k]]
			bot += fw[order[n-1-k]]
			topRes += res[order[k]]
			topBeta += rows[ids[order[k]]].beta
			cur[rows[ids[order[k]]].asset] = true
		}
		for _, x := range res {
			allRes += x
		}
		top /= float64(q)
		bot /= float64(q)
		v.TopAlpha += topRes/float64(q) - allRes/float64(n)
		v.TopBeta += topBeta / float64(q)
		v.TopExcess += top - mean
		v.BotExcess += bot - mean
		changed := 1.0
		if prevTop != nil {
			c := 0
			for a := range cur {
				if !prevTop[a] {
					c++
				}
			}
			changed = float64(c) / float64(q)
			turn += changed
			nTurn++
		}
		prevTop = cur
		r := top - 2*v.Strategy.CostPerTrade*changed
		eq *= 1 + r
		bench *= 1 + mean
		peak, bpeak = math.Max(peak, eq), math.Max(bpeak, bench)
		v.Strategy.MaxDrawdown = math.Min(v.Strategy.MaxDrawdown, eq/peak-1)
		v.Strategy.BenchMaxDD = math.Min(v.Strategy.BenchMaxDD, bench/bpeak-1)
		srets, brets = append(srets, r), append(brets, mean)
		v.Strategy.Equity, v.Strategy.Bench, v.Strategy.Dates = append(v.Strategy.Equity, eq), append(v.Strategy.Bench, bench), append(v.Strategy.Dates, d)
	}
	for i := range dec {
		if dec[i].N > 0 {
			dec[i].AvgExcess /= float64(dec[i].N)
			dec[i].BeatRate /= float64(dec[i].N)
		}
	}
	v.Deciles = dec
	v.Periods = len(ics)
	if v.Periods > 0 {
		v.TopExcess /= float64(v.Periods)
		v.BotExcess /= float64(v.Periods)
		v.TopAlpha /= float64(v.Periods)
		v.TopBeta /= float64(v.Periods)
	}
	if nTurn > 0 {
		v.Strategy.Turnover = turn / float64(nTurn)
	}
	if v.Strategy.Turnover > 0 {
		v.Strategy.BreakEvenCost = v.TopExcess / (2 * v.Strategy.Turnover)
	}
	mIC, sIC, pos := 0.0, 0.0, 0.0
	for _, c := range ics {
		mIC += c
		if c > 0 {
			pos++
		}
	}
	if len(ics) > 0 {
		mIC /= float64(len(ics))
		for _, c := range ics {
			sIC += (c - mIC) * (c - mIC)
		}
		if len(ics) > 1 {
			sIC = math.Sqrt(sIC / float64(len(ics)-1))
		}
		v.ICHitRate = pos / float64(len(ics))
	}
	v.MeanIC = mIC
	if sIC > 0 {
		v.ICTStat = mIC / sIC * math.Sqrt(float64(len(ics)))
	}
	barsPerYear := 252.0
	if h.Bars == 7 || h.Bars == 30 || h.Bars == 365 {
		barsPerYear = 365
	}
	years := float64(len(srets)*h.Bars) / barsPerYear
	if years > 0 {
		v.Strategy.CAGR = math.Pow(math.Max(eq, 1e-9), 1/years) - 1
		v.Strategy.BenchCAGR = math.Pow(math.Max(bench, 1e-9), 1/years) - 1
	}
	ppy := barsPerYear / float64(h.Bars)
	v.Strategy.Sharpe, v.Strategy.BenchSharpe = sharpe(srets, ppy), sharpe(brets, ppy)
	v.Strategy.Equity, v.Strategy.Bench, v.Strategy.Dates = thin(v.Strategy.Equity, v.Strategy.Bench, v.Strategy.Dates, 300)

	switch {
	case v.ICTStat >= 2 && v.TopExcess > 0 && v.TopAlpha > 0 && v.Strategy.CAGR > v.Strategy.BenchCAGR && v.Periods >= 20:
		v.Status = "validated"
		v.StatusNote = "Top-ranked assets beat the universe out-of-sample with a statistically significant rank correlation, after costs and after removing their market exposure (beta)."
	case v.MeanIC > 0 && v.TopExcess > 0:
		v.Status = "weak"
		v.StatusNote = "Rankings leaned the right way out-of-sample, but not strongly enough to be statistically reliable (needs IC t-stat ≥ 2 and a top-fifth basket that beat the universe after costs)."
	default:
		v.Status = "no_edge"
		v.StatusNote = "Out-of-sample, the model's top picks did not beat the rest. Treat today's ranking as uninformative."
	}
	return v
}

// liveRanking ranks today's cross-section with the final model.
func (m *RankModel) liveRanking(assets []*Asset) {
	latest := ""
	for _, a := range assets {
		if n := len(a.Dates); n > 0 && a.Dates[n-1] > latest {
			latest = a.Dates[n-1]
		}
	}
	m.AsOf = latest
	// Each asset contributes its own last bar if it is recent (markets on
	// different holidays). Re-key them onto a common pseudo-date.
	cutoff := addDaysStr(latest, -5)
	type cand struct {
		ai int
		t  int
	}
	var cands []cand
	for ai, a := range assets {
		n := len(a.Dates)
		if n > 0 && a.Dates[n-1] >= cutoff {
			cands = append(cands, cand{ai, n - 1})
		}
	}
	if len(cands) < minCrossSection {
		return
	}
	sub := make([]*Asset, len(cands))
	for i, c := range cands {
		a := *assets[c.ai]
		a.dateIdx = map[string]int{"live": c.t}
		sub[i] = &a
	}
	cs := buildCrossSection("live", sub, m.Keys, 0, true)
	if cs == nil {
		return
	}
	ps := make([]float64, len(cs.assets))
	for i := range cs.assets {
		ps[i] = m.Final.Prob(cs.x[i])
		if m.GBM != nil {
			ps[i] = (ps[i] + m.GBM.Prob(cs.x[i])) / 2
		}
	}
	pr := ranks(ps)
	n := len(ps)
	for i, si := range cs.assets {
		a := sub[si]
		t := cs.ts[i]
		it := RankItem{Symbol: a.Symbol, Name: a.Name, Price: a.Close[t], LastDate: a.Dates[t], POutperform: ps[i], Of: n}
		dk := min(9, int(pr[i]*10/float64(n)))
		if dk < len(m.Validation.Deciles) {
			it.ExpectedExcess = m.Validation.Deciles[dk].AvgExcess
		}
		for j, k := range m.Keys {
			if math.IsNaN(cs.raw[i][j]) {
				continue
			}
			c := m.Final.W[j] * cs.x[i][j]
			it.Drivers = append(it.Drivers, RankDriver{Key: k, Label: m.Labels[j], Value: cs.raw[i][j], Percentile: cs.pct[i][j], Contribution: c})
		}
		sort.Slice(it.Drivers, func(x, y int) bool {
			return math.Abs(it.Drivers[x].Contribution) > math.Abs(it.Drivers[y].Contribution)
		})
		if len(it.Drivers) > 6 {
			it.Drivers = it.Drivers[:6]
		}
		m.Live = append(m.Live, it)
	}
	sort.Slice(m.Live, func(i, j int) bool { return m.Live[i].POutperform > m.Live[j].POutperform })
	for i := range m.Live {
		m.Live[i].Rank = i + 1
	}
}

// ranks returns average 0-based ranks (ties averaged).
func ranks(x []float64) []float64 {
	n := len(x)
	ord := make([]int, n)
	for i := range ord {
		ord[i] = i
	}
	sort.Slice(ord, func(a, b int) bool { return x[ord[a]] < x[ord[b]] })
	out := make([]float64, n)
	for r := 0; r < n; {
		e := r
		for e+1 < n && x[ord[e+1]] == x[ord[r]] {
			e++
		}
		for q := r; q <= e; q++ {
			out[ord[q]] = float64(r+e) / 2
		}
		r = e + 1
	}
	return out
}

func spearman(a, b []float64) float64 {
	ra, rb := ranks(a), ranks(b)
	n := float64(len(a))
	if n < 3 {
		return 0
	}
	ma, mb := (n-1)/2, (n-1)/2
	var num, da, db float64
	for i := range ra {
		num += (ra[i] - ma) * (rb[i] - mb)
		da += (ra[i] - ma) * (ra[i] - ma)
		db += (rb[i] - mb) * (rb[i] - mb)
	}
	if da == 0 || db == 0 {
		return 0
	}
	return num / math.Sqrt(da*db)
}

func sharpe(r []float64, ppy float64) float64 {
	if len(r) < 2 {
		return 0
	}
	m, v := 0.0, 0.0
	for _, x := range r {
		m += x
	}
	m /= float64(len(r))
	for _, x := range r {
		v += (x - m) * (x - m)
	}
	sd := math.Sqrt(v / float64(len(r)-1))
	if sd == 0 {
		return 0
	}
	return m / sd * math.Sqrt(ppy)
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

func addDaysStr(d string, n int) string {
	t, err := time.Parse("2006-01-02", d)
	if err != nil {
		return d
	}
	return t.AddDate(0, 0, n).Format("2006-01-02")
}

var keyLabels = map[string]string{
	"ret_1": "1-day return", "ret_5": "1-week return", "ret_21": "1-month return", "ret_63": "3-month return",
	"mom_12_1": "12-month momentum (ex last month)", "mom_vol": "Risk-adjusted 12-month momentum",
	"dist_sma20": "Distance from 20-day average", "dist_sma50": "Distance from 50-day average",
	"dist_sma200": "Distance from 200-day average", "sma50_200": "50-day vs 200-day average",
	"adx_dir": "Directional trend strength", "rsi14": "RSI (14)", "bb_z20": "Bollinger z-score",
	"stoch14": "Stochastic %K", "macd_hist": "MACD histogram", "vol_21": "1-month volatility",
	"vol_ratio": "Volatility regime", "atr_pct": "Average true range %", "dist_52w_high": "Distance from 52-week high",
	"dist_52w_low": "Distance from 52-week low", "volume_z": "Volume vs normal", "obv_flow": "On-balance volume flow",
	"bench_ret_21": "Benchmark 1-month return", "bench_trend": "Benchmark vs 200-day average",
	"rel_strength_63": "3-month relative strength", "vix_z": "VIX vs its norm", "vix_chg_5": "VIX 1-week change",
	"tnx_chg_21": "10-year yield 1-month change", "dxy_chg_21": "Dollar index 1-month change",
	"earn_soon": "Earnings report approaching", "pead": "Last EPS surprise (decaying)",
	"earn_react": "Last earnings price reaction (decaying)", "fng": "Crypto Fear & Greed", "fng_chg_7": "Fear & Greed 1-week change",
	"vix_term": "VIX term structure", "skew_z": "SKEW index vs norm", "vvix_z": "VVIX vs norm", "beat4": "Earnings beat streak",
	"funding_7d": "Perpetual funding (7d, annualised)", "funding_z": "Funding vs 3-month norm",
}

func labelFor(k string) string {
	if l, ok := keyLabels[k]; ok {
		return l
	}
	return k
}

// computeBetas sets each asset's trailing 252-bar beta to the equal-weight
// universe (members only), clipped to [0, 3]. Fewer than 120 shared days
// means beta 1.
func computeBetas(assets []*Asset) {
	sum, cnt := map[string]float64{}, map[string]int{}
	for _, a := range assets {
		r := a.Raw["ret_1"]
		if r == nil {
			continue
		}
		for t, d := range a.Dates {
			if !math.IsNaN(r[t]) && a.IsMember(d) {
				sum[d] += r[t]
				cnt[d]++
			}
		}
	}
	const win, minObs = 252, 120
	for _, a := range assets {
		n := len(a.Dates)
		a.beta = make([]float64, n)
		r := a.Raw["ret_1"]
		valid := make([]bool, n)
		mk := make([]float64, n)
		var sx, sm, sxm, smm float64
		k := 0
		for t := 0; t < n; t++ {
			if r != nil && !math.IsNaN(r[t]) && cnt[a.Dates[t]] >= minCrossSection {
				valid[t], mk[t] = true, sum[a.Dates[t]]/float64(cnt[a.Dates[t]])
				sx, sm, sxm, smm = sx+r[t], sm+mk[t], sxm+r[t]*mk[t], smm+mk[t]*mk[t]
				k++
			}
			if t >= win && valid[t-win] {
				o := t - win
				sx, sm, sxm, smm = sx-r[o], sm-mk[o], sxm-r[o]*mk[o], smm-mk[o]*mk[o]
				k--
			}
			a.beta[t] = 1
			if k >= minObs {
				fk := float64(k)
				if vm := smm - sm*sm/fk; vm > 0 {
					a.beta[t] = math.Max(0, math.Min(3, (sxm-sx*sm/fk)/vm))
				}
			}
		}
	}
}

// fitGBM trains the boosted-tree component on rows whose labels are known
// by `from`: the last 15% of those dates (purged) drive early stopping. At
// most 250k training rows are used (an even sample) to bound training time.
func fitGBM(rows []row, from string) *gbm.Model {
	var eligible []row
	for _, r := range rows {
		if r.labelEnd <= from {
			eligible = append(eligible, r)
		}
	}
	ds := uniqueDates(eligible)
	if len(ds) < 100 {
		return nil
	}
	cut := ds[len(ds)*85/100]
	var tr, va []int
	for i, r := range eligible {
		switch {
		case r.labelEnd <= cut:
			tr = append(tr, i)
		case r.date > cut:
			va = append(va, i)
		}
	}
	thin := func(idx []int, limit int) []int {
		if len(idx) <= limit {
			return idx
		}
		out := make([]int, 0, limit)
		step := float64(len(idx)) / float64(limit)
		for k := 0; k < limit; k++ {
			out = append(out, idx[int(float64(k)*step)])
		}
		return out
	}
	tr, va = thin(tr, 250000), thin(va, 60000)
	if len(tr) < 2000 || len(va) < 200 {
		return nil
	}
	X, y := make([][]float64, len(tr)), make([]float64, len(tr))
	for k, i := range tr {
		X[k], y[k] = eligible[i].x, eligible[i].y
	}
	vX, vy := make([][]float64, len(va)), make([]float64, len(va))
	for k, i := range va {
		vX[k], vy[k] = eligible[i].x, eligible[i].y
	}
	return gbm.Fit(X, y, vX, vy, gbm.DefaultParams())
}

// icStats scores one forecast series: mean rank correlation with the
// beta-neutral outcome on non-overlapping dates, and its t-stat.
func icStats(rows []row, pred []float64, step int) ComponentStat {
	byDate := map[string][]int{}
	for i := range rows {
		if !math.IsNaN(pred[i]) {
			byDate[rows[i].date] = append(byDate[rows[i].date], i)
		}
	}
	var dates []string
	for d := range byDate {
		dates = append(dates, d)
	}
	sort.Strings(dates)
	var ics []float64
	for di, d := range dates {
		if di%step != 0 {
			continue
		}
		ids := byDate[d]
		ps, rs := make([]float64, len(ids)), make([]float64, len(ids))
		for k, i := range ids {
			ps[k], rs[k] = pred[i], rows[i].resid
		}
		ics = append(ics, spearman(ps, rs))
	}
	var st ComponentStat
	if len(ics) < 2 {
		return st
	}
	m, v := 0.0, 0.0
	for _, c := range ics {
		m += c
	}
	m /= float64(len(ics))
	for _, c := range ics {
		v += (c - m) * (c - m)
	}
	sd := math.Sqrt(v / float64(len(ics)-1))
	st.MeanIC = m
	if sd > 0 {
		st.ICTStat = m / sd * math.Sqrt(float64(len(ics)))
	}
	return st
}
