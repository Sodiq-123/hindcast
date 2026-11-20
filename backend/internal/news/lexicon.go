package news

import (
	"math"
	"regexp"
	"strings"
	"time"
)

// A compact finance-specific lexicon (in the spirit of Loughran-McDonald).
// General-purpose sentiment lexicons misread finance ("liability", "tax",
// "crude" are neutral here), so the words below are chosen for market impact.
// Multi-word phrases are matched first and carry more weight.
var phrases = map[string]float64{
	"beats estimates": 2, "beat estimates": 2, "tops estimates": 2, "better than expected": 1.5,
	"misses estimates": -2, "missed estimates": -2, "worse than expected": -1.5, "falls short": -1.5,
	"raises guidance": 2, "raised guidance": 2, "raises forecast": 2, "boosts outlook": 2,
	"cuts guidance": -2, "lowers guidance": -2, "cuts forecast": -2, "slashes": -1.5,
	"record high": 1.5, "all-time high": 1.5, "52-week high": 1, "52-week low": -1,
	"price target raised": 1.5, "raises price target": 1.5, "price target cut": -1.5, "cuts price target": -1.5,
	"rate cut": 1, "rate cuts": 1, "rate hike": -1, "rate hikes": -1,
	"share buyback": 1.5, "stock buyback": 1.5, "dividend increase": 1.5, "raises dividend": 1.5,
	"dividend cut": -2, "suspends dividend": -2, "going concern": -3, "chapter 11": -3,
	"sec charges": -2, "class action": -1.5, "data breach": -1.5, "short seller": -1.5,
	"strong demand": 1.2, "weak demand": -1.2, "supply chain": -0.3, "trade war": -1.2,
	"etf approval": 1.5, "etf inflows": 1.2, "etf outflows": -1.2, "exchange hack": -2.5,
}

var words = map[string]float64{
	// positive
	"surge": 1.2, "surges": 1.2, "soar": 1.3, "soars": 1.3, "jump": 1, "jumps": 1, "rally": 1, "rallies": 1,
	"gain": 0.6, "gains": 0.6, "rise": 0.6, "rises": 0.6, "climb": 0.7, "climbs": 0.7, "rebound": 0.8,
	"upgrade": 1.3, "upgraded": 1.3, "upgrades": 1.3, "outperform": 1, "bullish": 1.2, "beat": 1, "beats": 1,
	"strong": 0.6, "growth": 0.5, "profit": 0.5, "profitable": 0.7, "record": 0.6, "expansion": 0.5,
	"approval": 0.9, "approved": 0.9, "partnership": 0.6, "breakthrough": 1, "wins": 0.7, "win": 0.5,
	"optimism": 0.8, "optimistic": 0.8, "robust": 0.7, "accelerate": 0.6, "accelerates": 0.6, "boost": 0.7,
	"boosts": 0.7, "upside": 0.8, "recovery": 0.6, "acquire": 0.4, "acquisition": 0.4, "inflows": 0.8,
	"adoption": 0.6, "launch": 0.4, "launches": 0.4, "milestone": 0.6, "buy": 0.6,
	// negative
	"plunge": -1.4, "plunges": -1.4, "plummet": -1.4, "plummets": -1.4, "crash": -1.6, "crashes": -1.6,
	"tumble": -1.2, "tumbles": -1.2, "slump": -1.1, "slumps": -1.1, "fall": -0.6, "falls": -0.6,
	"drop": -0.6, "drops": -0.6, "decline": -0.6, "declines": -0.6, "sink": -0.9, "sinks": -0.9,
	"downgrade": -1.3, "downgraded": -1.3, "downgrades": -1.3, "underperform": -1, "bearish": -1.2,
	"miss": -1, "misses": -1, "weak": -0.6, "loss": -0.7, "losses": -0.7, "warning": -0.9, "warns": -0.9,
	"lawsuit": -1, "sued": -1, "probe": -1, "investigation": -1, "fraud": -2, "scandal": -1.6,
	"bankruptcy": -2.5, "default": -1.5, "layoffs": -0.8, "recall": -1, "delay": -0.6, "delays": -0.6,
	"hack": -1.5, "hacked": -1.5, "exploit": -1.4, "ban": -1.2, "bans": -1.2, "crackdown": -1.2,
	"fine": -0.7, "fined": -1, "penalty": -0.8, "sanctions": -0.8, "tariff": -0.6, "tariffs": -0.6,
	"recession": -1.2, "inflation": -0.4, "selloff": -1.2, "sell-off": -1.2, "fears": -0.8, "fear": -0.7,
	"concern": -0.5, "concerns": -0.5, "risk": -0.3, "volatile": -0.4, "outflows": -0.8, "sell": -0.6,
	"halt": -1, "halted": -1, "liquidation": -1.3, "liquidations": -1.3, "dilution": -1, "shortfall": -1,
}

