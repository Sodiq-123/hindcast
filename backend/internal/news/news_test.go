package news

import (
	"testing"
	"time"
)

func TestLexicon(t *testing.T) {
	pos, _ := LexiconScore("Apple beats estimates and raises guidance as iPhone demand surges")
	neg, _ := LexiconScore("Shares plunge after company cuts guidance; SEC charges CFO with fraud")
	negated, _ := LexiconScore("Company does not expect layoffs")
	if pos < 0.5 || neg > -0.5 {
		t.Fatalf("lexicon polarity wrong: pos=%v neg=%v", pos, neg)
	}
	if negated <= 0 {
		t.Fatalf("negation not handled: %v", negated)
	}
}

func TestAggregateDecayAndCap(t *testing.T) {
	now := time.Now()
	arts := []Article{
		{Published: now.Add(-2 * time.Hour), Relevance: 1, Direction: 1, Impact: "high"},
		{Published: now.Add(-24 * 30 * time.Hour), Relevance: 1, Direction: -1, Impact: "high"},
	}
	d := AggregateFor(arts, "1d", now)
	if d.Score < 0.9 {
		t.Fatalf("month-old news should barely count for 1d: %+v", d)
	}
	if d.LogitShift > horizonNews["1d"].kappa+1e-9 {
		t.Fatalf("news shift exceeds cap: %+v", d)
	}
	if AggregateFor(nil, "1d", now).LogitShift != 0 {
		t.Fatal("no news must mean no shift")
	}
}

func TestCleanName(t *testing.T) {
	for in, want := range map[string]string{"Apple Inc.": "Apple", "NVIDIA Corporation": "NVIDIA", "Shell plc": "Shell", "Bitcoin USD": "Bitcoin USD"} {
		if got := cleanName(in); got != want {
			t.Errorf("cleanName(%q) = %q, want %q", in, got, want)
		}
	}
}
