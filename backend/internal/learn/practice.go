package learn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"strings"
	"time"

	"github.com/sodiq/financial-market/backend/internal/market"
	"github.com/sodiq/financial-market/backend/internal/store"
	"github.com/sodiq/financial-market/backend/internal/token"
)

// Practice rounds: the person decides on a real past day with the name and
// date hidden, then sees what happened straight away.
const (
	RoundsPerSet  = 10
	Stake         = 1000.0 // each practice buy puts in this much
	beforeBars    = 130    // bars shown before the day
	minHistory    = 1300   // bars needed before the day, so the app's analysis has enough to train and test on
	DefaultStop   = 0.08
	DefaultTarget = 0.15
)

// StopChoices and TargetChoices are the plans offered.
var (
	StopChoices   = []float64{0.05, 0.08, 0.12}
	TargetChoices = []float64{0.10, 0.15, 0.25}
)

// PracticePool is where practice days come from (plus the person's own
// watchlist): well-known names with long histories.
var PracticePool = map[string]string{
	"AAPL":    "A large US phone and computer maker",
	"MSFT":    "A large US software company",
	"AMD":     "A large US chip maker",
	"NVDA":    "A large US chip maker",
	"INTC":    "A large US chip maker",
	"AMZN":    "A large US online retailer",
	"GOOGL":   "A large US internet company",
	"META":    "A large US social media company",
	"TSLA":    "A US electric car maker",
	"JPM":     "A large US bank",
	"XOM":     "A large US oil company",
	"KO":      "A large US drinks maker",
	"WMT":     "A large US retailer",
	"COST":    "A large US warehouse retailer",
	"DIS":     "A large US entertainment company",
	"NFLX":    "A US streaming company",
	"BA":      "A large US plane maker",
	"PFE":     "A large US drug maker",
	"SPY":     "A fund that tracks the 500 largest US companies",
	"QQQ":     "A fund that tracks large US tech companies",
	"IWM":     "A fund that tracks small US companies",
	"GLD":     "A fund that tracks the price of gold",
	"BTC-USD": "The largest crypto coin",
	"ETH-USD": "The second-largest crypto coin",
}

// AppSaid is what the app's analysis said on a practice day, for the next month.
type AppSaid struct {
	Available bool    `json:"available"`
	Text      string  `json:"text"`
	Lean      string  `json:"lean"` // up | down | ""
	Status    string  `json:"status"`
	Tested    bool    `json:"tested"` // a tested edge pointing up
	PUp       float64 `json:"pUp"`
	BaseRate  float64 `json:"baseRate"`
	Signal    string  `json:"signal"` // the month-ahead signal, as the app showed it
}

var statusMark = map[string]string{"validated": "✓ tested edge", "weak": "◐ weak evidence", "no_edge": "○ not proven"}
var statusWords = map[string]string{"validated": "a tested edge", "weak": "weak evidence", "no_edge": "no proven edge"}

// AppSaidFrom builds what the app said from the month-ahead forecast.
func AppSaidFrom(available bool, signal, status string, pUp, base float64) AppSaid {
	if !available {
		return AppSaid{Text: "The app had no month-ahead reading for that day."}
	}
	a := AppSaid{Available: true, Status: status, PUp: pUp, BaseRate: base, Signal: signal}
	// The lean is the app's own call (a buy or sell signal), as for paper
	// trades; most days with no proven edge get no call.
	a.Lean = LeanOf(signal)
	a.Tested = status == "validated" && a.Lean == "up"
	mark := statusMark[status]
	if mark == "" {
		mark = "○ not proven"
	}
	call := map[string]string{"up": "buy call", "down": "sell call", "": "no call"}[a.Lean]
	a.Text = fmt.Sprintf("%s · %s · %.0f%% chance higher in a month (usually %.0f%%).", mark, call, pUp*100, base*100)
	return a
}

// roundBody is a prepared round as stored.
type roundBody struct {
	Hint   string       `json:"hint"`
	Name   string       `json:"name"`
	Crypto bool         `json:"crypto"`
	Hold   int          `json:"hold"`
	Entry  float64      `json:"entry"`
	Before []float64    `json:"before"` // closes ÷ entry, ending with the day itself
	After  []market.Bar `json:"after"`  // the bars after the day, up to Hold
	Setup  Setup        `json:"setup"`
	Fee    float64      `json:"fee"` // per side
}

