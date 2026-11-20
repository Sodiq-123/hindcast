// Package learn is step 2 of personalisation: the app notices what a person
// does and suggests changes, which the person accepts or not. It never
// changes anything on its own, and never touches a forecast, a test result or
// a warning.
//
// Suggestions come from fixed, readable rules (rules.go). Which one is shown,
// and when, is learned per person from their own answers, starting from what
// everyone tends to accept (Rank). Journal habits are stated only when the
// record clearly shows them (habits.go).
package learn

import (
	"fmt"
	"sort"
	"time"
)

// Kinds of suggestion.
const (
	KindAdd      = "add"      // add a much-visited asset to the watchlist
	KindRemove   = "remove"   // remove an asset nobody opens
	KindHorizon  = "horizon"  // make the most-picked view the default
	KindDetail   = "detail"   // switch to full detail
	KindLevel    = "level"    // explain with the numbers
	KindReminder = "reminder" // "Worth a look" in the trade form
)

// KindLabels name each kind for people.
var KindLabels = map[string]string{
	KindAdd: "Adding to your watchlist", KindRemove: "Removing from your watchlist", KindHorizon: "Default views",
	KindDetail: "Detail level", KindLevel: "How it’s explained", KindReminder: "Worth a look reminders",
}

// Event kinds people's actions are recorded as.
var EventKinds = map[string]bool{"visit": true, "view": true, "detail": true, "page": true}

// Pages are the pages whose visits are recorded ("page" events): the ones
// people who want the evidence behind forecasts keep going back to.
var Pages = map[string]string{"track-record": "Track record", "replay": "Replay"}

// Views are the watchlist horizons that can become a default.
var Views = map[string]string{"1d": "day", "1w": "week", "1m": "month", "1y": "year"}

// Card is a suggestion as it is shown.
type Card struct {
	Say  string   `json:"say"`
	More string   `json:"more"`
	Yes  string   `json:"yes"`
	No   string   `json:"no"`
	Why  []string `json:"why"`
	Done string   `json:"done"` // shown after a yes
	Undo string   `json:"undo"` // shown after an undo
}

// Candidate is a suggestion a rule produced.
type Candidate struct {
	Kind    string
	Subject string
	Card    Card
}

// Event is a recorded action.
type Event struct {
	Kind, Subject string
	At            time.Time
}

// Past is an earlier suggestion and its answer.
type Past struct {
	Kind, Subject, Answer string
	ShownAt               time.Time
	SnoozeUntil           *time.Time
}

// State is everything the rules look at.
type State struct {
	Now        time.Time
	Events     []Event              // the last 90 days, oldest first
	Since      *time.Time           // when recording began
	Watch      map[string]time.Time // assets on the watchlist and when each was added
	Horizon    string               // the effective default horizon (1d, 1w, 1m, 1y)
	Detail     string               // the effective detail level (simple | full)
	Level      string               // the effective wording (beginner | intermediate | advanced)
	LevelChose bool                 // the wording is the person's own choice
	QuizAt     *time.Time           // when the knowledge check was last taken
	Names      map[string]string    // display names for symbols
	History    []Past
	MinVisits  int // visits in a week before suggesting an add (4)
	IdleDays   int // days without a visit before suggesting a remove (30)
	ViewWindow int // recent watchlist picks considered (10)
}

func (st State) name(sym string) string {
	if n := st.Names[sym]; n != "" {
		return n
	}
	return sym
}

func day(t time.Time) string { return t.Local().Format("2 Jan") }

// Candidates applies every rule. It leaves out anything waiting for an
// answer, snoozed, or already the setting.
func Candidates(st State) []Candidate {
	if st.MinVisits == 0 {
		st.MinVisits = 4
	}
	if st.IdleDays == 0 {
		st.IdleDays = 30
	}
	if st.ViewWindow == 0 {
		st.ViewWindow = 10
	}
	var out []Candidate
	for _, c := range []*Candidate{addRule(st), removeRule(st), horizonRule(st), detailRule(st), levelRule(st)} {
		if c != nil && !blocked(st, c.Kind, c.Subject) {
			out = append(out, *c)
		}
	}
	return out
}

