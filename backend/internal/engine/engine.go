package engine

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sodiq/financial-market/backend/internal/earnings"
	"github.com/sodiq/financial-market/backend/internal/features"
	"github.com/sodiq/financial-market/backend/internal/market"
	"github.com/sodiq/financial-market/backend/internal/model"
	"github.com/sodiq/financial-market/backend/internal/news"
	"github.com/sodiq/financial-market/backend/internal/pooled"
)

// pending is an analysis whose quantitative part is done and whose news
// judgment may still be on its way (synchronously or in a batch).
type pending struct {
	t0      time.Time
	opt     Options
	rep     *Report
	ser     *market.Series
	mat     *features.Matrix
	arts    []news.Article
	results []*model.Result
	newArts []news.Article // headlines Claude has not judged before
	options []market.Expiry
	full    *market.Series
}

type Engine struct {
	Market   *market.Service
	News     *news.Collector
	Analyst  *news.Analyst     // nil when no API key
	Memory   news.Memory       // judgment reuse + usage log; nil disables both
	Earnings *earnings.Service // nil disables earnings features
	Pooled   *pooled.Trainer   // nil disables cross-asset models
	Monitor  DriftMonitor      // live-record drift checks; nil disables
	Config   model.Config
	Log      *slog.Logger
}

// DriftMonitor reports symbols and horizons whose live forecasts have been
// worse than the base rate (see tracker.Tracker).
type DriftMonitor interface {
	Drift(symbol, horizon string) *Drift
}

type Options struct {
	Force  bool   // refetch prices even if cached
	UseLLM bool   // run the Claude news analyst (if configured)
	NoNews bool   // skip news entirely (fast scans)
	AsOf   string // replay as of this past date (YYYY-MM-DD); "" = today
}

// Analyze runs a complete analysis synchronously.
func (e *Engine) Analyze(ctx context.Context, symbol string, opt Options) (*Report, error) {
	p, err := e.begin(ctx, symbol, opt)
	if err != nil {
		return nil, err
	}
	var an *news.Analysis
	var llmErr error
	if req, ok := e.llmRequest(ctx, p); ok {
		lctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		var u news.Usage
		an, u, llmErr = e.Analyst.Analyze(lctx, req)
		cancel()
		e.recordUsage(ctx, p.rep.Symbol, u)
	}
	e.finish(ctx, p, an, llmErr)
	return p.rep, nil
}

// BatchItem is one symbol's result from AnalyzeBatch.
type BatchItem struct {
	Symbol string
	Report *Report
	Err    error
}

// AnalyzeBatch analyses several symbols with every Claude call sent as one
// Message Batch (half price). For scheduled runs, where nobody is waiting.
// If the batch cannot be submitted or doesn't finish in time, the affected
// symbols fall back to synchronous calls so no analysis loses its news
// judgment.
func (e *Engine) AnalyzeBatch(ctx context.Context, symbols []string, opt Options) []BatchItem {
	items := make([]BatchItem, len(symbols))
	ps := make([]*pending, len(symbols))
	reqs := map[string]news.AnalysisRequest{}
	for i, sym := range symbols {
		items[i].Symbol = sym
		p, err := e.begin(ctx, sym, opt)
		if err != nil {
			items[i].Err = err
			continue
		}
		ps[i] = p
		if req, ok := e.llmRequest(ctx, p); ok {
			reqs[p.rep.Symbol] = req
		}
	}
	var outcomes map[string]news.BatchOutcome
	if len(reqs) > 0 {
		var err error
		outcomes, err = e.Analyst.RunBatch(ctx, reqs, 30*time.Second, 3*time.Hour)
		if err != nil {
			e.Log.Warn("batch analysis failed; falling back to synchronous calls", "err", err)
		}
	}
	for i, p := range ps {
		if p == nil {
			continue
		}
		var an *news.Analysis
		var llmErr error
		if req, ok := reqs[p.rep.Symbol]; ok {
			o, done := outcomes[p.rep.Symbol]
			if done {
				e.recordUsage(ctx, p.rep.Symbol, o.Usage)
			}
			if done && o.Err == nil {
				an = o.Analysis
			} else {
				lctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
				var u news.Usage
				an, u, llmErr = e.Analyst.Analyze(lctx, req)
				cancel()
				e.recordUsage(ctx, p.rep.Symbol, u)
			}
		}
		e.finish(ctx, p, an, llmErr)
		items[i].Report = p.rep
	}
	return items
}

