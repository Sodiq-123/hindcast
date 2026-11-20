package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/sodiq/financial-market/backend/internal/auth"
	"github.com/sodiq/financial-market/backend/internal/profile"
)

var (
	ErrEmailTaken    = errors.New("an account with this email already exists")
	ErrInviteInvalid = errors.New("this invite code is not valid: it may have expired, been used up or been revoked")
	ErrResetInvalid  = errors.New("this reset link is not valid any more; ask for a new one")
	ErrBootstrapUsed = errors.New("the first account has already been created")
)

func isUnique(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// NormEmail is how emails are compared and stored.
func NormEmail(e string) string { return strings.ToLower(strings.TrimSpace(e)) }

// ---------------------------------------------------------------------- users

const userCols = `id, email, name, role, created_at`

func scanUser(row interface{ Scan(...any) error }, extra ...any) (*auth.User, error) {
	u := &auth.User{}
	err := row.Scan(append([]any{&u.ID, &u.Email, &u.Name, &u.Role, &u.CreatedAt}, extra...)...)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return u, err
}

func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&n)
	return n, err
}

// Signup describes a new account. Exactly one of InviteHash or Bootstrap is
// used (or neither, when sign-up is open to anyone).
type Signup struct {
	Email, Name, PasswordHash string
	InviteHash                []byte
	Bootstrap                 bool // the first account: becomes admin and claims pre-account data
}

// CreateAccount creates the user, consuming the invite, in one transaction.
func (s *Store) CreateAccount(ctx context.Context, p Signup) (*auth.User, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	role := auth.RoleMember
	switch {
	case p.Bootstrap:
		// Serialise concurrent bootstrap attempts, then check none succeeded.
		if _, err := tx.ExecContext(ctx, `LOCK TABLE users IN SHARE ROW EXCLUSIVE MODE`); err != nil {
			return nil, err
		}
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&n); err != nil {
			return nil, err
		}
		if n > 0 {
			return nil, ErrBootstrapUsed
		}
		role = auth.RoleAdmin
	case p.InviteHash != nil:
		err := tx.QueryRowContext(ctx, `UPDATE invites SET uses = uses + 1
			WHERE code_hash = $1 AND NOT revoked AND expires_at > now() AND uses < max_uses RETURNING role`, p.InviteHash).Scan(&role)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrInviteInvalid
		}
		if err != nil {
			return nil, err
		}
	}
	u, err := scanUser(tx.QueryRowContext(ctx, `INSERT INTO users(email, name, password_hash, role) VALUES($1,$2,$3,$4) RETURNING `+userCols,
		NormEmail(p.Email), strings.TrimSpace(p.Name), p.PasswordHash, role))
	if isUnique(err) {
		return nil, ErrEmailTaken
	}
	if err != nil {
		return nil, err
	}
	if p.Bootstrap {
		for _, q := range []string{
			`UPDATE watchlist SET user_id = $1 WHERE user_id IS NULL`,
			`UPDATE positions SET user_id = $1 WHERE user_id IS NULL`,
		} {
			if _, err := tx.ExecContext(ctx, q, u.ID); err != nil {
				return nil, err
			}
		}
		// The journal's settings move from the shared key to the admin's.
		if _, err := tx.ExecContext(ctx, `UPDATE settings SET key = $1 WHERE key = 'journal'`, JournalSettingsKey(u.ID)); err != nil {
			return nil, err
		}
	}
	return u, tx.Commit()
}

// JournalSettingsKey is where a person's journal settings live in settings.
func JournalSettingsKey(uid int64) string { return "journal:" + strconv.FormatInt(uid, 10) }

// Credentials returns the user for a login attempt, with the password hash.
func (s *Store) Credentials(ctx context.Context, email string) (*auth.User, string, bool, error) {
	var hash string
	var disabled bool
	u, err := scanUser(s.db.QueryRowContext(ctx, `SELECT `+userCols+`, password_hash, disabled FROM users WHERE lower(email) = $1`, NormEmail(email)), &hash, &disabled)
	return u, hash, disabled, err
}

