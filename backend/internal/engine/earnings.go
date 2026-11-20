package engine

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/sodiq/financial-market/backend/internal/features"
	"github.com/sodiq/financial-market/backend/internal/model"
)

// EarningsInfo is everything the report says about earnings.
type EarningsInfo struct {
	Covered     bool           `json:"covered"`
	Note        string         `json:"note"`
	Next        *EarningsNext  `json:"next,omitempty"`
	History     []EarningsPast `json:"history"`
	Reactions   int            `json:"reactions"`
	AvgAbsMove  float64        `json:"avgAbsMove"` // mean |earnings-window return|
	RMSMove     float64        `json:"rmsMove"`
	NormalMove  float64        `json:"normalMove"`          // median |return| over a same-length window on ordinary days
	BeatRate    float64        `json:"beatRate"`            // share of reports with a positive EPS surprise
	DriftBeat   *float64       `json:"driftBeat,omitempty"` // avg 20-bar return after the reaction, beats
	DriftMiss   *float64       `json:"driftMiss,omitempty"`
	CoveredFrom string         `json:"coveredFrom,omitempty"`
}

type EarningsNext struct {
	Date        string   `json:"date"`
	Time        string   `json:"time"`
	BarsAway    int      `json:"barsAway"`
	EPSForecast *float64 `json:"epsForecast,omitempty"`
}

type EarningsPast struct {
	Date        string   `json:"date"`
	Time        string   `json:"time"`
	EPS         *float64 `json:"eps,omitempty"`
	Forecast    *float64 `json:"forecast,omitempty"`
	SurprisePct *float64 `json:"surprisePct,omitempty"`
	Reaction    float64  `json:"reaction"` // simple return over the reaction window
	Drift20     *float64 `json:"drift20,omitempty"`
}

func ptr(x float64) *float64 {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return nil
	}
	return &x
}

func buildEarnings(p *prepared, coveredFrom string) EarningsInfo {
	info := EarningsInfo{Covered: p.covered, CoveredFrom: coveredFrom, History: []EarningsPast{}}
	switch {
	case !p.covered:
		info.Note = "No earnings calendar for this instrument (only US-listed companies, including ADRs, are covered)."
		return info
	case coveredFrom == "":
		info.Note = "Earnings history is still downloading in the background; check back later."
	}
	m := p.mat
	dates, c := m.Dates, m.Close
	n := len(dates)
	today := time.Now().UTC().Format("2006-01-02")
	var reacts, beatDrift, missDrift []float64
	var reported, beats int
	isReact := map[int]bool{}
	for _, ev := range p.events {
		if ev.Date >= today && math.IsNaN(ev.EPS) {
			if info.Next == nil {
				k := tradingDaysAhead(dates[n-1], ev.Date)
				if ev.Time != "pre" {
					k++
				}
				info.Next = &EarningsNext{Date: ev.Date, Time: ev.Time, BarsAway: k, EPSForecast: ptr(ev.Forecast)}
			}
			continue
		}
		b0, r, ok := features.ReactionBars(dates, features.EarningsEvent{Date: ev.Date, Time: ev.Time})
		if !ok {
			continue
		}
		past := EarningsPast{Date: ev.Date, Time: ev.Time, EPS: ptr(ev.EPS), Forecast: ptr(ev.Forecast), SurprisePct: ptr(ev.SurprisePct), Reaction: c[r]/c[b0] - 1}
		for k := b0 + 1; k <= r; k++ {
			isReact[k] = true
		}
		reacts = append(reacts, math.Log(c[r]/c[b0]))
		if r+20 < n {
			d := c[r+20]/c[r] - 1
			past.Drift20 = &d
			if !math.IsNaN(ev.SurprisePct) {
				if ev.SurprisePct > 0 {
					beatDrift = append(beatDrift, d)
				} else if ev.SurprisePct < 0 {
					missDrift = append(missDrift, d)
				}
			}
		}
		if !math.IsNaN(ev.SurprisePct) {
			reported++
			if ev.SurprisePct > 0 {
				beats++
			}
		}
		info.History = append(info.History, past)
	}
	sort.Slice(info.History, func(i, j int) bool { return info.History[i].Date > info.History[j].Date })
	info.Reactions = len(reacts)
	if len(reacts) > 0 {
		var abs, sq float64
		for _, x := range reacts {
			abs += math.Abs(math.Exp(x) - 1)
			sq += x * x
		}
		info.AvgAbsMove = abs / float64(len(reacts))
		info.RMSMove = math.Sqrt(sq / float64(len(reacts)))
		// Ordinary-day comparison over the typical (1-2 bar) window.
		var normal []float64
		for i := max(1, n-1500); i < n; i++ {
			if !isReact[i] {
				normal = append(normal, math.Abs(c[i]/c[i-1]-1))
			}
		}
		info.NormalMove = model.Quantile(normal, 0.5)
	}
	if reported > 0 {
		info.BeatRate = float64(beats) / float64(reported)
	}
	if len(beatDrift) >= 3 {
		info.DriftBeat = ptr(mean(beatDrift))
	}
	if len(missDrift) >= 3 {
		info.DriftMiss = ptr(mean(missDrift))
	}
	if len(info.History) > 12 {
		info.History = info.History[:12]
	}
	if info.Note == "" && info.Next == nil {
		info.Note = "No upcoming report in the next ~6 weeks of the calendar."
	}
	return info
}