func (e *Engine) recordUsage(ctx context.Context, symbol string, u news.Usage) {
	if e.Memory == nil || u.Model == "" {
		return
	}
	if err := e.Memory.RecordUsage(ctx, symbol, u); err != nil {
		e.Log.Warn("record llm usage", "err", err)
	}
}

// llmRequest applies judgments remembered from earlier runs and builds the
// request for the rest. ok=false when the analyst is off or there is no news.
func (e *Engine) llmRequest(ctx context.Context, p *pending) (news.AnalysisRequest, bool) {
	if !p.opt.UseLLM || e.Analyst == nil || len(p.arts) == 0 {
		return news.AnalysisRequest{}, false
	}
	known := map[string]news.Judgment{}
	if e.Memory != nil {
		if k, err := e.Memory.LoadJudgments(ctx, p.rep.Symbol); err == nil {
			known = k
		}
	}
	req := news.AnalysisRequest{Symbol: p.rep.Symbol, Name: p.rep.Name, QuantSummary: quantSummary(p.rep)}
	var reused []news.Judgment
	for _, a := range p.arts {
		if j, ok := known[a.ID]; ok {
			reused = append(reused, j)
		} else {
			req.New = append(req.New, a)
		}
	}
	(&news.Analysis{Articles: reused}).Apply(p.arts)
	for _, a := range p.arts {
		if a.ScoredBy == "llm" {
			req.Known = append(req.Known, a)
		}
	}
	p.newArts = req.New
	return req, true
}

// begin does everything except the Claude call and the final assembly.
func (e *Engine) begin(ctx context.Context, symbol string, opt Options) (*pending, error) {
	t0 := time.Now()
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	if opt.AsOf != "" {
		opt.NoNews = true // no historical news archive; option chains are live-only too
	}
	prep, err := e.prepare(ctx, symbol, opt.Force, opt.AsOf)
	if err != nil {
		return nil, err
	}
	ser, class, mat := prep.ser, prep.class, prep.mat
	rep := &Report{
		Symbol: ser.Meta.Symbol, Name: ser.Meta.Name, Class: string(class), Currency: ser.Meta.Currency,
		Exchange: ser.Meta.Exchange, Provider: ser.Meta.Provider, GeneratedAt: time.Now().UTC(),
		AsOf: ser.Last().Date, HistoryFrom: ser.Bars[0].Date, HistoryBars: len(ser.Bars),
		Benchmark: prep.benchmark, Warnings: prep.warnings,
	}
	if rep.Name == "" {
		rep.Name = rep.Symbol
	}
	rep.Price = ser.Last().Close
	if n := len(ser.Bars); n > 1 {
		rep.Change1d = ser.Bars[n-1].Close/ser.Bars[n-2].Close - 1
	}
	// News and the option chain are collected concurrently with model fitting.
	var arts []news.Article
	var opts []market.Expiry
	newsDone := make(chan struct{})
	go func() {
		defer close(newsDone)
		if opt.NoNews {
			return
		}
		if class == market.ClassEquity || class == market.ClassETF {
			octx, ocancel := context.WithTimeout(ctx, 20*time.Second)
			opts, _ = e.Market.OptionsImplied(octx, symbol, rep.Price)
			ocancel()
		}
		nctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		arts = e.News.Collect(nctx, news.Request{Symbol: symbol, Name: rep.Name, AssetType: string(class)})
		news.ScoreLexicon(arts, symbol, rep.Name)
	}()
	coveredFrom := ""
	if e.Earnings != nil {
		coveredFrom = e.Earnings.CoveredFrom()
	}
	rep.Earnings = buildEarnings(prep, coveredFrom)
	if e.Pooled != nil && opt.AsOf == "" {
		rep.Ranks = e.Pooled.RanksFor(rep.Symbol)
	}
	for _, d := range mat.Defs {
		rep.Features.Used = append(rep.Features.Used, d.Label)
	}
	rep.Features.Dropped = mat.Dropped

	// Fit every horizon in parallel.
	hs := market.HorizonsFor(class)
	results := make([]*model.Result, len(hs))
	cross := make([]*pooled.DirApplied, len(hs))
	if e.Pooled != nil {
		for i, h := range hs {
			if dm := e.Pooled.DirFor(class, h.Key); dm != nil {
				cross[i] = pooled.ApplyDir(dm, rep.Symbol, class, mat)
				if opt.AsOf != "" {
					// The final model saw the future; use the walk-forward fold
					// that covers the replay date instead.
					cross[i].Live = cross[i].OOS[len(cross[i].OOS)-1]
				}
			}
		}
	}
	errs := make([]error, len(hs))
	var hwg sync.WaitGroup
	for i, h := range hs {
		hwg.Add(1)
		go func(i int, h market.Horizon) {
			defer hwg.Done()
			var ext *model.External
			if cross[i] != nil && cross[i].Model.Skilled() {
				ext = &model.External{OOS: cross[i].OOS, Live: cross[i].Live}
			}
			results[i], errs[i] = model.RunWith(mat, h.Bars, market.BarsPerYear(class), e.Config, ext)
		}(i, h)
	}
	hwg.Wait()

	last := len(mat.Close) - 1
	horizonBars := map[string]int{}
	for i, h := range hs {
		hf := HorizonForecast{Key: h.Key, Label: h.Label, Bars: h.Bars, TargetDate: targetDate(rep.AsOf, h.Bars, class)}
		res := results[i]
		switch {
		case errs[i] != nil:
			hf.Reason = errs[i].Error()
		case res.Live == nil:
			hf.Reason = "latest bar has incomplete features"
		default:
			hf.Available = true
			horizonBars[h.Key] = h.Bars
			e.fillQuant(&hf, res, mat, rep.Price, last)
			fillCross(&hf, cross[i], res)
		}
		rep.Horizons = append(rep.Horizons, hf)
	}
	rep.Indicators = readings(mat, rep.Price)
	rep.Evidence = conditionalEvidence(mat, horizonBars)
	rep.Chart = chart(ser, mat, 750)

	<-newsDone
	rep.Options = opts
	return &pending{t0: t0, opt: opt, rep: rep, ser: ser, full: prep.full, mat: mat, arts: arts, results: results, options: opts}, nil
}