// Round is a practice round as the page gets it. The answer fields are
// filled only once the person has decided.
type Round struct {
	ID      int64     `json:"id"`
	Set     int       `json:"set"`
	N       int       `json:"n"`
	Of      int       `json:"of"`
	Hint    string    `json:"hint"`
	Before  []float64 `json:"before"`
	Hold    int       `json:"hold"`
	App     *AppSaid  `json:"app,omitempty"`
	Working bool      `json:"working"` // the app's view of the day is still being worked out
	Decided bool      `json:"decided"`

	Call      string    `json:"call,omitempty"`
	StopPct   float64   `json:"stopPct,omitempty"`
	TargetPct float64   `json:"targetPct,omitempty"`
	Symbol    string    `json:"symbol,omitempty"`
	Name      string    `json:"name,omitempty"`
	Day       string    `json:"day,omitempty"`
	Entry     float64   `json:"entry,omitempty"`
	After     []float64 `json:"after,omitempty"`  // closes ÷ entry for the month after
	Result    string    `json:"result,omitempty"` // target | stop | time
	ExitBar   int       `json:"exitBar,omitempty"`
	ExitPrice float64   `json:"exitPrice,omitempty"`
	PnL       float64   `json:"pnl,omitempty"` // on Stake, after fees (had they bought, for a skip)
	Headline  string    `json:"headline,omitempty"`
	AppLine   string    `json:"appLine,omitempty"`
}

// SetSummary is what a finished set of rounds shows.
type SetSummary struct {
	Rounds   int       `json:"rounds"`
	Bought   int       `json:"bought"`
	Target   int       `json:"target"`
	Stop     int       `json:"stop"`
	Time     int       `json:"time"`
	PnL      float64   `json:"pnl"`
	Headline string    `json:"headline"`
	Lines    []string  `json:"lines"`
	Worth    *Reminder `json:"worth,omitempty"`
}

// PracticeState is the practice page's data.
type PracticeState struct {
	Set      int         `json:"set"` // 0 before the first set
	Rounds   []Round     `json:"rounds"`
	Current  *Round      `json:"current,omitempty"` // the round waiting for a decision
	Summary  *SetSummary `json:"summary,omitempty"` // once the set is finished
	Done     int         `json:"done"`              // rounds decided, all sets
	Bought   int         `json:"bought"`
	Stops    []float64   `json:"stops"`
	Targets  []float64   `json:"targets"`
	Stake    float64     `json:"stake"`
	Finished bool        `json:"finished"`
}

// Outcome plays a plan out over the bars after the entry: the stop or the
// target, whichever is reached first (the stop if both are in one day, to be
// safe), otherwise the last close. A day that opens past the stop fills at
// the open, as a real order would.
func Outcome(entry float64, after []market.Bar, stop, target float64) (result string, bar int, exit float64) {
	sp, tp := entry*(1-stop), entry*(1+target)
	for k, b := range after {
		lo, hi := b.Low, b.High
		if lo <= 0 {
			lo = b.Close
		}
		if hi <= 0 {
			hi = b.Close
		}
		if lo <= sp {
			if b.Open > 0 && b.Open < sp {
				return "stop", k + 1, b.Open
			}
			return "stop", k + 1, sp
		}
		if hi >= tp {
			if b.Open > tp {
				return "target", k + 1, b.Open
			}
			return "target", k + 1, tp
		}
	}
	if len(after) == 0 {
		return "time", 0, entry
	}
	return "time", len(after), after[len(after)-1].Close
}

// PnL on Stake, after a fee on the buy and on the sell.
func PnL(entry, exit, fee float64) float64 {
	qty := Stake / (entry * (1 + fee))
	return qty*exit*(1-fee) - Stake
}

func hintFor(sym string, class market.AssetClass) string {
	if h, ok := PracticePool[sym]; ok {
		return h
	}
	switch class {
	case market.ClassCrypto:
		return "A crypto coin"
	case market.ClassETF, market.ClassFund:
		return "A fund"
	}
	return "A listed company"
}

