package tracker

import (
	"context"
	"math"
	"sort"
	"sync"

	"github.com/sodiq/financial-market/backend/internal/engine"
	"github.com/sodiq/financial-market/backend/internal/store"
)

const (
	driftWindow  = 100 // most recent live forecasts (one per day) judged
	driftMinN    = 30  // at least this many resolved days
	driftMinIndp = 10  // and this many non-overlapping periods (days ÷ horizon bars)
)

type driftCache struct {
	mu sync.RWMutex
	m  map[string]*engine.Drift
}

func driftKey(symbol, horizon string) string { return symbol + "|" + horizon }

// Drift implements engine.DriftMonitor. It returns nil unless the live
// record for this symbol and horizon is long enough to judge and worse than
// always forecasting the base rate.
func (t *Tracker) Drift(symbol, horizon string) *engine.Drift {
	t.drift.mu.RLock()
	defer t.drift.mu.RUnlock()
	if d := t.drift.m[driftKey(symbol, horizon)]; d != nil {
		c := *d
		return &c
	}
	return nil
}

// Drifting lists every drifting symbol and horizon (for the track record page).
func (t *Tracker) Drifting() []DriftRow {
	t.drift.mu.RLock()
	defer t.drift.mu.RUnlock()
	out := []DriftRow{}
	for k, d := range t.drift.m {
		for i := len(k) - 1; i >= 0; i-- {
			if k[i] == '|' {
				out = append(out, DriftRow{Symbol: k[:i], Horizon: k[i+1:], Drift: *d})
				break
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Symbol+out[i].Horizon < out[j].Symbol+out[j].Horizon })
	return out
}

type DriftRow struct {
	Symbol  string `json:"symbol"`
	Horizon string `json:"horizon"`
	engine.Drift
}

// RefreshDrift recomputes the drift table from resolved predictions.
func (t *Tracker) RefreshDrift(ctx context.Context) error {
	res, err := t.Store.ResolvedPredictions(ctx)
	if err != nil {
		return err
	}
	m := computeDrift(res)
	t.drift.mu.Lock()
	t.drift.m = m
	t.drift.mu.Unlock()
	return nil
}

func computeDrift(res []store.Prediction) map[string]*engine.Drift {
	// One forecast per symbol, horizon and as-of day (the latest run wins):
	// twice-daily runs on the same bar are not independent evidence.
	type day struct {
		asOf string
		p    store.Prediction
	}
	groups := map[string]map[string]store.Prediction{}
	for _, p := range res { // newest first
		if p.RealizedReturn == nil {
			continue
		}
		k := driftKey(p.Symbol, p.Horizon)
		if groups[k] == nil {
			groups[k] = map[string]store.Prediction{}
		}
		if _, seen := groups[k][p.AsOf]; !seen {
			groups[k][p.AsOf] = p
		}
	}
	out := map[string]*engine.Drift{}
	for k, byDay := range groups {
		days := make([]day, 0, len(byDay))
		for d, p := range byDay {
			days = append(days, day{d, p})
		}
		sort.Slice(days, func(i, j int) bool { return days[i].asOf > days[j].asOf })
		if len(days) > driftWindow {
			days = days[:driftWindow]
		}
		n := len(days)
		if n < driftMinN || n/max(1, days[0].p.Bars) < driftMinIndp {
			continue
		}
		// Compare each forecast with the base rate the model knew at the
		// time (50% for rows saved before that was recorded). Demote only
		// when the live record is worse by at least one standard error,
		// counting overlapping horizons as one period.
		var brier, base, hit, sum, sum2 float64
		for _, d := range days {
			y := b2f(*d.p.RealizedReturn > 0)
			b := 0.5
			if d.p.BaseRate != nil {
				b = *d.p.BaseRate
			}
			lm, lb := (d.p.PUp-y)*(d.p.PUp-y), (b-y)*(b-y)
			brier, base = brier+lm, base+lb
			sum, sum2 = sum+lm-lb, sum2+(lm-lb)*(lm-lb)
			if (d.p.PUp > 0.5) == (y == 1) {
				hit++
			}
		}
		fn := float64(n)
		mean := sum / fn
		sd := math.Sqrt(math.Max(0, sum2/fn-mean*mean))
		indp := float64(n / max(1, days[0].p.Bars))
		if mean <= 0 || mean < sd/math.Sqrt(indp) {
			continue
		}
		brier, base = brier/fn, base/fn
		out[k] = &engine.Drift{N: n, Brier: brier, BaseBrier: base, HitRate: hit / fn, FirstAsOf: days[n-1].asOf, LastAsOf: days[0].asOf}
	}
	return out
}