// finish merges the news judgment (an may be nil), applies the bounded news
// overlay and finalises every signal.
func (e *Engine) finish(ctx context.Context, p *pending, an *news.Analysis, llmErr error) {
	rep, arts, mat := p.rep, p.arts, p.mat
	rep.News = NewsSection{ScoredBy: "none", Articles: arts, Aggregates: map[string]news.Aggregate{}}
	if len(arts) > 0 {
		rep.News.ScoredBy = "lexicon"
	}
	switch {
	case an != nil:
		an.Apply(arts)
		rep.News.ScoredBy = "llm"
		rep.UsedLLM, rep.LLMModel = true, an.Model
		rep.Narrative = an.Narrative
		rep.BullCase, rep.BearCase, rep.Catalysts, rep.Risks = an.BullCase, an.BearCase, an.Catalysts, an.Risks
		if e.Memory != nil && len(an.Articles) > 0 {
			newIDs := map[string]bool{}
			for _, a := range p.newArts {
				newIDs[a.ID] = true
			}
			var fresh []news.Judgment
			for _, j := range an.Articles {
				if newIDs[j.ID] {
					fresh = append(fresh, j)
				}
			}
			if err := e.Memory.SaveJudgments(ctx, rep.Symbol, an.Model, fresh); err != nil {
				e.Log.Warn("save judgments", "err", err)
			}
		}
	case llmErr != nil:
		rep.News.Error = "Claude analysis failed, lexicon scores used for new headlines: " + llmErr.Error()
		e.Log.Warn("llm analysis failed", "symbol", rep.Symbol, "err", llmErr)
	case p.opt.UseLLM && e.Analyst == nil:
		rep.Warnings = append(rep.Warnings, "ANTHROPIC_API_KEY not set: news scored with the built-in finance lexicon (less accurate than the Claude analyst).")
	}

	// Apply the bounded news overlay and finalise signals.
	last := len(mat.Close) - 1
	now := time.Now()
	for i := range rep.Horizons {
		hf := &rep.Horizons[i]
		agg := news.AggregateFor(arts, hf.Key, now)
		if hf.Available {
			final := sigmoid(logit(hf.PModel) + agg.LogitShift)
			agg.ProbShift = final - hf.PModel
			hf.PUp, hf.NewsShift = final, agg.ProbShift
			var drift *Drift
			if e.Monitor != nil && p.opt.AsOf == "" {
				drift = e.Monitor.Drift(rep.Symbol, hf.Key)
			}
			e.finalise(hf, rep.Price, p.results[i], mat, last, rep.Earnings, drift)
			if hf.Options = optionsView(p.options, hf, rep.AsOf); hf.Options != nil {
				hf.Why = append(hf.Why, optionsLine(hf))
			}
		}
		rep.News.Aggregates[hf.Key] = agg
	}
	if rep.Narrative == "" {
		rep.Narrative = templateNarrative(rep)
	}
	rep.Headline = headline(rep)
	if p.opt.AsOf != "" {
		rep.Replay = buildReplay(p.full, rep)
	} else {
		rep.Warnings = append(rep.Warnings, dataWarnings(rep, p.ser, mat)...)
	}
	rep.ElapsedMs = time.Since(p.t0).Milliseconds()
}

