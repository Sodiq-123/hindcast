package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// Position is one paper trade in the journal. Kind is "asset" (a symbol the
// market service prices) or "token" (a DEX token contract address).
type Position struct {
	ID        int64    `json:"id"`
	Symbol    string   `json:"symbol"`
	Kind      string   `json:"kind"`
	Chain     string   `json:"chain,omitempty"`
	Name      string   `json:"name,omitempty"`
	OpenedAt  string   `json:"openedAt"`
	Entry     float64  `json:"entry"`
	Quantity  float64  `json:"quantity"`
	Stop      *float64 `json:"stop,omitempty"`
	Target    *float64 `json:"target,omitempty"`
	Signal    string   `json:"signal,omitempty"` // the platform's signal when opened
	Note      string   `json:"note,omitempty"`
	ClosedAt  *string  `json:"closedAt,omitempty"`
	Exit      *float64 `json:"exit,omitempty"`
	CreatedAt string   `json:"createdAt"`
	FeeRate   float64  `json:"feeRate"`            // fee per side, fixed when the trade was opened
	FeeOpen   float64  `json:"feeOpen"`            // fee paid on the buy
	FeeClose  *float64 `json:"feeClose,omitempty"` // fee paid on the sell
	Edge      string   `json:"edge,omitempty"`     // tested | untested | "" (unknown): was the signal a tested edge when opened
}

// Positions returns a person's journal.
func (s *Store) Positions(ctx context.Context, uid int64) ([]Position, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, symbol, kind, chain, name, opened_at::text, entry, quantity, stop, target, signal, note,
		closed_at::text, exit_price, `+tsText("created_at")+`, fee_rate, fee_open, fee_close, edge FROM positions WHERE user_id = $1 ORDER BY opened_at, id`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Position
	for rows.Next() {
		var p Position
		if err := rows.Scan(&p.ID, &p.Symbol, &p.Kind, &p.Chain, &p.Name, &p.OpenedAt, &p.Entry, &p.Quantity, &p.Stop, &p.Target,
			&p.Signal, &p.Note, &p.ClosedAt, &p.Exit, &p.CreatedAt, &p.FeeRate, &p.FeeOpen, &p.FeeClose, &p.Edge); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) AddPosition(ctx context.Context, uid int64, p *Position) error {
	return s.db.QueryRowContext(ctx, `INSERT INTO positions(user_id, symbol, kind, chain, name, opened_at, entry, quantity, stop, target, signal, note, fee_rate, fee_open, edge)
		VALUES($14,$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$15) RETURNING id`,
		p.Symbol, p.Kind, p.Chain, p.Name, p.OpenedAt, p.Entry, p.Quantity, p.Stop, p.Target, p.Signal, p.Note, p.FeeRate, p.FeeOpen, uid, p.Edge).Scan(&p.ID)
}

// ClosePosition, UpdatePositionPlan and DeletePosition only touch the
// person's own positions (another id reports ErrNotFound).
func (s *Store) ClosePosition(ctx context.Context, uid, id int64, date string, exit float64) error {
	res, err := s.db.ExecContext(ctx, `UPDATE positions SET closed_at = $1, exit_price = $2, fee_close = $2 * quantity * fee_rate
		WHERE id = $3 AND user_id = $4 AND closed_at IS NULL`, date, exit, id, uid)
	return affected(res, err)
}

func (s *Store) UpdatePositionPlan(ctx context.Context, uid, id int64, stop, target *float64) error {
	res, err := s.db.ExecContext(ctx, `UPDATE positions SET stop = $1, target = $2 WHERE id = $3 AND user_id = $4`, stop, target, id, uid)
	return affected(res, err)
}

func (s *Store) DeletePosition(ctx context.Context, uid, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM positions WHERE id = $1 AND user_id = $2`, id, uid)
	return affected(res, err)
}

func affected(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Setting loads a JSON setting into v; it reports false if it isn't set.
func (s *Store) Setting(ctx context.Context, key string, v any) (bool, error) {
	var body []byte
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = $1`, key).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, json.Unmarshal(body, v)
}

func (s *Store) SetSetting(ctx context.Context, key string, v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO settings(key, value) VALUES($1,$2) ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`, key, body)
	return err
}

// MarkAlert records that an alert was sent; it reports false if this key was
// already sent within the last `within` (so the alert is a duplicate).
func (s *Store) MarkAlert(ctx context.Context, key string, within time.Duration) (bool, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO alerts_sent(key, sent_at) VALUES($1, now())
		ON CONFLICT (key) DO UPDATE SET sent_at = now() WHERE alerts_sent.sent_at < $2`, key, time.Now().Add(-within))
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}
