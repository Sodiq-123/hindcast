package market

import (
	"context"
	"encoding/csv"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// CBOE serves the official daily history of CBOE's options-derived indices
// (free CSVs). These describe the options market itself: the VIX term
// structure (9-day, 30-day, 3-month implied volatility), the SKEW index
// (price of crash protection) and VVIX (volatility of volatility).
type CBOE struct{ c *politeClient }

func NewCBOE() *CBOE { return &CBOE{c: newPoliteClient(500 * time.Millisecond)} }

func (c *CBOE) Name() string { return "cboe" }

// CBOEIndices maps app symbols to CBOE file names.
var CBOEIndices = map[string]string{"^VIX9D": "VIX9D", "^VIX3M": "VIX3M", "^SKEW": "SKEW", "^VVIX": "VVIX"}

func (c *CBOE) Fetch(ctx context.Context, symbol string) (*Series, error) {
	file, ok := CBOEIndices[strings.ToUpper(symbol)]
	if !ok {
		return nil, ErrNotFound
	}
	body, err := c.c.get(ctx, "https://cdn.cboe.com/api/global/us_indices/daily_prices/"+file+"_History.csv", map[string]string{"Accept": "text/csv"})
	if err != nil {
		return nil, fmt.Errorf("cboe %s: %w", symbol, err)
	}
	rows, err := csv.NewReader(strings.NewReader(string(body))).ReadAll()
	if err != nil || len(rows) < 2 {
		return nil, fmt.Errorf("cboe %s: bad csv", symbol)
	}
	var bars []Bar
	for _, r := range rows[1:] {
		d, err := time.Parse("01/02/2006", strings.TrimSpace(r[0]))
		if err != nil {
			continue
		}
		cl, _ := strconv.ParseFloat(strings.TrimSpace(r[len(r)-1]), 64)
		if cl <= 0 {
			continue
		}
		b := Bar{Date: d.Format("2006-01-02"), Open: cl, High: cl, Low: cl, Close: cl}
		if len(r) >= 5 {
			b.Open, _ = strconv.ParseFloat(r[1], 64)
			b.High, _ = strconv.ParseFloat(r[2], 64)
			b.Low, _ = strconv.ParseFloat(r[3], 64)
		}
		bars = append(bars, b)
	}
	sort.Slice(bars, func(i, j int) bool { return bars[i].Date < bars[j].Date })
	if len(bars) == 0 {
		return nil, ErrNotFound
	}
	offset := -4 * 3600
	if loc, err := time.LoadLocation("America/Chicago"); err == nil {
		_, offset = time.Now().In(loc).Zone()
	}
	return &Series{
		Meta: Meta{Symbol: strings.ToUpper(symbol), Name: "CBOE " + file, Currency: "USD", Exchange: "CBOE", Class: ClassIndex,
			Timezone: "America/Chicago", GMTOffset: offset, Price: bars[len(bars)-1].Close, PriceTime: time.Now(), Provider: "cboe"},
		Bars: bars, FetchedAt: time.Now(),
	}, nil
}