func (e *Engine) fillQuant(hf *HorizonForecast, res *model.Result, mat *features.Matrix, price float64, last int) {
	lv := res.Live
	hf.PLogistic, hf.PAnalogs, hf.PRaw, hf.PModel, hf.BaseRate = lv.PLogistic, lv.PAnalogs, lv.PRaw, lv.PCalibrated, lv.BaseRate
	hf.PUp = lv.PCalibrated
	hf.Validation = res.Validation
	hf.ExpectedReturn = lv.ExpectedRet
	hf.PriceTargets, hf.ReturnRange = map[string]float64{}, map[string]float64{}
	for k, q := range lv.RetQuantiles {
		hf.PriceTargets[k] = price * math.Exp(q)
		hf.ReturnRange[k] = math.Exp(q) - 1
	}
	hf.Drivers, hf.GroupImpact = buildDrivers(mat, lv, hf.Label)

	nb := lv.Neighbors
	scale := mat.VolDaily[last] * math.Sqrt(float64(hf.Bars))
	as := AnalogSummary{K: len(nb)}
	rets := make([]float64, len(nb))
	maxD := 0.0
	for _, n := range nb {
		maxD = math.Max(maxD, n.Distance)
	}
	for i, n := range nb {
		rets[i] = math.Exp(n.RetNorm*scale) - 1
		as.UpRate += n.Y
		as.MeanReturn += rets[i]
	}
	if len(nb) > 0 {
		as.UpRate /= float64(len(nb))
		as.MeanReturn /= float64(len(nb))
		as.MedianReturn = model.Quantile(rets, 0.5)
	}
	for i := 0; i < min(12, len(nb)); i++ {
		sim := 1.0
		if maxD > 0 {
			sim = 1 - nb[i].Distance/(maxD*1.25)
		}
		as.Examples = append(as.Examples, Analog{Date: mat.Dates[nb[i].Index], Similarity: sim, Return: math.Exp(nb[i].Ret) - 1})
	}
	hf.Analogs = as
}

