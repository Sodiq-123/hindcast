// Package tracker resolves past predictions against what actually happened
// and summarises the live (true out-of-sample) track record.
package tracker

import (
	"context"
	"log/slog"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/sodiq/financial-market/backend/internal/market"
	"github.com/sodiq/financial-market/backend/internal/model"
	"github.com/sodiq/financial-market/backend/internal/store"
)

type Tracker struct {
	Store  *store.Store
	Market *market.Service
	Log    *slog.Logger

	drift driftCache
}

// Resolve scores every prediction whose horizon has elapsed. Returns the
// number resolved.
func (t *Tracker) Resolve(ctx context.Context) (int, error) {
	due, err := t.Store.DuePredictions(ctx, time.Now().UTC().Format("2006-01-02"))
	if err != nil {
		return 0, err
	}
	bySym := map[string][]store.Prediction{}
	for _, p := range due {
		bySym[p.Symbol] = append(bySym[p.Symbol], p)
	}
	n := 0
	for sym, preds := range bySym {
		ser, err := t.Market.Series(ctx, sym, false)
		if err != nil {
			t.Log.Warn("resolve: no data", "symbol", sym, "err", err)
			continue
		}
		for _, p := range preds {
			// Locate the bar the prediction was made on (last bar ≤ as_of).
			i := sort.Search(len(ser.Bars), func(k int) bool { return ser.Bars[k].Date > p.AsOf }) - 1
			if i < 0 || i+p.Bars >= len(ser.Bars) {
				continue // horizon not complete in the data yet
			}
			// Both prices from the current (consistently adjusted) series.
			r := ser.Bars[i+p.Bars].Close/ser.Bars[i].Close - 1
			if err := t.Store.ResolvePrediction(ctx, p.ID, ser.Bars[i+p.Bars].Date, r); err == nil {
				n++
			}
		}
	}
	if err := t.RefreshDrift(ctx); err != nil {
		t.Log.Warn("drift refresh", "err", err)
	}
	return n, nil
}

type Stats struct {
	N         int     `json:"n"`
	HitRate   float64 `json:"hitRate"`
	Brier     float64 `json:"brier"`
	AvgReturn float64 `json:"avgReturn"` // average realised return in the signal's direction
	BaseRate  float64 `json:"baseRate"`  // share of periods that were up
}

type Record struct {
	Total       int                `json:"total"`
	Open        int                `json:"open"`
	Overall     Stats              `json:"overall"`
	ByHorizon   map[string]Stats   `json:"byHorizon"`
	BySignal    map[string]Stats   `json:"bySignal"`
	ByStatus    map[string]Stats   `json:"byStatus"`
	ModelOnly   Stats              `json:"modelOnly"` // quant probability without news
	WithNews    Stats              `json:"withNews"`  // final probability
	Calibration []model.Bucket     `json:"calibration"`
	Recent      []store.Prediction `json:"recent"`
	Drift       []DriftRow         `json:"drift"` // symbols/horizons demoted by live monitoring
	Pending     []store.Prediction `json:"pending"`
}

func (t *Tracker) Summary(ctx context.Context) (*Record, error) {
	res, err := t.Store.ResolvedPredictions(ctx)
	if err != nil {
		return nil, err
	}
	open, err := t.Store.OpenPredictions(ctx, 200)
	if err != nil {
		return nil, err
	}
	rec := &Record{Total: len(res), Open: len(open), ByHorizon: map[string]Stats{}, BySignal: map[string]Stats{}, ByStatus: map[string]Stats{}, Pending: open}
	group := func(key func(store.Prediction) string, prob func(store.Prediction) float64) map[string]Stats {
		acc := map[string][]store.Prediction{}
		for _, p := range res {
			acc[key(p)] = append(acc[key(p)], p)
		}
		out := map[string]Stats{}
		for k, ps := range acc {
			out[k] = stats(ps, prob)
		}
		return out
	}
	final := func(p store.Prediction) float64 { return p.PUp }
	rec.Overall = stats(res, final)
	rec.ByHorizon = group(func(p store.Prediction) string { return p.Horizon }, final)
	rec.BySignal = group(func(p store.Prediction) string { return p.Signal }, final)
	rec.ByStatus = group(func(p store.Prediction) string { return p.Status }, final)
	rec.WithNews = rec.Overall
	rec.ModelOnly = stats(res, func(p store.Prediction) float64 { return p.PModel })

	var pr, y, ret []float64
	for _, p := range res {
		if p.RealizedReturn == nil {
			continue
		}
		pr = append(pr, p.PUp)
		r := *p.RealizedReturn
		y = append(y, b2f(r > 0))
		ret = append(ret, math.Log1p(r))
	}
	rec.Calibration = model.Calibration(pr, y, ret)
	rec.Drift = t.Drifting()
	if len(res) > 100 {
		rec.Recent = res[:100]
	} else {
		rec.Recent = res
	}
	return rec, nil
}

func stats(ps []store.Prediction, prob func(store.Prediction) float64) Stats {
	var s Stats
	var hit, brier, ret, up float64
	for _, p := range ps {
		if p.RealizedReturn == nil {
			continue
		}
		r := *p.RealizedReturn
		y := b2f(r > 0)
		pu := prob(p)
		s.N++
		up += y
		brier += (pu - y) * (pu - y)
		switch {
		case strings.Contains(p.Signal, "BUY"):
			hit += y
			ret += r
		case strings.Contains(p.Signal, "SELL"):
			hit += 1 - y
			ret -= r
		default:
			if (pu > 0.5) == (r > 0) {
				hit++
			}
		}
	}
	if s.N > 0 {
		n := float64(s.N)
		s.HitRate, s.Brier, s.AvgReturn, s.BaseRate = hit/n, brier/n, ret/n, up/n
	}
	return s
}

func b2f(b bool) float64 {
	if b {
		return 1
	}
	return 0
}
