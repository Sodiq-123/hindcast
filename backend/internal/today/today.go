// Package today builds a person's home screen: a short briefing, the one
// thing worth doing today, and where each watched asset could land.
//
// Everything here is a pure function of its input so the wording and the
// choice of suggestion can be tested without a database.
package today

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/sodiq/financial-market/backend/internal/engine"
	"github.com/sodiq/financial-market/backend/internal/plain"
	"github.com/sodiq/financial-market/backend/internal/token"
)

// Asset is one watched symbol with its latest analysis (nil when it has not
// been analysed yet).
type Asset struct {
	Symbol string
	Report *engine.Report
}

// Position is an open journal trade.
type Position struct {
	ID      int64
	Symbol  string
	HasStop bool
}

// StepState is what a person has done with a suggestion.
type StepState struct {
	Done         bool // acted on it at some point
	SkippedToday bool // said "not today" today
}

type Input struct {
	Now      time.Time // in the person's (server's) time zone
	Level    string    // beginner | intermediate | advanced
	Horizon  string    // the person's horizon key
	Profile  string    // profile status: "" | skipped | done
	LastSeen *time.Time
	Assets   []Asset
	Tokens   []token.Brief
	Open     []Position
	Steps    map[string]StepState
}

// Line is one item in the briefing: a short tag, a bold lead and the rest.
type Line struct {
	Tag    string `json:"tag"`
	Tone   string `json:"tone"` // accent | up | down | muted
	Strong string `json:"strong,omitempty"`
	Text   string `json:"text"`
	Link   string `json:"link,omitempty"`
}

// Step is the one suggested thing to do today.
type Step struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Body    string `json:"body"`
	Action  string `json:"action"`
	Link    string `json:"link"`
	Skipped bool   `json:"skipped"`
}

// Range is one horizon's forecast as a span.
type Range struct {
	Key       string  `json:"key"`
	Available bool    `json:"available"`
	Reason    string  `json:"reason,omitempty"`
	PUp       float64 `json:"pUp"`
	Base      float64 `json:"base"`
	P10       float64 `json:"p10"`
	P50       float64 `json:"p50"`
	P90       float64 `json:"p90"`
	Status    string  `json:"status"`
	Signal    string  `json:"signal"`
	Verdict   string  `json:"verdict"`
}

type Row struct {
	Symbol   string  `json:"symbol"`
	Name     string  `json:"name"`
	Price    float64 `json:"price"`
	Change1d float64 `json:"change1d"`
	Currency string  `json:"currency"`
	Analysed bool    `json:"analysed"`
	Ranges   []Range `json:"ranges"`
}

type TokenRow struct {
	token.Brief
	Level int    `json:"level"` // 1 (fewest warning signs) .. 4 (very high risk)
	Label string `json:"label"`
}

type Today struct {
	Title    string     `json:"title"`
	LastSeen *time.Time `json:"lastSeen,omitempty"`
	AsOf     *time.Time `json:"asOf,omitempty"` // newest analysis on the list
	Lines    []Line     `json:"lines"`
	Step     *Step      `json:"step"`
	Rows     []Row      `json:"rows"`
	Tokens   []TokenRow `json:"tokens"`
}

// Horizons in display order.
var Horizons = []string{"1d", "1w", "1m", "1y"}

func Build(in Input) Today {
	out := Today{Title: "Today at a glance", LastSeen: in.LastSeen, Lines: []Line{}, Rows: []Row{}, Tokens: []TokenRow{}}
	if in.LastSeen != nil {
		out.Title = "Since you last looked"
	}
	for _, a := range in.Assets {
		out.Rows = append(out.Rows, row(a))
		if a.Report != nil && (out.AsOf == nil || a.Report.GeneratedAt.After(*out.AsOf)) {
			g := a.Report.GeneratedAt
			out.AsOf = &g
		}
	}
	for _, b := range in.Tokens {
		lvl, label := riskLevel(b.Verdict)
		out.Tokens = append(out.Tokens, TokenRow{Brief: b, Level: lvl, Label: label})
	}
	out.Lines = briefing(in, out.AsOf)
	out.Step = pick(in)
	return out
}

// ---------- briefing ----------