func (e *Engine) finalise(hf *HorizonForecast, price float64, res *model.Result, mat *features.Matrix, last int, ei EarningsInfo, drift *Drift) {
	hf.Status, hf.StatusNote, hf.Signal = classify(hf.Validation, hf.PUp)
	if drift != nil {
		d := *drift
		hf.Drift = &d
		if hf.Status != "no_edge" {
			d.Demoted = true
			hf.StatusNote = fmt.Sprintf("Demoted to NO EDGE by live monitoring. The backtest rated this horizon %s, but its last %d real-time forecasts (%s to %s) scored worse than simply predicting the base rate (Brier %.3f vs %.3f, hit rate %.0f%%). The live record overrides the backtest until it recovers.",
				hf.Status, d.N, d.FirstAsOf, d.LastAsOf, d.Brier, d.BaseBrier, 100*d.HitRate)
			hf.Status, hf.Signal = "no_edge", "NO EDGE"
		}
	}
	// Direction and magnitude must agree: a higher-than-even chance of rising
	// with a negative expected return (fat left tail) is not a buy.
	if strings.Contains(hf.Signal, "BUY") && hf.ExpectedReturn <= 0 {
		hf.Signal = "HOLD"
		hf.StatusNote += " Downgraded to HOLD: P(up) favours a rise but the expected return is negative (losses in similar setups were larger than gains)."
	} else if strings.Contains(hf.Signal, "SELL") && hf.ExpectedReturn >= 0 {
		hf.Signal = "HOLD"
		hf.StatusNote += " Downgraded to HOLD: P(up) favours a fall but the expected return is positive."
	}
	hf.TrackAtConf = trackAt(hf.Validation, hf.PRaw)
	hf.Conviction, hf.ConvictionParts = conviction(hf.Status, hf.PUp, hf.TrackAtConf, hf.Validation, hf.PLogistic, hf.PAnalogs)
	scale := mat.VolDaily[last] * math.Sqrt(float64(hf.Bars))
	q := applyEarnings(hf, ei, volAdjust(hf, mat, last, res.Live.RetQuantiles), mat.VolDaily[last])
	for k, v := range q {
		hf.PriceTargets[k] = price * math.Exp(v)
		hf.ReturnRange[k] = math.Exp(v) - 1
	}
	hf.Risk = riskPlan(price, hf.PUp, q, res.Live.Neighbors, scale, hf.Signal)
	if d := (price - hf.Risk.StopLoss) / price; d > 0 {
		hf.Risk.OnePctSize = math.Min(1, 0.01/d)
	}
	if hf.EarningsInside && hf.Bars <= 5 && hf.Risk.SuggestedSize > 0 {
		hf.Risk.SuggestedSize /= 2
		hf.Risk.Note += " Halved because an earnings report falls inside the horizon."
	}
	hf.Why = whyChain(hf)
	if hf.Vol != nil && hf.Vol.Note != "" {
		hf.Why = append(hf.Why, hf.Vol.Note)
	}
}

func whyChain(hf *HorizonForecast) []string {
	v := hf.Validation
	e := v.Ensemble
	var w []string
	w = append(w, fmt.Sprintf("Bottom line: %s probability that the price is higher %s (by %s), versus a %s base rate for any %d-bar window in this asset's history. Signal: %s.",
		pctu(hf.PUp), barsPhrase(hf.Bars), hf.TargetDate, pctu(hf.BaseRate), hf.Bars, hf.Signal))
	w = append(w, fmt.Sprintf("Track record of this exact method on this asset: %d out-of-sample forecasts from %s to %s, each made using only data available at the time. Hit rate %s vs %s for always guessing the majority outcome; Brier skill %+.2f%%; AUC %.3f.",
		e.N, v.TestFrom, v.TestTo, pctu(e.Accuracy), pctu(e.BaseAccuracy), 100*e.BrierSkill, e.AUC))
	w = append(w, "Validation status: "+strings.ToUpper(hf.Status)+". "+hf.StatusNote)
	if t := hf.TrackAtConf; t != nil && t.MinEdge > 0 {
		w = append(w, fmt.Sprintf("When the model was at least this confident in the past (≥%.1f pp from 50/50), it was right %s of %d times (for comparison, always guessing the majority outcome was right %s).", 100*t.MinEdge, pctu(t.Accuracy), t.N, pctu(e.BaseAccuracy)))
	}
	var pos, neg []string
	for _, d := range hf.Drivers {
		if len(pos) < 3 && d.ProbImpact > 0.002 {
			pos = append(pos, fmt.Sprintf("%s (%s)", d.Label, pp(d.ProbImpact)))
		}
		if len(neg) < 3 && d.ProbImpact < -0.002 {
			neg = append(neg, fmt.Sprintf("%s (%s)", d.Label, pp(d.ProbImpact)))
		}
	}
	if len(pos) > 0 {
		w = append(w, "Strongest factors pushing UP: "+strings.Join(pos, "; ")+".")
	}
	if len(neg) > 0 {
		w = append(w, "Strongest factors pushing DOWN: "+strings.Join(neg, "; ")+".")
	}
	a := hf.Analogs
	w = append(w, fmt.Sprintf("Historical analogs: of the %d past days that looked most like today across all %d inputs, %s were higher %s later; median outcome %s (scaled to today's volatility).",
		a.K, len(hf.Drivers), pctu(a.UpRate), strings.TrimPrefix(barsPhrase(hf.Bars), "in "), pct(a.MedianReturn)))
	agree := "agree"
	if (hf.PLogistic > 0.5) != (hf.PAnalogs > 0.5) {
		agree = "DISAGREE — lower confidence"
	}
	if ca := hf.CrossAsset; ca != nil && !ca.Used {
		w = append(w, fmt.Sprintf("The cross-asset model (trained on %d %s assets) says %s, but it is NOT used: it has not beaten the base rate out-of-sample for this horizon (Brier skill %+.2f%%, p=%.2f).",
			ca.Assets, ca.Pool, pctu(ca.P), 100*ca.Overall.BrierSkill, ca.Overall.PValue))
	}
	if hf.PCross != nil && hf.CrossAsset != nil {
		ca := hf.CrossAsset
		w = append(w, fmt.Sprintf("Model components: linear factor model %s, analog model %s, cross-asset model %s (trained on %d %s assets; out-of-sample hit rate %s vs %s naive, Brier skill %+.2f%%); blended equally and calibrated to %s.",
			pctu(hf.PLogistic), pctu(hf.PAnalogs), pctu(*hf.PCross), ca.Assets, ca.Pool, pctu(ca.Overall.Accuracy), pctu(ca.Overall.BaseAccuracy), 100*ca.Overall.BrierSkill, pctu(hf.PModel)))
	} else {
		w = append(w, fmt.Sprintf("Model components: linear factor model %s, analog model %s (they %s); blended and calibrated to %s.", pctu(hf.PLogistic), pctu(hf.PAnalogs), agree, pctu(hf.PModel)))
	}
	if math.Abs(hf.NewsShift) >= 0.001 {
		w = append(w, fmt.Sprintf("News flow shifts the forecast by %s (bounded overlay; see the news section for each article's reasoning).", pp(hf.NewsShift)))
	} else {
		w = append(w, "News flow does not materially change this horizon's forecast.")
	}
	if hf.EarningsNote != "" {
		w = append(w, hf.EarningsNote)
	}

	w = append(w, fmt.Sprintf("Expected range (10th–90th percentile): %s to %s; expected return %s. Suggested stop %s, target %s.",
		pct(hf.ReturnRange["p10"]), pct(hf.ReturnRange["p90"]), pct(hf.ExpectedReturn), num(hf.Risk.StopLoss), num(hf.Risk.TakeProfit)))
	return w
}