var negators = map[string]bool{"not": true, "no": true, "never": true, "without": true, "fails": true, "failed": true, "despite": true}

var tokenRe = regexp.MustCompile(`[a-z0-9\-]+`)

// LexiconScore returns a sentiment in [-1, 1] and the terms that drove it.
func LexiconScore(text string) (float64, []string) {
	t := strings.ToLower(text)
	total := 0.0
	var hits []string
	for p, w := range phrases {
		if strings.Contains(t, p) {
			total += w
			hits = append(hits, p)
			t = strings.ReplaceAll(t, p, " ")
		}
	}
	toks := tokenRe.FindAllString(t, -1)
	for i, tok := range toks {
		w, ok := words[tok]
		if !ok {
			continue
		}
		for k := max(0, i-3); k < i; k++ {
			if negators[toks[k]] {
				w = -w * 0.7
				break
			}
		}
		total += w
		hits = append(hits, tok)
	}
	return math.Tanh(total / 2.5), hits
}

// ScoreLexicon fills lexicon fields and, when no LLM judgement exists,
// derives direction/relevance from the lexicon.
func ScoreLexicon(arts []Article, symbol, name string) {
	base := strings.ToLower(strings.SplitN(symbol, ".", 2)[0])
	base = strings.TrimSuffix(base, "-usd")
	nm := strings.ToLower(cleanName(name))
	for i := range arts {
		a := &arts[i]
		a.LexScore, a.LexHits = LexiconScore(a.Title + ". " + a.Summary)
		if a.ScoredBy == "llm" {
			continue
		}
		a.ScoredBy = "lexicon"
		a.Direction = a.LexScore
		tl := strings.ToLower(a.Title)
		switch {
		case a.Scope == "asset" && (strings.Contains(tl, base) || (nm != "" && strings.Contains(tl, strings.Fields(nm)[0]))):
			a.Relevance = 0.9
		case a.Scope == "asset":
			a.Relevance = 0.5
		default:
			a.Relevance = 0.25
		}
		switch m := math.Abs(a.LexScore); {
		case m > 0.6:
			a.Impact = "high"
		case m > 0.3:
			a.Impact = "medium"
		default:
			a.Impact = "low"
		}
	}
}

// Aggregate is the news signal for one forecast horizon.
type Aggregate struct {
	Score      float64 `json:"score"`      // -1..1 weighted direction
	Coverage   float64 `json:"coverage"`   // 0..1, how much relevant recent news exists
	Articles   int     `json:"articles"`   // count with non-trivial weight
	LogitShift float64 `json:"logitShift"` // adjustment applied to the forecast log-odds
	ProbShift  float64 `json:"probShift"`  // resulting change in probability (pp/100)
}

// Per-horizon decay half-life and maximum influence on the log-odds. News
// matters most for the next day and fades for long horizons. These caps are
// deliberately small because the news overlay cannot be backtested (no
// historical news archive); its real value is measured by the live track
// record, which logs forecasts with and without it.
var horizonNews = map[string]struct {
	halfLife time.Duration
	kappa    float64
}{
	"1d": {24 * time.Hour, 0.30},
	"1w": {72 * time.Hour, 0.22},
	"1m": {10 * 24 * time.Hour, 0.12},
	"1y": {30 * 24 * time.Hour, 0.05},
}

var impactWeight = map[string]float64{"low": 0.5, "medium": 1, "high": 1.6}

func AggregateFor(arts []Article, horizonKey string, now time.Time) Aggregate {
	cfg, ok := horizonNews[horizonKey]
	if !ok {
		return Aggregate{}
	}
	var num, den float64
	n := 0
	for _, a := range arts {
		age := now.Sub(a.Published)
		if a.Published.IsZero() {
			age = 48 * time.Hour
		}
		if age < 0 {
			age = 0
		}
		decay := math.Pow(0.5, age.Hours()/cfg.halfLife.Hours())
		iw := impactWeight[a.Impact]
		if iw == 0 {
			iw = 0.5
		}
		w := decay * a.Relevance * iw
		if w < 0.02 {
			continue
		}
		n++
		num += w * a.Direction
		den += w
	}
	if den == 0 {
		return Aggregate{}
	}
	agg := Aggregate{Score: num / den, Coverage: 1 - math.Exp(-den/2), Articles: n}
	agg.LogitShift = cfg.kappa * agg.Score * agg.Coverage
	return agg
}
