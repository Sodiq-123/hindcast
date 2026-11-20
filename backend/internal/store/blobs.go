package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/sodiq/financial-market/backend/internal/market"
)

// ------------------------------------------------------------ price cache

// PriceCache stores downloaded price histories in the price_series table.
// It implements market.Cache. Bars are kept as one JSONB array per symbol
// because providers re-adjust the whole history after dividends and splits,
// so the series is always replaced as a unit.
type PriceCache struct{ s *Store }

func (s *Store) PriceCache() *PriceCache { return &PriceCache{s} }

func (c *PriceCache) Get(key string, maxAge time.Duration) (*market.Series, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var meta, bars []byte
	var fetched time.Time
	err := c.s.db.QueryRowContext(ctx, `SELECT meta, bars, fetched_at FROM price_series WHERE symbol = $1`, key).Scan(&meta, &bars, &fetched)
	if err != nil {
		return nil, false
	}
	ser := &market.Series{FetchedAt: fetched}
	if json.Unmarshal(meta, &ser.Meta) != nil || json.Unmarshal(bars, &ser.Bars) != nil || len(ser.Bars) == 0 {
		return nil, false
	}
	if maxAge > 0 && time.Since(fetched) > maxAge {
		return ser, false // stale, but returned so callers can fall back to it
	}
	return ser, true
}

func (c *PriceCache) Put(key string, ser *market.Series) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	meta, err := json.Marshal(ser.Meta)
	if err != nil {
		return err
	}
	bars, err := json.Marshal(ser.Bars)
	if err != nil {
		return err
	}
	var first, last any
	if n := len(ser.Bars); n > 0 {
		first, last = ser.Bars[0].Date, ser.Bars[n-1].Date
	}
	_, err = c.s.db.ExecContext(ctx, `INSERT INTO price_series(symbol, provider, first_date, last_date, n_bars, meta, bars, fetched_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT (symbol) DO UPDATE SET provider = EXCLUDED.provider, first_date = EXCLUDED.first_date, last_date = EXCLUDED.last_date,
		  n_bars = EXCLUDED.n_bars, meta = EXCLUDED.meta, bars = EXCLUDED.bars, fetched_at = EXCLUDED.fetched_at`,
		key, ser.Meta.Provider, first, last, len(ser.Bars), meta, bars, ser.FetchedAt)
	return err
}

// ------------------------------------------------------------ pooled models

// SaveModel stores a trained cross-asset model (pooled.ModelStore).
func (s *Store) SaveModel(ctx context.Context, name, kind string, trainedAt time.Time, v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO pooled_models(name, kind, trained_at, body) VALUES($1,$2,$3,$4)
		ON CONFLICT (name) DO UPDATE SET kind = EXCLUDED.kind, trained_at = EXCLUDED.trained_at, body = EXCLUDED.body`,
		name, kind, trainedAt, body)
	return err
}

// LoadModels returns every stored model body by kind ("dir" | "rank").
func (s *Store) LoadModels(ctx context.Context) (map[string][][]byte, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT kind, body FROM pooled_models`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][][]byte{}
	for rows.Next() {
		var kind string
		var body []byte
		if err := rows.Scan(&kind, &body); err != nil {
			return nil, err
		}
		out[kind] = append(out[kind], body)
	}
	if err := rows.Err(); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	return out, nil
}