func barsPhrase(n int) string {
	if n == 1 {
		return "in 1 bar"
	}
	return fmt.Sprintf("in %d bars", n)
}

func headline(r *Report) string {
	var parts []string
	for _, h := range r.Horizons {
		if h.Available {
			parts = append(parts, fmt.Sprintf("%s: %s (%s up)", h.Key, h.Signal, pctu(h.PUp)))
		}
	}
	return strings.Join(parts, " · ")
}

func templateNarrative(r *Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s (%s) last traded at %s %s (%s on the day).\n\n", r.Name, r.Symbol, num(r.Price), r.Currency, pct(r.Change1d))
	for _, h := range r.Horizons {
		if !h.Available {
			continue
		}
		fmt.Fprintf(&b, "%s — %s with %s probability of a higher price. ", h.Label, h.Signal, pctu(h.PUp))
		switch h.Status {
		case "validated":
			fmt.Fprintf(&b, "This horizon has a validated out-of-sample edge on this asset. ")
		case "weak":
			fmt.Fprintf(&b, "Out-of-sample evidence is weak; treat as a tilt. ")
		default:
			fmt.Fprintf(&b, "No demonstrated edge here, so this is effectively a coin flip. ")
		}
		if len(h.GroupImpact) > 0 {
			g := h.GroupImpact[0]
			fmt.Fprintf(&b, "The biggest influence is %s (%s).", strings.ToLower(g.Group), pp(g.ProbImpact))
		}
		b.WriteString("\n\n")
	}
	b.WriteString("Set ANTHROPIC_API_KEY to get a written analysis of the news flow and an expert narrative.")
	return b.String()
}

