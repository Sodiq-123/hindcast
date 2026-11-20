package learn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/sodiq/financial-market/backend/internal/journal"
	"github.com/sodiq/financial-market/backend/internal/market"
	"github.com/sodiq/financial-market/backend/internal/store"
	"github.com/sodiq/financial-market/backend/internal/token"
)

// Model thresholds: an early read (◐) from EarlyTrades closed trades, a full
// test (which it must pass to earn ✓) from FullTrades.
const (
	EarlyTrades = 10
	FullTrades  = 30
)

type Service struct {
	Store *store.Store
	Now   func() time.Time // for tests; time.Now when nil

	// For practice rounds and the personal model (any may be nil in tests).
	Bars     func(ctx context.Context, symbol string) (*market.Series, error)  // daily history
	PastView func(ctx context.Context, symbol, day string) (AppSaid, error)    // the app's analysis as of a past day
	Fees     func(ctx context.Context, uid int64, kind, symbol string) float64 // fee per side
	BaseCtx  context.Context                                                   // stops background work on shutdown
	Seed     int64                                                             // fixed practice days (tests); random when 0
	Sync     bool                                                              // work out the app's views before returning (tests)

	working sync.Map // users whose practice days are being analysed
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func dayStart(t time.Time) time.Time {
	y, m, d := t.Local().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.Local)
}

// Shown is a suggestion as the pages get it.
type Shown struct {
	ID      int64      `json:"id"`
	Kind    string     `json:"kind"`
	Subject string     `json:"subject"`
	Card    Card       `json:"card"`
	Answer  string     `json:"answer"`
	ShownAt time.Time  `json:"shownAt"`
	Snooze  *time.Time `json:"snoozeUntil,omitempty"`
}

func shown(r store.SuggestionRow) Shown {
	var c Card
	_ = json.Unmarshal(r.Body, &c)
	return Shown{ID: r.ID, Kind: r.Kind, Subject: r.Subject, Card: c, Answer: r.Answer, ShownAt: r.ShownAt, Snooze: r.SnoozeUntil}
}

func past(rows []store.SuggestionRow, skip int64) []Past {
	out := make([]Past, 0, len(rows))
	for _, r := range rows {
		if r.ID != skip {
			out = append(out, Past{Kind: r.Kind, Subject: r.Subject, Answer: r.Answer, ShownAt: r.ShownAt, SnoozeUntil: r.SnoozeUntil})
		}
	}
	return out
}

// state gathers what the rules look at.
// displayName is an asset's name without the quote currency Yahoo adds to
// crypto ("Solana USD" → "Solana"), or the symbol when it was never analysed.
func (s *Service) displayName(ctx context.Context, sym string) string {
	return strings.TrimSuffix(s.Store.AssetName(ctx, sym), " USD")
}

func (s *Service) state(ctx context.Context, uid int64, rows []store.SuggestionRow, skip int64) (State, error) {
	now := s.now()
	st := State{Now: now, History: past(rows, skip), Names: map[string]string{}}
	evs, err := s.Store.Events(ctx, uid, now.AddDate(0, 0, -90))
	if err != nil {
		return st, err
	}
	for _, e := range evs {
		st.Events = append(st.Events, Event{e.Kind, e.Subject, e.At})
		if e.Kind == "visit" {
			if _, ok := st.Names[e.Subject]; !ok {
				st.Names[e.Subject] = s.displayName(ctx, e.Subject)
			}
		}
	}
	if st.Since, err = s.Store.FirstEvent(ctx, uid); err != nil {
		return st, err
	}
	if st.Watch, err = s.Store.WatchAdded(ctx, uid); err != nil {
		return st, err
	}
	for sym := range st.Watch {
		if _, ok := st.Names[sym]; !ok {
			st.Names[sym] = s.displayName(ctx, sym)
		}
	}
	p, err := s.Store.Profile(ctx, uid)
	if err != nil {
		return st, err
	}
	set := p.Settings()
	st.Horizon, st.Detail, st.Level, st.LevelChose = set.Horizon, set.Detail, set.Level, p.Wording != ""
	if p.Quiz != nil {
		st.QuizAt = &p.Quiz.TakenAt
	}
	return st, nil
}

