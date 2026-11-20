-- Meme-token safety reports.

CREATE TABLE IF NOT EXISTS token_reports (
  id         BIGSERIAL PRIMARY KEY,
  address    TEXT NOT NULL,
  chain      TEXT NOT NULL,
  symbol     TEXT NOT NULL,
  verdict    TEXT NOT NULL,
  risk_score INTEGER NOT NULL,
  created_at TIMESTAMPTZ NOT NULL,
  body       JSONB NOT NULL
);
CREATE INDEX IF NOT EXISTS token_reports_addr ON token_reports(address, id DESC);