func quantSummary(r *Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Price %.6g %s (1d %s). Class %s. Benchmark %s. History %d bars from %s.\n", r.Price, r.Currency, pct(r.Change1d), r.Class, r.Benchmark, r.HistoryBars, r.HistoryFrom)
	for _, h := range r.Horizons {
		if !h.Available {
			fmt.Fprintf(&b, "\n[%s] unavailable: %s\n", h.Label, h.Reason)
			continue
		}
		e := h.Validation.Ensemble
		st, _, _ := classify(h.Validation, h.PModel)
		fmt.Fprintf(&b, "\n[%s, %d bars] P(up) model=%.1f%% (base rate %.1f%%). Validation: %s, OOS hit %.1f%% vs baseline %.1f%%, Brier skill %+.2f%%, AUC %.3f, n_eff=%d.\n",
			h.Label, h.Bars, 100*h.PModel, 100*h.BaseRate, st, 100*e.Accuracy, 100*e.BaseAccuracy, 100*e.BrierSkill, e.AUC, e.NEffective)
		for i, d := range h.Drivers {
			if i >= 5 {
				break
			}
			fmt.Fprintf(&b, "  driver: %s → %s\n", describe(d.Key, d.Value), pp(d.ProbImpact))
		}
		fmt.Fprintf(&b, "  analogs: %.0f%% of %d similar setups rose; median %s. 10-90%% range %s to %s.\n",
			100*h.Analogs.UpRate, h.Analogs.K, pct(h.Analogs.MedianReturn), pct(h.ReturnRange["p10"]), pct(h.ReturnRange["p90"]))
	}
	if r.Earnings.Next != nil {
		fmt.Fprintf(&b, "\nNext earnings: %s (%s), %d bars away. Past reports moved the stock ±%.1f%% on average vs ±%.1f%% on a normal day; EPS beat rate %.0f%%.\n",
			r.Earnings.Next.Date, timingLabel(r.Earnings.Next.Time), r.Earnings.Next.BarsAway, 100*r.Earnings.AvgAbsMove, 100*r.Earnings.NormalMove, 100*r.Earnings.BeatRate)
	}
	for _, rk := range r.Ranks {
		fmt.Fprintf(&b, "Cross-asset ranking (%s, %s): #%d of %d, P(beat median)=%.1f%%, model status %s.\n", rk.Universe, rk.Horizon, rk.Item.Rank, rk.Item.Of, 100*rk.Item.POutperform, rk.Status)
	}
	b.WriteString("\nKey readings:\n")
	for _, ir := range r.Indicators {
		fmt.Fprintf(&b, "  %s: %s (%s)\n", ir.Name, ir.Display, ir.Signal)
	}
	return b.String()
}

func dataWarnings(r *Report, ser *market.Series, mat *features.Matrix) []string {
	var w []string
	if last, err := time.Parse("2006-01-02", r.AsOf); err == nil {
		limit := 5 * 24 * time.Hour
		if r.Class == string(market.ClassCrypto) {
			limit = 2 * 24 * time.Hour
		}
		if time.Since(last) > limit {
			w = append(w, fmt.Sprintf("Latest price bar is from %s — data may be stale (provider outage or rate limit).", r.AsOf))
		}
	}
	if time.Since(ser.FetchedAt) > 12*time.Hour {
		w = append(w, fmt.Sprintf("Price history was fetched %s ago; live providers are failing, cached data is in use.", time.Since(ser.FetchedAt).Round(time.Hour)))
	}
	if r.Provider == "nasdaq" {
		w = append(w, "Using the Nasdaq fallback (Yahoo unavailable): 10 years of split-adjusted but not dividend-adjusted prices, so yearly forecasts are weaker and dividend payers' returns are slightly understated.")
	}
	if r.Provider == "coingecko" {
		w = append(w, "Using the CoinGecko fallback: only ~1 year of history and approximated OHLC, so longer horizons are unavailable and accuracy is reduced.")
	}
	if len(mat.Dropped) > 0 {
		w = append(w, "Inputs skipped for insufficient history: "+strings.Join(mat.Dropped, ", ")+".")
	}
	if r.HistoryBars < 1500 {
		w = append(w, fmt.Sprintf("Only %d bars of history — validation statistics have wide error bars.", r.HistoryBars))
	}
	return w
}

func chart(ser *market.Series, mat *features.Matrix, n int) Chart {
	start := max(0, len(ser.Bars)-n)
	var c Chart
	for i := start; i < len(ser.Bars); i++ {
		b := ser.Bars[i]
		c.Dates = append(c.Dates, b.Date)
		c.Open, c.High, c.Low, c.Close, c.Volume = append(c.Open, b.Open), append(c.High, b.High), append(c.Low, b.Low), append(c.Close, b.Close), append(c.Volume, b.Volume)
		c.SMA50 = append(c.SMA50, NullFloat(mat.Raw["sma50"][i]))
		c.SMA200 = append(c.SMA200, NullFloat(mat.Raw["sma200"][i]))
	}
	return c
}

