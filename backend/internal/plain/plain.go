// Package plain turns a report's numbers into the asset page's short answer
// and reasons, in plain words. It only restates what the report already
// says; it never adds a view of its own.
package plain

import (
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"

	"github.com/sodiq/financial-market/backend/internal/engine"
)

// Reason is one numbered point: a bold lead and the rest of the sentence.
type Reason struct {
	Lead string `json:"lead"`
	Text string `json:"text"`
}

// Answer is the plain reading of one horizon.
type Answer struct {
	Key       string   `json:"key"`
	Say       string   `json:"say"`  // the one-sentence answer
	More      string   `json:"more"` // the sentence or two under it
	Tested    string   `json:"tested"`
	TestLabel string   `json:"testLabel"`
	Tests     int      `json:"tests"`
	Right     float64  `json:"right"`
	Guess     float64  `json:"guess"`
	GuessWord string   `json:"guessWord"` // "up" or "down": what always-guessing picks
	Reasons   []Reason `json:"reasons"`
	Shown     int      `json:"shown"` // how many reasons show before "show all"
}

var phrase = map[string]string{"1d": "for the next trading day", "1w": "for the next week", "1m": "for the next month", "1y": "for the next year"}
var later = map[string]string{"1d": "a day later", "1w": "a week later", "1m": "a month later", "1y": "a year later"}

// Explain reads every available horizon. level is beginner | intermediate | advanced.
func Explain(rep *engine.Report, level string) []Answer {
	out := []Answer{}
	for i := range rep.Horizons {
		h := &rep.Horizons[i]
		if !h.Available {
			continue
		}
		out = append(out, explain(rep, h, level))
	}
	return out
}

func explain(rep *engine.Report, h *engine.HorizonForecast, level string) Answer {
	e := h.Validation.Ensemble
	a := Answer{Key: h.Key, Tests: e.N, Right: e.Accuracy, Guess: e.BaseAccuracy, GuessWord: "up", Reasons: []Reason{}}
	if e.UpRate < 0.5 {
		a.GuessWord = "down"
	}
	name := Short(rep.Name, rep.Symbol)
	ph := phrase[h.Key]
	p, base := h.PUp, h.BaseRate
	gap := p - base
	up := strings.Contains(h.Signal, "BUY")
	down := strings.Contains(h.Signal, "SELL")

	switch h.Status {
	case "validated":
		a.Tested, a.TestLabel = "proven", "Tested edge"
	case "weak":
		a.Tested, a.TestLabel = "weak", "Weak evidence"
	default:
		a.Tested, a.TestLabel = "not_proven", "Not proven"
	}

	// The answer.
	switch {
	case h.Status == "validated" && up:
		a.Say = "Likely higher " + ph + "."
	case h.Status == "validated" && down:
		a.Say = "Likely lower " + ph + "."
	case h.Status == "validated":
		a.Say = "No clear lean " + ph + ", from a model that has passed testing."
	case h.Status == "weak" && math.Abs(gap) >= 0.03:
		dir := "higher"
		if gap < 0 {
			dir = "lower"
		}
		a.Say = "Leaning " + dir + " " + ph + ", but the evidence is weak."
	case base >= 0.75 && e.BaseAccuracy > e.Accuracy:
		a.Say = "Usually higher a year on, and this model can't add to that."
		if h.Key != "1y" {
			a.Say = "Usually higher over this stretch, and this model can't add to that."
		}
	default:
		a.Say = "Too close to call " + ph + "."
	}

	// The sentence under it.
	when := "by " + dayMonth(h.TargetDate)
	if h.Key == "1d" {
		when = "at the next close"
	}
	cmp := fmt.Sprintf("about the usual %s", pct(base))
	if math.Abs(gap) >= 0.05 {
		word := "above"
		if gap < 0 {
			word = "below"
		}
		cmp = fmt.Sprintf("%s the usual %s", word, pct(base))
	}
	more := []string{fmt.Sprintf("%s chance it's higher %s, %s.", pct(p), when, cmp)}
	k := h.Analogs
	switch {
	case k.K > 0 && p > 0.5 && k.UpRate < 0.4:
		more = append(more, fmt.Sprintf("Days like today were mostly followed by falls, so hold that %s loosely.", pct(p)))
	case k.K > 0 && p < 0.5 && k.UpRate > 0.6:
		more = append(more, fmt.Sprintf("Days like today were mostly followed by rises, so hold that %s loosely.", pct(p)))
	}
	if h.EarningsInside && rep.Earnings.Next != nil && h.Bars <= 21 {
		more = append(more, fmt.Sprintf("%s's earnings report on %s makes this less predictable than usual.", name, dayMonth(rep.Earnings.Next.Date)))
	}
	if h.Status != "validated" && e.BaseAccuracy > e.Accuracy+0.05 {
		more = append(more, fmt.Sprintf("In testing, simply guessing “%s” beat this model.", a.GuessWord))
	}
	a.More = strings.Join(more, " ")

	// Reasons, most important first.
	add := func(lead, text string) { a.Reasons = append(a.Reasons, Reason{lead, text}) }
	tests := fmt.Sprintf(" In %s past tests on %s this method was right %s of the time; always guessing “%s” was right %s.", thousands(e.N), name, pct1(e.Accuracy), a.GuessWord, pct1(e.BaseAccuracy))
	if level == "advanced" {
		tests += fmt.Sprintf(" Brier skill %+.2f%%, AUC %.3f.", 100*e.BrierSkill, e.AUC)
	}
	switch a.Tested {
	case "proven":
		add("A tested edge.", tests)
	case "weak":
		add("Weak evidence.", tests)
	default:
		add("No proven edge.", tests)
	}
	if k.K > 0 {
		lead := "Days like today were mixed."
		if k.UpRate < 0.4 {
			lead = "Days like today leaned lower."
		} else if k.UpRate > 0.6 {
			lead = "Days like today leaned higher."
		}
		add(lead, fmt.Sprintf(" Of the %d past days most like today, %s were higher %s (middle result %s).", k.K, pct(k.UpRate), later[h.Key], spct(k.MedianReturn)))
	}
	if d := standout(h.Drivers); d != nil {
		add("Most unusual right now.", " "+driverSentence(d.Text, level))
	}
	if h.EarningsInside && rep.Earnings.Reactions >= 4 && h.Bars <= 21 {
		add("Earnings widen the range.", fmt.Sprintf(" Past reports moved it about %s either way, against %s on a normal day.", pct1(rep.Earnings.AvgAbsMove), pct1(rep.Earnings.NormalMove)))
	}
	a.Shown = len(a.Reasons)

	if math.Abs(h.PLogistic-h.PAnalogs) >= 0.15 {
		add("The two models disagree.", fmt.Sprintf(" The factor model says %s; the similar-days model says %s. Disagreement means less confidence.", pct(h.PLogistic), pct(h.PAnalogs)))
	}
	if ca := h.CrossAsset; ca != nil {
		if ca.Used {
			add("A wider model is part of the answer.", fmt.Sprintf(" One trained on %d assets says %s and has beaten guessing in testing, so it's part of the answer.", ca.Assets, pct(ca.P)))
		} else {
			add("A wider model isn't used yet.", fmt.Sprintf(" One trained on %d assets says %s, but it hasn't beaten guessing in testing, so it's left out.", ca.Assets, pct(ca.P)))
		}
	}
	if o := h.Options; o != nil && o.ImpliedMove > 0 {
		r := o.ModelMove / o.ImpliedMove
		lead := "Options traders expect a similar size of move."
		if r > 1.3 {
			lead = "This model expects bigger swings than options traders do."
		} else if r < 0.77 {
			lead = "Options traders expect bigger swings than this model."
		}
		add(lead, fmt.Sprintf(" Option prices imply about ±%s by %s; this model expects ±%s.", pct1(o.ImpliedMove), dayMonth(o.Expiry), pct1(o.ModelMove)))
	}
	if math.Abs(h.NewsShift) < 0.01 {
		add("News barely moves this forecast.", "")
	} else {
		dir := "up"
		if h.NewsShift < 0 {
			dir = "down"
		}
		add("News moves it a little.", fmt.Sprintf(" Recent headlines shift the chance %s by %.0f points.", dir, math.Abs(100*h.NewsShift)))
	}
	return a
}

