-- Watchlists and the paper-trading journal, plus key-value settings (journal
-- rules among them). Their owner columns are added with the accounts.

CREATE TABLE IF NOT EXISTS watchlist (
  symbol   TEXT NOT NULL,
  added_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
ALTER TABLE watchlist ADD COLUMN IF NOT EXISTS kind TEXT NOT NULL DEFAULT 'asset';
ALTER TABLE watchlist ADD COLUMN IF NOT EXISTS chain TEXT NOT NULL DEFAULT '';

CREATE TABLE IF NOT EXISTS positions (
  id         BIGSERIAL PRIMARY KEY,
  symbol     TEXT NOT NULL,
  kind       TEXT NOT NULL,
  chain      TEXT NOT NULL DEFAULT '',
  name       TEXT NOT NULL DEFAULT '',
  opened_at  DATE NOT NULL,
  entry      DOUBLE PRECISION NOT NULL,
  quantity   DOUBLE PRECISION NOT NULL,
  stop       DOUBLE PRECISION,
  target     DOUBLE PRECISION,
  signal     TEXT NOT NULL DEFAULT '',
  note       TEXT NOT NULL DEFAULT '',
  closed_at  DATE,
  exit_price DOUBLE PRECISION,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE positions ADD COLUMN IF NOT EXISTS fee_rate DOUBLE PRECISION NOT NULL DEFAULT 0;
ALTER TABLE positions ADD COLUMN IF NOT EXISTS fee_open DOUBLE PRECISION NOT NULL DEFAULT 0;
ALTER TABLE positions ADD COLUMN IF NOT EXISTS fee_close DOUBLE PRECISION;

-- Whether the signal of a trade was a tested edge when it was opened: tested, untested or empty (unknown).
ALTER TABLE positions ADD COLUMN IF NOT EXISTS edge TEXT NOT NULL DEFAULT '';

CREATE TABLE IF NOT EXISTS settings (
  key   TEXT PRIMARY KEY,
  value JSONB NOT NULL
);
