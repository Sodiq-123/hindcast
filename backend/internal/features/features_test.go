package features

import (
	"math"
	"testing"
	"time"

	"github.com/sodiq/financial-market/backend/internal/market"
)

func TestAlignLag(t *testing.T) {
	ext := &market.Series{Bars: []market.Bar{{Date: "2024-01-02", Close: 1}, {Date: "2024-01-03", Close: 2}, {Date: "2024-01-05", Close: 3}}}
	dates := []string{"2024-01-01", "2024-01-03", "2024-01-04", "2024-01-05"}
	same := Align(dates, ext, 0)
	lag := Align(dates, ext, 1)
	if !math.IsNaN(same[0]) || same[1] != 2 || same[2] != 2 || same[3] != 3 {
		t.Fatalf("lag0 wrong: %v", same)
	}
	// With lag 1, a same-date external close must never be visible.
	if lag[1] != 1 || lag[2] != 2 || lag[3] != 2 {
		t.Fatalf("lag1 wrong: %v", lag)
	}
}

func TestEarningsColumns(t *testing.T) {
	n := 400
	dates := make([]string, n)
	c := make([]float64, n)
	vol := make([]float64, n)
	d := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := range dates {
		dates[i] = d.Format("2006-01-02")
		d = d.AddDate(0, 0, 1)
		c[i] = 100
		vol[i] = 0.01
	}
	// A pre-market beat on bar 300 with a +8% jump.
	for i := 300; i < n; i++ {
		c[i] = 108
	}
	in := &EarningsInput{CoveredFrom: dates[0], Events: []EarningsEvent{
		{Date: dates[300], Time: "pre", Surprise: 20},
		{Date: dates[380], Time: "after", Surprise: math.NaN()},
	}}
	soon, pead, react, beat4 := EarningsColumns(dates, c, vol, in)
	if !math.IsNaN(soon[50]) {
		t.Fatal("features must be NaN before coverage + warm-up")
	}
	if soon[299] != 1 || soon[270] != 0 || soon[290] <= 0 {
		t.Fatalf("earn_soon wrong: %v %v %v", soon[270], soon[290], soon[299])
	}
	if pead[300] <= 0.39 || react[300] <= 0.9 || pead[299] != 0 {
		t.Fatalf("post-earnings features wrong: pead=%v react=%v before=%v", pead[300], react[300], pead[299])
	}
	if beat4[300] != 1 {
		t.Fatalf("one known beat => streak +1, got %v", beat4[300])
	}
	if pead[330] >= pead[301] {
		t.Fatal("post-earnings features must decay")
	}
	b0, r, ok := ReactionBars(dates, in.Events[1])
	if !ok || b0 != 380 || r != 381 {
		t.Fatalf("after-hours reaction bars wrong: %d %d", b0, r)
	}
}
