package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/sodiq/financial-market/backend/internal/fundamentals"
)

func (s *Store) LoadFundamentals(ctx context.Context, symbol string) (*fundamentals.Company, error) {
	var body []byte
	err := s.db.QueryRowContext(ctx, `SELECT body FROM fundamentals WHERE symbol = $1`, symbol).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var c fundamentals.Company
	return &c, json.Unmarshal(body, &c)
}

func (s *Store) SaveFundamentals(ctx context.Context, c *fundamentals.Company) error {
	body, err := json.Marshal(c)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO fundamentals(symbol, cik, fetched_at, body) VALUES($1,$2,$3,$4)
		ON CONFLICT (symbol) DO UPDATE SET cik = EXCLUDED.cik, fetched_at = EXCLUDED.fetched_at, body = EXCLUDED.body`, c.Symbol, c.CIK, c.FetchedAt, body)
	return err
}
