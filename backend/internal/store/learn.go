package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// LearnEvent is one thing a person did that the app may learn from: an asset
// page visit ("visit"), a horizon picked on the watchlist ("view") or the
// technical detail opened on an asset page ("detail") or Track record or
// Replay opened ("page").
type LearnEvent struct {
	Kind    string    `json:"kind"`
	Subject string    `json:"subject"`
	At      time.Time `json:"at"`
}

// SuggestionRow is a suggestion the app made and what the person answered.
// Body holds the wording shown (JSON), so the history reads as it was asked.
type SuggestionRow struct {
	ID          int64      `json:"id"`
	Kind        string     `json:"kind"`
	Subject     string     `json:"subject"`
	Body        []byte     `json:"-"`
	Answer      string     `json:"answer"` // "" (waiting) | yes | no | undone | expired | noted | muted
	Prev        string     `json:"-"`      // the setting before a yes, for undo
	ShownAt     time.Time  `json:"shownAt"`
	AnsweredAt  *time.Time `json:"answeredAt,omitempty"`
	SnoozeUntil *time.Time `json:"snoozeUntil,omitempty"`
}

// LearnPrefs are a person's learning switches.
type LearnPrefs struct {
	Paused bool     `json:"paused"`
	Muted  []string `json:"muted"` // trade classes with reminders switched off
}

// AddEvent records an event unless the same one was recorded in the last 30
// minutes (so refreshing a page doesn't count as another visit). It reports
// whether a row was added.
func (s *Store) AddEvent(ctx context.Context, uid int64, kind, subject string, at time.Time) (bool, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO user_events(user_id, kind, subject, at)
		SELECT $1::bigint, $2::text, $3::text, $4::timestamptz WHERE NOT EXISTS (
		  SELECT 1 FROM user_events WHERE user_id = $1 AND kind = $2 AND subject = $3
		  AND at > $4::timestamptz - interval '30 minutes' AND at < $4::timestamptz + interval '30 minutes')`,
		uid, kind, subject, at)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// Events returns a person's events since a moment, oldest first.
func (s *Store) Events(ctx context.Context, uid int64, since time.Time) ([]LearnEvent, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT kind, subject, at FROM user_events WHERE user_id = $1 AND at >= $2 ORDER BY at, id`, uid, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LearnEvent{}
	for rows.Next() {
		var e LearnEvent
		if err := rows.Scan(&e.Kind, &e.Subject, &e.At); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// FirstEvent is when recording began (nil if nothing is recorded).
func (s *Store) FirstEvent(ctx context.Context, uid int64) (*time.Time, error) {
	var t *time.Time
	err := s.db.QueryRowContext(ctx, `SELECT min(at) FROM user_events WHERE user_id = $1`, uid).Scan(&t)
	return t, err
}

// ForgetEvents deletes a person's events of the given kinds (all kinds if none).
func (s *Store) ForgetEvents(ctx context.Context, uid int64, kinds ...string) error {
	if len(kinds) == 0 {
		_, err := s.db.ExecContext(ctx, `DELETE FROM user_events WHERE user_id = $1`, uid)
		return err
	}
	for _, k := range kinds {
		if _, err := s.db.ExecContext(ctx, `DELETE FROM user_events WHERE user_id = $1 AND kind = $2`, uid, k); err != nil {
			return err
		}
	}
	return nil
}

func scanSuggestions(rows *sql.Rows) ([]SuggestionRow, error) {
	defer rows.Close()
	out := []SuggestionRow{}
	for rows.Next() {
		var r SuggestionRow
		if err := rows.Scan(&r.ID, &r.Kind, &r.Subject, &r.Body, &r.Answer, &r.Prev, &r.ShownAt, &r.AnsweredAt, &r.SnoozeUntil); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

const suggestionCols = `id, kind, subject, body, answer, prev, shown_at, answered_at, snooze_until`

// Suggestions returns a person's suggestions, newest first.
func (s *Store) Suggestions(ctx context.Context, uid int64) ([]SuggestionRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+suggestionCols+` FROM user_suggestions WHERE user_id = $1 ORDER BY shown_at DESC, id DESC`, uid)
	if err != nil {
		return nil, err
	}
	return scanSuggestions(rows)
}

// Suggestion returns one of a person's suggestions.
func (s *Store) Suggestion(ctx context.Context, uid, id int64) (*SuggestionRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+suggestionCols+` FROM user_suggestions WHERE user_id = $1 AND id = $2`, uid, id)
	if err != nil {
		return nil, err
	}
	rs, err := scanSuggestions(rows)
	if err != nil {
		return nil, err
	}
	if len(rs) == 0 {
		return nil, ErrNotFound
	}
	return &rs[0], nil
}

// AddSuggestion records a suggestion as shown (answer may already be set, as
// for a reminder answered in the same moment).
func (s *Store) AddSuggestion(ctx context.Context, uid int64, r *SuggestionRow) error {
	if r.ShownAt.IsZero() {
		r.ShownAt = now()
	}
	return s.db.QueryRowContext(ctx, `INSERT INTO user_suggestions(user_id, kind, subject, body, answer, prev, shown_at, answered_at, snooze_until)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id`,
		uid, r.Kind, r.Subject, r.Body, r.Answer, r.Prev, r.ShownAt, r.AnsweredAt, r.SnoozeUntil).Scan(&r.ID)
}

// AnswerSuggestion stores an answer, the setting it replaced and how long to
// wait before asking again.
func (s *Store) AnswerSuggestion(ctx context.Context, uid, id int64, answer, prev string, snooze *time.Time) error {
	res, err := s.db.ExecContext(ctx, `UPDATE user_suggestions SET answer = $3, prev = CASE WHEN $4 = '' THEN prev ELSE $4 END,
		answered_at = now(), snooze_until = $5 WHERE user_id = $1 AND id = $2`, uid, id, answer, prev, snooze)
	return affected(res, err)
}

// ForgetSuggestions deletes a person's suggestion history.
func (s *Store) ForgetSuggestions(ctx context.Context, uid int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM user_suggestions WHERE user_id = $1`, uid)
	return err
}

// AcceptanceByKind counts, per suggestion kind, how often everyone except
// one person said yes, out of all answers (for the shared starting point).
func (s *Store) AcceptanceByKind(ctx context.Context, except int64) (map[string][2]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT kind, count(*) FILTER (WHERE answer IN ('yes','noted')), count(*)
		FROM user_suggestions WHERE user_id <> $1 AND answer IN ('yes','no','undone','noted','muted') GROUP BY kind`, except)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][2]int{}
	for rows.Next() {
		var k string
		var y, n int
		if err := rows.Scan(&k, &y, &n); err != nil {
			return nil, err
		}
		out[k] = [2]int{y, n}
	}
	return out, rows.Err()
}

