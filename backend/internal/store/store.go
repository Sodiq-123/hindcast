// Package store persists everything the app keeps in PostgreSQL: reports,
// individual predictions (for the live track record), the watchlist, scans,
// earnings history, Claude's headline judgments and usage, the price-history
// cache and the trained cross-asset models.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/sodiq/financial-market/backend/internal/engine"
)

type Store struct {
	db *sql.DB
}

// Open connects (e.g. postgres://localhost:5432/market_oracle?sslmode=disable)
// and applies the schema.
func Open(ctx context.Context, databaseURL string) (*Store, error) {
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(10)
	db.SetConnMaxIdleTime(5 * time.Minute)
	pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := db.PingContext(pctx); err != nil {
		db.Close()
		return nil, err
	}
	if err := applySchema(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// DB exposes the connection for packages that query their own tables (earnings).
func (s *Store) DB() *sql.DB { return s.db }

func now() time.Time { return time.Now().UTC() }

// tsText renders a TIMESTAMPTZ column as an RFC 3339 UTC string.
func tsText(col string) string {
	return "to_char(" + col + ` AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')`
}

// ------------------------------------------------------------------ watchlist

// MaxWatch caps each person's watchlist: every symbol on any list is
// re-analysed at each scheduled run.
const MaxWatch = 60

var ErrWatchFull = fmt.Errorf("your watchlist is full (%d items); remove something first", MaxWatch)

// Watchlist returns a person's assets (not tokens), oldest first.
func (s *Store) Watchlist(ctx context.Context, uid int64) ([]string, error) {
	return s.symbols(ctx, `SELECT symbol FROM watchlist WHERE user_id = $1 AND kind = 'asset' ORDER BY added_at, symbol`, uid)
}

// AllWatched returns every asset on anyone's watchlist (for scheduled runs).
func (s *Store) AllWatched(ctx context.Context) ([]string, error) {
	return s.symbols(ctx, `SELECT symbol FROM watchlist WHERE kind = 'asset' GROUP BY symbol ORDER BY min(added_at), symbol`)
}

func (s *Store) symbols(ctx context.Context, q string, args ...any) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var sym string
		if err := rows.Scan(&sym); err != nil {
			return nil, err
		}
		out = append(out, sym)
	}
	return out, rows.Err()
}

func (s *Store) watchCount(ctx context.Context, uid int64) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM watchlist WHERE user_id = $1`, uid).Scan(&n)
	return n, err
}

func (s *Store) AddWatch(ctx context.Context, uid int64, symbol string) error {
	return s.addWatch(ctx, uid, strings.ToUpper(symbol), "asset", "")
}

func (s *Store) AddWatchToken(ctx context.Context, uid int64, address, chain string) error {
	return s.addWatch(ctx, uid, address, "token", chain)
}

func (s *Store) addWatch(ctx context.Context, uid int64, symbol, kind, chain string) error {
	if n, err := s.watchCount(ctx, uid); err != nil {
		return err
	} else if n >= MaxWatch {
		return ErrWatchFull
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO watchlist(user_id, symbol, kind, chain, added_at) VALUES($1, $2, $3, $4, $5)
		ON CONFLICT (user_id, symbol) DO NOTHING`, uid, symbol, kind, chain, now())
	return err
}