// prepRound builds a round from a symbol's history, on a random day with
// enough history before it and a full month after.
func prepRound(rng *rand.Rand, sym string, ser *market.Series, fee float64) (*roundBody, string, bool) {
	crypto := ser.Meta.Class == market.ClassCrypto || market.IsCrypto(sym)
	hold := 21
	if crypto {
		hold = 30
	}
	bars := ser.Bars
	hi := len(bars) - hold - 2
	if hi <= minHistory {
		return nil, "", false
	}
	i := minHistory + rng.Intn(hi-minHistory)
	entry := bars[i].Close
	if entry <= 0 {
		return nil, "", false
	}
	b := &roundBody{Hint: hintFor(sym, ser.Meta.Class), Name: strings.TrimSuffix(ser.Meta.Name, " USD"), Crypto: crypto, Hold: hold, Entry: entry, Fee: fee}
	if b.Name == "" {
		b.Name = sym
	}
	for k := i - beforeBars + 1; k <= i; k++ {
		b.Before = append(b.Before, round4(bars[k].Close/entry))
	}
	b.After = append([]market.Bar(nil), bars[i+1:i+1+hold]...)
	b.Setup = SetupFrom(bars, i, crypto)
	return b, bars[i].Date, true
}

func round4(x float64) float64 { return math.Round(x*10000) / 10000 }

// ErrPractice is a practice request that doesn't fit.
var ErrPractice = errors.New("that round can't be decided now")

func (s *Service) feeFor(ctx context.Context, uid int64, sym string) float64 {
	if s.Fees != nil {
		return s.Fees(ctx, uid, "asset", sym)
	}
	return 0.001
}

// StartPractice prepares a new set of rounds (or returns the unfinished one).
func (s *Service) StartPractice(ctx context.Context, uid int64) (*PracticeState, error) {
	rows, err := s.Store.PracticeRounds(ctx, uid)
	if err != nil {
		return nil, err
	}
	set := 0
	for _, r := range rows {
		set = max(set, r.Set)
		if r.Call == "" {
			return s.Practice(ctx, uid) // finish this one first
		}
	}
	if s.Bars == nil {
		return nil, errors.New("practice rounds need price history, which isn't available")
	}
	set++
	pool := []string{}
	for sym := range PracticePool {
		pool = append(pool, sym)
	}
	if w, err := s.Store.WatchAdded(ctx, uid); err == nil {
		for sym := range w {
			if _, ok := PracticePool[sym]; !ok && !token.LooksLikeAddress(sym) {
				pool = append(pool, sym)
			}
		}
	}
	rng := s.rng()
	rng.Shuffle(len(pool), func(i, j int) { pool[i], pool[j] = pool[j], pool[i] })
	// Load histories a few at a time until there are enough usable names.
	type got struct {
		sym string
		ser *market.Series
	}
	var picked []got
	for start := 0; start < len(pool) && len(picked) < RoundsPerSet; start += 6 {
		batch := pool[start:min(start+6, len(pool))]
		res := make([]got, len(batch))
		done := make(chan struct{}, len(batch))
		for k, sym := range batch {
			go func(k int, sym string) {
				defer func() { done <- struct{}{} }()
				if ser, err := s.Bars(ctx, sym); err == nil && ser != nil {
					res[k] = got{sym, ser}
				}
			}(k, sym)
		}
		for range batch {
			<-done
		}
		for _, g := range res {
			if g.ser != nil && len(g.ser.Bars) > minHistory+40 && len(picked) < RoundsPerSet {
				picked = append(picked, g)
			}
		}
	}
	if len(picked) == 0 {
		return nil, errors.New("couldn't load any price history for practice rounds; try again in a minute")
	}
	n := 0
	for k := 0; n < RoundsPerSet && k < RoundsPerSet*3; k++ {
		g := picked[k%len(picked)]
		b, day, ok := prepRound(rng, g.sym, g.ser, s.feeFor(ctx, uid, g.sym))
		if !ok {
			continue
		}
		body, _ := json.Marshal(b)
		n++
		row := store.PracticeRound{Set: set, N: n, Symbol: g.sym, Day: day, Body: body}
		if err := s.Store.AddPracticeRound(ctx, uid, &row); err != nil {
			return nil, err
		}
	}
	s.prepareViews(uid)
	return s.Practice(ctx, uid)
}