// blocked: waiting for an answer, or a "not now" that hasn't expired.
func blocked(st State, kind, subject string) bool {
	for _, p := range st.History {
		if p.Kind != kind || p.Subject != subject {
			continue
		}
		if p.Answer == "" {
			return true
		}
		if p.SnoozeUntil != nil && p.SnoozeUntil.After(st.Now) {
			return true
		}
	}
	return false
}

func addRule(st State) *Candidate {
	weekAgo := st.Now.AddDate(0, 0, -7)
	count, first := map[string]int{}, map[string]time.Time{}
	for _, e := range st.Events {
		if e.Kind != "visit" || e.At.Before(weekAgo) {
			continue
		}
		if _, on := st.Watch[e.Subject]; on {
			continue
		}
		if count[e.Subject] == 0 {
			first[e.Subject] = e.At
		}
		count[e.Subject]++
	}
	best := ""
	for sym, n := range count {
		if n >= st.MinVisits && (best == "" || n > count[best] || (n == count[best] && sym < best)) {
			best = sym
		}
	}
	if best == "" {
		return nil
	}
	n, nm := count[best], st.name(best)
	return &Candidate{Kind: KindAdd, Subject: best, Card: Card{
		Say:  fmt.Sprintf("You’ve opened %s %d times this week, but it isn’t on your watchlist.", nm, n),
		More: "Add it, and it’s analysed with the rest of your watchlist on every scheduled run.",
		Yes:  "Add to watchlist", No: "Not now",
		Why: []string{
			fmt.Sprintf("What it saw: %d visits to %s between %s and %s.", n, nm, day(first[best]), day(st.Now)),
			fmt.Sprintf("The rule: %d or more visits in a week to something not on your watchlist.", st.MinVisits),
		},
		Done: nm + " is on your watchlist. Its first analysis runs at the next scheduled run.",
		Undo: nm + " is off your watchlist again.",
	}}
}

func removeRule(st State) *Candidate {
	idle := time.Duration(st.IdleDays) * 24 * time.Hour
	// Only once the app has been watching long enough to know.
	if st.Since == nil || st.Now.Sub(*st.Since) < idle {
		return nil
	}
	last := map[string]time.Time{}
	for _, e := range st.Events {
		if e.Kind == "visit" && e.At.After(last[e.Subject]) {
			last[e.Subject] = e.At
		}
	}
	best, bestIdle := "", time.Duration(0)
	syms := make([]string, 0, len(st.Watch))
	for sym := range st.Watch {
		syms = append(syms, sym)
	}
	sort.Strings(syms)
	for _, sym := range syms {
		added := st.Watch[sym]
		if st.Now.Sub(added) < idle {
			continue
		}
		since := *st.Since
		if t, ok := last[sym]; ok {
			since = t
		}
		if d := st.Now.Sub(since); d >= idle && d > bestIdle {
			best, bestIdle = sym, d
		}
	}
	if best == "" {
		return nil
	}
	nm, days := st.name(best), int(bestIdle.Hours()/24)
	saw := fmt.Sprintf("What it saw: no visits to %s since %s; it’s analysed on every run all the same.", nm, day(st.Now.Add(-bestIdle)))
	say := fmt.Sprintf("You haven’t opened %s in %d days.", nm, days)
	if _, ok := last[best]; !ok {
		saw = fmt.Sprintf("What it saw: no visits to %s since it started noticing, on %s.", nm, day(*st.Since))
		say = fmt.Sprintf("You haven’t opened %s in over %d days.", nm, days)
	}
	return &Candidate{Kind: KindRemove, Subject: best, Card: Card{
		Say: say, More: "Remove it from your watchlist? Its past forecasts stay in your track record.",
		Yes: "Remove " + nm, No: "Keep it",
		Why:  []string{saw, fmt.Sprintf("The rule: %d days without a visit.", st.IdleDays)},
		Done: nm + " is off your watchlist. Its past forecasts are still scored.",
		Undo: nm + " is back on your watchlist.",
	}}
}

