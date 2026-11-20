// Package profile is what a user tells the app about themselves in the setup
// quiz, and what the app does with it. Personalisation changes what is shown
// and how it is explained, never a forecast, a validation verdict or a
// warning's facts.
package profile

import (
	"fmt"
	"slices"
	"time"
)

type Profile struct {
	Experience   string    `json:"experience"`   // new | some | experienced
	Goals        []string  `json:"goals"`        // grow | income | trade | learn | protect
	Horizon      string    `json:"horizon"`      // 1d | 1w | 1m | 1y
	Interests    []string  `json:"interests"`    // stocks | funds | crypto | meme
	DropReaction string    `json:"dropReaction"` // sell_all | sell_some | hold | buy_more
	Detail       string    `json:"detail"`       // "" (decided from the quiz) | simple | full
	Wording      string    `json:"wording"`      // "" (decided from the answers) | beginner | intermediate | advanced
	Quiz         *Quiz     `json:"quiz,omitempty"`
	Status       string    `json:"status"` // "" (not started) | skipped | done
	UpdatedAt    time.Time `json:"updatedAt"`
}

// Quiz is the knowledge check: the option picked per question and the score.
type Quiz struct {
	Answers map[string]int `json:"answers"`
	Score   int            `json:"score"`
	Total   int            `json:"total"`
	TakenAt time.Time      `json:"takenAt"`
}

var (
	Experiences   = []string{"new", "some", "experienced"}
	Goals         = []string{"grow", "income", "trade", "learn", "protect"}
	Horizons      = []string{"1d", "1w", "1m", "1y"}
	Interests     = []string{"stocks", "funds", "crypto", "meme"}
	DropReactions = []string{"sell_all", "sell_some", "hold", "buy_more"}
	Details       = []string{"", "simple", "full"}
	Levels        = []string{"beginner", "intermediate", "advanced"}
)

// Validate checks every answer against the allowed values.
func (p *Profile) Validate() error {
	check := func(field, v string, allowed []string, optional bool) error {
		if (v == "" && optional) || slices.Contains(allowed, v) {
			return nil
		}
		return fmt.Errorf("%s: %q is not one of %v", field, v, allowed)
	}
	for _, c := range []error{
		check("experience", p.Experience, Experiences, true),
		check("horizon", p.Horizon, Horizons, true),
		check("dropReaction", p.DropReaction, DropReactions, true),
		check("detail", p.Detail, Details, true),
		check("wording", p.Wording, Levels, true),
		check("status", p.Status, []string{"", "skipped", "done"}, true),
	} {
		if c != nil {
			return c
		}
	}
	for _, g := range p.Goals {
		if err := check("goals", g, Goals, false); err != nil {
			return err
		}
	}
	for _, i := range p.Interests {
		if err := check("interests", i, Interests, false); err != nil {
			return err
		}
	}
	p.Goals, p.Interests = dedupe(p.Goals), dedupe(p.Interests)
	return nil
}

func dedupe(xs []string) []string {
	out := []string{}
	for _, x := range xs {
		if !slices.Contains(out, x) {
			out = append(out, x)
		}
	}
	return out
}

// ------------------------------------------------------------- the quiz

type Question struct {
	ID      string   `json:"id"`
	Text    string   `json:"text"`
	Options []string `json:"options"`
	correct int
	Explain string `json:"explain"` // shown after answering
}

// Questions test the ideas the app's pages lean on. Answers are graded on the
// server; the correct option is never sent before the quiz is submitted.
var Questions = []Question{
	{ID: "pe", Text: "A company's shares trade at a P/E (price-to-earnings) ratio of 30. What does that mean?",
		Options: []string{"The share price rose 30% this year", "Buyers pay $30 for every $1 of the company's yearly profit", "The company keeps 30% of its sales as profit"},
		correct: 1, Explain: "P/E is price ÷ yearly profit per share. A higher P/E means investors are paying more for each dollar the company earns."},
	{ID: "base_rate", Text: "A forecast says a stock has a 60% chance of being higher in a month. Historically it was higher in 58% of months anyway. The forecast is…",
		Options: []string{"A strong reason to buy", "Barely better than what usually happens", "A guarantee it will rise"},
		correct: 1, Explain: "Compare every probability with the usual rate. 60% against a normal 58% adds very little. That's why the app shows the base rate next to each forecast."},
	{ID: "diversify", Text: "Which is riskier: all your money in one stock, or the same money spread over 20 different stocks?",
		Options: []string{"One stock", "They're equally risky", "20 stocks"},
		correct: 0, Explain: "One company can collapse on its own bad news; twenty rarely all do at once. Spreading out lowers risk without necessarily lowering the expected return."},
	{ID: "holders", Text: "A coin's 10 largest wallets hold 80% of all its tokens. What's the main risk?",
		Options: []string{"None, it means the coin is popular", "A few holders could crash the price by selling", "The coin can't be traded"},
		correct: 1, Explain: "When a few wallets own most of the supply, any one of them selling can crash the price. The token safety check flags this."},
	{ID: "sizing", Text: "Your account has $10,000 and you'll risk at most 1% on a trade. You buy at $100 with a stop-loss at $90. How much should you buy?",
		Options: []string{"$100 worth", "$1,000 worth (10 shares)", "$9,000 worth"},
		correct: 1, Explain: "1% of $10,000 is $100. Each share loses $10 if the stop is hit, so 10 shares ($1,000) risks exactly $100."},
}

