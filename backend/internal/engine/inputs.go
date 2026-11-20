package engine

import (
	"context"
	"fmt"
	"math"
	"sort"
	"sync"

	"github.com/sodiq/financial-market/backend/internal/earnings"
	"github.com/sodiq/financial-market/backend/internal/features"
	"github.com/sodiq/financial-market/backend/internal/market"
)

// prepared is everything derived from data before modelling.
type prepared struct {
	ser       *market.Series
	full      *market.Series // untruncated history (replays: what happened next)
	class     market.AssetClass
	benchmark string
	mat       *features.Matrix
	events    []earnings.Event
	covered   bool // symbol is within the earnings calendar's coverage
	warnings  []string
}

// prepare loads the asset, its benchmark, macro series, crypto sentiment and
// earnings history, and builds the feature matrix. Context failures degrade
// the analysis (with a warning) but never block it.
//
// asOf (YYYY-MM-DD, optional) replays the past: the asset's history is cut at
// that date, so every model trains only on what was known then. External
// series need no cutting because every feature is causal (each row only reads
// data dated on or before it).
func (e *Engine) prepare(ctx context.Context, symbol string, force bool, asOf string) (*prepared, error) {
	ser, err := e.Market.Series(ctx, symbol, force)
	if err != nil {
		return nil, err
	}
	full := ser
	if asOf != "" {
		cut := sort.Search(len(ser.Bars), func(i int) bool { return ser.Bars[i].Date > asOf })
		if cut == 0 {
			return nil, fmt.Errorf("%s has no data on or before %s", symbol, asOf)
		}
		trunc := *ser
		trunc.Bars = ser.Bars[:cut:cut]
		trunc.Meta.Price = trunc.Bars[cut-1].Close
		ser = &trunc
	}
	if len(ser.Bars) < 300 {
		return nil, fmt.Errorf("%s has only %d daily bars; at least 300 are needed for a meaningful analysis", symbol, len(ser.Bars))
	}
	p := &prepared{ser: ser, full: full, class: ser.Meta.Class}
	if p.class == market.ClassUnknown && market.IsCrypto(symbol) {
		p.class = market.ClassCrypto
		ser.Meta.Class = p.class
	}
	in := features.Inputs{Asset: ser, Macro: map[string]*market.Series{}}
	p.benchmark = market.Benchmark(symbol, p.class)
	var wg sync.WaitGroup
	var mu sync.Mutex
	fetch := func(sym string, assign func(*market.Series)) {
		defer wg.Done()
		s, err := e.Market.Series(ctx, sym, false)
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			p.warnings = append(p.warnings, fmt.Sprintf("Could not load %s (%v); related features were skipped.", sym, err))
			return
		}
		assign(s)
	}
	if p.benchmark != symbol {
		wg.Add(1)
		go fetch(p.benchmark, func(s *market.Series) { in.Benchmark = s })
	}
	for _, sym := range []string{"^VIX", "^TNX", "DX-Y.NYB", "^VIX3M", "^SKEW", "^VVIX"} {
		if sym == symbol {
			continue
		}
		wg.Add(1)
		go fetch(sym, func(s *market.Series) { in.Macro[sym] = s })
	}
	if p.class == market.ClassCrypto {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fg, err := e.Market.FearGreed(ctx)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				p.warnings = append(p.warnings, "Crypto Fear & Greed index unavailable: "+err.Error())
				return
			}
			in.FearGreed = fg
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			fd, err := e.Market.Funding(ctx, symbol)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				p.warnings = append(p.warnings, "Perpetual-futures funding history unavailable for this coin; that input was skipped.")
				return
			}
			in.Funding = fd
		}()
	}
	wg.Wait()

	if e.Earnings != nil && p.class == market.ClassEquity {
		evs, covered := e.Earnings.Events(ctx, symbol)
		if asOf != "" {
			// Results reported after the replay date were unknown then.
			for i := range evs {
				if evs[i].Date > asOf {
					evs[i].EPS, evs[i].SurprisePct, evs[i].Upcoming = math.NaN(), math.NaN(), true
				}
			}
		}
		p.events, p.covered = evs, covered
		if from := e.Earnings.CoveredFrom(); covered && from != "" {
			ei := &features.EarningsInput{CoveredFrom: from}
			for _, ev := range evs {
				ei.Events = append(ei.Events, features.EarningsEvent{Date: ev.Date, Time: ev.Time, Surprise: ev.SurprisePct})
			}
			in.Earnings = ei
		}
	}
	p.mat = features.Build(in)
	return p, nil
}

// MatrixSource adapts prepare for the pooled-model trainer.
func (e *Engine) MatrixSource(ctx context.Context, symbol string) (*features.Matrix, *market.Series, error) {
	p, err := e.prepare(ctx, symbol, false, "")
	if err != nil {
		return nil, nil, err
	}
	return p.mat, p.ser, nil
}
