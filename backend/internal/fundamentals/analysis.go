package fundamentals

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"time"
)

// Prices is a daily close series (split- and dividend-adjusted).
type Prices struct {
	Dates  []string
	Closes []float64
}

// At returns the last close on or before date, and its index (-1 if none).
func (p Prices) At(date string) (float64, int) {
	i := sort.SearchStrings(p.Dates, date)
	if i < len(p.Dates) && p.Dates[i] == date {
		return p.Closes[i], i
	}
	if i == 0 {
		return math.NaN(), -1
	}
	return p.Closes[i-1], i - 1
}

// Rules is the "stronger business, lower price" pattern. The thresholds are
// fixed in advance (not tuned to the backtest).
type Rules struct {
	LookbackDays int     // window for the price high
	MinDrop      float64 // price at least this far below the high
	MinRevenue   float64 // TTM revenue growth since the high
	MinProfit    float64 // TTM operating (or net) income growth since the high
	MaxDebtRise  float64 // long-term debt may not rise more than this
}

var DefaultRules = Rules{LookbackDays: 3 * 365, MinDrop: 0.20, MinRevenue: 0.05, MinProfit: 0.10, MaxDebtRise: 0.50}

type Check struct {
	Name   string `json:"name"`
	Pass   bool   `json:"pass"`
	Detail string `json:"detail"`
}

// Comparison is the business now against the business when the stock was at
// its recent high, using only figures that had been filed by each date.
type Comparison struct {
	Date      string  `json:"date"`
	Price     float64 `json:"price"`
	PeakDate  string  `json:"peakDate"`
	PeakPrice float64 `json:"peakPrice"`
	FromPeak  float64 `json:"fromPeak"` // price now ÷ peak − 1

	ThenQuarter string `json:"thenQuarter"` // latest quarter known at the peak
	NowQuarter  string `json:"nowQuarter"`

	RevenueChange   float64 `json:"revenueChange"`
	OpIncomeChange  float64 `json:"opIncomeChange"`
	NetIncomeChange float64 `json:"netIncomeChange"`
	FCFChange       float64 `json:"fcfChange"`
	DebtChange      float64 `json:"debtChange"`

	// Price-to-earnings then and now, assuming today's share count for both
	// (historical share counts aren't split-adjusted in filings).
	PEThen float64 `json:"peThen"`
	PENow  float64 `json:"peNow"`

	Signal bool    `json:"signal"`
	Checks []Check `json:"checks"`
}

func change(now, then float64) float64 {
	if math.IsNaN(now) || math.IsNaN(then) || then <= 0 {
		return math.NaN() // not meaningful from a loss or zero base
	}
	return now/then - 1
}

func latestShares(c *Company, upTo string) float64 {
	for i := len(c.Quarters) - 1; i >= 0; i-- {
		q := c.Quarters[i]
		if q.End <= upTo && !math.IsNaN(q.Shares) && q.Shares > 0 {
			return q.Shares
		}
	}
	return math.NaN()
}

func pct(x float64) string {
	if math.IsNaN(x) {
		return "n/a"
	}
	return fmt.Sprintf("%+.0f%%", 100*x)
}

