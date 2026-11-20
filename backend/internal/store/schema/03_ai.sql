-- Claude: headline judgments (reused instead of asked again), what each call
-- cost, and deep-research reports.

CREATE TABLE IF NOT EXISTS news_judgments (
  symbol     TEXT NOT NULL,
  article_id TEXT NOT NULL,
  relevance  DOUBLE PRECISION NOT NULL,
  direction  DOUBLE PRECISION NOT NULL,
  impact     TEXT NOT NULL,
  horizon    TEXT NOT NULL,
  reasoning  TEXT NOT NULL,
  model      TEXT NOT NULL,
  judged_at  TIMESTAMPTZ NOT NULL,
  PRIMARY KEY(symbol, article_id)
);

CREATE TABLE IF NOT EXISTS llm_usage (
  id          BIGSERIAL PRIMARY KEY,
  at          TIMESTAMPTZ NOT NULL,
  symbol      TEXT NOT NULL,
  model       TEXT NOT NULL,
  batch       BOOLEAN NOT NULL,
  input       BIGINT NOT NULL,
  cache_write BIGINT NOT NULL,
  cache_read  BIGINT NOT NULL,
  output      BIGINT NOT NULL,
  cost_usd    DOUBLE PRECISION NOT NULL
);
CREATE INDEX IF NOT EXISTS llm_usage_at ON llm_usage(at);

CREATE TABLE IF NOT EXISTS research_reports (
  id         BIGSERIAL PRIMARY KEY,
  subject    TEXT NOT NULL,
  kind       TEXT NOT NULL,
  name       TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL,
  price      DOUBLE PRECISION NOT NULL,
  cost_usd   DOUBLE PRECISION NOT NULL,
  body       JSONB NOT NULL
);
CREATE INDEX IF NOT EXISTS research_reports_subject ON research_reports(subject, id DESC);