func (s *Service) picks(ctx context.Context, uid int64, rows []store.SuggestionRow) ([]Pick, error) {
	others, err := s.Store.AcceptanceByKind(ctx, uid)
	if err != nil {
		return nil, err
	}
	return Picks(others, past(rows, 0)), nil
}

// Current returns today's suggestion, if there is one. At most one new
// suggestion is made a day, and none while learning is paused.
func (s *Service) Current(ctx context.Context, uid int64) (*Shown, error) {
	prefs, err := s.Store.LearnPrefs(ctx, uid)
	if err != nil || prefs.Paused {
		return nil, err
	}
	rows, err := s.Store.Suggestions(ctx, uid)
	if err != nil {
		return nil, err
	}
	today := dayStart(s.now())
	for _, r := range rows {
		if r.Kind == KindReminder || r.Answer == "expired" {
			continue
		}
		if r.Answer == "" {
			// Still waiting: show it again if it still applies.
			st, err := s.state(ctx, uid, rows, r.ID)
			if err != nil {
				return nil, err
			}
			for _, c := range Candidates(st) {
				if c.Kind == r.Kind && c.Subject == r.Subject {
					sh := shown(r)
					return &sh, nil
				}
			}
			if err := s.Store.AnswerSuggestion(ctx, uid, r.ID, "expired", "", nil); err != nil {
				return nil, err
			}
			continue
		}
		if !r.ShownAt.Before(today) {
			return nil, nil // one a day: today's has been answered
		}
	}
	st, err := s.state(ctx, uid, rows, 0)
	if err != nil {
		return nil, err
	}
	picks, err := s.picks(ctx, uid, rows)
	if err != nil {
		return nil, err
	}
	ranked := Rank(Candidates(st), picks, st.History, st.Now)
	if len(ranked) == 0 {
		return nil, nil
	}
	c := ranked[0]
	body, _ := json.Marshal(c.Card)
	row := store.SuggestionRow{Kind: c.Kind, Subject: c.Subject, Body: body}
	if err := s.Store.AddSuggestion(ctx, uid, &row); err != nil {
		return nil, err
	}
	sh := shown(row)
	return &sh, nil
}

// ErrAnswer is a request that doesn't fit the suggestion's state.
var ErrAnswer = errors.New("that answer doesn't apply to this suggestion now")

// Answer handles yes, no, undo (after a yes) and again (after a no).
func (s *Service) Answer(ctx context.Context, uid, id int64, answer string) (*Shown, error) {
	r, err := s.Store.Suggestion(ctx, uid, id)
	if err != nil {
		return nil, err
	}
	snooze := s.now().Add(Snooze(r.Kind))
	switch {
	case answer == "yes" && r.Answer == "":
		prev, err := s.apply(ctx, uid, r.Kind, r.Subject, false, "")
		if err != nil {
			return nil, err
		}
		err = s.Store.AnswerSuggestion(ctx, uid, id, "yes", prev, nil)
		if err != nil {
			return nil, err
		}
	case answer == "no" && r.Answer == "":
		if err := s.Store.AnswerSuggestion(ctx, uid, id, "no", "", &snooze); err != nil {
			return nil, err
		}
	case answer == "undo" && r.Answer == "yes":
		if _, err := s.apply(ctx, uid, r.Kind, r.Subject, true, r.Prev); err != nil {
			return nil, err
		}
		if err := s.Store.AnswerSuggestion(ctx, uid, id, "undone", "", &snooze); err != nil {
			return nil, err
		}
	case answer == "again" && (r.Answer == "no" || r.Answer == "undone"):
		if err := s.Store.AnswerSuggestion(ctx, uid, id, r.Answer, "", nil); err != nil {
			return nil, err
		}
	default:
		return nil, ErrAnswer
	}
	r, err = s.Store.Suggestion(ctx, uid, id)
	if err != nil {
		return nil, err
	}
	sh := shown(*r)
	return &sh, nil
}