// Grade scores the answers (question id → option index).
func Grade(answers map[string]int) (*Quiz, error) {
	q := &Quiz{Answers: map[string]int{}, Total: len(Questions), TakenAt: time.Now().UTC()}
	for _, qu := range Questions {
		a, ok := answers[qu.ID]
		if !ok {
			continue // unanswered counts as wrong
		}
		if a < 0 || a >= len(qu.Options) {
			return nil, fmt.Errorf("question %s: no option %d", qu.ID, a)
		}
		q.Answers[qu.ID] = a
		if a == qu.correct {
			q.Score++
		}
	}
	return q, nil
}

// Correct returns the right option for each question (for the results page).
func Correct() map[string]int {
	out := map[string]int{}
	for _, q := range Questions {
		out[q.ID] = q.correct
	}
	return out
}

// ---------------------------------------------------------- what it changes

// Settings are the profile turned into decisions the pages apply.
type Settings struct {
	Level      string   `json:"level"`      // beginner | intermediate | advanced
	Detail     string   `json:"detail"`     // simple | full
	Horizon    string   `json:"horizon"`    // default forecast horizon
	ShowTokens bool     `json:"showTokens"` // show the meme-coin sections
	Risk       string   `json:"risk"`       // low | medium | high comfort with falls
	Tips       []string `json:"tips"`
}

// Level is how things are explained: the person's own choice when they made
// one, otherwise what their answers show.
func (p *Profile) Level() string {
	if p.Wording != "" {
		return p.Wording
	}
	return p.fromAnswers()
}

// fromAnswers combines what the user says with what the quiz shows; the quiz
// wins when they disagree. Someone who said nothing is treated as a beginner.
func (p *Profile) fromAnswers() string {
	if p.Quiz != nil && p.Quiz.Total > 0 {
		switch s := p.Quiz.Score; {
		case s <= 2:
			return "beginner"
		case s <= 4:
			return "intermediate"
		}
		if p.Experience == "new" {
			return "intermediate"
		}
		return "advanced"
	}
	switch p.Experience {
	case "experienced":
		return "advanced"
	case "some":
		return "intermediate"
	}
	return "beginner"
}

// answered reports whether the level is based on anything the person said.
func (p *Profile) answered() bool { return p.Experience != "" || p.Quiz != nil }

func (p *Profile) Settings() Settings {
	s := Settings{Level: p.Level(), Detail: p.Detail, Horizon: p.Horizon, ShowTokens: true, Tips: []string{}}
	// Until someone shows they want more, assume a beginner: the simple view.
	// Answers that show some experience open everything; so can a yes to the
	// "full detail" suggestion, or a choice on About you. The wording choice
	// doesn't change this, so each setting changes only what it says.
	if s.Detail == "" {
		s.Detail = "simple"
		if p.answered() && p.fromAnswers() != "beginner" && p.Experience != "new" {
			s.Detail = "full"
		}
	}
	if s.Horizon == "" {
		s.Horizon = "1d"
	}
	if len(p.Interests) > 0 && !slices.Contains(p.Interests, "meme") {
		s.ShowTokens = false
	}
	switch p.DropReaction {
	case "sell_all":
		s.Risk = "low"
	case "sell_some":
		s.Risk = "medium"
	case "hold", "buy_more":
		s.Risk = "high"
	}
	s.Tips = p.tips(s)
	return s
}

