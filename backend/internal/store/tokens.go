package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/sodiq/financial-market/backend/internal/token"
)

func (s *Store) SaveTokenReport(ctx context.Context, r *token.Report) error {
	body, err := json.Marshal(r)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO token_reports(address, chain, symbol, verdict, risk_score, created_at, body) VALUES($1,$2,$3,$4,$5,$6,$7)`,
		r.Address, r.Chain, r.Symbol, r.Verdict, r.RiskScore, r.FetchedAt, body)
	return err
}

// LatestTokenReport returns the newest stored report younger than maxAge.
func (s *Store) LatestTokenReport(ctx context.Context, address string, maxAge time.Duration) (*token.Report, error) {
	var body []byte
	err := s.db.QueryRowContext(ctx, `SELECT body FROM token_reports WHERE lower(address) = lower($1) AND created_at > $2 ORDER BY id DESC LIMIT 1`,
		strings.TrimSpace(address), time.Now().Add(-maxAge)).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var r token.Report
	return &r, json.Unmarshal(body, &r)
}