// targetDate approximates the calendar date the horizon resolves on.
func targetDate(asOf string, bars int, class market.AssetClass) string {
	d, err := time.Parse("2006-01-02", asOf)
	if err != nil {
		return ""
	}
	if class == market.ClassCrypto {
		return d.AddDate(0, 0, bars).Format("2006-01-02")
	}
	for k := 0; k < bars; {
		d = d.AddDate(0, 0, 1)
		if d.Weekday() != time.Saturday && d.Weekday() != time.Sunday {
			k++
		}
	}
	return d.Format("2006-01-02")
}

func sigmoid(z float64) float64 { return 1 / (1 + math.Exp(-z)) }
func logit(p float64) float64 {
	p = math.Max(1e-6, math.Min(1-1e-6, p))
	return math.Log(p / (1 - p))
}

// SortByOpportunity orders reports for the scanner: validated long signals
// first, by conviction then expected return.
func SortByOpportunity(items []ScanItem) {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Score != items[j].Score {
			return items[i].Score > items[j].Score
		}
		return items[i].ExpectedReturn > items[j].ExpectedReturn
	})
}

func fillCross(hf *HorizonForecast, ap *pooled.DirApplied, res *model.Result) {
	if ap == nil || math.IsNaN(ap.Live) {
		return
	}
	p := ap.Live
	if ap.Model.Skilled() {
		hf.PCross = &p
	}
	m := ap.Model
	ca := &CrossAssetInfo{Pool: m.Pool, Used: m.Skilled(), Assets: len(m.Assets), InSample: ap.InSample, P: p, Overall: m.Validation,
		OnAsset: res.Validation.External, TestFrom: m.TestFrom, TestTo: m.TestTo, TrainedAt: m.TrainedAt}
	for i, d := range ap.Drivers {
		if i >= 12 {
			break
		}
		ca.Drivers = append(ca.Drivers, d)
	}
	hf.CrossAsset = ca
}

// optionsView reads the options-implied move to a horizon's target date from
// the first expiry on or after it (scaled back by √time when the expiry is
// later), and the model's own expected absolute move for comparison.
func optionsView(exps []market.Expiry, hf *HorizonForecast, asOf string) *OptionsView {
	if len(exps) == 0 || hf.TargetDate == "" {
		return nil
	}
	start, err1 := time.Parse("2006-01-02", asOf)
	target, err2 := time.Parse("2006-01-02", hf.TargetDate)
	if err1 != nil || err2 != nil {
		return nil
	}
	for _, e := range exps {
		exp, err := time.Parse("2006-01-02", e.Date)
		if err != nil || exp.Before(target) {
			continue
		}
		if exp.Sub(target) > 60*24*time.Hour {
			return nil // nearest listed expiry is too far beyond the horizon
		}
		scale := math.Sqrt(target.Sub(start).Hours() / math.Max(1, exp.Sub(start).Hours()))
		// Model's typical absolute move ≈ 0.8 × σ; read σ off its 10–90% range.
		sigma := (hf.ReturnRange["p90"] - hf.ReturnRange["p10"]) / (2 * 1.2816)
		return &OptionsView{Expiry: e.Date, ImpliedMove: e.ImpliedMove * scale, ImpliedVol: e.ImpliedVol, ModelMove: 0.8 * sigma}
	}
	return nil
}

func optionsLine(hf *HorizonForecast) string {
	o := hf.Options
	cmp := "about the same as"
	switch {
	case o.ModelMove > 1.25*o.ImpliedMove:
		cmp = "wider than"
	case o.ModelMove < 0.8*o.ImpliedMove:
		cmp = "narrower than"
	}
	return fmt.Sprintf("Options market: the at-the-money straddle prices a typical move of ±%s by %s (implied volatility %.0f%%). The model's expected move, ±%s, is %s that.",
		pctu(o.ImpliedMove), hf.TargetDate, 100*o.ImpliedVol, pctu(o.ModelMove), cmp)
}
