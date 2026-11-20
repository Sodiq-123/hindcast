-- Learning from what people do (step 2 of personalisation). Events are kept
-- 90 days. Everything here can be paused, listed and forgotten by the person.
CREATE TABLE IF NOT EXISTS user_events (
  id      BIGSERIAL PRIMARY KEY,
  user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  kind    TEXT NOT NULL,
  subject TEXT NOT NULL,
  at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS user_events_user ON user_events(user_id, kind, at);
CREATE TABLE IF NOT EXISTS user_suggestions (
  id           BIGSERIAL PRIMARY KEY,
  user_id      BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  kind         TEXT NOT NULL,
  subject      TEXT NOT NULL,
  body         JSONB NOT NULL,
  answer       TEXT NOT NULL DEFAULT '',
  prev         TEXT NOT NULL DEFAULT '',
  shown_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  answered_at  TIMESTAMPTZ,
  snooze_until TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS user_suggestions_user ON user_suggestions(user_id, shown_at);
ALTER TABLE user_prefs ADD COLUMN IF NOT EXISTS learn_paused BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE user_prefs ADD COLUMN IF NOT EXISTS reminders_muted TEXT NOT NULL DEFAULT '';

-- Practice rounds: decisions on hidden real past days.
CREATE TABLE IF NOT EXISTS practice_rounds (
  id          BIGSERIAL PRIMARY KEY,
  user_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  set_no      INT NOT NULL,
  n           INT NOT NULL,
  symbol      TEXT NOT NULL,
  day         TEXT NOT NULL,
  body        JSONB NOT NULL,
  app         JSONB,
  call        TEXT NOT NULL DEFAULT '',
  stop_pct    DOUBLE PRECISION NOT NULL DEFAULT 0,
  target_pct  DOUBLE PRECISION NOT NULL DEFAULT 0,
  result      TEXT NOT NULL DEFAULT '',
  exit_bar    INT NOT NULL DEFAULT 0,
  pnl         DOUBLE PRECISION NOT NULL DEFAULT 0,
  pnl_pct     DOUBLE PRECISION NOT NULL DEFAULT 0,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  decided_at  TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS practice_rounds_user ON practice_rounds(user_id, set_no, n);