func (s *Service) rng() *rand.Rand {
	if s.Seed != 0 {
		return rand.New(rand.NewSource(s.Seed))
	}
	return rand.New(rand.NewSource(time.Now().UnixNano()))
}

// prepareViews works out what the app said on each undecided day, one at a
// time and in order, in the background (each is a full past-day analysis).
func (s *Service) prepareViews(uid int64) {
	if _, busy := s.working.LoadOrStore(uid, true); busy {
		return
	}
	run := func() {
		defer s.working.Delete(uid)
		base := s.BaseCtx
		if base == nil {
			base = context.Background()
		}
		for tries := 0; tries < RoundsPerSet*2; tries++ {
			rows, err := s.Store.PracticeRounds(base, uid)
			if err != nil {
				return
			}
			var next *store.PracticeRound
			for i := range rows {
				if rows[i].App == nil {
					next = &rows[i]
					break
				}
			}
			if next == nil || base.Err() != nil {
				return
			}
			a := AppSaid{Text: "The app couldn't work out its view of that day."}
			if s.PastView != nil {
				ctx, cancel := context.WithTimeout(base, 5*time.Minute)
				if v, err := s.PastView(ctx, next.Symbol, next.Day); err == nil {
					a = v
				}
				cancel()
			}
			body, _ := json.Marshal(a)
			if err := s.Store.SetPracticeApp(base, uid, next.ID, body); err != nil && !errors.Is(err, store.ErrNotFound) {
				return
			}
		}
	}
	if s.Sync {
		run()
		return
	}
	go run()
}

func decode(r store.PracticeRound) (roundBody, *AppSaid) {
	var b roundBody
	_ = json.Unmarshal(r.Body, &b)
	if r.App == nil {
		return b, nil
	}
	var a AppSaid
	_ = json.Unmarshal(r.App, &a)
	return b, &a
}

func money0(x float64) string {
	s := fmt.Sprintf("$%.2f", math.Abs(x))
	if x < 0 {
		return "−" + s
	}
	return "+" + s
}

func view(r store.PracticeRound) Round {
	b, app := decode(r)
	v := Round{ID: r.ID, Set: r.Set, N: r.N, Of: RoundsPerSet, Hint: b.Hint, Before: b.Before, Hold: b.Hold, App: app, Working: app == nil, Decided: r.Call != ""}
	if !v.Decided {
		return v
	}
	v.Call, v.StopPct, v.TargetPct, v.Symbol, v.Name, v.Day, v.Entry = r.Call, r.StopPct, r.TargetPct, r.Symbol, b.Name, r.Day, b.Entry
	v.Result, v.ExitBar, v.PnL = r.Result, r.ExitBar, r.PnL
	for _, a := range b.After {
		v.After = append(v.After, round4(a.Close/b.Entry))
	}
	_, _, v.ExitPrice = Outcome(b.Entry, b.After, r.StopPct, r.TargetPct)
	whose := "the"
	if r.Call == "buy" {
		whose = "your"
	}
	what := fmt.Sprintf("%s %s was hit on day %d", whose, r.Result, r.ExitBar)
	if r.Result == "time" {
		what = fmt.Sprintf("the month ran out with neither %s stop nor %s target hit", whose, whose)
	}
	if r.Call == "buy" {
		v.Headline = fmt.Sprintf("%s: about %s on $1,000, after fees.", capFirst(what), money0(r.PnL))
	} else {
		v.Headline = fmt.Sprintf("You skipped. Had you bought, %s: about %s on $1,000.", what, money0(r.PnL))
	}
	if app != nil {
		v.AppLine = appLine(*app, b)
	}
	return v
}