func horizonRule(st State) *Candidate {
	var picks []Event
	for _, e := range st.Events {
		if e.Kind == "view" {
			if _, ok := Views[e.Subject]; ok {
				picks = append(picks, e)
			}
		}
	}
	if len(picks) > st.ViewWindow {
		picks = picks[len(picks)-st.ViewWindow:]
	}
	need := st.ViewWindow * 8 / 10
	if len(picks) < need {
		return nil
	}
	count := map[string]int{}
	for _, p := range picks {
		count[p.Subject]++
	}
	best := ""
	for k, n := range count {
		if best == "" || n > count[best] || (n == count[best] && k < best) {
			best = k
		}
	}
	if count[best] < need || best == st.Horizon {
		return nil
	}
	v := Views[best]
	return &Candidate{Kind: KindHorizon, Subject: best, Card: Card{
		Say:  fmt.Sprintf("You picked the %s view on %d of your last %d visits to the watchlist.", v, count[best], len(picks)),
		More: fmt.Sprintf("Open on the %s view by default? Asset pages will open on it too.", v),
		Yes:  "Make " + v + " the default", No: "Not now",
		Why: []string{
			fmt.Sprintf("What it saw: %s chosen %d times out of %d since %s.", v, count[best], len(picks), day(picks[0].At)),
			fmt.Sprintf("The rule: the same view on at least %d of the last %d picks.", need, st.ViewWindow),
			"It changes only where pages open; every horizon stays one tap away.",
		},
		Done: fmt.Sprintf("Your watchlist and asset pages now open on the %s view.", v),
		Undo: "Your default view is back to what it was.",
	}}
}

func detailRule(st State) *Candidate {
	if st.Detail != "simple" {
		return nil
	}
	pages, deep := pageVisits(st)
	if pages < 8 || deep*4 < pages*3 {
		return nil
	}
	return &Candidate{Kind: KindDetail, Subject: "full", Card: Card{
		Say:  fmt.Sprintf("You opened the technical detail on %d of your last %d asset pages.", deep, pages),
		More: "Switch to full detail? Asset pages open on the technical tab and the watchlist shows every horizon.",
		Yes:  "Switch to full detail", No: "Not now",
		Why: []string{
			fmt.Sprintf("What it saw: the Technical, Drivers or Full reasoning tab opened on %d of %d asset pages in the last 30 days.", deep, pages),
			"The rule: detail opened on at least 3 in 4 asset pages, over 8 or more pages.",
		},
		Done: "Full detail is on. Change it any time on About you.",
		Undo: "You’re back on the simple view.",
	}}
}

// levelRule offers explanations with the numbers to someone whose use of the
// app shows they want the evidence: going back to Track record or Replay on
// several days, or, already on full detail, opening the drivers and full
// reasoning on most asset pages. It stays quiet when the wording is already
// advanced, when they chose a wording themselves, and for a month after the
// knowledge check, which has just said where they are.
func levelRule(st State) *Candidate {
	if st.Level == "advanced" || st.LevelChose {
		return nil
	}
	if st.QuizAt != nil && st.QuizAt.After(st.Now.AddDate(0, 0, -30)) {
		return nil
	}
	var saw, rule string
	days, first := evidenceDays(st)
	pages, deep := pageVisits(st)
	switch {
	case days >= 4:
		saw = fmt.Sprintf("You’ve opened Track record or Replay on %d different days in the past month.", days)
		rule = "The rule: Track record or Replay opened on 4 or more days in 30."
		first = "What it saw: visits on " + fmt.Sprint(days) + " days since " + first + "."
	case st.Detail == "full" && pages >= 8 && deep*2 >= pages:
		saw = fmt.Sprintf("You opened the drivers or the full reasoning on %d of your last %d asset pages.", deep, pages)
		rule = "The rule: on full detail, the deeper tabs opened on at least half of 8 or more asset pages."
		first = fmt.Sprintf("What it saw: %d of %d asset pages in the last 30 days.", deep, pages)
	default:
		return nil
	}
	more := "Show the numbers behind each forecast too? Explanations add the test statistics (Brier skill, AUC) and how unusual each move is."
	if st.Level == "beginner" {
		more += " Today switches to the precise wording."
	}
	more += " Or take the 5-question check in the setup and let it decide."
	return &Candidate{Kind: KindLevel, Subject: "advanced", Card: Card{
		Say: saw, More: more,
		Yes: "Show the numbers", No: "Not now",
		Why: []string{first, rule, "It changes only the wording. Forecasts, tests and warnings stay the same."},
		Done: "Explanations now include the numbers. Change it any time on About you.",
		Undo: "Explanations are back to how they were.",
	}}
}