// apply makes the change a suggestion offers, or (reverse) puts back what
// was there before (undo). It returns the previous setting.
func (s *Service) apply(ctx context.Context, uid int64, kind, subject string, reverse bool, undo string) (string, error) {
	switch kind {
	case KindAdd, KindRemove:
		add := (kind == KindAdd) != reverse
		if add {
			return "", s.Store.AddWatch(ctx, uid, subject)
		}
		return "", s.Store.RemoveWatch(ctx, uid, subject)
	case KindHorizon, KindDetail, KindLevel:
		p, err := s.Store.Profile(ctx, uid)
		if err != nil {
			return "", err
		}
		field := &p.Horizon
		switch kind {
		case KindDetail:
			field = &p.Detail
		case KindLevel:
			field = &p.Wording
		}
		prev := *field
		if prev == "" {
			prev = "-" // nothing chosen yet; "" means "no undo value" in the store
		}
		next := subject
		if reverse {
			next = strings.TrimPrefix(undo, "-")
		}
		*field = next
		if err := p.Validate(); err != nil {
			return "", err
		}
		return prev, s.Store.SaveProfile(ctx, uid, p)
	}
	return "", fmt.Errorf("unknown suggestion kind %q", kind)
}

// Record stores something the person did, unless learning is paused.
func (s *Service) Record(ctx context.Context, uid int64, kind, subject string) error {
	if !EventKinds[kind] {
		return fmt.Errorf("unknown event %q", kind)
	}
	subject = strings.TrimSpace(subject)
	switch kind {
	case "view":
		if _, ok := Views[subject]; !ok && subject != "all" {
			return fmt.Errorf("unknown view %q", subject)
		}
	case "page":
		if _, ok := Pages[subject]; !ok {
			return fmt.Errorf("unknown page %q", subject)
		}
	default:
		subject = strings.ToUpper(subject)
	}
	if subject == "" || len(subject) > 64 {
		return errors.New("subject required (64 characters at most)")
	}
	prefs, err := s.Store.LearnPrefs(ctx, uid)
	if err != nil || prefs.Paused {
		return err
	}
	_, err = s.Store.AddEvent(ctx, uid, kind, subject, s.now())
	return err
}

// ------------------------------------------------------------ About you

type Asked struct {
	Shown
	Label string `json:"label"`
}

type Overview struct {
	Paused  bool       `json:"paused"`
	Muted   []string   `json:"muted"`
	Since   *time.Time `json:"since,omitempty"`
	Events  int        `json:"events"`
	Asked   []Asked    `json:"asked"`
	Noticed []Noticed  `json:"noticed"`
	Picks   []Pick     `json:"picks"`
	Habits  Habits     `json:"habits"`
	Model   Read       `json:"model"`
}

func (s *Service) Overview(ctx context.Context, uid int64) (*Overview, error) {
	prefs, err := s.Store.LearnPrefs(ctx, uid)
	if err != nil {
		return nil, err
	}
	rows, err := s.Store.Suggestions(ctx, uid)
	if err != nil {
		return nil, err
	}
	st, err := s.state(ctx, uid, rows, 0)
	if err != nil {
		return nil, err
	}
	picks, err := s.picks(ctx, uid, rows)
	if err != nil {
		return nil, err
	}
	h, err := s.Habits(ctx, uid)
	if err != nil {
		return nil, err
	}
	ss, err := s.Samples(ctx, uid)
	if err != nil {
		return nil, err
	}
	o := &Overview{Paused: prefs.Paused, Muted: prefs.Muted, Since: st.Since, Events: len(st.Events), Asked: []Asked{},
		Noticed: Notice(st), Picks: picks, Habits: h, Model: Model(ss)}
	for _, r := range rows {
		if r.Answer == "expired" {
			continue
		}
		o.Asked = append(o.Asked, Asked{shown(r), KindLabels[r.Kind]})
		if len(o.Asked) == 40 {
			break
		}
	}
	return o, nil
}

