package market

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Perpetual-futures funding rates: what leveraged longs pay shorts (or vice
// versa) to hold positions. Persistently high funding means crowded,
// leveraged longs - a well-known contrarian signal for crypto. Daily values
// are the sum of that UTC day's funding payments, as a fraction of position.
//
// Sources: BitMEX for BTC and ETH (history from 2016 / 2018), Hyperliquid for
// other coins (from 2023). Both are free and keyless.

var bitmexSymbols = map[string]string{"BTC": "XBTUSD", "ETH": "ETHUSD"}

// Funding returns daily funding by UTC date for a crypto symbol (BTC-USD).
// History is cached as a Series (Close = daily funding) and refreshed
// incrementally.
func (s *Service) Funding(ctx context.Context, symbol string) (map[string]float64, error) {
	coin := strings.SplitN(strings.ToUpper(symbol), "-", 2)[0]
	key := "FUNDING:" + coin
	cached, fresh := s.cache.Get(key, s.maxAge)
	if !fresh {
		since := time.Date(2016, 1, 1, 0, 0, 0, 0, time.UTC)
		if cached != nil && len(cached.Bars) > 0 {
			if d, err := time.Parse("2006-01-02", cached.Last().Date); err == nil {
				since = d.AddDate(0, 0, -2)
			}
		}
		daily, err := s.fetchFunding(ctx, coin, since)
		if err != nil {
			if cached != nil {
				return seriesMap(cached), nil
			}
			return nil, err
		}
		merged := map[string]float64{}
		if cached != nil {
			merged = seriesMap(cached)
		}
		for d, v := range daily {
			merged[d] = v
		}
		ser := &Series{Meta: Meta{Symbol: key, Name: coin + " perpetual funding (daily)", Provider: "funding"}, FetchedAt: time.Now()}
		for d, v := range merged {
			ser.Bars = append(ser.Bars, Bar{Date: d, Open: v, High: v, Low: v, Close: v})
		}
		sort.Slice(ser.Bars, func(i, j int) bool { return ser.Bars[i].Date < ser.Bars[j].Date })
		// Today's value is partial; drop it so it isn't mistaken for a full day.
		today := time.Now().UTC().Format("2006-01-02")
		if n := len(ser.Bars); n > 0 && ser.Bars[n-1].Date == today {
			ser.Bars = ser.Bars[:n-1]
		}
		if len(ser.Bars) == 0 {
			return nil, ErrNotFound
		}
		_ = s.cache.Put(key, ser)
		cached = ser
	}
	return seriesMap(cached), nil
}

func seriesMap(s *Series) map[string]float64 {
	m := make(map[string]float64, len(s.Bars))
	for _, b := range s.Bars {
		m[b.Date] = b.Close
	}
	return m
}

func (s *Service) fetchFunding(ctx context.Context, coin string, since time.Time) (map[string]float64, error) {
	if s.fundC == nil {
		s.fundC = newPoliteClient(2100 * time.Millisecond)
	}
	sym, ok := bitmexSymbols[coin]
	if !ok {
		return s.fetchHyperliquid(ctx, coin, since)
	}
	// BTC/ETH: BitMEX for the long history (it stopped updating XBTUSD in
	// Sept 2026), Hyperliquid from mid-2023 onwards.
	switchover := time.Date(2023, 6, 1, 0, 0, 0, 0, time.UTC)
	out := map[string]float64{}
	if since.Before(switchover) {
		old, err := s.fetchBitmex(ctx, sym, since)
		if err != nil {
			return nil, err
		}
		for d, v := range old {
			if d < switchover.Format("2006-01-02") {
				out[d] = v
			}
		}
		since = switchover
	}
	recent, err := s.fetchHyperliquid(ctx, coin, since)
	if err != nil {
		return nil, err
	}
	for d, v := range recent {
		out[d] = v
	}
	return out, nil
}

func (s *Service) fetchBitmex(ctx context.Context, sym string, since time.Time) (map[string]float64, error) {
	out := map[string]float64{}
	start := since
	for i := 0; i < 200 && start.Before(time.Now()); i++ {
		u := fmt.Sprintf("https://www.bitmex.com/api/v1/funding?symbol=%s&count=500&reverse=false&startTime=%s", sym, start.UTC().Format(time.RFC3339))
		body, err := s.fundC.get(ctx, u, nil)
		if err != nil {
			return nil, fmt.Errorf("bitmex funding: %w", err)
		}
		var rows []struct {
			Timestamp   time.Time `json:"timestamp"`
			FundingRate float64   `json:"fundingRate"`
		}
		if err := json.Unmarshal(body, &rows); err != nil {
			return nil, err
		}
		if len(rows) == 0 {
			break
		}
		for _, r := range rows {
			out[r.Timestamp.UTC().Format("2006-01-02")] += r.FundingRate
		}
		next := rows[len(rows)-1].Timestamp.Add(time.Minute)
		if !next.After(start) || len(rows) < 500 {
			break
		}
		start = next
	}
	return out, nil
}

func (s *Service) fetchHyperliquid(ctx context.Context, coin string, since time.Time) (map[string]float64, error) {
	out := map[string]float64{}
	start := since.UnixMilli()
	client := &http.Client{Timeout: 30 * time.Second}
	for i := 0; i < 400; i++ {
		payload, _ := json.Marshal(map[string]any{"type": "fundingHistory", "coin": coin, "startTime": start})
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.hyperliquid.xyz/info", bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("hyperliquid funding: %w", err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			if len(out) > 0 {
				break
			}
			return nil, fmt.Errorf("hyperliquid funding %s: http %d", coin, resp.StatusCode)
		}
		var rows []struct {
			FundingRate string `json:"fundingRate"`
			Time        int64  `json:"time"`
		}
		if err := json.Unmarshal(body, &rows); err != nil {
			return nil, err
		}
		if len(rows) == 0 {
			break
		}
		for _, r := range rows {
			v, _ := strconv.ParseFloat(r.FundingRate, 64)
			out[time.UnixMilli(r.Time).UTC().Format("2006-01-02")] += v
		}
		last := rows[len(rows)-1].Time
		if last <= start || len(rows) < 500 {
			break
		}
		start = last + 1
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("hyperliquid has no funding history for %s: %w", coin, ErrNotFound)
	}
	return out, nil
}
