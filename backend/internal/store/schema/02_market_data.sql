-- Market data kept locally: the daily price cache, the earnings calendar and
-- company fundamentals.

CREATE TABLE IF NOT EXISTS price_series (
  symbol     TEXT PRIMARY KEY,
  provider   TEXT NOT NULL,
  first_date DATE,
  last_date  DATE,
  n_bars     INTEGER NOT NULL,
  meta       JSONB NOT NULL,
  bars       JSONB NOT NULL,
  fetched_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS earnings (
  symbol   TEXT NOT NULL,
  date     DATE NOT NULL,
  time     TEXT NOT NULL,
  eps      DOUBLE PRECISION,
  forecast DOUBLE PRECISION,
  surprise DOUBLE PRECISION,
  fiscal   TEXT,
  PRIMARY KEY(symbol, date)
);
CREATE INDEX IF NOT EXISTS earnings_date ON earnings(date);

CREATE TABLE IF NOT EXISTS earnings_days (
  date       DATE PRIMARY KEY,
  fetched_at TIMESTAMPTZ NOT NULL,
  rows       INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS fundamentals (
  symbol     TEXT PRIMARY KEY,
  cik        INTEGER NOT NULL,
  fetched_at TIMESTAMPTZ NOT NULL,
  body       JSONB NOT NULL
);