// Habits works out the journal facts and patterns.
func (s *Service) Habits(ctx context.Context, uid int64) (Habits, error) {
	ps, err := s.Store.Positions(ctx, uid)
	if err != nil {
		return Habits{}, err
	}
	var ts []Trade
	for _, r := range journal.ClosedRows(ps) {
		name := r.Symbol
		if r.Kind == "token" && r.Name != "" {
			name = r.Name
		}
		ts = append(ts, Trade{Name: name, Kind: r.Kind, Symbol: r.Symbol, Stop: r.Stop, Exit: *r.Exit, PnL: r.PnL, PnLPct: r.PnLPct, Days: r.Days, Edge: r.Edge})
	}
	return HabitsFrom(ts), nil
}

// Samples gathers the personal model's trades: closed paper trades and
// practice buys, each with its setup.
func (s *Service) Samples(ctx context.Context, uid int64) ([]Sample, error) {
	ps, err := s.Store.Positions(ctx, uid)
	if err != nil {
		return nil, err
	}
	var out []Sample
	series := map[string]*market.Series{}
	for _, r := range journal.ClosedRows(ps) {
		st := Setup{Class: Class(r.Kind, r.Symbol)}
		if r.Kind != "token" && s.Bars != nil {
			ser, ok := series[r.Symbol]
			if !ok {
				bctx, cancel := context.WithTimeout(ctx, 20*time.Second)
				ser, _ = s.Bars(bctx, r.Symbol)
				cancel()
				series[r.Symbol] = ser
			}
			if ser != nil {
				day := r.OpenedAt
				if len(day) > 10 {
					day = day[:10]
				}
				full := SetupFrom(ser.Bars, BarIndex(ser.Bars, day), st.Class == "crypto")
				full.Class = st.Class
				st = full
			}
		}
		if r.Stop != nil && *r.Stop > 0 && *r.Stop < r.Entry {
			st.StopPct = 1 - *r.Stop/r.Entry
		}
		if r.Signal != "" || r.Edge != "" {
			st.AppKnown, st.AppLean, st.AppTested = true, LeanOf(r.Signal), r.Edge == "tested"
		}
		at, _ := time.Parse("2006-01-02", r.OpenedAt[:min(10, len(r.OpenedAt))])
		out = append(out, Sample{At: at, Win: r.PnL > 0, Answers: st.Answers()})
	}
	rounds, err := s.Store.PracticeRounds(ctx, uid)
	if err != nil {
		return nil, err
	}
	for _, r := range rounds {
		if r.Call != "buy" || r.DecidedAt == nil {
			continue
		}
		out = append(out, Sample{At: *r.DecidedAt, Practice: true, Win: r.PnL > 0, Answers: practiceSetup(r).Answers()})
	}
	return out, nil
}

// SetPrefs pauses or resumes learning and sets which trade classes have
// reminders muted.
func (s *Service) SetPrefs(ctx context.Context, uid int64, p store.LearnPrefs) error {
	clean := []string{}
	for _, m := range p.Muted {
		if _, ok := ClassLabel[m]; ok && !slices.Contains(clean, m) {
			clean = append(clean, m)
		}
	}
	p.Muted = clean
	return s.Store.SaveLearnPrefs(ctx, uid, p)
}