// standout is the input furthest from normal (at least 2σ), if any.
func standout(ds []engine.Driver) *engine.Driver {
	var best *engine.Driver
	for i := range ds {
		d := &ds[i]
		if math.Abs(d.Z) >= 2 && d.Text != "" && (best == nil || math.Abs(d.Z) > math.Abs(best.Z)) {
			best = d
		}
	}
	return best
}

var sigma = regexp.MustCompile(`\s*\([−+-]?[0-9.]+σ?\)`) // "(-2.9σ)" or a raw reading like "(-0.63)"

// driverSentence keeps the first sentence of a driver's text, without the σ
// figure for anyone but advanced readers: "Past month -28.4% — far below
// normal for this asset (-2.9σ). The model…" -> "Past month -28.4%, far below
// normal for this asset."
func driverSentence(t, level string) string {
	if i := strings.Index(t, ". "); i > 0 {
		t = t[:i+1]
	}
	t = strings.ReplaceAll(t, " — ", ", ")
	if level != "advanced" {
		t = sigma.ReplaceAllString(t, "")
	}
	if !strings.HasSuffix(t, ".") {
		t += "."
	}
	return t
}

// Short is a company's everyday name: "NVIDIA Corporation" -> "NVIDIA".
// Long fund names fall back to the ticker.
func Short(name, symbol string) string {
	n := strings.TrimSpace(name)
	for _, suf := range []string{" USD", " Corporation", " Corp.", " Corp", " Inc.", " Inc", " plc", " PLC", " Ltd.", " Ltd", " N.V.", " S.A.", " Holdings", " Company"} {
		n = strings.TrimSuffix(n, suf)
	}
	n = strings.TrimSuffix(strings.TrimSpace(n), ",")
	if n == "" || len(n) > 24 {
		return symbol
	}
	return n
}

func dayMonth(d string) string {
	t, err := time.Parse("2006-01-02", d)
	if err != nil {
		return d
	}
	if t.Year() != time.Now().Year() {
		return t.Format("2 Jan 2006")
	}
	return t.Format("2 Jan")
}

func pct(x float64) string { return fmt.Sprintf("%.0f%%", 100*x) }

func pct1(x float64) string { return fmt.Sprintf("%.1f%%", 100*x) }

func spct(x float64) string {
	v := 100 * x
	s := "+"
	if v < 0 {
		s = "−"
	}
	if math.Abs(v) < 0.05 {
		return "0%"
	}
	if math.Abs(v) >= 10 {
		return fmt.Sprintf("%s%.0f%%", s, math.Abs(v))
	}
	return fmt.Sprintf("%s%.1f%%", s, math.Abs(v))
}

func thousands(n int) string {
	s := fmt.Sprint(n)
	var b []string
	for len(s) > 3 {
		b = append([]string{s[len(s)-3:]}, b...)
		s = s[:len(s)-3]
	}
	return strings.Join(append([]string{s}, b...), ",")
}
