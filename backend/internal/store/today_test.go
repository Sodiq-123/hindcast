package store

import (
	"context"
	"testing"
	"time"
)

func TestPrefsVisitsAndSteps(t *testing.T) {
	st := OpenTest(t)
	ctx := context.Background()
	a, b := testUser(t, st, "a@example.com"), testUser(t, st, "b@example.com")

	if p, err := st.Prefs(ctx, a); err != nil || p.Theme != "auto" || p.Accent != "teal" {
		t.Fatalf("default prefs = %+v, %v", p, err)
	}
	if err := st.SavePrefs(ctx, a, Prefs{Theme: "dark", Accent: "plum"}); err != nil {
		t.Fatal(err)
	}
	if p, _ := st.Prefs(ctx, a); p.Theme != "dark" || p.Accent != "plum" {
		t.Errorf("saved prefs = %+v", p)
	}
	if p, _ := st.Prefs(ctx, b); p.Accent != "teal" {
		t.Errorf("prefs leaked to another person: %+v", p)
	}

	// Visits: the first has no previous one; refreshes within the gap
	// keep the same previous visit; a later visit moves it on.
	t0 := time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)
	gap := 30 * time.Minute
	if prev, err := st.MarkSeen(ctx, a, t0, gap); err != nil || prev != nil {
		t.Fatalf("first visit prev = %v, %v", prev, err)
	}
	if prev, _ := st.MarkSeen(ctx, a, t0.Add(10*time.Minute), gap); prev != nil {
		t.Errorf("refresh should not count as a new visit, prev = %v", prev)
	}
	prev, _ := st.MarkSeen(ctx, a, t0.Add(5*time.Hour), gap)
	if prev == nil || !prev.Equal(t0.Add(10*time.Minute)) {
		t.Errorf("later visit prev = %v", prev)
	}
	if again, _ := st.MarkSeen(ctx, a, t0.Add(5*time.Hour+time.Minute), gap); again == nil || !again.Equal(*prev) {
		t.Errorf("refresh after a visit should keep its prev, got %v", again)
	}
	if p, _ := st.Prefs(ctx, a); p.Theme != "dark" {
		t.Error("marking a visit must not reset the theme")
	}

	// Suggestions: skip, undo, done; per person.
	day := time.Now().Truncate(24 * time.Hour).Add(-time.Hour)
	if err := st.StepAction(ctx, a, "earnings:NVDA", "skip", day); err != nil {
		t.Fatal(err)
	}
	s, _ := st.StepStates(ctx, a, day)
	if !s["earnings:NVDA"].SkippedToday || s["earnings:NVDA"].Done {
		t.Errorf("after skip = %+v", s)
	}
	if s, _ := st.StepStates(ctx, a, time.Now().Add(time.Hour)); s["earnings:NVDA"].SkippedToday {
		t.Error("a skip from before today should not count")
	}
	if s, _ := st.StepStates(ctx, b, day); len(s) != 0 {
		t.Errorf("steps leaked to another person: %+v", s)
	}
	_ = st.StepAction(ctx, a, "earnings:NVDA", "undo", day)
	if s, _ := st.StepStates(ctx, a, day); s["earnings:NVDA"].SkippedToday {
		t.Error("undo should clear today's skip")
	}
	_ = st.StepAction(ctx, a, "earnings:NVDA", "done", day)
	if s, _ := st.StepStates(ctx, a, day); !s["earnings:NVDA"].Done {
		t.Error("done not recorded")
	}
	if h, _ := st.Steps(ctx, a); len(h) != 1 || h[0]["action"] != "done" {
		t.Errorf("history = %+v", h)
	}
}