func briefing(in Input, asOf *time.Time) []Line {
	simple := in.Level == "beginner"
	lines := []Line{}

	for _, e := range upcoming(in, 7) {
		text := fmt.Sprintf("After its last %d reports the price moved about %s, up or down.", e.reactions, pct(e.avgMove))
		if !simple {
			text = fmt.Sprintf("Average move around reports ±%s over %d reports, against ±%s on an ordinary window.", pct(e.avgMove), e.reactions, pct(e.normal))
		}
		if e.reactions == 0 {
			text = "There's no history of how it reacts to reports yet."
		}
		lines = append(lines, Line{Tag: "EARNINGS", Tone: "accent", Strong: fmt.Sprintf("%s reports %s%s.", e.name, e.when, e.timing), Text: text, Link: "/asset/" + e.symbol})
		if len(lines) == 2 {
			break
		}
	}

	for _, b := range in.Tokens {
		lvl, _ := riskLevel(b.Verdict)
		if lvl < 3 {
			continue
		}
		risk := strings.ToLower(strings.SplitN(b.Verdict, " (", 2)[0])
		lead := fmt.Sprintf("%s still rates %s.", b.Symbol, risk)
		switch {
		case b.Change24h <= -0.01:
			lead = fmt.Sprintf("%s fell %s and still rates %s.", b.Symbol, pct(-b.Change24h), risk)
		case b.Change24h >= 0.01:
			lead = fmt.Sprintf("%s rose %s but still rates %s.", b.Symbol, pct(b.Change24h), risk)
		}
		text := ""
		if b.Top10 >= 0.3 {
			text = fmt.Sprintf("Its ten biggest wallets hold %s of the supply.", pct(b.Top10))
		}
		lines = append(lines, Line{Tag: "RISK", Tone: "down", Strong: lead, Text: text, Link: "/token/" + b.Address})
	}

	if l, ok := moves(in.Assets); ok {
		lines = append(lines, l)
	}

	var sig []string
	tone := "muted"
	for _, a := range in.Assets {
		if a.Report == nil {
			continue
		}
		for _, h := range a.Report.Horizons {
			if h.Available && h.Status == "validated" && (strings.Contains(h.Signal, "BUY") || strings.Contains(h.Signal, "SELL")) {
				sig = append(sig, fmt.Sprintf("%s: a tested %s signal for %s", display(a), strings.ToLower(h.Signal), horizonPhrase(h.Key)))
				if strings.Contains(h.Signal, "BUY") {
					tone = "up"
				} else {
					tone = "down"
				}
			}
		}
	}
	switch {
	case len(sig) > 0:
		lines = append(lines, Line{Tag: "SIGNALS", Tone: tone, Text: strings.Join(sig, "; ") + "."})
	case hasAnalysed(in.Assets):
		text := "No reliable signals on your list. That's the usual state of things."
		if !simple {
			text = "No validated signals on any horizon."
		}
		lines = append(lines, Line{Tag: "SIGNALS", Tone: "muted", Text: text})
	}

	var waiting []string
	for _, a := range in.Assets {
		if a.Report == nil {
			waiting = append(waiting, a.Symbol)
		}
	}
	if len(waiting) > 0 {
		verb := "hasn't"
		if len(waiting) > 1 {
			verb = "haven't"
		}
		lines = append(lines, Line{Tag: "NEW", Tone: "muted", Text: fmt.Sprintf("%s %s been analysed yet. It takes a minute or two after adding.", joinAnd(waiting), verb)})
	}

	if asOf != nil && in.Now.Sub(*asOf) > 3*24*time.Hour {
		lines = append(lines, Line{Tag: "DATA", Tone: "muted", Text: fmt.Sprintf("The newest analysis is from %s, so prices may have moved since.", asOf.In(in.Now.Location()).Format("2 January"))})
	}
	return lines
}

func moves(assets []Asset) (Line, bool) {
	type mv struct {
		name string
		chg  float64
	}
	var all []mv
	for _, a := range assets {
		if a.Report != nil {
			all = append(all, mv{display(a), a.Report.Change1d})
		}
	}
	if len(all) == 0 {
		return Line{}, false
	}
	sort.SliceStable(all, func(i, j int) bool { return math.Abs(all[i].chg) > math.Abs(all[j].chg) })
	var named []string
	for _, m := range all {
		if math.Abs(m.chg) < 0.01 || len(named) == 2 {
			break
		}
		verb := "rose"
		if m.chg < 0 {
			verb = "fell"
		}
		named = append(named, fmt.Sprintf("%s %s %s", m.name, verb, pct(math.Abs(m.chg))))
	}
	switch {
	case len(named) == 0:
		return Line{Tag: "MOVES", Tone: "muted", Text: "Nothing on your list moved more than 1% on its last trading day."}, true
	case len(named) == len(all):
		return Line{Tag: "MOVES", Tone: "muted", Text: strings.Join(named, ", ") + " on the last trading day."}, true
	}
	return Line{Tag: "MOVES", Tone: "muted", Text: strings.Join(named, ", ") + " on the last trading day. Everything else moved less than 1%."}, true
}

