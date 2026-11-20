package learn

import (
	"fmt"
	"math"
	"math/rand"
	"sort"
	"time"
	"unicode"
	"unicode/utf8"
)

// The personal model learns from one person's own trades (paper trades and
// practice buys) which of their setups tend to lose. With tens of trades,
// anything bigger than a single yes-or-no question would fit noise, so it
// asks each question in Conds and keeps the one that splits winners from
// losers most. The chance of luck accounts for having picked the best of
// many questions: the same search is rerun on shuffled results.
const (
	MinSide   = 3    // trades on each side of a question before it's compared
	ClearLuck = 0.25 // an early read names a hint below this chance of luck
	PassLuck  = 0.05 // the full test needs less than this
	shuffles  = 2000
)

// Sample is one trade as the model sees it.
type Sample struct {
	At       time.Time
	Practice bool
	Win      bool // made money after fees
	Answers  map[string]bool
}

// Finding is the question that splits a person's trades most.
type Finding struct {
	Key     string  `json:"key"`
	Worse   bool    `json:"worse"` // trades with this setup did worse
	Title   string  `json:"title"`
	Detail  string  `json:"detail"`
	In      int     `json:"in"`
	InWins  int     `json:"inWins"`
	Out     int     `json:"out"`
	OutWins int     `json:"outWins"`
	Rate    float64 `json:"rate"`  // best guess: share of these trades that make money
	Lo      float64 `json:"lo"`    // 90% range for it
	Hi      float64 `json:"hi"`    //
	Other   float64 `json:"other"` // the share for the rest of their trades
	Luck    float64 `json:"luck"`  // chance a difference this big comes from luck alone
}

// FullTest is the check run from FullTrades trades: the question is picked on
// the earlier two-thirds of trades and checked on the later third it never
// saw, and the luck check must pass on all of them.
type FullTest struct {
	Passed  bool   `json:"passed"`
	Earlier int    `json:"earlier"`
	Later   int    `json:"later"`
	HeldUp  bool   `json:"heldUp"` // the later trades leaned the same way
	Text    string `json:"text"`
}

// Read is what the personal model can say so far.
type Read struct {
	Stage    string    `json:"stage"` // collecting | early | full
	Trades   int       `json:"trades"`
	Paper    int       `json:"paper"`
	Practice int       `json:"practice"`
	Early    int       `json:"early"`
	Full     int       `json:"full"`
	Clear    bool      `json:"clear"` // a hint stands out
	Finding  *Finding  `json:"finding,omitempty"`
	Test     *FullTest `json:"test,omitempty"`
}

type split struct {
	key                string
	in, inW, out, outW int
	gap                float64 // share winning with the setup − without
	ok                 bool
}

// best finds the question with the biggest gap between trades with and
// without the setup (ties: the earlier question in Conds).
func best(ss []Sample, wins []bool) split {
	var top split
	for _, c := range Conds {
		sp := split{key: c.Key}
		for i, s := range ss {
			v, known := s.Answers[c.Key]
			if !known {
				continue
			}
			if v {
				sp.in++
				if wins[i] {
					sp.inW++
				}
			} else {
				sp.out++
				if wins[i] {
					sp.outW++
				}
			}
		}
		if sp.in < MinSide || sp.out < MinSide {
			continue
		}
		sp.gap = float64(sp.inW)/float64(sp.in) - float64(sp.outW)/float64(sp.out)
		sp.ok = true
		if !top.ok || math.Abs(sp.gap) > math.Abs(top.gap)+1e-12 {
			top = sp
		}
	}
	return top
}

// luck reruns the search on shuffled results: the share of shuffles whose
// best gap is at least as big as the real one.
func luck(ss []Sample, gap float64) float64 {
	rng := rand.New(rand.NewSource(1))
	wins := make([]bool, len(ss))
	for i, s := range ss {
		wins[i] = s.Win
	}
	hits := 0
	for r := 0; r < shuffles; r++ {
		rng.Shuffle(len(wins), func(i, j int) { wins[i], wins[j] = wins[j], wins[i] })
		if b := best(ss, wins); b.ok && math.Abs(b.gap) >= math.Abs(gap)-1e-12 {
			hits++
		}
	}
	return float64(hits+1) / float64(shuffles+1)
}

