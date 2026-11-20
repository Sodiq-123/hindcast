package scheduler

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"
)

// A computer asleep at the scheduled time: the run happens as soon as the
// clock shows it is past due, then the next run is the following slot.
func TestCatchesUpRunMissedWhileAsleep(t *testing.T) {
	tz := time.UTC
	var mu sync.Mutex
	clock := time.Date(2026, 11, 17, 22, 35, 0, 0, tz) // just after the 22:30 run
	var runs []time.Time
	done := make(chan struct{}, 4)
	s := &Scheduler{Times: []string{"07:30", "22:30"}, TZ: tz, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), tick: 5 * time.Millisecond}
	s.clock = func() time.Time { mu.Lock(); defer mu.Unlock(); return clock }
	s.run = func(context.Context, bool) {
		mu.Lock()
		runs = append(runs, clock)
		mu.Unlock()
		done <- struct{}{}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Start(ctx)

	time.Sleep(30 * time.Millisecond)
	mu.Lock()
	if len(runs) != 0 {
		t.Fatalf("ran before it was due: %v", runs)
	}
	clock = time.Date(2026, 11, 18, 9, 10, 0, 0, tz) // woke up after the 07:30 slot
	mu.Unlock()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("missed run was not caught up after waking")
	}
	time.Sleep(30 * time.Millisecond)
	mu.Lock()
	if len(runs) != 1 {
		t.Fatalf("expected exactly one catch-up run, got %d", len(runs))
	}
	clock = time.Date(2026, 11, 18, 22, 30, 5, 0, tz) // the evening slot
	mu.Unlock()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("evening run did not happen")
	}
}

func TestNextSlot(t *testing.T) {
	s := &Scheduler{Times: []string{"07:30", "22:30"}, TZ: time.UTC}
	cases := map[time.Time]time.Time{
		time.Date(2026, 11, 17, 22, 35, 0, 0, time.UTC): time.Date(2026, 11, 18, 7, 30, 0, 0, time.UTC),
		time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC):   time.Date(2026, 11, 18, 22, 30, 0, 0, time.UTC),
		time.Date(2026, 11, 18, 7, 30, 0, 0, time.UTC):  time.Date(2026, 11, 18, 22, 30, 0, 0, time.UTC),
	}
	for from, want := range cases {
		if got := s.next(from); !got.Equal(want) {
			t.Errorf("next(%v) = %v, want %v", from, got, want)
		}
	}
}