func (s *Store) PasswordHash(ctx context.Context, uid int64) (string, error) {
	var h string
	err := s.db.QueryRowContext(ctx, `SELECT password_hash FROM users WHERE id = $1`, uid).Scan(&h)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return h, err
}

func (s *Store) User(ctx context.Context, uid int64) (*auth.User, error) {
	return scanUser(s.db.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE id = $1`, uid))
}

func (s *Store) TouchLogin(ctx context.Context, uid int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE users SET last_login_at = now() WHERE id = $1`, uid)
	return err
}

func (s *Store) UpdateAccount(ctx context.Context, uid int64, name, email string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE users SET name = $1, email = $2 WHERE id = $3`, strings.TrimSpace(name), NormEmail(email), uid)
	if isUnique(err) {
		return ErrEmailTaken
	}
	return err
}

// SetPassword changes the password and signs out every session except keep
// (nil signs out all of them).
func (s *Store) SetPassword(ctx context.Context, uid int64, hash string, keep []byte) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE users SET password_hash = $1 WHERE id = $2`, hash, uid); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = $1 AND ($2::bytea IS NULL OR token_hash <> $2)`, uid, keep); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM password_resets WHERE user_id = $1`, uid); err != nil {
		return err
	}
	return tx.Commit()
}

// OwnerID is the first admin: the person the server's Telegram alerts go to.
func (s *Store) OwnerID(ctx context.Context) (int64, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, `SELECT id FROM users WHERE role = 'admin' AND NOT disabled ORDER BY id LIMIT 1`).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	return id, err
}

// UserRow is a user as the admin page lists them.
type UserRow struct {
	auth.User
	Disabled      bool       `json:"disabled"`
	LastLoginAt   *time.Time `json:"lastLoginAt,omitempty"`
	Watching      int        `json:"watching"`
	Positions     int        `json:"positions"`
	ProfileStatus string     `json:"profileStatus"`
}