// ---------- the one thing to do ----------

// pick returns the first suggestion the person hasn't acted on. If they said
// "not today" to it, it comes back marked skipped and nothing replaces it:
// the app suggests one thing a day, not a queue.
func pick(in Input) *Step {
	for _, c := range candidates(in) {
		st := in.Steps[c.ID]
		if st.Done {
			continue
		}
		c.Skipped = st.SkippedToday
		return &c
	}
	return nil
}

func candidates(in Input) []Step {
	var cs []Step
	add := func(s Step) { cs = append(cs, s) }

	if len(in.Assets) == 0 && len(in.Tokens) == 0 {
		add(Step{ID: "watch:first", Title: "Add the first thing you want to follow.",
			Body:   "A stock, a fund or a coin you're curious about. It gets analysed straight away, and every morning after that.",
			Action: "Add to my watchlist", Link: "/watchlist"})
	}

	held := map[string]Position{}
	for _, p := range in.Open {
		if _, ok := held[p.Symbol]; !ok || !p.HasStop {
			held[p.Symbol] = p
		}
	}

	for _, e := range upcoming(in, 2) {
		id := "earnings:" + e.symbol + ":" + e.date
		move := ""
		if e.reactions > 0 {
			move = fmt.Sprintf(" Past reports moved it about %s overnight, either way.", pct(e.avgMove))
		}
		p, holds := held[e.symbol]
		switch {
		case holds && !p.HasStop:
			add(Step{ID: id, Title: fmt.Sprintf("Set a plan for your %s position before its report %s.", e.name, e.when),
				Body:   "Pick the price where you'd sell to limit a loss, and where you'd take profit." + move + " Deciding now beats deciding at 6 a.m.",
				Action: "Open my journal", Link: "/journal"})
		case holds:
			add(Step{ID: id, Title: fmt.Sprintf("Check your %s plan still fits before its report %s.", e.name, e.when),
				Body:   "Make sure your stop and target still make sense if the price jumps." + move,
				Action: "Open my journal", Link: "/journal"})
		default:
			body := "Knowing the usual swing now means a big move won't come as a shock."
			if e.reactions > 0 {
				body = fmt.Sprintf("Over its last %d reports the price moved about %s overnight, either way. %s", e.reactions, pct(e.avgMove), body)
			}
			add(Step{ID: id, Title: fmt.Sprintf("See how %s usually moves after its reports.", e.name),
				Body: fmt.Sprintf("It reports %s%s. %s", e.when, e.timing, body), Action: "See past reports", Link: "/asset/" + e.symbol + "#earnings"})
		}
	}

	day := in.Now.Format("2006-01-02")
	for _, b := range in.Tokens {
		if lvl, _ := riskLevel(b.Verdict); lvl >= 4 && b.Change24h <= -0.10 {
			add(Step{ID: "token-drop:" + b.Address + ":" + day, Title: fmt.Sprintf("Look at %s's warning signs before doing anything.", b.Symbol),
				Body:   fmt.Sprintf("It fell %s in a day and rates very high risk. Sharp drops in coins like this often keep going; the safety check shows why.", pct(-b.Change24h)),
				Action: "Open the safety check", Link: "/token/" + b.Address})
		}
	}

	for _, p := range in.Open {
		if !p.HasStop {
			add(Step{ID: fmt.Sprintf("stop:%d", p.ID), Title: fmt.Sprintf("Add a stop to your %s trade.", p.Symbol),
				Body:   "A stop is the price where you'd sell to limit a loss. Choosing it calmly now is easier than in the middle of a drop.",
				Action: "Open my journal", Link: "/journal"})
		}
	}

	if in.Profile == "skipped" {
		add(Step{ID: "setup:finish", Title: "Answer a few questions so the app fits you.",
			Body:   "Two minutes. It picks your horizon, how much detail you see and how things are explained. You can change any answer later.",
			Action: "Start", Link: "/welcome"})
	}

	if in.Level == "beginner" && hasAnalysed(in.Assets) {
		add(Step{ID: "learn:no-signal", Title: "Read why most days have no signal.",
			Body:   "A three-minute read on how forecasts are tested here, and why \"no edge\" is an honest, normal answer.",
			Action: "Read it", Link: "/methodology"})
	}
	return cs
}

