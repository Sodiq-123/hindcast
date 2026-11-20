package profile

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func TestGradeAndLevel(t *testing.T) {
	all := Correct()
	q, err := Grade(all)
	if err != nil || q.Score != len(Questions) || q.Total != len(Questions) {
		t.Fatalf("all correct: %+v %v", q, err)
	}
	if _, err := Grade(map[string]int{"pe": 7}); err == nil {
		t.Error("out-of-range option accepted")
	}
	none, _ := Grade(map[string]int{})
	if none.Score != 0 {
		t.Errorf("unanswered: %d", none.Score)
	}
	// The quiz outweighs the self-rating.
	p := Profile{Experience: "experienced", Quiz: none}
	if p.Level() != "beginner" || p.Settings().Detail != "simple" {
		t.Errorf("experienced but 0/5: %s %s", p.Level(), p.Settings().Detail)
	}
	p = Profile{Experience: "some", Quiz: q}
	if p.Level() != "advanced" || p.Settings().Detail != "full" {
		t.Errorf("5/5: %s", p.Level())
	}
	p = Profile{Experience: "new"}
	if p.Level() != "beginner" {
		t.Errorf("no quiz, new: %s", p.Level())
	}
	p.Detail = "full" // an explicit choice wins
	if p.Settings().Detail != "full" {
		t.Error("detail choice ignored")
	}
}

func TestValidate(t *testing.T) {
	p := Profile{Horizon: "1w", Goals: []string{"grow", "grow"}, Interests: []string{"crypto"}}
	if err := p.Validate(); err != nil || len(p.Goals) != 1 {
		t.Errorf("valid: %v %v", err, p.Goals)
	}
	for _, bad := range []Profile{{Horizon: "5y"}, {Goals: []string{"get rich"}}, {Interests: []string{"forex"}}, {Status: "x"}, {Detail: "max"}} {
		if err := bad.Validate(); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
}

func TestSettingsAndTips(t *testing.T) {
	p := Profile{Interests: []string{"stocks"}, Horizon: "1y", Goals: []string{"grow"}, DropReaction: "sell_all"}
	s := p.Settings()
	if s.ShowTokens || s.Horizon != "1y" || s.Risk != "low" || len(s.Tips) == 0 {
		t.Errorf("settings: %+v", s)
	}
	// The warning about 30% falls only applies to people interested in crypto.
	fallTip := func(tips []string) bool {
		return slices.ContainsFunc(tips, func(t string) bool { return strings.HasPrefix(t, "You said") })
	}
	if fallTip(s.Tips) {
		t.Error("crypto warning for a stocks-only profile")
	}
	p.Interests = append(p.Interests, "crypto")
	if !fallTip(p.Settings().Tips) {
		t.Error("missing the warning for someone who'd sell after a 30% fall")
	}
	empty := &Profile{Status: "skipped"}
	if b, _ := json.Marshal(empty.Settings()); !strings.Contains(string(b), `"tips":[]`) {
		t.Errorf("tips must be a list, never null: %s", b)
	}
	if es := empty.Settings(); !es.ShowTokens || es.Horizon != "1d" || es.Detail != "simple" || es.Level != "beginner" || len(es.Tips) != 0 {
		t.Errorf("skipping setup must assume a beginner and change nothing else: %+v", es)
	}
	// The wording choice wins over the answers, and leaves the detail alone.
	chose := &Profile{Status: "skipped", Wording: "advanced"}
	if cs := chose.Settings(); cs.Level != "advanced" || cs.Detail != "simple" {
		t.Errorf("wording choice: %+v", cs)
	}
	if err := (&Profile{Wording: "expert"}).Validate(); err == nil {
		t.Error("unknown wording accepted")
	}
	if d := (&Profile{Experience: "some"}).Settings().Detail; d != "full" {
		t.Errorf("some experience, no quiz: %s", d)
	}
	three, _ := Grade(map[string]int{"pe": 1, "base_rate": 1, "diversify": 0})
	if d := (&Profile{Experience: "new", Quiz: three}).Settings().Detail; d != "simple" {
		t.Errorf("self-described beginner with 3/5: %s", d)
	}
	if got := p.StarterWatchlist([]string{"SPY"}); !slices.Equal(got, []string{"AAPL", "MSFT", "NVDA", "BTC-USD", "ETH-USD"}) {
		t.Errorf("starter: %v", got)
	}
	if got := (&Profile{}).StarterWatchlist([]string{"SPY"}); !slices.Equal(got, []string{"SPY"}) {
		t.Errorf("fallback: %v", got)
	}
	if len(p.Effects()) != 8 {
		t.Error("effects")
	}
}