// evidenceDays counts the different days in the last 30 with a visit to
// Track record or Replay, and the first of them.
func evidenceDays(st State) (int, string) {
	cut := st.Now.AddDate(0, 0, -30)
	seen := map[string]bool{}
	first := ""
	for _, e := range st.Events {
		if e.Kind != "page" || e.At.Before(cut) {
			continue
		}
		d := e.At.Local().Format("2006-01-02")
		if !seen[d] {
			seen[d] = true
			if first == "" {
				first = day(e.At)
			}
		}
	}
	return len(seen), first
}

// pageVisits counts asset-page visits in the last 30 days (one per asset a
// day) and how many of them had the technical detail opened.
func pageVisits(st State) (pages, deep int) {
	cut := st.Now.AddDate(0, 0, -30)
	visits, details := map[string]bool{}, map[string]bool{}
	for _, e := range st.Events {
		if e.At.Before(cut) {
			continue
		}
		k := e.Subject + "|" + e.At.Local().Format("2006-01-02")
		switch e.Kind {
		case "visit":
			visits[k] = true
		case "detail":
			details[k] = true
		}
	}
	for k := range visits {
		pages++
		if details[k] {
			deep++
		}
	}
	return pages, deep
}

// ------------------------------------------------------------- per person

// PriorWeight is how many answers the shared starting point is worth: after
// a few of your own answers, yours count for more.
const PriorWeight = 4

// Pick is what the app has learned about one kind of suggestion for one person.
type Pick struct {
	Kind     string  `json:"kind"`
	Label    string  `json:"label"`
	Everyone float64 `json:"everyone"` // share of yes from everyone else (0.5 with no data)
	Yes      int     `json:"yes"`
	Answered int     `json:"answered"`
	Estimate float64 `json:"estimate"` // the chance this person says yes, as the app now judges it
}

// Estimate blends the shared rate with the person's own answers.
func Estimate(everyone float64, yes, n int) float64 {
	return (everyone*PriorWeight + float64(yes)) / (PriorWeight + float64(n))
}

// Picks works out the per-kind estimates. others maps kind → [yes, answered]
// for everyone else; history is the person's own suggestions.
func Picks(others map[string][2]int, history []Past) []Pick {
	var out []Pick
	for _, k := range []string{KindAdd, KindRemove, KindHorizon, KindDetail, KindLevel, KindReminder} {
		o := others[k]
		// Smooth everyone's rate towards a half so a handful of answers can't swing it.
		every := (float64(o[0]) + 1) / (float64(o[1]) + 2)
		p := Pick{Kind: k, Label: KindLabels[k], Everyone: every}
		for _, h := range history {
			if h.Kind != k {
				continue
			}
			switch h.Answer {
			case "yes", "noted":
				p.Yes++
				p.Answered++
			case "no", "undone", "muted":
				p.Answered++
			}
		}
		p.Estimate = Estimate(p.Everyone, p.Yes, p.Answered)
		out = append(out, p)
	}
	return out
}

// Rank orders candidates by how likely this person is to say yes, adds the
// "why you're seeing it" line, and holds back kinds the person keeps
// declining (3 or more answers, and well under the shared rate) unless they
// haven't been asked one in 60 days.
func Rank(cands []Candidate, picks []Pick, history []Past, now time.Time) []Candidate {
	by := map[string]Pick{}
	for _, p := range picks {
		by[p.Kind] = p
	}
	var out []Candidate
	for _, c := range cands {
		p := by[c.Kind]
		if p.Answered >= 3 && p.Estimate < 0.6*p.Everyone && askedWithin(history, c.Kind, now.AddDate(0, 0, -60)) {
			continue
		}
		c.Card.Why = append(c.Card.Why, whyLine(p))
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool { return by[out[i].Kind].Estimate > by[out[j].Kind].Estimate })
	return out
}