// appLine says how the app's lean that day compares with what happened.
func appLine(a AppSaid, b roundBody) string {
	if !a.Available {
		return a.Text
	}
	later := 0.0
	if n := len(b.After); n > 0 {
		later = b.After[n-1].Close/b.Entry - 1
	}
	move := fmt.Sprintf("A month later the price was %.0f%% higher.", later*100)
	switch {
	case math.Abs(later) < 0.005:
		move = "A month later the price was about the same."
	case later < 0:
		move = fmt.Sprintf("A month later the price was %.0f%% lower.", -later*100)
	}
	if a.Lean == "" {
		return fmt.Sprintf("No buy or sell call, %s. %s", statusWords[a.Status], move)
	}
	right := " One wrong call proves little either way."
	if (a.Lean == "up") == (later > 0) {
		right = " One right call proves little either way."
	}
	return fmt.Sprintf("A %s call, with %s. %s%s", map[string]string{"up": "buy", "down": "sell"}[a.Lean], statusWords[a.Status], move, right)
}

// Practice returns the practice page's data.
func (s *Service) Practice(ctx context.Context, uid int64) (*PracticeState, error) {
	rows, err := s.Store.PracticeRounds(ctx, uid)
	if err != nil {
		return nil, err
	}
	st := &PracticeState{Rounds: []Round{}, Stops: StopChoices, Targets: TargetChoices, Stake: Stake}
	working := false
	for _, r := range rows {
		st.Set = max(st.Set, r.Set)
		if r.Call != "" {
			st.Done++
			if r.Call == "buy" {
				st.Bought++
			}
		}
		working = working || r.App == nil
	}
	if working {
		s.prepareViews(uid) // picks up after a restart
	}
	var cur []store.PracticeRound
	for _, r := range rows {
		if r.Set == st.Set {
			cur = append(cur, r)
		}
	}
	for _, r := range cur {
		if r.Call == "" {
			if st.Current == nil {
				v := view(r)
				st.Current = &v
				st.Rounds = append(st.Rounds, v)
			}
			continue
		}
		st.Rounds = append(st.Rounds, view(r))
	}
	if st.Set > 0 && st.Current == nil {
		st.Finished = true
		st.Summary = summarise(cur)
	}
	return st, nil
}

// DecideRound locks in a call and returns the round with its answer.
func (s *Service) DecideRound(ctx context.Context, uid, id int64, call string, stop, target float64) (*Round, error) {
	if call != "buy" && call != "skip" {
		return nil, fmt.Errorf("call must be buy or skip")
	}
	if stop < 0.01 || stop > 0.5 || target < 0.01 || target > 2 {
		return nil, fmt.Errorf("stop must be 1–50%% and target 1–200%%")
	}
	rows, err := s.Store.PracticeRounds(ctx, uid)
	if err != nil {
		return nil, err
	}
	var r *store.PracticeRound
	for i := range rows {
		if rows[i].Call == "" {
			if rows[i].ID == id {
				r = &rows[i]
			}
			break // only the first waiting round can be decided
		}
	}
	if r == nil {
		return nil, ErrPractice
	}
	b, _ := decode(*r)
	res, bar, exit := Outcome(b.Entry, b.After, stop, target)
	r.Call, r.StopPct, r.TargetPct, r.Result, r.ExitBar = call, stop, target, res, bar
	r.PnL = PnL(b.Entry, exit, b.Fee)
	r.PnLPct = r.PnL / Stake
	if err := s.Store.DecidePracticeRound(ctx, uid, r); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, ErrPractice
		}
		return nil, err
	}
	v := view(*r)
	return &v, nil
}

// ClearPractice deletes all practice rounds.
func (s *Service) ClearPractice(ctx context.Context, uid int64) error {
	return s.Store.DeletePracticeRounds(ctx, uid, false)
}

// practiceSetup is a decided round's setup, with what the app said and the plan.
func practiceSetup(r store.PracticeRound) Setup {
	b, app := decode(r)
	st := b.Setup
	st.StopPct = r.StopPct
	if app != nil && app.Available {
		st.AppKnown, st.AppLean, st.AppTested = true, app.Lean, app.Tested
	}
	return st
}

