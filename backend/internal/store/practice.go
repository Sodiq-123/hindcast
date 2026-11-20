package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// PracticeRound is one practice round: a real past day, hidden until the
// person decides. Body holds the prepared round (the chart, what came after,
// the setup), App what the app's analysis said that day (nil while it is
// still being worked out).
type PracticeRound struct {
	ID        int64      `json:"id"`
	Set       int        `json:"set"`
	N         int        `json:"n"`
	Symbol    string     `json:"symbol"`
	Day       string     `json:"day"`
	Body      []byte     `json:"-"`
	App       []byte     `json:"-"`
	Call      string     `json:"call"` // "" (waiting) | buy | skip
	StopPct   float64    `json:"stopPct"`
	TargetPct float64    `json:"targetPct"`
	Result    string     `json:"result"` // target | stop | time (what happened to the plan, bought or not)
	ExitBar   int        `json:"exitBar"`
	PnL       float64    `json:"pnl"` // on the stake, after fees (had they bought, for a skip)
	PnLPct    float64    `json:"pnlPct"`
	CreatedAt time.Time  `json:"createdAt"`
	DecidedAt *time.Time `json:"decidedAt,omitempty"`
}

const roundCols = `id, set_no, n, symbol, day, body, app, call, stop_pct, target_pct, result, exit_bar, pnl, pnl_pct, created_at, decided_at`

// PracticeRounds returns a person's rounds, oldest first.
func (s *Store) PracticeRounds(ctx context.Context, uid int64) ([]PracticeRound, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+roundCols+` FROM practice_rounds WHERE user_id = $1 ORDER BY set_no, n`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PracticeRound{}
	for rows.Next() {
		var r PracticeRound
		if err := rows.Scan(&r.ID, &r.Set, &r.N, &r.Symbol, &r.Day, &r.Body, &r.App, &r.Call, &r.StopPct, &r.TargetPct,
			&r.Result, &r.ExitBar, &r.PnL, &r.PnLPct, &r.CreatedAt, &r.DecidedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// AddPracticeRound stores a prepared round.
func (s *Store) AddPracticeRound(ctx context.Context, uid int64, r *PracticeRound) error {
	return s.db.QueryRowContext(ctx, `INSERT INTO practice_rounds(user_id, set_no, n, symbol, day, body) VALUES($1,$2,$3,$4,$5,$6) RETURNING id, created_at`,
		uid, r.Set, r.N, r.Symbol, r.Day, r.Body).Scan(&r.ID, &r.CreatedAt)
}

// SetPracticeApp stores what the app said on a round's day.
func (s *Store) SetPracticeApp(ctx context.Context, uid, id int64, app []byte) error {
	res, err := s.db.ExecContext(ctx, `UPDATE practice_rounds SET app = $3 WHERE user_id = $1 AND id = $2`, uid, id, app)
	return affected(res, err)
}

// DecidePracticeRound stores the person's call and how it turned out. It
// fails with ErrNotFound if the round was already decided.
func (s *Store) DecidePracticeRound(ctx context.Context, uid int64, r *PracticeRound) error {
	err := s.db.QueryRowContext(ctx, `UPDATE practice_rounds SET call = $3, stop_pct = $4, target_pct = $5, result = $6, exit_bar = $7,
		pnl = $8, pnl_pct = $9, decided_at = now() WHERE user_id = $1 AND id = $2 AND call = '' RETURNING decided_at`,
		uid, r.ID, r.Call, r.StopPct, r.TargetPct, r.Result, r.ExitBar, r.PnL, r.PnLPct).Scan(&r.DecidedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

// DeletePracticeRounds deletes a person's practice rounds (all, or only the
// undecided ones of a set left unfinished).
func (s *Store) DeletePracticeRounds(ctx context.Context, uid int64, undecidedOnly bool) error {
	q := `DELETE FROM practice_rounds WHERE user_id = $1`
	if undecidedOnly {
		q += ` AND call = ''`
	}
	_, err := s.db.ExecContext(ctx, q, uid)
	return err
}