func askedWithin(history []Past, kind string, since time.Time) bool {
	for _, h := range history {
		if h.Kind == kind && h.ShownAt.After(since) {
			return true
		}
	}
	return false
}

func whyLine(p Pick) string {
	switch {
	case p.Answered == 0:
		return "Why now: it’s the first suggestion of this kind, so it starts from what people in general accept."
	case p.Estimate > p.Everyone+0.02:
		return fmt.Sprintf("Why you’re seeing it: you said yes to %d of %d suggestions like this, so these come first for you.", p.Yes, p.Answered)
	case p.Estimate < p.Everyone-0.02:
		return fmt.Sprintf("Why you’re seeing it: you said yes to %d of %d suggestions like this, so these come up less often for you.", p.Yes, p.Answered)
	}
	return fmt.Sprintf("Why you’re seeing it: you said yes to %d of %d suggestions like this, about as often as people in general.", p.Yes, p.Answered)
}

// Snooze is how long "not now" waits before asking the same thing again.
func Snooze(kind string) time.Duration {
	if kind == KindRemove {
		return 90 * 24 * time.Hour
	}
	return 30 * 24 * time.Hour
}

// ------------------------------------------------------------- noticed

// Noticed is one thing the app has noticed, shown on About you.
type Noticed struct {
	Key    string `json:"key"` // the event kind it comes from; forgetting deletes those events
	Text   string `json:"text"`
	Detail string `json:"detail"`
}

func Notice(st State) []Noticed {
	out := []Noticed{}
	var picks []Event
	for _, e := range st.Events {
		if e.Kind == "view" {
			if _, ok := Views[e.Subject]; ok {
				picks = append(picks, e)
			}
		}
	}
	if len(picks) > 10 {
		picks = picks[len(picks)-10:]
	}
	if len(picks) >= 5 {
		count := map[string]int{}
		best := ""
		for _, p := range picks {
			count[p.Subject]++
			if best == "" || count[p.Subject] > count[best] {
				best = p.Subject
			}
		}
		out = append(out, Noticed{"view", fmt.Sprintf("You look at the %s view most.", Views[best]),
			fmt.Sprintf("%s on %d of your last %d watchlist picks", capital(Views[best]), count[best], len(picks))})
	}
	if pages, deep := pageVisits(st); pages >= 4 {
		t := "You rarely open the technical detail."
		if deep*2 >= pages {
			t = "You open the technical detail often."
		}
		out = append(out, Noticed{"detail", t, fmt.Sprintf("%d of your last %d asset pages", deep, pages)})
	}
	if days, _ := evidenceDays(st); days >= 2 {
		out = append(out, Noticed{"page", "You check the evidence: Track record and Replay.", fmt.Sprintf("Opened on %d of the last 30 days", days)})
	}
	cut := st.Now.AddDate(0, 0, -30)
	count := map[string]int{}
	for _, e := range st.Events {
		if e.Kind == "visit" && !e.At.Before(cut) {
			count[e.Subject]++
		}
	}
	var syms []string
	for s, n := range count {
		if n >= 2 {
			syms = append(syms, s)
		}
	}
	sort.Slice(syms, func(i, j int) bool {
		if count[syms[i]] != count[syms[j]] {
			return count[syms[i]] > count[syms[j]]
		}
		return syms[i] < syms[j]
	})
	if len(syms) > 3 {
		syms = syms[:3]
	}
	if len(syms) > 0 {
		names := ""
		for i, s := range syms {
			if i > 0 {
				names += ", "
			}
			names += st.name(s)
		}
		out = append(out, Noticed{"visit", "Most visited: " + names + ".", "Last 30 days"})
	}
	return out
}

func capital(s string) string {
	if s == "" {
		return s
	}
	return string(s[0]-32) + s[1:]
}
