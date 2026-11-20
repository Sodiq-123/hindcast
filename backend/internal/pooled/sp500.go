package pooled

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// S&P 500 membership history (every ticker that has been in the index since
// 1996 with its start and end dates), maintained at github.com/fja05680/sp500.
const sp500URL = "https://raw.githubusercontent.com/fja05680/sp500/master/sp500_ticker_start_end.csv"

// Membership maps a Yahoo symbol to the date ranges it was in the index.
type Membership map[string][][2]string

func fetchSP500(ctx context.Context) (Membership, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sp500URL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("sp500 membership: http %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	rows, err := csv.NewReader(strings.NewReader(string(body))).ReadAll()
	if err != nil || len(rows) < 100 {
		return nil, fmt.Errorf("sp500 membership: bad csv")
	}
	m := Membership{}
	for _, r := range rows[1:] {
		if len(r) < 3 || r[0] == "" || r[1] == "" {
			continue
		}
		sym := strings.ReplaceAll(strings.ToUpper(strings.TrimSpace(r[0])), ".", "-")
		m[sym] = append(m[sym], [2]string{strings.TrimSpace(r[1]), strings.TrimSpace(r[2])})
	}
	return m, nil
}

// sp500Membership downloads the list, falling back to the copy saved at the
// last successful training.
func (t *Trainer) sp500Membership(ctx context.Context) (Membership, error) {
	m, err := fetchSP500(ctx)
	if err == nil {
		if t.Models != nil {
			_ = t.Models.SaveModel(ctx, "sp500-membership", "meta", time.Now().UTC(), m)
		}
		return m, nil
	}
	if t.Models != nil {
		if all, lerr := t.Models.LoadModels(ctx); lerr == nil {
			for _, b := range all["meta"] {
				var saved Membership
				if json.Unmarshal(b, &saved) == nil && len(saved) > 100 {
					t.Log.Warn("sp500 membership download failed; using saved copy", "err", err)
					return saved, nil
				}
			}
		}
	}
	return nil, err
}

// symbolsSince lists tickers that were members at any time on or after date.
func (m Membership) symbolsSince(date string) []string {
	var out []string
	for sym, ivs := range m {
		for _, iv := range ivs {
			if iv[1] == "" || iv[1] >= date {
				out = append(out, sym)
				break
			}
		}
	}
	return out
}

// intervalsFor returns membership ranges that the price history actually
// covers. A range starting well before the history does is dropped: the
// ticker was most likely reused by a different company.
func (m Membership) intervalsFor(sym, firstBar string) [][2]string {
	var out [][2]string
	for _, iv := range m[sym] {
		if firstBar > addDaysStr(iv[0], 30) {
			if iv[1] != "" && iv[1] < firstBar {
				continue
			}
			// Partially covered: only trust it from the first bar if the
			// history began within a year of joining (late data, same firm).
			if firstBar > addDaysStr(iv[0], 365) {
				continue
			}
			iv[0] = firstBar
		}
		out = append(out, iv)
	}
	return out
}

// SP500 returns the point-in-time S&P 500 membership (downloaded, or the
// copy saved at the last training).
func (t *Trainer) SP500(ctx context.Context) (Membership, error) { return t.sp500Membership(ctx) }

// SymbolsSince lists tickers that were members at any time on or after date.
func (m Membership) SymbolsSince(date string) []string { return m.symbolsSince(date) }

// MemberOn reports whether sym was in the index on date.
func (m Membership) MemberOn(sym, date string) bool {
	for _, iv := range m[sym] {
		if iv[0] <= date && (iv[1] == "" || iv[1] >= date) {
			return true
		}
	}
	return false
}
