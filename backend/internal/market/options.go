package market

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Expiry is the options market's expectation for one expiration date,
// read from the at-the-money straddle (call + put at the strike nearest the
// price). The straddle costs roughly the expected absolute move to expiry.
type Expiry struct {
	Date        string  `json:"date"`
	Strike      float64 `json:"strike"`
	Straddle    float64 `json:"straddle"`
	ImpliedMove float64 `json:"impliedMove"` // straddle / spot
	ImpliedVol  float64 `json:"impliedVol"`  // annualised, ≈ move / (0.8·√T)
}

// OptionsImplied fetches today's option chain for a US-listed stock or ETF
// from Nasdaq and summarises each expiry. It is a snapshot: there is no free
// history, so it informs the report but is not a model input.
func (s *Service) OptionsImplied(ctx context.Context, symbol string, spot float64) ([]Expiry, error) {
	sym := strings.ToUpper(symbol)
	if strings.ContainsAny(sym, "^=.") || IsCrypto(sym) || spot <= 0 {
		return nil, ErrNotFound
	}
	if s.optC == nil {
		s.optC = newPoliteClient(600 * time.Millisecond)
	}
	today := time.Now().UTC()
	for _, ac := range []string{"stocks", "etf"} {
		q := url.Values{"assetclass": {ac}, "limit": {"2000"}, "fromdate": {today.Format("2006-01-02")},
			"todate": {today.AddDate(2, 0, 0).Format("2006-01-02")}, "money": {"at"}, "type": {"all"}, "callput": {"callput"}, "excode": {"oprac"}}
		body, err := s.optC.get(ctx, "https://api.nasdaq.com/api/quote/"+url.PathEscape(strings.ReplaceAll(sym, "-", "."))+"/option-chain?"+q.Encode(),
			map[string]string{"Accept": "application/json"})
		if err != nil {
			return nil, fmt.Errorf("options %s: %w", symbol, err)
		}
		var r struct {
			Data *struct {
				Table struct {
					Rows []map[string]any `json:"rows"`
				} `json:"table"`
			} `json:"data"`
		}
		if json.Unmarshal(body, &r) != nil || r.Data == nil || len(r.Data.Table.Rows) == 0 {
			continue
		}
		return summariseChain(r.Data.Table.Rows, spot, today), nil
	}
	return nil, ErrNotFound
}

func summariseChain(rows []map[string]any, spot float64, today time.Time) []Expiry {
	num := func(v any) float64 {
		s, _ := v.(string)
		f, err := strconv.ParseFloat(strings.ReplaceAll(s, ",", ""), 64)
		if err != nil {
			return math.NaN()
		}
		return f
	}
	mid := func(row map[string]any, side string) float64 {
		b, a, l := num(row[side+"_Bid"]), num(row[side+"_Ask"]), num(row[side+"_Last"])
		if b > 0 && a > 0 && a >= b && (a-b) <= 0.5*a {
			return (b + a) / 2 // tight enough quote
		}
		if l > 0 {
			return l
		}
		return math.NaN()
	}
	var out []Expiry
	var cur *Expiry
	best := math.Inf(1)
	flush := func() {
		if cur != nil && cur.Straddle > 0 {
			out = append(out, *cur)
		}
	}
	for _, row := range rows {
		if g, _ := row["expirygroup"].(string); g != "" {
			flush()
			d, err := time.Parse("January 2, 2006", g)
			cur, best = nil, math.Inf(1)
			if err == nil && d.After(today) {
				cur = &Expiry{Date: d.Format("2006-01-02")}
			}
			continue
		}
		if cur == nil {
			continue
		}
		k := num(row["strike"])
		c, p := mid(row, "c"), mid(row, "p")
		if math.IsNaN(k) || math.IsNaN(c) || math.IsNaN(p) {
			continue
		}
		if dist := math.Abs(k - spot); dist < best {
			best = dist
			cur.Strike, cur.Straddle = k, c+p
		}
	}
	flush()
	for i := range out {
		e := &out[i]
		e.ImpliedMove = e.Straddle / spot
		d, _ := time.Parse("2006-01-02", e.Date)
		if years := d.Sub(today).Hours() / (24 * 365); years > 0 {
			e.ImpliedVol = e.ImpliedMove / (0.8 * math.Sqrt(years))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date < out[j].Date })
	return out
}