func (s *Store) LearnPrefs(ctx context.Context, uid int64) (LearnPrefs, error) {
	p := LearnPrefs{Muted: []string{}}
	var muted string
	err := s.db.QueryRowContext(ctx, `SELECT learn_paused, reminders_muted FROM user_prefs WHERE user_id = $1`, uid).Scan(&p.Paused, &muted)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return p, err
	}
	for _, m := range strings.Split(muted, ",") {
		if m = strings.TrimSpace(m); m != "" {
			p.Muted = append(p.Muted, m)
		}
	}
	return p, nil
}

func (s *Store) SaveLearnPrefs(ctx context.Context, uid int64, p LearnPrefs) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO user_prefs(user_id, learn_paused, reminders_muted) VALUES($1,$2,$3)
		ON CONFLICT (user_id) DO UPDATE SET learn_paused = EXCLUDED.learn_paused, reminders_muted = EXCLUDED.reminders_muted`,
		uid, p.Paused, strings.Join(p.Muted, ","))
	return err
}

// WatchAdded returns when each of a person's assets joined the watchlist.
func (s *Store) WatchAdded(ctx context.Context, uid int64) (map[string]time.Time, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT symbol, added_at FROM watchlist WHERE user_id = $1 AND kind = 'asset'`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]time.Time{}
	for rows.Next() {
		var sym string
		var t time.Time
		if err := rows.Scan(&sym, &t); err != nil {
			return nil, err
		}
		out[sym] = t
	}
	return out, rows.Err()
}

// AssetName is the name from an asset's latest analysis ("" if never analysed).
func (s *Store) AssetName(ctx context.Context, symbol string) string {
	var name sql.NullString
	_ = s.db.QueryRowContext(ctx, `SELECT body->>'name' FROM reports WHERE symbol = $1 ORDER BY id DESC LIMIT 1`, symbol).Scan(&name)
	return name.String
}