// Forget deletes what was learned: one kind of noticing ("view", "detail",
// "visit") or everything ("all": events, suggestions and muted reminders).
// Trades stay; the habits come from the journal itself.
func (s *Service) Forget(ctx context.Context, uid int64, key string) error {
	switch key {
	case "view", "detail", "visit", "page":
		return s.Store.ForgetEvents(ctx, uid, key)
	case "practice":
		return s.ClearPractice(ctx, uid)
	case "all":
		if err := s.Store.ForgetEvents(ctx, uid); err != nil {
			return err
		}
		if err := s.Store.ForgetSuggestions(ctx, uid); err != nil {
			return err
		}
		p, err := s.Store.LearnPrefs(ctx, uid)
		if err != nil {
			return err
		}
		p.Muted = []string{}
		return s.Store.SaveLearnPrefs(ctx, uid, p)
	}
	return fmt.Errorf("unknown thing to forget %q", key)
}

// ------------------------------------------------------------ reminders

type ReminderSet struct {
	Class      string     `json:"class"`
	ClassLabel string     `json:"classLabel"`
	Muted      bool       `json:"muted"`
	Items      []Reminder `json:"items"`
	GapPct     float64    `json:"gapPct"`
	GapName    string     `json:"gapName,omitempty"`
}

// ClassOf works out a symbol's class for reminders.
func ClassOf(symbol string) string {
	if token.LooksLikeAddress(symbol) {
		return "token"
	}
	return Class("asset", strings.ToUpper(symbol))
}

// Reminders returns what's worth a look before a trade in this symbol.
func (s *Service) Reminders(ctx context.Context, uid int64, symbol string) (*ReminderSet, error) {
	prefs, err := s.Store.LearnPrefs(ctx, uid)
	if err != nil {
		return nil, err
	}
	h, err := s.Habits(ctx, uid)
	if err != nil {
		return nil, err
	}
	c := ClassOf(symbol)
	set := &ReminderSet{Class: c, ClassLabel: ClassLabel[c], Muted: Muted(prefs.Muted, c), Items: []Reminder{}, GapPct: h.GapPct, GapName: h.GapName}
	if !set.Muted {
		set.Items = Reminders(h, c)
	}
	return set, nil
}

// AnswerReminder records "Noted" or "Not on … trades" (which mutes the class).
func (s *Service) AnswerReminder(ctx context.Context, uid int64, class, answer, title string) error {
	if _, ok := ClassLabel[class]; !ok {
		return fmt.Errorf("unknown class %q", class)
	}
	if answer != "noted" && answer != "muted" {
		return ErrAnswer
	}
	title = strings.TrimSpace(title)
	if len(title) > 300 {
		title = title[:300]
	}
	body, _ := json.Marshal(Card{Say: "Worth a look: " + title})
	t := s.now()
	row := store.SuggestionRow{Kind: KindReminder, Subject: class, Body: body, Answer: answer, ShownAt: t, AnsweredAt: &t}
	if err := s.Store.AddSuggestion(ctx, uid, &row); err != nil {
		return err
	}
	if answer == "muted" {
		p, err := s.Store.LearnPrefs(ctx, uid)
		if err != nil {
			return err
		}
		if !slices.Contains(p.Muted, class) {
			p.Muted = append(p.Muted, class)
		}
		return s.Store.SaveLearnPrefs(ctx, uid, p)
	}
	return nil
}

// Edge works out whether a new trade's signal was a tested edge, from the
// asset's latest analysis (at most 4 days old). Signals look like
// "1 month: BUY"; without one, any tested buy signal counts.
func (s *Service) Edge(ctx context.Context, kind, symbol, signal string) string {
	if kind == "token" {
		return "untested"
	}
	rep, err := s.Store.LatestReport(ctx, symbol)
	if err != nil || rep == nil || s.now().Sub(rep.GeneratedAt) > 4*24*time.Hour {
		return ""
	}
	label, _, hasLabel := strings.Cut(signal, ":")
	for _, h := range rep.Horizons {
		if hasLabel && !strings.EqualFold(strings.TrimSpace(label), h.Label) {
			continue
		}
		if h.Available && h.Status == "validated" && (hasLabel || strings.Contains(h.Signal, "BUY")) {
			return "tested"
		}
	}
	return "untested"
}
