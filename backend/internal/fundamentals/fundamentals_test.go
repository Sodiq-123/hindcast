package fundamentals

import (
	"encoding/json"
	"math"
	"math/rand/v2"
	"testing"
	"time"
)

func doc(t *testing.T, tags map[string][]fact) []byte {
	g := map[string]any{}
	for k, fs := range tags {
		g[k] = map[string]any{"units": map[string]any{"USD": fs}}
	}
	b, err := json.Marshal(map[string]any{"entityName": "Test Co", "facts": map[string]any{"us-gaap": g}})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseQuartersYTDAndPointInTime(t *testing.T) {
	// Fiscal year = calendar year. Revenue reported per quarter (Q1-Q3) and
	// for the year (so Q4 = year - 9-month YTD); cash flow only as YTD.
	var rev, ocf []fact
	for y := 2021; y <= 2022; y++ {
		ys := func(m, d int) string {
			return time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC).Format("2006-01-02")
		}
		base := float64(100 * (y - 2020)) // 100 per quarter in 2021, 200 in 2022
		rev = append(rev,
			fact{ys(1, 1), ys(3, 31), base, ys(5, 1), "10-Q"},
			fact{ys(4, 1), ys(6, 30), base, ys(8, 1), "10-Q"},
			fact{ys(7, 1), ys(9, 30), base, ys(11, 1), "10-Q"},
			fact{ys(1, 1), ys(9, 30), 3 * base, ys(11, 1), "10-Q"},
			fact{ys(1, 1), ys(12, 31), 4 * base, time.Date(y+1, 2, 15, 0, 0, 0, 0, time.UTC).Format("2006-01-02"), "10-K"},
		)
		ocf = append(ocf,
			fact{ys(1, 1), ys(3, 31), 10, ys(5, 1), "10-Q"},
			fact{ys(1, 1), ys(6, 30), 20, ys(8, 1), "10-Q"},
			fact{ys(1, 1), ys(9, 30), 30, ys(11, 1), "10-Q"},
			fact{ys(1, 1), ys(12, 31), 40, time.Date(y+1, 2, 15, 0, 0, 0, 0, time.UTC).Format("2006-01-02"), "10-K"},
		)
	}
	// A later restatement of 2021 revenue must not leak into the past.
	rev = append(rev, fact{"2021-01-01", "2021-12-31", 999, "2023-02-15", "10-K"})
	c, err := Parse(doc(t, map[string][]fact{"Revenues": rev, "NetCashProvidedByUsedInOperatingActivities": ocf, "NetIncomeLoss": rev}))
	if err != nil {
		t.Fatal(err)
	}
	var fy21, fy22 *Quarter
	for i := range c.Quarters {
		switch c.Quarters[i].End {
		case "2021-12-31":
			fy21 = &c.Quarters[i]
		case "2022-12-31":
			fy22 = &c.Quarters[i]
		}
	}
	if fy21 == nil || fy21.Revenue != 400 || fy21.OCF != 40 || fy21.Known != "2022-02-15" {
		t.Fatalf("FY2021 TTM wrong: %+v", fy21)
	}
	if fy22 == nil || fy22.Revenue != 800 || fy22.OCF != 40 {
		t.Fatalf("FY2022 TTM wrong: %+v", fy22)
	}
	if q := c.KnownAt("2023-02-01"); q == nil || q.End != "2022-09-30" {
		t.Errorf("before the 10-K is filed, the latest known quarter is Q3: %+v", q)
	}
}

func series(start string, closes []float64) Prices {
	t0, _ := time.Parse("2006-01-02", start)
	p := Prices{}
	for i, c := range closes {
		p.Dates = append(p.Dates, t0.AddDate(0, 0, i).Format("2006-01-02"))
		p.Closes = append(p.Closes, c)
	}
	return p
}

func company(rev0, rev1, ni0, ni1 float64) *Company {
	return &Company{Symbol: "X", Quarters: []Quarter{
		{End: "2020-03-31", Known: "2020-05-01", Revenue: rev0, OpIncome: ni0, NetIncome: ni0, FCF: ni0, Shares: 100, Debt: 50},
		{End: "2021-12-31", Known: "2022-02-01", Revenue: rev1, OpIncome: ni1, NetIncome: ni1, FCF: ni1, Shares: 100, Debt: 55},
	}}
}

func TestComparePattern(t *testing.T) {
	// Price peaks at 100 in mid-2020 and falls to 70 by 2022.
	closes := make([]float64, 800)
	for i := range closes {
		closes[i] = 100 - 30*math.Max(0, float64(i-100))/700
	}
	p := series("2020-06-01", closes)
	last := len(closes) - 1
	c := Compare(company(1000, 1200, 100, 130), p, last, DefaultRules)
	if c == nil || !c.Signal {
		t.Fatalf("revenue +20%%, profit +30%%, price -30%%: should signal, got %+v", c)
	}
	if math.Abs(c.PEThen-100*100/100.0) > 1e-9 || c.PENow >= c.PEThen {
		t.Errorf("P/E should fall: then %.2f now %.2f", c.PEThen, c.PENow)
	}
	if c := Compare(company(1000, 1200, 100, 90), p, last, DefaultRules); c == nil || c.Signal {
		t.Error("falling profit must not signal")
	}
}

func TestBacktestFindsPlantedEdge(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	mk := func(edge bool, seed int) Item {
		n := 2400
		closes := make([]float64, n)
		closes[0] = 100
		for i := 1; i < n; i++ {
			drift := 0.0
			// Every stock dips 30% in its first two years; the "good" ones
			// then rally hard.
			if i > 300 && i < 600 {
				drift = -0.0012
			}
			if edge && i >= 620 {
				drift = 0.002
			}
			closes[i] = closes[i-1] * math.Exp(drift+0.01*rng.NormFloat64())
		}
		rev1, ni1 := 1000.0, 90.0
		if edge {
			rev1, ni1 = 1300, 150
		}
		c := &Company{Symbol: "S", Quarters: []Quarter{
			{End: "2010-03-31", Known: "2010-04-30", Revenue: 1000, OpIncome: 100, NetIncome: 100, FCF: 100, Shares: 1, Debt: 10},
			{End: "2011-06-30", Known: "2011-08-01", Revenue: rev1, OpIncome: ni1, NetIncome: ni1, FCF: ni1, Shares: 1, Debt: 10},
		}}
		return Item{Company: c, Prices: series("2010-01-01", closes)}
	}
	var items []Item
	for i := 0; i < 60; i++ {
		items = append(items, mk(i%3 == 0, i))
	}
	bt := RunBacktest(items, DefaultRules, "2010-01-01")
	if bt.Groups["signal"].N == 0 || bt.Groups["signal"].Excess12m <= bt.Groups["dip_only"].Excess12m {
		t.Fatalf("planted edge not found: %+v", bt.Groups)
	}
	if bt.Portfolio.MeanExcess <= 0 {
		t.Errorf("portfolio should beat the universe: %+v", bt.Portfolio)
	}
}

func TestComparisonJSONWithNaN(t *testing.T) {
	c := Comparison{Date: "2026-01-01", RevenueChange: math.NaN(), PENow: 12.5, Checks: []Check{{"x", true, "y"}}}
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	var back Comparison
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if !math.IsNaN(back.RevenueChange) || back.PENow != 12.5 || back.Date != "2026-01-01" || len(back.Checks) != 1 {
		t.Errorf("round trip: %s -> %+v", b, back)
	}
}