func (s *Store) Users(ctx context.Context) ([]UserRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT u.id, u.email, u.name, u.role, u.created_at, u.disabled, u.last_login_at,
		(SELECT count(*) FROM watchlist w WHERE w.user_id = u.id),
		(SELECT count(*) FROM positions p WHERE p.user_id = u.id),
		coalesce((SELECT body->>'status' FROM user_profiles f WHERE f.user_id = u.id), '')
		FROM users u ORDER BY u.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []UserRow{}
	for rows.Next() {
		var r UserRow
		if err := rows.Scan(&r.ID, &r.Email, &r.Name, &r.Role, &r.CreatedAt, &r.Disabled, &r.LastLoginAt, &r.Watching, &r.Positions, &r.ProfileStatus); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ErrLastAdmin stops the last active admin being removed or demoted.
var ErrLastAdmin = errors.New("this is the only active admin; make someone else an admin first")

// otherAdmins counts active admins other than uid.
func otherAdmins(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, uid int64) (int, error) {
	var n int
	err := q.QueryRowContext(ctx, `SELECT count(*) FROM users WHERE role = 'admin' AND NOT disabled AND id <> $1`, uid).Scan(&n)
	return n, err
}

// SetRole and SetDisabled refuse to leave the server without an admin.
func (s *Store) SetRole(ctx context.Context, uid int64, role string) error {
	return s.adminChange(ctx, uid, role != auth.RoleAdmin, `UPDATE users SET role = $1 WHERE id = $2`, role, uid)
}

func (s *Store) SetDisabled(ctx context.Context, uid int64, disabled bool) error {
	if err := s.adminChange(ctx, uid, disabled, `UPDATE users SET disabled = $1 WHERE id = $2`, disabled, uid); err != nil {
		return err
	}
	if disabled {
		_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = $1`, uid)
		return err
	}
	return nil
}

// DeleteUser removes the account and everything that belongs to it.
func (s *Store) DeleteUser(ctx context.Context, uid int64) error {
	return s.adminChange(ctx, uid, true, `DELETE FROM users WHERE id = $1`, uid)
}

func (s *Store) adminChange(ctx context.Context, uid int64, removesAdmin bool, q string, args ...any) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if removesAdmin {
		if _, err := tx.ExecContext(ctx, `LOCK TABLE users IN SHARE ROW EXCLUSIVE MODE`); err != nil {
			return err
		}
		var role string
		var disabled bool
		if err := tx.QueryRowContext(ctx, `SELECT role, disabled FROM users WHERE id = $1`, uid).Scan(&role, &disabled); errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		if role == auth.RoleAdmin && !disabled {
			if n, err := otherAdmins(ctx, tx, uid); err != nil {
				return err
			} else if n == 0 {
				return ErrLastAdmin
			}
		}
	}
	res, err := tx.ExecContext(ctx, q, args...)
	if err := affected(res, err); err != nil {
		return err
	}
	return tx.Commit()
}

// ------------------------------------------------------------------- sessions

func (s *Store) CreateSession(ctx context.Context, hash []byte, uid int64, userAgent, ip string) error {
	if len(userAgent) > 300 {
		userAgent = userAgent[:300]
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO sessions(token_hash, user_id, expires_at, user_agent, ip) VALUES($1,$2,$3,$4,$5)`,
		hash, uid, now().Add(auth.SessionTTL), userAgent, ip)
	return err
}

// SessionUser returns the user for a live session, extending the session
// (at most once an hour) so active people stay signed in.
func (s *Store) SessionUser(ctx context.Context, hash []byte) (*auth.User, error) {
	var seen time.Time
	u, err := scanUser(s.db.QueryRowContext(ctx, `SELECT u.id, u.email, u.name, u.role, u.created_at, s.last_seen_at
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = $1 AND s.expires_at > now() AND NOT u.disabled`, hash), &seen)
	if err != nil {
		return nil, err
	}
	if time.Since(seen) > time.Hour {
		_, _ = s.db.ExecContext(ctx, `UPDATE sessions SET last_seen_at = now(), expires_at = $2 WHERE token_hash = $1`, hash, now().Add(auth.SessionTTL))
	}
	return u, nil
}

func (s *Store) DeleteSession(ctx context.Context, hash []byte) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = $1`, hash)
	return err
}

type SessionInfo struct {
	ID         int64     `json:"id"`
	CreatedAt  time.Time `json:"createdAt"`
	LastSeenAt time.Time `json:"lastSeenAt"`
	UserAgent  string    `json:"userAgent"`
	IP         string    `json:"ip"`
	Current    bool      `json:"current"`
}

func (s *Store) Sessions(ctx context.Context, uid int64, current []byte) ([]SessionInfo, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, created_at, last_seen_at, user_agent, ip, coalesce(token_hash = $2, false) FROM sessions
		WHERE user_id = $1 AND expires_at > now() ORDER BY last_seen_at DESC`, uid, current)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SessionInfo{}
	for rows.Next() {
		var x SessionInfo
		if err := rows.Scan(&x.ID, &x.CreatedAt, &x.LastSeenAt, &x.UserAgent, &x.IP, &x.Current); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// RevokeSession signs out one of the person's sessions (id 0: all but current).
func (s *Store) RevokeSession(ctx context.Context, uid, id int64, current []byte) error {
	if id == 0 {
		_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = $1 AND token_hash <> $2`, uid, current)
		return err
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = $1 AND id = $2`, uid, id)
	return affected(res, err)
}

// PurgeExpired deletes dead sessions, used reset links and expired invites.
func (s *Store) PurgeExpired(ctx context.Context) error {
	for _, q := range []string{
		`DELETE FROM sessions WHERE expires_at < now()`,
		`DELETE FROM password_resets WHERE expires_at < now() OR used_at IS NOT NULL`,
		`DELETE FROM user_usage WHERE at < now() - interval '62 days'`,
		`DELETE FROM user_events WHERE at < now() - interval '90 days'`,
	} {
		if _, err := s.db.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	return nil
}

// -------------------------------------------------------------------- invites

type Invite struct {
	ID        int64     `json:"id"`
	Note      string    `json:"note"`
	Role      string    `json:"role"`
	MaxUses   int       `json:"maxUses"`
	Uses      int       `json:"uses"`
	ExpiresAt time.Time `json:"expiresAt"`
	Revoked   bool      `json:"revoked"`
	CreatedBy string    `json:"createdBy"`
	CreatedAt time.Time `json:"createdAt"`
}

func (s *Store) CreateInvite(ctx context.Context, hash []byte, note, role string, maxUses int, expires time.Time, by int64) (int64, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, `INSERT INTO invites(code_hash, note, role, max_uses, expires_at, created_by) VALUES($1,$2,$3,$4,$5,$6) RETURNING id`,
		hash, strings.TrimSpace(note), role, maxUses, expires, by).Scan(&id)
	return id, err
}

func (s *Store) Invites(ctx context.Context) ([]Invite, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT i.id, i.note, i.role, i.max_uses, i.uses, i.expires_at, i.revoked, coalesce(u.email, ''), i.created_at
		FROM invites i LEFT JOIN users u ON u.id = i.created_by ORDER BY i.id DESC LIMIT 200`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Invite{}
	for rows.Next() {
		var x Invite
		if err := rows.Scan(&x.ID, &x.Note, &x.Role, &x.MaxUses, &x.Uses, &x.ExpiresAt, &x.Revoked, &x.CreatedBy, &x.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func (s *Store) RevokeInvite(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `UPDATE invites SET revoked = true WHERE id = $1`, id)
	return affected(res, err)
}

// InviteValid reports whether a code can still be used (for the sign-up page).
func (s *Store) InviteValid(ctx context.Context, hash []byte) (bool, error) {
	var ok bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM invites WHERE code_hash = $1 AND NOT revoked AND expires_at > now() AND uses < max_uses)`, hash).Scan(&ok)
	return ok, err
}

// ------------------------------------------------------------ password resets

// CreateReset stores a reset link, replacing any earlier one for the user.
func (s *Store) CreateReset(ctx context.Context, hash []byte, uid int64, ttl time.Duration) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM password_resets WHERE user_id = $1`, uid); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO password_resets(token_hash, user_id, expires_at) VALUES($1,$2,$3)`, hash, uid, now().Add(ttl)); err != nil {
		return err
	}
	return tx.Commit()
}

