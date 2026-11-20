-- Accounts. Emails are unique ignoring case. Session, invite and reset
-- tokens are stored only as SHA-256 hashes, so a database leak can't be
-- used to sign in.
CREATE TABLE IF NOT EXISTS users (
  id            BIGSERIAL PRIMARY KEY,
  email         TEXT NOT NULL,
  name          TEXT NOT NULL DEFAULT '',
  password_hash TEXT NOT NULL,
  role          TEXT NOT NULL DEFAULT 'member',
  disabled      BOOLEAN NOT NULL DEFAULT false,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_login_at TIMESTAMPTZ
);
CREATE UNIQUE INDEX IF NOT EXISTS users_email ON users(lower(email));

CREATE TABLE IF NOT EXISTS sessions (
  id           BIGSERIAL PRIMARY KEY,
  token_hash   BYTEA NOT NULL UNIQUE,
  user_id      BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at   TIMESTAMPTZ NOT NULL,
  user_agent   TEXT NOT NULL DEFAULT '',
  ip           TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS sessions_user ON sessions(user_id);

CREATE TABLE IF NOT EXISTS invites (
  id         BIGSERIAL PRIMARY KEY,
  code_hash  BYTEA NOT NULL UNIQUE,
  note       TEXT NOT NULL DEFAULT '',
  role       TEXT NOT NULL DEFAULT 'member',
  max_uses   INTEGER NOT NULL,
  uses       INTEGER NOT NULL DEFAULT 0,
  expires_at TIMESTAMPTZ NOT NULL,
  revoked    BOOLEAN NOT NULL DEFAULT false,
  created_by BIGINT REFERENCES users(id) ON DELETE SET NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS password_resets (
  token_hash BYTEA PRIMARY KEY,
  user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  expires_at TIMESTAMPTZ NOT NULL,
  used_at    TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS user_profiles (
  user_id    BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  body       JSONB NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL
);

-- Counts of costly actions, for per-person allowances.
CREATE TABLE IF NOT EXISTS user_usage (
  id      BIGSERIAL PRIMARY KEY,
  user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  kind    TEXT NOT NULL,
  at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS user_usage_user ON user_usage(user_id, kind, at);

-- Look and feel each person picked, and when they last opened Today.
CREATE TABLE IF NOT EXISTS user_prefs (
  user_id      BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  theme        TEXT NOT NULL DEFAULT 'auto',
  accent       TEXT NOT NULL DEFAULT 'teal',
  seen_at      TIMESTAMPTZ,
  prev_seen_at TIMESTAMPTZ
);

-- What people did with the app's suggestions (done, skipped for the day).
CREATE TABLE IF NOT EXISTS user_steps (
  id      BIGSERIAL PRIMARY KEY,
  user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  step_id TEXT NOT NULL,
  action  TEXT NOT NULL,
  at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS user_steps_user ON user_steps(user_id, step_id, at);

-- Watchlists and journals belong to a person. Rows from before accounts
-- existed have no user until the first admin signs up and claims them.
ALTER TABLE watchlist ADD COLUMN IF NOT EXISTS user_id BIGINT REFERENCES users(id) ON DELETE CASCADE;
ALTER TABLE watchlist DROP CONSTRAINT IF EXISTS watchlist_pkey;
CREATE UNIQUE INDEX IF NOT EXISTS watchlist_user_symbol ON watchlist(user_id, symbol);
ALTER TABLE positions ADD COLUMN IF NOT EXISTS user_id BIGINT REFERENCES users(id) ON DELETE CASCADE;
CREATE INDEX IF NOT EXISTS positions_user ON positions(user_id, opened_at);
