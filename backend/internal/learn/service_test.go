package learn

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/sodiq/financial-market/backend/internal/store"
)

func TestServiceSuggestAnswerUndo(t *testing.T) {
	st := store.OpenTest(t)
	ctx := context.Background()
	u, err := st.CreateAccount(ctx, store.Signup{Email: "learner@example.com", PasswordHash: "x"})
	if err != nil {
		t.Fatal(err)
	}
	clock := time.Now()
	svc := &Service{Store: st, Now: func() time.Time { return clock }}

	// Five visits to Solana, a day apart, and none of them on the watchlist.
	for i := range 5 {
		clock = time.Now().Add(-time.Duration(5-i) * 24 * time.Hour)
		if err := svc.Record(ctx, u.ID, "visit", "sol-usd"); err != nil {
			t.Fatal(err)
		}
		// A refresh within 30 minutes isn't another visit.
		if err := svc.Record(ctx, u.ID, "visit", "SOL-USD"); err != nil {
			t.Fatal(err)
		}
	}
	clock = time.Now()
	evs, _ := st.Events(ctx, u.ID, time.Time{})
	if len(evs) != 5 {
		t.Fatalf("want 5 visits after de-duplication, got %d", len(evs))
	}
	if err := svc.Record(ctx, u.ID, "nonsense", "x"); err == nil {
		t.Fatal("an unknown event kind was accepted")
	}

	sh, err := svc.Current(ctx, u.ID)
	if err != nil || sh == nil || sh.Kind != KindAdd || sh.Subject != "SOL-USD" {
		t.Fatalf("want an add suggestion, got %+v %v", sh, err)
	}
	again, _ := svc.Current(ctx, u.ID)
	if again == nil || again.ID != sh.ID {
		t.Fatalf("the waiting suggestion should be shown again, got %+v", again)
	}

	if _, err := svc.Answer(ctx, u.ID, sh.ID, "yes"); err != nil {
		t.Fatal(err)
	}
	wl, _ := st.Watchlist(ctx, u.ID)
	if !slices.Contains(wl, "SOL-USD") {
		t.Fatalf("yes should add to the watchlist: %v", wl)
	}
	if next, _ := svc.Current(ctx, u.ID); next != nil {
		t.Fatalf("one suggestion a day, got another: %+v", next)
	}
	if _, err := svc.Answer(ctx, u.ID, sh.ID, "yes"); err == nil {
		t.Fatal("answering twice was accepted")
	}
	if _, err := svc.Answer(ctx, u.ID, sh.ID, "undo"); err != nil {
		t.Fatal(err)
	}
	wl, _ = st.Watchlist(ctx, u.ID)
	if slices.Contains(wl, "SOL-USD") {
		t.Fatalf("undo should remove it again: %v", wl)
	}

	// Another person can't answer it.
	other, _ := st.CreateAccount(ctx, store.Signup{Email: "other@example.com", PasswordHash: "x"})
	if _, err := svc.Answer(ctx, other.ID, sh.ID, "again"); err == nil {
		t.Fatal("someone else answered this person's suggestion")
	}

	o, err := svc.Overview(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(o.Asked) != 1 || o.Asked[0].Answer != "undone" || o.Events != 5 {
		t.Fatalf("overview: %+v", o)
	}

	// Pausing stops recording and suggestions; forgetting clears the history.
	if err := svc.SetPrefs(ctx, u.ID, store.LearnPrefs{Paused: true, Muted: []string{"token", "bogus"}}); err != nil {
		t.Fatal(err)
	}
	_ = svc.Record(ctx, u.ID, "visit", "NVDA")
	if evs, _ := st.Events(ctx, u.ID, time.Time{}); len(evs) != 5 {
		t.Fatal("recorded while paused")
	}
	p, _ := st.LearnPrefs(ctx, u.ID)
	if !p.Paused || !slices.Equal(p.Muted, []string{"token"}) {
		t.Fatalf("prefs: %+v", p)
	}
	if err := svc.Forget(ctx, u.ID, "all"); err != nil {
		t.Fatal(err)
	}
	o, _ = svc.Overview(ctx, u.ID)
	if len(o.Asked) != 0 || o.Events != 0 || len(o.Muted) != 0 {
		t.Fatalf("forget all left things behind: %+v", o)
	}
}

func TestServiceHorizonUndoRestoresUnset(t *testing.T) {
	st := store.OpenTest(t)
	ctx := context.Background()
	u, _ := st.CreateAccount(ctx, store.Signup{Email: "views@example.com", PasswordHash: "x"})
	svc := &Service{Store: st}
	// Identical events within 30 minutes count once, so space them out.
	for i := range 10 {
		if _, err := st.AddEvent(ctx, u.ID, "view", "1w", time.Now().Add(-time.Duration(i+2)*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	sh, err := svc.Current(ctx, u.ID)
	if err != nil || sh == nil || sh.Kind != KindHorizon {
		t.Fatalf("want a horizon suggestion, got %+v %v", sh, err)
	}
	if _, err := svc.Answer(ctx, u.ID, sh.ID, "yes"); err != nil {
		t.Fatal(err)
	}
	p, _ := st.Profile(ctx, u.ID)
	if p.Horizon != "1w" {
		t.Fatalf("horizon not set: %q", p.Horizon)
	}
	if _, err := svc.Answer(ctx, u.ID, sh.ID, "undo"); err != nil {
		t.Fatal(err)
	}
	p, _ = st.Profile(ctx, u.ID)
	if p.Horizon != "" {
		t.Fatalf("undo should restore the unset horizon, got %q", p.Horizon)
	}
}

func TestServiceReminders(t *testing.T) {
	st := store.OpenTest(t)
	ctx := context.Background()
	u, _ := st.CreateAccount(ctx, store.Signup{Email: "trader@example.com", PasswordHash: "x"})
	svc := &Service{Store: st}
	addr := "7GCihgDB8fe6KNjn2MYtkzZcRjQy3t9GHdC8uHYmW2hr"
	for i, exit := range []float64{1.2, 1.1, 0.8, 1.05} {
		stop := 0.95
		p := store.Position{Symbol: addr[:40] + string(rune('a'+i)), Kind: "token", Name: "T", OpenedAt: "2026-10-06", Entry: 1, Quantity: 100, Stop: &stop, FeeRate: 0.01, FeeOpen: 1}
		if err := st.AddPosition(ctx, u.ID, &p); err != nil {
			t.Fatal(err)
		}
		if err := st.ClosePosition(ctx, u.ID, p.ID, "2026-10-12", exit); err != nil {
			t.Fatal(err)
		}
	}
	set, err := svc.Reminders(ctx, u.ID, addr)
	if err != nil || set.Class != "token" || len(set.Items) != 2 || set.GapPct < 0.15 {
		t.Fatalf("reminders: %+v %v", set, err)
	}
	if err := svc.AnswerReminder(ctx, u.ID, "token", "muted", set.Items[0].Title); err != nil {
		t.Fatal(err)
	}
	set, _ = svc.Reminders(ctx, u.ID, addr)
	if !set.Muted || len(set.Items) != 0 {
		t.Fatalf("muted class still shows reminders: %+v", set)
	}
	if set, _ := svc.Reminders(ctx, u.ID, "NVDA"); set.Muted || set.Class != "stock" {
		t.Fatalf("muting tokens shouldn't mute stocks: %+v", set)
	}
}

func TestServiceLevelYesAndUndo(t *testing.T) {
	st := store.OpenTest(t)
	ctx := context.Background()
	u, _ := st.CreateAccount(ctx, store.Signup{Email: "level@example.com", PasswordHash: "x"})
	svc := &Service{Store: st}
	if err := svc.Record(ctx, u.ID, "page", "nowhere"); err == nil {
		t.Error("an unknown page was recorded")
	}
	for i := range 4 {
		if _, err := st.AddEvent(ctx, u.ID, "page", "replay", time.Now().Add(-time.Duration(i+1)*26*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	sh, err := svc.Current(ctx, u.ID)
	if err != nil || sh == nil || sh.Kind != KindLevel {
		t.Fatalf("want a level suggestion, got %+v %v", sh, err)
	}
	if _, err := svc.Answer(ctx, u.ID, sh.ID, "yes"); err != nil {
		t.Fatal(err)
	}
	p, _ := st.Profile(ctx, u.ID)
	if p.Wording != "advanced" || p.Settings().Level != "advanced" || p.Settings().Detail != "simple" {
		t.Fatalf("yes should set only the wording: %q %+v", p.Wording, p.Settings())
	}
	if _, err := svc.Answer(ctx, u.ID, sh.ID, "undo"); err != nil {
		t.Fatal(err)
	}
	if p, _ = st.Profile(ctx, u.ID); p.Wording != "" || p.Settings().Level != "beginner" {
		t.Fatalf("undo should go back to the answers: %q %s", p.Wording, p.Settings().Level)
	}
	if err := svc.Forget(ctx, u.ID, "page"); err != nil {
		t.Fatal(err)
	}
}
