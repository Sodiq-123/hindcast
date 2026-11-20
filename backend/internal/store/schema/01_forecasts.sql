-- Forecasts: every analysis, each horizon's prediction (resolved later for the
-- live track record), scanner results, alerts already sent and the trained
-- cross-asset models.

CREATE TABLE IF NOT EXISTS reports (
  id         BIGSERIAL PRIMARY KEY,
  symbol     TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL,
  as_of      DATE NOT NULL,
  headline   TEXT NOT NULL,
  body       JSONB NOT NULL
);
CREATE INDEX IF NOT EXISTS reports_symbol ON reports(symbol, id DESC);

CREATE TABLE IF NOT EXISTS predictions (
  id              BIGSERIAL PRIMARY KEY,
  report_id       BIGINT NOT NULL REFERENCES reports(id) ON DELETE CASCADE,
  symbol          TEXT NOT NULL,
  class           TEXT NOT NULL,
  horizon         TEXT NOT NULL,
  bars            INTEGER NOT NULL,
  created_at      TIMESTAMPTZ NOT NULL,
  as_of           DATE NOT NULL,
  target_date     DATE NOT NULL,
  price           DOUBLE PRECISION NOT NULL,
  p_up            DOUBLE PRECISION NOT NULL,
  p_model         DOUBLE PRECISION NOT NULL,
  news_shift      DOUBLE PRECISION NOT NULL,
  signal          TEXT NOT NULL,
  status          TEXT NOT NULL,
  conviction      INTEGER NOT NULL,
  expected_return DOUBLE PRECISION NOT NULL,
  used_llm        BOOLEAN NOT NULL,
  resolved        BOOLEAN NOT NULL DEFAULT false,
  resolved_at     TIMESTAMPTZ,
  exit_date       DATE,
  realized_return DOUBLE PRECISION
);
CREATE INDEX IF NOT EXISTS predictions_open ON predictions(resolved, target_date);
CREATE INDEX IF NOT EXISTS predictions_symbol ON predictions(symbol, created_at);
ALTER TABLE predictions ADD COLUMN IF NOT EXISTS base_rate DOUBLE PRECISION;

CREATE TABLE IF NOT EXISTS scans (
  id         BIGSERIAL PRIMARY KEY,
  created_at TIMESTAMPTZ NOT NULL,
  body       JSONB NOT NULL
);

CREATE TABLE IF NOT EXISTS alerts_sent (
  key     TEXT PRIMARY KEY,
  sent_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS pooled_models (
  name       TEXT PRIMARY KEY,
  kind       TEXT NOT NULL,
  trained_at TIMESTAMPTZ NOT NULL,
  body       JSONB NOT NULL
);
