// Package earnings maintains a local database of company earnings reports
// (date, timing, EPS, consensus and surprise) built from Nasdaq's public
// earnings calendar, and answers per-symbol questions from it.
//
// Nasdaq's calendar is queried one day at a time. A background backfill walks
// backwards from the near future to a configured start date (default 2010),
// newest first, and remembers which days are done so it resumes after a
// restart. Coverage is US-listed companies, including ADRs.
package earnings

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sodiq/financial-market/backend/internal/market"
)

// Event is one earnings report.
type Event struct {
	Symbol      string  `json:"symbol"`
	Date        string  `json:"date"` // report date YYYY-MM-DD
	Time        string  `json:"time"` // "pre" | "after" | "unknown"
	EPS         float64 `json:"eps"`  // NaN until reported
	Forecast    float64 `json:"forecast"`
	SurprisePct float64 `json:"surprisePct"` // NaN until reported / unknown
	Fiscal      string  `json:"fiscal"`
	Upcoming    bool    `json:"upcoming"`
}

// Tables (earnings, earnings_days) are created by the store's schema.

type Service struct {
	db   *sql.DB
	http *market.PoliteClient
	log  *slog.Logger
	from string // backfill start date

	mu   sync.Mutex
	prog Progress
}

// Progress describes the backfill.
type Progress struct {
	Running     bool   `json:"running"`
	DaysDone    int    `json:"daysDone"`
	DaysTotal   int    `json:"daysTotal"`
	CoveredFrom string `json:"coveredFrom"` // earliest date with unbroken coverage up to today
	Events      int    `json:"events"`
	LastError   string `json:"lastError,omitempty"`
}

func New(db *sql.DB, from string, log *slog.Logger) (*Service, error) {
	if from == "" {
		from = "2010-01-01"
	}
	s := &Service{db: db, http: market.NewPoliteClient(600 * time.Millisecond), log: log, from: from}
	s.refreshProgress(context.Background())
	return s, nil
}

func (s *Service) Progress() Progress {
	if s == nil {
		return Progress{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.prog
}

// NasdaqSymbol maps a Yahoo-style ticker to Nasdaq's (BRK-B → BRK.B).
// Non-US listings (with an exchange suffix) have no coverage.
func NasdaqSymbol(yahoo string) (string, bool) {
	s := strings.ToUpper(yahoo)
	if strings.ContainsAny(s, "^=") || market.IsCrypto(s) || strings.Contains(s, ".") {
		return "", false
	}
	return strings.ReplaceAll(s, "-", "."), true
}

// -------------------------------------------------------------- fetching

type nasdaqResp struct {
	Data struct {
		Rows []map[string]string `json:"rows"`
	} `json:"data"`
}

func (s *Service) fetchDay(ctx context.Context, day string) ([]Event, error) {
	body, err := s.http.Get(ctx, "https://api.nasdaq.com/api/calendar/earnings?date="+day, map[string]string{"Accept": "application/json"})
	if err != nil {
		return nil, err
	}
	var r nasdaqResp
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("nasdaq decode: %w", err)
	}
	upcoming := day >= time.Now().UTC().Format("2006-01-02")
	var out []Event
	for _, row := range r.Data.Rows {
		sym := strings.TrimSpace(row["symbol"])
		if sym == "" {
			continue
		}
		t := "unknown"
		switch row["time"] {
		case "time-pre-market":
			t = "pre"
		case "time-after-hours":
			t = "after"
		}
		out = append(out, Event{
			Symbol: sym, Date: day, Time: t,
			EPS: money(row["eps"]), Forecast: money(row["epsForecast"]), SurprisePct: num(row["surprise"]),
			Fiscal: row["fiscalQuarterEnding"], Upcoming: upcoming,
		})
	}
	return out, nil
}

func money(s string) float64 {
	s = strings.TrimSpace(s)
	neg := strings.HasPrefix(s, "(") || strings.HasPrefix(s, "-")
	s = strings.NewReplacer("$", "", "(", "", ")", "", ",", "", "-", "").Replace(s)
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return math.NaN()
	}
	if neg {
		v = -v
	}
	return v
}

func num(s string) float64 {
	v, err := strconv.ParseFloat(strings.TrimSpace(strings.ReplaceAll(s, ",", "")), 64)
	if err != nil {
		return math.NaN()
	}
	return v
}

func nullable(x float64) any {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return nil
	}
	return x
}

func (s *Service) saveDay(ctx context.Context, day string, evs []Event) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Upcoming days are re-fetched, and companies move dates: replace the day.
	if _, err := tx.ExecContext(ctx, `DELETE FROM earnings WHERE date = $1`, day); err != nil {
		return err
	}
	for _, e := range evs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO earnings(symbol, date, time, eps, forecast, surprise, fiscal) VALUES($1,$2,$3,$4,$5,$6,$7)
			ON CONFLICT (symbol, date) DO UPDATE SET time = EXCLUDED.time, eps = EXCLUDED.eps, forecast = EXCLUDED.forecast,
			  surprise = EXCLUDED.surprise, fiscal = EXCLUDED.fiscal`,
			e.Symbol, e.Date, e.Time, nullable(e.EPS), nullable(e.Forecast), nullable(e.SurprisePct), e.Fiscal); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO earnings_days(date, fetched_at, rows) VALUES($1,$2,$3)
		ON CONFLICT (date) DO UPDATE SET fetched_at = EXCLUDED.fetched_at, rows = EXCLUDED.rows`, day, time.Now().UTC(), len(evs)); err != nil {
		return err
	}
	return tx.Commit()
}