// wilson is the 90% range for a share of k out of n.
func wilson(k, n int) (float64, float64) {
	if n == 0 {
		return 0, 1
	}
	z, p, fn := 1.645, float64(k)/float64(n), float64(n)
	d := 1 + z*z/fn
	c := (p + z*z/(2*fn)) / d
	h := z * math.Sqrt(p*(1-p)/fn+z*z/(4*fn*fn)) / d
	return math.Max(0, c-h), math.Min(1, c+h)
}

func winsOf(ss []Sample) []bool {
	w := make([]bool, len(ss))
	for i, s := range ss {
		w[i] = s.Win
	}
	return w
}

func finding(sp split, ss []Sample) *Finding {
	c := condByKey(sp.key)
	f := &Finding{Key: sp.key, Worse: sp.gap < 0, In: sp.in, InWins: sp.inW, Out: sp.out, OutWins: sp.outW,
		Rate: float64(sp.inW) / float64(sp.in), Other: float64(sp.outW) / float64(sp.out)}
	f.Lo, f.Hi = wilson(sp.inW, sp.in)
	verb := "tend to make money"
	if f.Worse {
		verb = "tend to lose"
	}
	f.Title = fmt.Sprintf("%s %s.", c.Subject, verb)
	f.Detail = fmt.Sprintf("%s: %d of %d made money. Otherwise, %d of %d did.", capFirst(c.Short), sp.inW, sp.in, sp.outW, sp.out)
	f.Luck = luck(ss, sp.gap)
	return f
}

// capFirst capitalises a sentence's first letter (digits and symbols stay).
func capFirst(s string) string {
	r, n := utf8.DecodeRuneInString(s)
	if n == 0 {
		return s
	}
	return string(unicode.ToUpper(r)) + s[n:]
}

// Model works out the personal model's read from a person's trades.
func Model(ss []Sample) Read {
	r := Read{Stage: "collecting", Trades: len(ss), Early: EarlyTrades, Full: FullTrades}
	for _, s := range ss {
		if s.Practice {
			r.Practice++
		} else {
			r.Paper++
		}
	}
	if len(ss) < EarlyTrades {
		return r
	}
	ss = append([]Sample(nil), ss...)
	sort.SliceStable(ss, func(i, j int) bool { return ss[i].At.Before(ss[j].At) })
	r.Stage = "early"
	sp := best(ss, winsOf(ss))
	if !sp.ok {
		return r
	}
	r.Finding = finding(sp, ss)
	r.Clear = r.Finding.Luck < ClearLuck
	if len(ss) < FullTrades {
		return r
	}
	r.Stage = "full"
	cut := len(ss) * 2 / 3
	early, later := ss[:cut], ss[cut:]
	t := &FullTest{Earlier: len(early), Later: len(later)}
	r.Test = t
	pick := best(early, winsOf(early))
	if pick.ok {
		var in, inW, out, outW int
		for _, s := range later {
			v, known := s.Answers[pick.key]
			switch {
			case !known:
			case v:
				in++
				if s.Win {
					inW++
				}
			default:
				out++
				if s.Win {
					outW++
				}
			}
		}
		if in > 0 && out > 0 {
			g := float64(inW)/float64(in) - float64(outW)/float64(out)
			t.HeldUp = g != 0 && (g < 0) == (pick.gap < 0)
		}
	}
	t.Passed = t.HeldUp && pick.key == sp.key && r.Finding.Luck < PassLuck
	switch {
	case t.Passed:
		t.Text = fmt.Sprintf("Passed: found on your first %d trades, it held up on the %d after, and luck is unlikely.", t.Earlier, t.Later)
	case !pick.ok:
		t.Text = fmt.Sprintf("Not passed: your first %d trades didn't split clearly on any question.", t.Earlier)
	case !t.HeldUp || pick.key != sp.key:
		t.Text = fmt.Sprintf("Not passed: what your first %d trades showed didn't hold up on the %d after.", t.Earlier, t.Later)
	default:
		t.Text = "Not passed: it held up on your later trades, but the difference could still be luck."
	}
	r.Clear = r.Clear || t.Passed
	return r
}
