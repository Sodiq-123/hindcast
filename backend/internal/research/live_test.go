package research

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/sodiq/financial-market/backend/internal/token"
)

// TestLiveSmoke makes small real calls (a few cents). Run with
// RESEARCH_LIVE=1 and ANTHROPIC_API_KEY set.
func TestLiveSmoke(t *testing.T) {
	if os.Getenv("RESEARCH_LIVE") != "1" {
		t.Skip("set RESEARCH_LIVE=1 to run")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	s := New(os.Getenv("ANTHROPIC_API_KEY"), "claude-opus-5-5")
	s.Tokens, s.Log = token.NewService(), slog.Default()
	s.http = &http.Client{Timeout: 25 * time.Second}

	for _, subj := range []string{"ZEC-USD", "pumpCmXqMfrsAkQ5r49WcJnRayYRqmXz6ae8H7H9Dfn"} {
		rep := &Report{Subject: subj}
		facts, err := s.gather(ctx, rep)
		if err != nil {
			t.Fatalf("%s gather: %v", subj, err)
		}
		t.Logf("=== %s (%s, %s) price %.6g\n%s", rep.Name, rep.Kind, rep.Symbol, rep.Price, facts)
	}

	s.limits = limits{searches: 1, fetches: 1, tokens: 8000}
	rep := &Report{Subject: "ZEC-USD", Name: "Zcash", Symbol: "ZEC-USD", Kind: "crypto", Facts: "(smoke test)"}
	notes, sources, cost, err := s.investigate(ctx, rep, nil)
	t.Logf("investigate cost $%.4f, %d searches, %d fetches, in %d out %d", cost.USD, cost.Searches, cost.Fetches, cost.Input, cost.Output)
	if err != nil {
		t.Fatalf("investigate: %v", err)
	}
	t.Logf("notes (first 600 chars): %.600s", notes)
	t.Logf("sources seen: %d", len(sources))
	rep.Notes, rep.Cost = notes, cost
	if err := s.structure(ctx, rep, nil, sources); err != nil {
		t.Fatalf("structure: %v", err)
	}
	t.Logf("structured: fundamentals=%s, %d sections, %d sources, overview: %.300s", rep.Fundamentals, len(rep.Sections), len(rep.Sources), rep.Overview)
	t.Logf("TOTAL smoke cost $%.4f", rep.Cost.USD)
}
