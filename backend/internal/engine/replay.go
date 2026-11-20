package engine

import (
	"context"
	"math"
	"sort"
	"strings"

	"github.com/sodiq/financial-market/backend/internal/market"
)

// ReplayInfo is what actually happened after a replayed analysis.
type ReplayInfo struct {
	AsOf     string                   `json:"asOf"`
	Note     string                   `json:"note"`
	Outcomes map[string]ReplayOutcome `json:"outcomes"` // by horizon key
	Path     []ReplayPoint            `json:"path"`     // prices from the replay date onwards (up to the longest horizon)
}

type ReplayOutcome struct {
	Complete    bool    `json:"complete"` // horizon has fully elapsed in the data
	ExitDate    string  `json:"exitDate"`
	ExitPrice   float64 `json:"exitPrice"`
	Return      float64 `json:"return"`
	WentUp      bool    `json:"wentUp"`
	Correct     *bool   `json:"correct,omitempty"` // nil for NO EDGE / HOLD (no directional call)
	DirectionOK bool    `json:"directionOk"`       // probability side (>50%) matched the outcome
	InRange     bool    `json:"inRange"`           // finished inside the forecast 10–90% range
	HitFirst    string  `json:"hitFirst"`          // "target" | "stop" | "neither" (closing prices)
	MaxGain     float64 `json:"maxGain"`
	MaxDrawdown float64 `json:"maxDrawdown"`
}

type ReplayPoint struct {
	Date  string  `json:"date"`
	Close float64 `json:"close"`
}

func buildReplay(full *market.Series, r *Report) *ReplayInfo {
	info := &ReplayInfo{AsOf: r.AsOf, Outcomes: map[string]ReplayOutcome{},
		Note: "Replay: every model was trained only on data up to this date. News and option chains have no free history, so they are not part of a replay."}
	start := sort.Search(len(full.Bars), func(i int) bool { return full.Bars[i].Date > r.AsOf }) - 1
	if start < 0 {
		return info
	}
	entry := full.Bars[start].Close
	maxBars := 0
	for _, h := range r.Horizons {
		maxBars = max(maxBars, h.Bars)
	}
	for i := start; i < len(full.Bars) && i <= start+maxBars; i++ {
		info.Path = append(info.Path, ReplayPoint{full.Bars[i].Date, full.Bars[i].Close})
	}
	for _, h := range r.Horizons {
		if !h.Available {
			continue
		}
		end := min(start+h.Bars, len(full.Bars)-1)
		o := ReplayOutcome{Complete: start+h.Bars < len(full.Bars), ExitDate: full.Bars[end].Date, ExitPrice: full.Bars[end].Close}
		o.Return = o.ExitPrice/entry - 1
		o.WentUp = o.Return > 0
		o.DirectionOK = (h.PUp > 0.5) == o.WentUp
		if strings.Contains(h.Signal, "BUY") || strings.Contains(h.Signal, "SELL") {
			c := strings.Contains(h.Signal, "BUY") == o.WentUp
			o.Correct = &c
		}
		lo, hi := h.ReturnRange["p10"], h.ReturnRange["p90"]
		o.InRange = o.Return >= lo && o.Return <= hi
		o.HitFirst = "neither"
		for i := start + 1; i <= end; i++ {
			p := full.Bars[i].Close
			o.MaxGain = math.Max(o.MaxGain, p/entry-1)
			o.MaxDrawdown = math.Min(o.MaxDrawdown, p/entry-1)
			if o.HitFirst == "neither" {
				if h.Risk.TakeProfit > 0 && p >= h.Risk.TakeProfit {
					o.HitFirst = "target"
				} else if h.Risk.StopLoss > 0 && p <= h.Risk.StopLoss {
					o.HitFirst = "stop"
				}
			}
		}
		info.Outcomes[h.Key] = o
	}
	return info
}

// ReplayRow is one horizon of one replayed date.
type ReplayRow struct {
	Date        string  `json:"date"`
	Horizon     string  `json:"horizon"`
	Signal      string  `json:"signal"`
	Status      string  `json:"status"`
	PUp         float64 `json:"pUp"`
	Return      float64 `json:"return"`
	Complete    bool    `json:"complete"`
	Correct     *bool   `json:"correct,omitempty"`
	DirectionOK bool    `json:"directionOk"`
	InRange     bool    `json:"inRange"`
}

// ReplaySummary aggregates completed outcomes for one horizon.
type ReplaySummary struct {
	Horizon       string  `json:"horizon"`
	Dates         int     `json:"dates"`
	DirectionHit  float64 `json:"directionHit"`  // P(up) side was right
	UpRate        float64 `json:"upRate"`        // how often it actually went up (the bar to beat)
	Calls         int     `json:"calls"`         // BUY/SELL signals issued
	CallHit       float64 `json:"callHit"`       // share of those calls that were right
	AvgCallReturn float64 `json:"avgCallReturn"` // average return in the call's direction
	InRange       float64 `json:"inRange"`       // share finishing inside the 10–90% range (≈80% if calibrated)
}

type ReplaySeries struct {
	Symbol  string          `json:"symbol"`
	Rows    []ReplayRow     `json:"rows"`
	Summary []ReplaySummary `json:"summary"`
	Failed  []string        `json:"failed,omitempty"`
}

// ReplayMany replays a symbol on each of the given dates.
func (e *Engine) ReplayMany(ctx context.Context, symbol string, dates []string) *ReplaySeries {
	out := &ReplaySeries{Symbol: strings.ToUpper(symbol)}
	agg := map[string]*struct {
		n, dirHit, up, calls, callHit, inRange int
		callRet                                float64
	}{}
	var order []string
	for _, d := range dates {
		if ctx.Err() != nil {
			break
		}
		rep, err := e.Analyze(ctx, symbol, Options{AsOf: d})
		if err != nil || rep.Replay == nil {
			out.Failed = append(out.Failed, d)
			continue
		}
		for _, h := range rep.Horizons {
			o, ok := rep.Replay.Outcomes[h.Key]
			if !ok {
				continue
			}
			out.Rows = append(out.Rows, ReplayRow{Date: rep.AsOf, Horizon: h.Key, Signal: h.Signal, Status: h.Status, PUp: h.PUp,
				Return: o.Return, Complete: o.Complete, Correct: o.Correct, DirectionOK: o.DirectionOK, InRange: o.InRange})
			if !o.Complete {
				continue
			}
			a := agg[h.Key]
			if a == nil {
				a = &struct {
					n, dirHit, up, calls, callHit, inRange int
					callRet                                float64
				}{}
				agg[h.Key] = a
				order = append(order, h.Key)
			}
			a.n++
			if o.DirectionOK {
				a.dirHit++
			}
			if o.WentUp {
				a.up++
			}
			if o.InRange {
				a.inRange++
			}
			if o.Correct != nil {
				a.calls++
				if *o.Correct {
					a.callHit++
				}
				if strings.Contains(h.Signal, "BUY") {
					a.callRet += o.Return
				} else {
					a.callRet -= o.Return
				}
			}
		}
	}
	for _, k := range order {
		a := agg[k]
		s := ReplaySummary{Horizon: k, Dates: a.n, Calls: a.calls}
		if a.n > 0 {
			s.DirectionHit, s.UpRate, s.InRange = float64(a.dirHit)/float64(a.n), float64(a.up)/float64(a.n), float64(a.inRange)/float64(a.n)
		}
		if a.calls > 0 {
			s.CallHit, s.AvgCallReturn = float64(a.callHit)/float64(a.calls), a.callRet/float64(a.calls)
		}
		out.Summary = append(out.Summary, s)
	}
	return out
}