// ---------- helpers ----------

type report struct {
	symbol, name, date, when, timing string
	reactions                        int
	avgMove, normal                  float64
}

// upcoming lists earnings dates within the next `days` days, soonest first.
func upcoming(in Input, days int) []report {
	today := dateOnly(in.Now)
	var out []report
	for _, a := range in.Assets {
		if a.Report == nil || a.Report.Earnings.Next == nil {
			continue
		}
		n := a.Report.Earnings.Next
		d, err := time.ParseInLocation("2006-01-02", n.Date, in.Now.Location())
		if err != nil {
			continue
		}
		gap := int(math.Round(d.Sub(today).Hours() / 24))
		if gap < 0 || gap > days {
			continue
		}
		when := "today"
		switch {
		case gap == 1:
			when = "tomorrow"
		case gap > 1:
			when = "on " + d.Format("Monday")
		}
		e := a.Report.Earnings
		timing := ""
		switch n.Time {
		case "pre":
			timing = ", before the market opens"
		case "after":
			timing = ", after the market closes"
		}
		out = append(out, report{symbol: a.Symbol, name: display(a), date: n.Date, when: when, timing: timing, reactions: e.Reactions, avgMove: e.AvgAbsMove, normal: e.NormalMove})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].date < out[j].date })
	return out
}

func joinAnd(xs []string) string {
	if len(xs) <= 1 {
		return strings.Join(xs, "")
	}
	return strings.Join(xs[:len(xs)-1], ", ") + " and " + xs[len(xs)-1]
}

func row(a Asset) Row {
	r := Row{Symbol: a.Symbol, Name: a.Symbol, Ranges: []Range{}}
	if a.Report == nil {
		return r
	}
	rep := a.Report
	r.Name, r.Price, r.Change1d, r.Currency, r.Analysed = display(a), rep.Price, rep.Change1d, rep.Currency, true
	for _, k := range Horizons {
		g := Range{Key: k, Reason: "Not part of this analysis."}
		for _, h := range rep.Horizons {
			if h.Key != k {
				continue
			}
			g = Range{Key: k, Available: h.Available, Reason: h.Reason, PUp: h.PUp, Base: h.BaseRate,
				P10: h.ReturnRange["p10"], P50: h.ReturnRange["p50"], P90: h.ReturnRange["p90"],
				Status: h.Status, Signal: h.Signal, Verdict: verdict(h)}
		}
		r.Ranges = append(r.Ranges, g)
	}
	return r
}

func verdict(h engine.HorizonForecast) string {
	switch {
	case !h.Available:
		return "Not enough history"
	case h.Status == "validated" && strings.Contains(h.Signal, "BUY"):
		return "Tested signal: likely higher"
	case h.Status == "validated" && strings.Contains(h.Signal, "SELL"):
		return "Tested signal: likely lower"
	case h.Status == "validated":
		return "Tested model, no clear lean"
	case h.Status == "weak":
		return "Weak evidence, too small to act on"
	}
	return "No edge over the usual"
}

func riskLevel(v string) (int, string) {
	switch {
	case strings.HasPrefix(v, "Very high"):
		return 4, "Very high risk"
	case strings.HasPrefix(v, "High"):
		return 3, "High risk"
	case strings.HasPrefix(v, "Elevated"):
		return 2, "Elevated risk"
	case v == "":
		return 0, "Not checked yet"
	}
	return 1, "Fewer warning signs"
}

func display(a Asset) string {
	if a.Report == nil {
		return a.Symbol
	}
	return plain.Short(a.Report.Name, a.Symbol)
}

func horizonPhrase(k string) string {
	switch k {
	case "1d":
		return "tomorrow"
	case "1w":
		return "the next week"
	case "1m":
		return "the next month"
	case "1y":
		return "the next year"
	}
	return k
}

func hasAnalysed(as []Asset) bool {
	for _, a := range as {
		if a.Report != nil {
			return true
		}
	}
	return false
}

func dateOnly(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

// DayStart is midnight today in t's time zone.
func DayStart(t time.Time) time.Time { return dateOnly(t) }

// pct formats a fraction as a whole or one-decimal percentage: 0.061 -> "6.1%",
// 0.6 -> "60%".
func pct(x float64) string {
	v := 100 * x
	if math.Abs(v) >= 10 || math.Abs(v-math.Round(v)) < 0.05 {
		return fmt.Sprintf("%.0f%%", v)
	}
	return fmt.Sprintf("%.1f%%", v)
}