func summarise(rows []store.PracticeRound) *SetSummary {
	sm := &SetSummary{Rounds: len(rows), Lines: []string{}}
	var skipped, skippedWins, withApp, withAppWins, against, againstWins int
	var buys []store.PracticeRound
	for _, r := range rows {
		if r.Call != "buy" {
			skipped++
			if r.PnL > 0 {
				skippedWins++
			}
			continue
		}
		buys = append(buys, r)
		sm.Bought++
		sm.PnL += r.PnL
		switch r.Result {
		case "target":
			sm.Target++
		case "stop":
			sm.Stop++
		default:
			sm.Time++
		}
		if _, app := decode(r); app != nil && app.Available && app.Lean != "" {
			if app.Lean == "up" {
				withApp++
				if r.PnL > 0 {
					withAppWins++
				}
			} else {
				against++
				if r.PnL > 0 {
					againstWins++
				}
			}
		}
	}
	sm.Headline = fmt.Sprintf("You bought %d of %d days.", sm.Bought, sm.Rounds)
	if sm.Bought > 0 {
		sm.Headline = fmt.Sprintf("You bought %d of %d days. %s hit the target, %s the stop.", sm.Bought, sm.Rounds, count(sm.Target), count(sm.Stop))
	}
	switch {
	case skipped == 1:
		sm.Lines = append(sm.Lines, fmt.Sprintf("The day you skipped %s have made money with your plan.", map[bool]string{true: "would", false: "wouldn't"}[skippedWins == 1]))
	case skipped > 1:
		sm.Lines = append(sm.Lines, fmt.Sprintf("Of the %d days you skipped, %d would have made money with your plan.", skipped, skippedWins))
	}
	side := func(n, wins int, call string) string {
		if n == 1 {
			return fmt.Sprintf("Your one buy on a day the app said %s %s money.", call, map[bool]string{true: "made", false: "didn't make"}[wins == 1])
		}
		return fmt.Sprintf("On days the app said %s, %d of your %d buys made money.", call, wins, n)
	}
	switch {
	case withApp > 0 && against > 0:
		sm.Lines = append(sm.Lines, side(withApp, withAppWins, "buy")+" "+side(against, againstWins, "sell"))
	case withApp > 0:
		sm.Lines = append(sm.Lines, side(withApp, withAppWins, "buy"))
	case against > 0:
		sm.Lines = append(sm.Lines, side(against, againstWins, "sell"))
	}
	sm.Worth = sharedByLosers(buys)
	return sm
}

func count(n int) string {
	if n == 0 {
		return "None"
	}
	return fmt.Sprint(n)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// sharedByLosers looks for a setup every losing buy had and most winning
// buys didn't. With one set of rounds it's a hint to watch, never a habit.
func sharedByLosers(buys []store.PracticeRound) *Reminder {
	var losers, winners []map[string]bool
	for _, r := range buys {
		a := practiceSetup(r).Answers()
		if r.PnL > 0 {
			winners = append(winners, a)
		} else {
			losers = append(losers, a)
		}
	}
	if len(losers) < 2 || len(winners) == 0 {
		return nil
	}
	for _, c := range Conds {
		all := true
		for _, l := range losers {
			all = all && l[c.Key]
		}
		if !all {
			continue
		}
		with := 0
		for _, w := range winners {
			if w[c.Key] {
				with++
			}
		}
		if with*2 >= len(winners) {
			continue
		}
		who := fmt.Sprintf("All %d losing buys", len(losers))
		if len(losers) == 2 {
			who = "Both losing buys"
		}
		return &Reminder{Key: c.Key, Title: fmt.Sprintf("%s came %s.", who, c.Short),
			Count:  fmt.Sprintf("%d of %d", len(losers), len(losers)),
			Detail: fmt.Sprintf("%d of your %d winning buys did. Too few to call a habit; it's checked again as rounds add up.", with, len(winners))}
	}
	return nil
}

// PracticeExport lists every practice round with its answer, for downloads.
func (s *Service) PracticeExport(ctx context.Context, uid int64) ([]Round, error) {
	rows, err := s.Store.PracticeRounds(ctx, uid)
	if err != nil {
		return nil, err
	}
	out := []Round{}
	for _, r := range rows {
		if r.Call != "" {
			out = append(out, view(r))
		}
	}
	return out, nil
}