func weekdays(from, to time.Time) []string {
	var out []string
	for d := to; !d.Before(from); d = d.AddDate(0, 0, -1) {
		if d.Weekday() != time.Saturday && d.Weekday() != time.Sunday {
			out = append(out, d.Format("2006-01-02"))
		}
	}
	return out // newest first
}

// RefreshUpcoming re-fetches the next `ahead` days and the last week (dates
// move and results arrive). Cheap: ~45 requests.
func (s *Service) RefreshUpcoming(ctx context.Context, ahead int) error {
	now := time.Now().UTC()
	days := weekdays(now.AddDate(0, 0, -7), now.AddDate(0, 0, ahead))
	return s.fetchDays(ctx, days, 3)
}

// Backfill fetches every not-yet-fetched weekday from today back to the
// configured start date. Safe to call repeatedly; it resumes.
func (s *Service) Backfill(ctx context.Context) error {
	s.mu.Lock()
	if s.prog.Running {
		s.mu.Unlock()
		return nil
	}
	s.prog.Running = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.prog.Running = false
		s.mu.Unlock()
		s.refreshProgress(context.Background())
	}()
	from, err := time.Parse("2006-01-02", s.from)
	if err != nil {
		return err
	}
	done := map[string]bool{}
	rows, err := s.db.QueryContext(ctx, `SELECT date::text FROM earnings_days`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var d string
		_ = rows.Scan(&d)
		done[d] = true
	}
	rows.Close()
	var todo []string
	all := weekdays(from, time.Now().UTC())
	for _, d := range all {
		if !done[d] {
			todo = append(todo, d)
		}
	}
	s.mu.Lock()
	s.prog.DaysTotal, s.prog.DaysDone = len(all), len(all)-len(todo)
	s.mu.Unlock()
	return s.fetchDays(ctx, todo, 3)
}

func (s *Service) fetchDays(ctx context.Context, days []string, workers int) error {
	ch := make(chan string)
	var wg sync.WaitGroup
	var firstErr error
	var errMu sync.Mutex
	fails := 0
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for day := range ch {
				evs, err := s.fetchDay(ctx, day)
				if err == nil {
					err = s.saveDay(ctx, day, evs)
				}
				errMu.Lock()
				if err != nil {
					fails++
					if firstErr == nil {
						firstErr = err
					}
					s.mu.Lock()
					s.prog.LastError = fmt.Sprintf("%s: %v", day, err)
					s.mu.Unlock()
				} else {
					s.mu.Lock()
					s.prog.DaysDone++
					s.mu.Unlock()
				}
				errMu.Unlock()
			}
		}()
	}
	for i, d := range days {
		if ctx.Err() != nil {
			break
		}
		// Stop early if the provider is clearly refusing us; resume later.
		errMu.Lock()
		f := fails
		errMu.Unlock()
		if f > 25 && f > i/4 {
			break
		}
		ch <- d
		if i%100 == 99 {
			s.refreshProgress(ctx)
		}
	}
	close(ch)
	wg.Wait()
	return firstErr
}

// refreshProgress recomputes the unbroken coverage window: the earliest
// date D such that every weekday from D to today has been fetched.
func (s *Service) refreshProgress(ctx context.Context) {
	done := map[string]bool{}
	rows, err := s.db.QueryContext(ctx, `SELECT date::text FROM earnings_days`)
	if err != nil {
		return
	}
	for rows.Next() {
		var d string
		_ = rows.Scan(&d)
		done[d] = true
	}
	rows.Close()
	covered := ""
	from, _ := time.Parse("2006-01-02", s.from)
	// Allow a few missing days (exchange holidays sometimes fail) inside the window.
	misses := 0
	for _, d := range weekdays(from, time.Now().UTC()) {
		if done[d] {
			covered = d
			continue
		}
		misses++
		if misses > 15 {
			break
		}
	}
	var n int
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM earnings`).Scan(&n)
	s.mu.Lock()
	s.prog.CoveredFrom, s.prog.Events = covered, n
	s.mu.Unlock()
}

// CoveredFrom returns the earliest date of unbroken coverage ("" if none).
func (s *Service) CoveredFrom() string {
	if s == nil {
		return ""
	}
	return s.Progress().CoveredFrom
}

// -------------------------------------------------------------- queries

// Events returns every known report for a Yahoo symbol, oldest first, and
// whether the symbol is in Nasdaq's coverage at all.
func (s *Service) Events(ctx context.Context, yahoo string) ([]Event, bool) {
	sym, ok := NasdaqSymbol(yahoo)
	if !ok {
		return nil, false
	}
	rows, err := s.db.QueryContext(ctx, `SELECT symbol, date::text, time, eps, forecast, surprise, fiscal FROM earnings WHERE symbol = $1 ORDER BY date`, sym)
	if err != nil {
		return nil, true
	}
	defer rows.Close()
	today := time.Now().UTC().Format("2006-01-02")
	var out []Event
	for rows.Next() {
		var e Event
		var eps, fc, sp sql.NullFloat64
		var fiscal sql.NullString
		if err := rows.Scan(&e.Symbol, &e.Date, &e.Time, &eps, &fc, &sp, &fiscal); err != nil {
			continue
		}
		e.EPS, e.Forecast, e.SurprisePct = nf(eps), nf(fc), nf(sp)
		e.Fiscal = fiscal.String
		e.Upcoming = e.Date >= today && math.IsNaN(e.EPS)
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date < out[j].Date })
	return out, true
}

func nf(x sql.NullFloat64) float64 {
	if x.Valid {
		return x.Float64
	}
	return math.NaN()
}