// UseReset consumes a reset link and sets the new password, signing out
// every session.
func (s *Store) UseReset(ctx context.Context, hash []byte, newHash string) (int64, error) {
	var uid int64
	err := s.db.QueryRowContext(ctx, `UPDATE password_resets r SET used_at = now() FROM users u
		WHERE r.token_hash = $1 AND r.used_at IS NULL AND r.expires_at > now() AND u.id = r.user_id AND NOT u.disabled
		RETURNING r.user_id`, hash).Scan(&uid)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrResetInvalid
	}
	if err != nil {
		return 0, err
	}
	return uid, s.SetPassword(ctx, uid, newHash, nil)
}

// ------------------------------------------------------------------- profiles

// Profile returns the person's profile (empty if they haven't started it).
func (s *Store) Profile(ctx context.Context, uid int64) (*profile.Profile, error) {
	p := &profile.Profile{Goals: []string{}, Interests: []string{}}
	var body []byte
	err := s.db.QueryRowContext(ctx, `SELECT body FROM user_profiles WHERE user_id = $1`, uid).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return p, nil
	}
	if err != nil {
		return nil, err
	}
	return p, json.Unmarshal(body, p)
}

func (s *Store) SaveProfile(ctx context.Context, uid int64, p *profile.Profile) error {
	p.UpdatedAt = now()
	body, err := json.Marshal(p)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO user_profiles(user_id, body, updated_at) VALUES($1,$2,$3)
		ON CONFLICT (user_id) DO UPDATE SET body = EXCLUDED.body, updated_at = EXCLUDED.updated_at`, uid, body, p.UpdatedAt)
	return err
}

// ---------------------------------------------------------------------- usage

func (s *Store) CountAction(ctx context.Context, uid int64, kind string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO user_usage(user_id, kind) VALUES($1,$2)`, uid, kind)
	return err
}

func (s *Store) ActionsSince(ctx context.Context, uid int64, kind string, since time.Time) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM user_usage WHERE user_id = $1 AND kind = $2 AND at >= $3`, uid, kind, since).Scan(&n)
	return n, err
}
