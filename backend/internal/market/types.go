// Package market fetches and normalises daily price history for any tradable
// instrument (global equities, ETFs, indices, crypto, commodities futures).
package market

import (
	"strings"
	"time"
)

// AssetClass drives horizon lengths, benchmark choice and which macro inputs apply.
type AssetClass string

const (
	ClassEquity  AssetClass = "equity"
	ClassETF     AssetClass = "etf"
	ClassIndex   AssetClass = "index"
	ClassCrypto  AssetClass = "crypto"
	ClassFuture  AssetClass = "future"
	ClassFund    AssetClass = "fund"
	ClassUnknown AssetClass = "unknown"
)

// Bar is one daily OHLCV bar. Prices are split/dividend adjusted so that
// returns computed from them are total returns.
type Bar struct {
	Date   string  `json:"date"` // YYYY-MM-DD in the instrument's exchange timezone
	Open   float64 `json:"open"`
	High   float64 `json:"high"`
	Low    float64 `json:"low"`
	Close  float64 `json:"close"`
	Volume float64 `json:"volume"`
}

// Meta describes the instrument.
type Meta struct {
	Symbol    string     `json:"symbol"`
	Name      string     `json:"name"`
	Currency  string     `json:"currency"`
	Exchange  string     `json:"exchange"`
	Class     AssetClass `json:"class"`
	Timezone  string     `json:"timezone"`
	GMTOffset int        `json:"gmtOffset"` // seconds east of UTC
	Price     float64    `json:"price"`     // latest regular-market price
	PriceTime time.Time  `json:"priceTime"`
	Provider  string     `json:"provider"`
}

// Series is a full daily history plus metadata.
type Series struct {
	Meta      Meta      `json:"meta"`
	Bars      []Bar     `json:"bars"`
	FetchedAt time.Time `json:"fetchedAt"`
}

func (s *Series) Closes() []float64 {
	out := make([]float64, len(s.Bars))
	for i, b := range s.Bars {
		out[i] = b.Close
	}
	return out
}

func (s *Series) Last() Bar {
	if len(s.Bars) == 0 {
		return Bar{}
	}
	return s.Bars[len(s.Bars)-1]
}

// IsCrypto is a symbol-level heuristic used before metadata is known.
func IsCrypto(symbol string) bool {
	s := strings.ToUpper(symbol)
	for _, q := range []string{"-USD", "-USDT", "-EUR", "-GBP", "-BTC", "-ETH"} {
		if strings.HasSuffix(s, q) {
			return true
		}
	}
	return false
}

// Horizon is a forecast horizon expressed in bars of the instrument.
type Horizon struct {
	Key   string `json:"key"`   // "1d" | "1w" | "1m" | "1y"
	Label string `json:"label"` // human label
	Bars  int    `json:"bars"`
}

// HorizonsFor returns the four forecast horizons. Crypto trades 7 days a week,
// so a "week" is 7 bars rather than 5.
func HorizonsFor(class AssetClass) []Horizon {
	if class == ClassCrypto {
		return []Horizon{
			{"1d", "Next day", 1},
			{"1w", "Next week", 7},
			{"1m", "Next month", 30},
			{"1y", "Next year", 365},
		}
	}
	return []Horizon{
		{"1d", "Next trading day", 1},
		{"1w", "Next week", 5},
		{"1m", "Next month", 21},
		{"1y", "Next year", 252},
	}
}

// BarsPerYear is used for annualising volatility.
func BarsPerYear(class AssetClass) float64 {
	if class == ClassCrypto {
		return 365
	}
	return 252
}
