package store

import (
	"context"
	"strings"
	"time"

	"github.com/sodiq/financial-market/backend/internal/news"
)

// Judgments older than this are forgotten; headlines are collected over
// the last 7 days, so a month is ample.
const judgmentTTL = 30 * 24 * time.Hour

func (s *Store) LoadJudgments(ctx context.Context, symbol string) (map[string]news.Judgment, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT article_id, relevance, direction, impact, horizon, reasoning FROM news_judgments WHERE symbol = $1`, strings.ToUpper(symbol))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]news.Judgment{}
	for rows.Next() {
		var j news.Judgment
		if err := rows.Scan(&j.ID, &j.Relevance, &j.Direction, &j.Impact, &j.Horizon, &j.Reasoning); err != nil {
			return nil, err
		}
		out[j.ID] = j
	}
	return out, rows.Err()
}

func (s *Store) SaveJudgments(ctx context.Context, symbol, model string, js []news.Judgment) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	at := now()
	for _, j := range js {
		if _, err := tx.ExecContext(ctx, `INSERT INTO news_judgments(symbol, article_id, relevance, direction, impact, horizon, reasoning, model, judged_at)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)
			ON CONFLICT (symbol, article_id) DO UPDATE SET relevance = EXCLUDED.relevance, direction = EXCLUDED.direction, impact = EXCLUDED.impact,
			  horizon = EXCLUDED.horizon, reasoning = EXCLUDED.reasoning, model = EXCLUDED.model, judged_at = EXCLUDED.judged_at`,
			strings.ToUpper(symbol), j.ID, j.Relevance, j.Direction, j.Impact, j.Horizon, j.Reasoning, model, at); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM news_judgments WHERE judged_at < $1`, now().Add(-judgmentTTL)); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) RecordUsage(ctx context.Context, symbol string, u news.Usage) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO llm_usage(at, symbol, model, batch, input, cache_write, cache_read, output, cost_usd) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		now(), strings.ToUpper(symbol), u.Model, u.Batch, u.Input, u.CacheWrite, u.CacheRead, u.Output, u.CostUSD)
	return err
}

// UsageSummary is the Claude spend shown in the UI.
type UsageSummary struct {
	TodayUSD      float64 `json:"todayUsd"`
	Last7USD      float64 `json:"last7Usd"`
	Last30USD     float64 `json:"last30Usd"`
	Calls30       int     `json:"calls30"`
	BatchShare30  float64 `json:"batchShare30"` // share of calls run through the Batch API
	AvgPerCallUSD float64 `json:"avgPerCallUsd"`
	AvgInput      float64 `json:"avgInput"`
	AvgOutput     float64 `json:"avgOutput"`
}

func (s *Store) UsageSummary(ctx context.Context) (UsageSummary, error) {
	var u UsageSummary
	t := now()
	day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	_ = s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(cost_usd),0) FROM llm_usage WHERE at >= $1`, day).Scan(&u.TodayUSD)
	_ = s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(cost_usd),0) FROM llm_usage WHERE at >= $1`, t.Add(-7*24*time.Hour)).Scan(&u.Last7USD)
	var batch int
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(cost_usd),0), COUNT(*), COUNT(*) FILTER (WHERE batch),
		COALESCE(AVG(input+cache_write+cache_read),0), COALESCE(AVG(output),0) FROM llm_usage WHERE at >= $1`, t.Add(-30*24*time.Hour)).
		Scan(&u.Last30USD, &u.Calls30, &batch, &u.AvgInput, &u.AvgOutput)
	if u.Calls30 > 0 {
		u.BatchShare30 = float64(batch) / float64(u.Calls30)
		u.AvgPerCallUSD = u.Last30USD / float64(u.Calls30)
	}
	return u, err
}
