package store

import (
	"context"
	"os"
	"testing"
)

// OpenTest opens the test database (TEST_DATABASE_URL, default the local
// market_oracle_test database) with every table emptied. It skips the test
// when Postgres isn't reachable.
func OpenTest(t testing.TB) *Store {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		url = "postgres://localhost:5432/market_oracle_test?sslmode=disable"
	}
	st, err := Open(context.Background(), url)
	if err != nil {
		t.Skipf("Postgres test database unavailable (%v); set TEST_DATABASE_URL", err)
	}
	if _, err := st.db.Exec(`TRUNCATE watchlist, reports, predictions, scans, earnings, earnings_days, news_judgments,
		llm_usage, price_series, pooled_models, token_reports, positions, settings, alerts_sent, research_reports, fundamentals,
		users, sessions, invites, password_resets, user_profiles, user_usage, user_prefs, user_steps, user_events, user_suggestions, practice_rounds RESTART IDENTITY CASCADE`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}
