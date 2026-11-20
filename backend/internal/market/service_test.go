package market

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"
)

type fakeRecent struct {
	full, recent *Series
	fullCalls    int
}

func (f *fakeRecent) Name() string { return "yahoo" }
func (f *fakeRecent) Fetch(context.Context, string) (*Series, error) {
	f.fullCalls++
	return f.full, nil
}
func (f *fakeRecent) FetchRecent(context.Context, string, time.Time) (*Series, error) {
	return f.recent, nil
}

func series(n int, scale float64) *Series {
	s := &Series{Meta: Meta{Symbol: "X", Provider: "yahoo"}, FetchedAt: time.Now().Add(-48 * time.Hour)}
	d := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < n; i++ {
		s.Bars = append(s.Bars, Bar{Date: d.AddDate(0, 0, i).Format("2006-01-02"), Close: scale * float64(100+i)})
	}
	return s
}

func TestTopUp(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	for _, tc := range []struct {
		name        string
		recentScale float64
		wantFull    bool
	}{{"matching history is topped up", 1, false}, {"re-adjusted history is refetched", 0.98, true}} {
		t.Run(tc.name, func(t *testing.T) {
			cache, _ := NewDiskCache(t.TempDir())
			_ = cache.Put("X", series(100, 1))
			recent := series(110, tc.recentScale)
			recent.Bars = recent.Bars[80:] // last 30 bars, 10 of them new
			p := &fakeRecent{full: series(110, 1), recent: recent}
			s := NewServiceWithProviders(cache, time.Hour, log, p)
			got, err := s.Series(context.Background(), "X", false)
			if err != nil {
				t.Fatal(err)
			}
			if len(got.Bars) != 110 || (p.fullCalls == 1) != tc.wantFull {
				t.Fatalf("bars=%d fullCalls=%d", len(got.Bars), p.fullCalls)
			}
			if fmt.Sprint(got.Last().Close) != "209" {
				t.Fatalf("last close %v", got.Last().Close)
			}
		})
	}
}

func TestCoarseDataRejected(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	coarse := series(200, 1)
	for i := range coarse.Bars {
		coarse.Bars[i].Close = float64(4 + (i/10)%2) // flat 90% of the time
	}
	if !Coarse(coarse) || Coarse(series(200, 1)) {
		t.Fatal("Coarse misclassified")
	}
	cache, _ := NewDiskCache(t.TempDir())
	_ = cache.Put("X", coarse) // stale bad data in cache must not be served
	good := &fakeRecent{full: series(150, 1)}
	bad := &fakeProvider{name: "yahoo", s: coarse}
	s := NewServiceWithProviders(cache, time.Hour, log, bad, &namedProvider{name: "coingecko", p: good})
	got, err := s.Series(context.Background(), "X", true)
	if err != nil || Coarse(got) || len(got.Bars) != 150 {
		t.Fatalf("expected the clean fallback series, got err=%v bars=%d", err, len(got.Bars))
	}
}

type fakeProvider struct {
	name string
	s    *Series
}

func (f *fakeProvider) Name() string                                   { return f.name }
func (f *fakeProvider) Fetch(context.Context, string) (*Series, error) { return f.s, nil }

type namedProvider struct {
	name string
	p    *fakeRecent
}

func (n *namedProvider) Name() string { return n.name }
func (n *namedProvider) Fetch(ctx context.Context, s string) (*Series, error) {
	return n.p.Fetch(ctx, s)
}
