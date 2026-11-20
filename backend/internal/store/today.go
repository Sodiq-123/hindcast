package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Prefs is the look a person picked.
type Prefs struct {
	Theme  string `json:"theme"`  // auto | light | dark
	Accent string `json:"accent"` // a name from Accents
}

// Accents are the colours people can choose; the frontend owns the values.
var Accents = []string{"teal", "indigo", "plum", "amber", "ocean", "graphite"}

var Themes = []string{"auto", "light", "dark"}

func (s *Store) Prefs(ctx context.Context, uid int64) (Prefs, error) {
	p := Prefs{Theme: "auto", Accent: "teal"}
	err := s.db.QueryRowContext(ctx, `SELECT theme, accent FROM user_prefs WHERE user_id = $1`, uid).Scan(&p.Theme, &p.Accent)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return p, err
	}
	return p, nil
}

func (s *Store) SavePrefs(ctx context.Context, uid int64, p Prefs) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO user_prefs(user_id, theme, accent) VALUES($1,$2,$3)
		ON CONFLICT (user_id) DO UPDATE SET theme = EXCLUDED.theme, accent = EXCLUDED.accent`, uid, p.Theme, p.Accent)
	return err
}

// MarkSeen records a visit to Today and returns when the previous visit
// was. Loads less than `gap` apart count as the same visit, so refreshing
// doesn't reset "since you last looked".
func (s *Store) MarkSeen(ctx context.Context, uid int64, now time.Time, gap time.Duration) (*time.Time, error) {
	var prev *time.Time
	err := s.db.QueryRowContext(ctx, `INSERT INTO user_prefs(user_id, seen_at) VALUES($1,$2)
		ON CONFLICT (user_id) DO UPDATE SET
		  prev_seen_at = CASE WHEN user_prefs.seen_at IS NOT NULL AND user_prefs.seen_at < $2 - make_interval(secs => $3)
		                      THEN user_prefs.seen_at ELSE user_prefs.prev_seen_at END,
		  seen_at = $2
		RETURNING prev_seen_at`, uid, now, gap.Seconds()).Scan(&prev)
	return prev, err
}

// StepState is what a person did with one suggestion.
type StepState struct{ Done, SkippedToday bool }

// StepStates summarises what a person did with each suggestion: done ever,
// or skipped since dayStart.
func (s *Store) StepStates(ctx context.Context, uid int64, dayStart time.Time) (map[string]StepState, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT step_id, bool_or(action = 'done'), bool_or(action = 'skip' AND at >= $2)
		FROM user_steps WHERE user_id = $1 GROUP BY step_id`, uid, dayStart)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]StepState{}
	for rows.Next() {
		var id string
		var st StepState
		if err := rows.Scan(&id, &st.Done, &st.SkippedToday); err != nil {
			return nil, err
		}
		out[id] = st
	}
	return out, rows.Err()
}

// StepAction records "done" or "skip", or undoes today's skip.
func (s *Store) StepAction(ctx context.Context, uid int64, stepID, action string, dayStart time.Time) error {
	if action == "undo" {
		_, err := s.db.ExecContext(ctx, `DELETE FROM user_steps WHERE user_id = $1 AND step_id = $2 AND action = 'skip' AND at >= $3`, uid, stepID, dayStart)
		return err
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO user_steps(user_id, step_id, action) VALUES($1,$2,$3)`, uid, stepID, action)
	return err
}

// Steps lists a person's suggestion history, newest first (for data export).
func (s *Store) Steps(ctx context.Context, uid int64) ([]map[string]any, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT step_id, action, at FROM user_steps WHERE user_id = $1 ORDER BY at DESC`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, action string
		var at time.Time
		if err := rows.Scan(&id, &action, &at); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"step": id, "action": action, "at": at})
	}
	return out, rows.Err()
}