func (p *Profile) tips(s Settings) []string {
	t := []string{} // never null in JSON
	has := func(xs []string, v string) bool { return slices.Contains(xs, v) }
	if has(p.Goals, "grow") || has(p.Goals, "protect") {
		t = append(t, "For long-term growth, the 1-year column and the \"Business vs price\" panel matter most. Day-to-day signals are mostly noise.")
	}
	if has(p.Goals, "trade") || p.Horizon == "1d" {
		t = append(t, "Short-term forecasts often show NO EDGE, meaning they haven't beaten the usual rate in testing. Practice in the Journal with fees switched on before risking real money.")
	}
	if has(p.Goals, "income") {
		t = append(t, "The app doesn't analyse dividends yet, so it can't rank income stocks for you.")
	}
	if has(p.Goals, "learn") || (p.answered() && s.Level == "beginner") {
		tip := "New here? \"How it works\" explains every number in plain English."
		if s.Detail == "simple" {
			tip += " Asset pages start with the simple view; open the technical detail when you want it."
		}
		t = append(t, tip)
	}
	if s.Risk == "low" && (has(p.Interests, "crypto") || has(p.Interests, "meme")) {
		t = append(t, "You said you'd sell everything after a 30% fall. Bitcoin has fallen that much within a month several times, and meme coins far more. Keep crypto small enough that a fall like that wouldn't make you sell.")
	}
	if has(p.Interests, "meme") {
		t = append(t, "Before buying any meme coin, paste its address into the search: the safety check flags copycats, concentrated holders and missing liquidity.")
	}
	return t
}

// Effect explains one answer and what the app does with it (the "About you"
// page lists them all, so nothing is used silently).
type Effect struct {
	Field  string `json:"field"`
	Answer string `json:"answer"`
	Effect string `json:"effect"`
}

var labels = map[string]string{
	"new": "New to investing", "some": "Some experience", "experienced": "Experienced",
	"grow": "Grow my savings over years", "income": "Regular income", "trade": "Short-term trading", "learn": "Learn how markets work", "protect": "Protect my money from inflation or a weak currency",
	"1d": "Days", "1w": "Weeks", "1m": "Months", "1y": "A year or more",
	"stocks": "Individual stocks", "funds": "Funds and ETFs", "crypto": "Major crypto (Bitcoin, Ethereum)", "meme": "Meme coins and DEX tokens",
	"sell_all": "Sell everything", "sell_some": "Sell some", "hold": "Hold and wait", "buy_more": "Buy more",
	"simple": "Simple", "full": "Full detail",
	"beginner": "Plain words", "intermediate": "Some terms", "advanced": "With the numbers",
}

func Label(v string) string {
	if l, ok := labels[v]; ok {
		return l
	}
	return v
}

func joinLabels(xs []string) string {
	if len(xs) == 0 {
		return "Not answered"
	}
	out := ""
	for i, x := range xs {
		if i > 0 {
			out += ", "
		}
		out += Label(x)
	}
	return out
}

func (p *Profile) Effects() []Effect {
	s := p.Settings()
	ans := func(v string) string {
		if v == "" {
			return "Not answered"
		}
		return Label(v)
	}
	quiz := "Not taken"
	if p.Quiz != nil {
		quiz = fmt.Sprintf("%d of %d correct", p.Quiz.Score, p.Quiz.Total)
	}
	from := " (chosen from your answers)"
	if !p.answered() {
		from = " (the starting point until you answer)"
	}
	detail := Label(s.Detail) + from
	if p.Detail != "" {
		detail = Label(p.Detail) + " (your choice)"
	}
	wording := Label(s.Level) + from
	if p.Wording != "" {
		wording = Label(p.Wording) + " (your choice)"
	}
	tokens := "Meme-coin sections are shown."
	if !s.ShowTokens {
		tokens = "Meme-coin sections are hidden unless you add a token to your watchlist."
	}
	return []Effect{
		{"Experience", ans(p.Experience), "Combined with the quiz to set how things are explained, unless you choose."},
		{"Knowledge check", quiz, "The quiz outweighs the self-rating when they disagree."},
		{"How it’s explained", wording, "Plain words leaves out the jargon. Some terms uses the precise wording on Today. With the numbers also gives the test statistics behind each forecast (Brier skill, AUC) and how unusual each move is."},
		{"How much is shown", detail, "Simple view shows the forecast, price chart and plain-English summary first and folds away the technical panels. Full shows everything."},
		{"Goals", joinLabels(p.Goals), "Picks the tips on your watchlist page."},
		{"Time horizon", ans(p.Horizon), "Your watchlist leads with this horizon and asset pages open on it."},
		{"Interests", joinLabels(p.Interests), "Chose your starter watchlist. " + tokens},
		{"After a 30% fall you would…", ans(p.DropReaction), "Sets the warnings about volatile assets in your tips. Comfort with falls: " + orDash(s.Risk) + "."},
	}
}

func orDash(s string) string {
	if s == "" {
		return "not known"
	}
	return s
}

// StarterWatchlist suggests a first watchlist from the user's interests.
func (p *Profile) StarterWatchlist(fallback []string) []string {
	var out []string
	for _, i := range p.Interests {
		switch i {
		case "stocks":
			out = append(out, "AAPL", "MSFT", "NVDA")
		case "funds":
			out = append(out, "SPY", "QQQ")
		case "crypto":
			out = append(out, "BTC-USD", "ETH-USD")
		}
	}
	if len(out) == 0 {
		return fallback
	}
	return dedupe(out)
}