func mean(x []float64) float64 {
	s := 0.0
	for _, v := range x {
		s += v
	}
	return s / float64(len(x))
}

func tradingDaysAhead(from, to string) int {
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

// applyEarnings adjusts one horizon for a report that falls inside it:
// widen the forecast range by the stock's typical earnings jump, cap
// short-horizon signals at plain BUY/SELL and halve position size. Technical
// edges are measured mostly on non-event days and don't survive a binary
// event intact.
//
// The widening adds the whole past reaction (rms) to the horizon's normal
// variance. Checked on 35,000 past reports (cmd/earncheck): its 80% ranges
// held 82% of actual moves; a leaner version that let the report day replace
// an ordinary day held only 76%, so this one stays (DECISIONS §8.14).
func applyEarnings(hf *HorizonForecast, info EarningsInfo, q map[string]float64, dailyVol float64) map[string]float64 {
	if info.Next == nil || info.Next.BarsAway > hf.Bars || info.Next.BarsAway < 1 {
		if hf.Bars >= 252 && info.Covered {
			hf.EarningsNote = "This horizon spans about four quarterly earnings reports."
		}
		return q
	}
	hf.EarningsInside = true
	hf.EarningsNote = fmt.Sprintf("Earnings on %s (%s) fall inside this horizon.", info.Next.Date, timingLabel(info.Next.Time))
	if info.Reactions >= 4 && dailyVol > 0 {
		f := math.Sqrt(1 + info.RMSMove*info.RMSMove/(dailyVol*dailyVol*float64(hf.Bars)))
		med := q["p50"]
		out := map[string]float64{}
		for k, v := range q {
			out[k] = med + (v-med)*f
		}
		hf.EarningsNote += fmt.Sprintf(" Past reports moved the stock ±%.1f%% on average (vs ±%.1f%% on a normal day), so the forecast range is widened %.0f%%.",
			100*info.AvgAbsMove, 100*info.NormalMove, 100*(f-1))
		q = out
	}
	if hf.Bars <= 5 && strings.HasPrefix(hf.Signal, "STRONG ") {
		hf.Signal = strings.TrimPrefix(hf.Signal, "STRONG ")
		hf.StatusNote += " Capped from STRONG because an earnings report falls inside the horizon."
	}
	return q
}

func timingLabel(t string) string {
	switch t {
	case "pre":
		return "before the open"
	case "after":
		return "after the close"
	}
	return "time not announced"
}