// Compare evaluates the pattern as of index t of the price series, using
// only prices up to t and filings made by that date.
func Compare(c *Company, p Prices, t int, r Rules) *Comparison {
	if t < 0 || t >= len(p.Dates) {
		return nil
	}
	date := p.Dates[t]
	cmp := &Comparison{Date: date, Price: p.Closes[t]}
	// Highest close in the lookback window (ignoring the last 20 sessions so
	// a peak isn't "now").
	start := sort.SearchStrings(p.Dates, addDays(date, -r.LookbackDays))
	peakI := -1
	for i := start; i <= t-20; i++ {
		if peakI < 0 || p.Closes[i] > p.Closes[peakI] {
			peakI = i
		}
	}
	if peakI < 0 {
		return nil
	}
	cmp.PeakDate, cmp.PeakPrice = p.Dates[peakI], p.Closes[peakI]
	cmp.FromPeak = cmp.Price/cmp.PeakPrice - 1
	now, then := c.KnownAt(date), c.KnownAt(cmp.PeakDate)
	if now == nil || then == nil || now.End == then.End {
		return nil
	}
	cmp.NowQuarter, cmp.ThenQuarter = now.End, then.End
	cmp.RevenueChange = change(now.Revenue, then.Revenue)
	cmp.OpIncomeChange = change(now.OpIncome, then.OpIncome)
	cmp.NetIncomeChange = change(now.NetIncome, then.NetIncome)
	cmp.FCFChange = change(now.FCF, then.FCF)
	cmp.DebtChange = math.NaN()
	if then.Debt > 0 && !math.IsNaN(now.Debt) {
		cmp.DebtChange = now.Debt/then.Debt - 1
	}
	sh := latestShares(c, now.End)
	cmp.PEThen, cmp.PENow = math.NaN(), math.NaN()
	if sh > 0 && then.NetIncome > 0 {
		cmp.PEThen = cmp.PeakPrice * sh / then.NetIncome
	}
	if sh > 0 && now.NetIncome > 0 {
		cmp.PENow = cmp.Price * sh / now.NetIncome
	}

	profit, label := cmp.OpIncomeChange, "Operating profit"
	if math.IsNaN(profit) {
		profit, label = cmp.NetIncomeChange, "Net profit"
	}
	cmp.Checks = []Check{
		{"Price well below its high", cmp.FromPeak <= -r.MinDrop,
			fmt.Sprintf("%s below the %s high (needs %.0f%% or more)", pct(-cmp.FromPeak)[1:], cmp.PeakDate, 100*r.MinDrop)},
		{"Revenue higher than at the high", !math.IsNaN(cmp.RevenueChange) && cmp.RevenueChange >= r.MinRevenue,
			fmt.Sprintf("12-month revenue %s since then (needs %+.0f%%)", pct(cmp.RevenueChange), 100*r.MinRevenue)},
		{label + " higher than at the high", !math.IsNaN(profit) && profit >= r.MinProfit,
			fmt.Sprintf("%s %s since then (needs %+.0f%%)", label, pct(profit), 100*r.MinProfit)},
		{"Profitable now", now.NetIncome > 0, fmt.Sprintf("12-month net income $%.2fB", now.NetIncome/1e9)},
		{"Debt not ballooning", math.IsNaN(cmp.DebtChange) || cmp.DebtChange <= r.MaxDebtRise,
			fmt.Sprintf("long-term debt %s since then (limit %+.0f%%)", pct(cmp.DebtChange), 100*r.MaxDebtRise)},
	}
	cmp.Signal = true
	for _, ch := range cmp.Checks {
		cmp.Signal = cmp.Signal && ch.Pass
	}
	return cmp
}

func addDays(date string, n int) string {
	t, err := parseDate(date)
	if err != nil {
		return date
	}
	return t.AddDate(0, 0, n).Format("2006-01-02")
}

func parseDate(s string) (time.Time, error) { return time.Parse("2006-01-02", s) }

func nf(x float64) *float64 {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return nil
	}
	return &x
}

func fn(p *float64) float64 {
	if p == nil {
		return math.NaN()
	}
	return *p
}

// MarshalJSON writes figures that don't exist (NaN) as null.
func (c Comparison) MarshalJSON() ([]byte, error) {
	type alias Comparison
	return json.Marshal(struct {
		alias
		RevenueChange   *float64 `json:"revenueChange"`
		OpIncomeChange  *float64 `json:"opIncomeChange"`
		NetIncomeChange *float64 `json:"netIncomeChange"`
		FCFChange       *float64 `json:"fcfChange"`
		DebtChange      *float64 `json:"debtChange"`
		PEThen          *float64 `json:"peThen"`
		PENow           *float64 `json:"peNow"`
	}{alias(c), nf(c.RevenueChange), nf(c.OpIncomeChange), nf(c.NetIncomeChange), nf(c.FCFChange), nf(c.DebtChange), nf(c.PEThen), nf(c.PENow)})
}

func (c *Comparison) UnmarshalJSON(b []byte) error {
	type alias Comparison
	var v struct {
		alias
		RevenueChange   *float64 `json:"revenueChange"`
		OpIncomeChange  *float64 `json:"opIncomeChange"`
		NetIncomeChange *float64 `json:"netIncomeChange"`
		FCFChange       *float64 `json:"fcfChange"`
		DebtChange      *float64 `json:"debtChange"`
		PEThen          *float64 `json:"peThen"`
		PENow           *float64 `json:"peNow"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	*c = Comparison(v.alias)
	c.RevenueChange, c.OpIncomeChange, c.NetIncomeChange, c.FCFChange = fn(v.RevenueChange), fn(v.OpIncomeChange), fn(v.NetIncomeChange), fn(v.FCFChange)
	c.DebtChange, c.PEThen, c.PENow = fn(v.DebtChange), fn(v.PEThen), fn(v.PENow)
	return nil
}
