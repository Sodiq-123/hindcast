package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/sodiq/financial-market/backend/internal/market"
	"github.com/sodiq/financial-market/backend/internal/store"
)

// importSQLite copies the old storage (data/market.db, data/cache/*.json,
// data/models/*.json) into Postgres. Safe to run more than once: existing
// rows are left alone.
func importSQLite(ctx context.Context, st *store.Store, dir string, log *slog.Logger) error {
	pg := st.DB()
	if path := filepath.Join(dir, "market.db"); fileExists(path) {
		lite, err := sql.Open("sqlite", path+"?mode=ro")
		if err != nil {
			return err
		}
		defer lite.Close()
		steps := []struct {
			name   string
			sel    string
			insert string
			cols   int
			seq    string
		}{
			{"watchlist", `SELECT symbol, added_at FROM watchlist`,
				`INSERT INTO watchlist(symbol, added_at) VALUES %s ON CONFLICT DO NOTHING`, 2, ""},
			{"reports", `SELECT id, symbol, created_at, as_of, headline, CAST(body AS TEXT) FROM reports`,
				`INSERT INTO reports(id, symbol, created_at, as_of, headline, body) VALUES %s ON CONFLICT DO NOTHING`, 6, "reports"},
			{"predictions", `SELECT id, report_id, symbol, class, horizon, bars, created_at, as_of, target_date, price, p_up, p_model, news_shift,
				signal, status, conviction, expected_return, used_llm = 1, resolved = 1, resolved_at, exit_date, realized_return FROM predictions`,
				`INSERT INTO predictions(id, report_id, symbol, class, horizon, bars, created_at, as_of, target_date, price, p_up, p_model, news_shift,
				signal, status, conviction, expected_return, used_llm, resolved, resolved_at, exit_date, realized_return) VALUES %s ON CONFLICT DO NOTHING`, 22, "predictions"},
			{"scans", `SELECT id, created_at, CAST(body AS TEXT) FROM scans`,
				`INSERT INTO scans(id, created_at, body) VALUES %s ON CONFLICT DO NOTHING`, 3, "scans"},
			{"earnings", `SELECT symbol, date, time, eps, forecast, surprise, fiscal FROM earnings`,
				`INSERT INTO earnings(symbol, date, time, eps, forecast, surprise, fiscal) VALUES %s ON CONFLICT DO NOTHING`, 7, ""},
			{"earnings_days", `SELECT date, fetched_at, rows FROM earnings_days`,
				`INSERT INTO earnings_days(date, fetched_at, rows) VALUES %s ON CONFLICT DO NOTHING`, 3, ""},
			{"news_judgments", `SELECT symbol, article_id, relevance, direction, impact, horizon, reasoning, model, judged_at FROM news_judgments`,
				`INSERT INTO news_judgments(symbol, article_id, relevance, direction, impact, horizon, reasoning, model, judged_at) VALUES %s ON CONFLICT DO NOTHING`, 9, ""},
			{"llm_usage", `SELECT at, symbol, model, batch = 1, input, cache_write, cache_read, output, cost_usd FROM llm_usage`,
				`INSERT INTO llm_usage(at, symbol, model, batch, input, cache_write, cache_read, output, cost_usd) VALUES %s`, 9, ""},
		}
		for _, stp := range steps {
			n, err := copyTable(ctx, lite, pg, stp.sel, stp.insert, stp.cols)
			if err != nil {
				if strings.Contains(err.Error(), "no such table") {
					log.Info("import: table not in old database, skipped", "table", stp.name)
					continue
				}
				return fmt.Errorf("%s: %w", stp.name, err)
			}
			if stp.seq != "" {
				if _, err := pg.ExecContext(ctx, fmt.Sprintf(`SELECT setval(pg_get_serial_sequence('%s','id'), COALESCE((SELECT MAX(id) FROM %s), 0) + 1, false)`, stp.seq, stp.seq)); err != nil {
					return err
				}
			}
			log.Info("import: copied", "table", stp.name, "rows", n)
		}
	} else {
		log.Info("import: no market.db found, skipping tables", "dir", dir)
	}

	// Price-history cache files.
	files, _ := filepath.Glob(filepath.Join(dir, "cache", "*.json"))
	cache := st.PriceCache()
	nCache := 0
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var ser market.Series
		if json.Unmarshal(b, &ser) != nil || len(ser.Bars) == 0 || ser.Meta.Symbol == "" {
			continue
		}
		if err := cache.Put(strings.ToUpper(ser.Meta.Symbol), &ser); err != nil {
			return fmt.Errorf("cache %s: %w", f, err)
		}
		nCache++
	}
	log.Info("import: copied price histories", "symbols", nCache)

	// Trained model files.
	models, _ := filepath.Glob(filepath.Join(dir, "models", "*.json"))
	nModels := 0
	for _, f := range models {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		name := strings.TrimSuffix(filepath.Base(f), ".json")
		kind, _, _ := strings.Cut(name, "-")
		var head struct {
			TrainedAt time.Time `json:"trainedAt"`
		}
		_ = json.Unmarshal(b, &head)
		if err := st.SaveModel(ctx, name, kind, head.TrainedAt, json.RawMessage(b)); err != nil {
			return fmt.Errorf("model %s: %w", name, err)
		}
		nModels++
	}
	log.Info("import: copied models", "models", nModels)
	fmt.Println("Import complete. The old files are untouched; delete the data folder once you're happy.")
	return nil
}

// copyTable streams rows from SQLite into Postgres in multi-row INSERTs.
func copyTable(ctx context.Context, from, to *sql.DB, sel, insert string, cols int) (int, error) {
	rows, err := from.QueryContext(ctx, sel)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	const chunk = 1000
	var args []any
	n, inChunk := 0, 0
	flush := func() error {
		if inChunk == 0 {
			return nil
		}
		var ph strings.Builder
		for r := 0; r < inChunk; r++ {
			if r > 0 {
				ph.WriteByte(',')
			}
			ph.WriteByte('(')
			for c := 0; c < cols; c++ {
				if c > 0 {
					ph.WriteByte(',')
				}
				fmt.Fprintf(&ph, "$%d", r*cols+c+1)
			}
			ph.WriteByte(')')
		}
		if _, err := to.ExecContext(ctx, fmt.Sprintf(insert, ph.String()), args...); err != nil {
			return err
		}
		args, inChunk = args[:0], 0
		return nil
	}
	for rows.Next() {
		vals := make([]any, cols)
		ptrs := make([]any, cols)
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return n, err
		}
		args = append(args, vals...)
		inChunk++
		n++
		if inChunk == chunk {
			if err := flush(); err != nil {
				return n, err
			}
		}
	}
	if err := rows.Err(); err != nil {
		return n, err
	}
	return n, flush()
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// redact hides a password in a database URL for logging.
func redact(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return raw
	}
	if _, has := u.User.Password(); has {
		u.User = url.UserPassword(u.User.Username(), "****")
	}
	return u.String()
}