// RemoveWatch removes an asset symbol or a token address (addresses are
// case-sensitive on Solana, so tokens match exactly).
func (s *Store) RemoveWatch(ctx context.Context, uid int64, symbol string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM watchlist WHERE user_id = $1 AND ((kind = 'asset' AND symbol = upper($2)) OR (kind = 'token' AND symbol = $2))`, uid, symbol)
	return err
}

// WatchToken is a DEX token on the watchlist.
type WatchToken struct {
	Address string `json:"address"`
	Chain   string `json:"chain"`
	AddedAt string `json:"addedAt"`
}

// WatchTokens returns a person's tokens; AllWatchTokens everyone's (once each).
func (s *Store) WatchTokens(ctx context.Context, uid int64) ([]WatchToken, error) {
	return s.watchTokens(ctx, `SELECT symbol, chain, `+tsText("added_at")+` FROM watchlist WHERE user_id = $1 AND kind = 'token' ORDER BY added_at, symbol`, uid)
}

func (s *Store) AllWatchTokens(ctx context.Context) ([]WatchToken, error) {
	return s.watchTokens(ctx, `SELECT symbol, min(chain), `+tsText("min(added_at)")+` FROM watchlist WHERE kind = 'token' GROUP BY symbol ORDER BY min(added_at), symbol`)
}

func (s *Store) watchTokens(ctx context.Context, q string, args ...any) ([]WatchToken, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WatchToken
	for rows.Next() {
		var t WatchToken
		if err := rows.Scan(&t.Address, &t.Chain, &t.AddedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// -------------------------------------------------------------------- reports

// SaveReport stores the full report and one prediction row per available
// horizon. Predictions are immutable once written: the track record must be
// exactly what the system said at the time.
func (s *Store) SaveReport(ctx context.Context, r *engine.Report) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var id int64
	if err := tx.QueryRowContext(ctx, `SELECT nextval(pg_get_serial_sequence('reports', 'id'))`).Scan(&id); err != nil {
		return err
	}
	r.ID = id
	body, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO reports(id, symbol, created_at, as_of, headline, body) VALUES($1,$2,$3,$4,$5,$6)`,
		id, r.Symbol, r.GeneratedAt, r.AsOf, r.Headline, body); err != nil {
		return err
	}
	for _, h := range r.Horizons {
		if !h.Available {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO predictions(report_id, symbol, class, horizon, bars, created_at, as_of, target_date, price,
			p_up, p_model, news_shift, signal, status, conviction, expected_return, used_llm, base_rate) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)`,
			id, r.Symbol, r.Class, h.Key, h.Bars, r.GeneratedAt, r.AsOf, h.TargetDate, r.Price,
			h.PUp, h.PModel, h.NewsShift, h.Signal, h.Status, h.Conviction, h.ExpectedReturn, r.UsedLLM, h.BaseRate); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) LatestReport(ctx context.Context, symbol string) (*engine.Report, error) {
	return s.reportWhere(ctx, `symbol = $1 ORDER BY id DESC LIMIT 1`, strings.ToUpper(symbol))
}

func (s *Store) Report(ctx context.Context, id int64) (*engine.Report, error) {
	return s.reportWhere(ctx, `id = $1`, id)
}

var ErrNotFound = errors.New("not found")

func (s *Store) reportWhere(ctx context.Context, where string, args ...any) (*engine.Report, error) {
	var body []byte
	err := s.db.QueryRowContext(ctx, `SELECT body FROM reports WHERE `+where, args...).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var r engine.Report
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

type ReportMeta struct {
	ID        int64  `json:"id"`
	Symbol    string `json:"symbol"`
	CreatedAt string `json:"createdAt"`
	AsOf      string `json:"asOf"`
	Headline  string `json:"headline"`
}

func (s *Store) ReportHistory(ctx context.Context, symbol string, limit int) ([]ReportMeta, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, symbol, `+tsText("created_at")+`, as_of::text, headline FROM reports WHERE symbol = $1 ORDER BY id DESC LIMIT $2`, strings.ToUpper(symbol), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ReportMeta
	for rows.Next() {
		var m ReportMeta
		if err := rows.Scan(&m.ID, &m.Symbol, &m.CreatedAt, &m.AsOf, &m.Headline); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ----------------------------------------------------------------- predictions

type Prediction struct {
	ID             int64    `json:"id"`
	ReportID       int64    `json:"reportId"`
	Symbol         string   `json:"symbol"`
	Class          string   `json:"class"`
	Horizon        string   `json:"horizon"`
	Bars           int      `json:"bars"`
	CreatedAt      string   `json:"createdAt"`
	AsOf           string   `json:"asOf"`
	TargetDate     string   `json:"targetDate"`
	Price          float64  `json:"price"`
	PUp            float64  `json:"pUp"`
	PModel         float64  `json:"pModel"`
	NewsShift      float64  `json:"newsShift"`
	Signal         string   `json:"signal"`
	Status         string   `json:"status"`
	Conviction     int      `json:"conviction"`
	ExpectedReturn float64  `json:"expectedReturn"`
	UsedLLM        bool     `json:"usedLlm"`
	Resolved       bool     `json:"resolved"`
	ExitDate       *string  `json:"exitDate,omitempty"`
	RealizedReturn *float64 `json:"realizedReturn,omitempty"`
	BaseRate       *float64 `json:"baseRate,omitempty"` // historical up-rate the model knew when forecasting
}

var predCols = `id, report_id, symbol, class, horizon, bars, ` + tsText("created_at") + `, as_of::text, target_date::text, price, p_up, p_model, news_shift,
	signal, status, conviction, expected_return, used_llm, resolved, exit_date::text, realized_return, base_rate`

func scanPred(rows *sql.Rows) (Prediction, error) {
	var p Prediction
	err := rows.Scan(&p.ID, &p.ReportID, &p.Symbol, &p.Class, &p.Horizon, &p.Bars, &p.CreatedAt, &p.AsOf, &p.TargetDate, &p.Price,
		&p.PUp, &p.PModel, &p.NewsShift, &p.Signal, &p.Status, &p.Conviction, &p.ExpectedReturn, &p.UsedLLM, &p.Resolved, &p.ExitDate, &p.RealizedReturn, &p.BaseRate)
	return p, err
}

func (s *Store) queryPreds(ctx context.Context, q string, args ...any) ([]Prediction, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+predCols+` FROM predictions `+q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Prediction
	for rows.Next() {
		p, err := scanPred(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// DuePredictions returns unresolved predictions whose target date has passed.
func (s *Store) DuePredictions(ctx context.Context, today string) ([]Prediction, error) {
	return s.queryPreds(ctx, `WHERE NOT resolved AND target_date <= $1 ORDER BY symbol`, today)
}

func (s *Store) ResolvePrediction(ctx context.Context, id int64, exitDate string, realized float64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE predictions SET resolved = true, resolved_at = $1, exit_date = $2, realized_return = $3 WHERE id = $4`, now(), exitDate, realized, id)
	return err
}

func (s *Store) ResolvedPredictions(ctx context.Context) ([]Prediction, error) {
	return s.queryPreds(ctx, `WHERE resolved ORDER BY created_at DESC`)
}

func (s *Store) OpenPredictions(ctx context.Context, limit int) ([]Prediction, error) {
	return s.queryPreds(ctx, `WHERE NOT resolved ORDER BY created_at DESC LIMIT $1`, limit)
}

// ---------------------------------------------------------------------- scans

func (s *Store) SaveScan(ctx context.Context, r *engine.ScanResult) error {
	body, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return s.db.QueryRowContext(ctx, `INSERT INTO scans(created_at, body) VALUES($1, $2) RETURNING id`, now(), body).Scan(&r.ID)
}

func (s *Store) LatestScan(ctx context.Context) (*engine.ScanResult, error) {
	var body []byte
	var id int64
	err := s.db.QueryRowContext(ctx, `SELECT id, body FROM scans ORDER BY id DESC LIMIT 1`).Scan(&id, &body)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var r engine.ScanResult
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, err
	}
	r.ID = id
	return &r, nil
}
