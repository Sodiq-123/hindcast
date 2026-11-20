package auth

import (
	"context"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// User is the signed-in person on a request.
type User struct {
	ID        int64     `json:"id"`
	Email     string    `json:"email"`
	Name      string    `json:"name"`
	Role      string    `json:"role"` // admin | member
	CreatedAt time.Time `json:"createdAt"`
}

const (
	RoleAdmin  = "admin"
	RoleMember = "member"
)

func (u *User) Admin() bool { return u != nil && u.Role == RoleAdmin }

type ctxKey struct{}

func WithUser(ctx context.Context, u *User) context.Context {
	return context.WithValue(ctx, ctxKey{}, u)
}

// UserFrom returns the signed-in user, or nil.
func UserFrom(ctx context.Context) *User {
	u, _ := ctx.Value(ctxKey{}).(*User)
	return u
}

// ------------------------------------------------------------------ cookies

const (
	CookieName = "mo_session"
	SessionTTL = 30 * 24 * time.Hour
)

// SetSessionCookie stores the session token in an HttpOnly cookie that
// scripts can't read and other sites' pages don't send.
func SetSessionCookie(w http.ResponseWriter, token string, secure bool) {
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: token, Path: "/", MaxAge: int(SessionTTL.Seconds()),
		HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode})
}

func ClearSessionCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode})
}

func SessionToken(r *http.Request) string {
	if c, err := r.Cookie(CookieName); err == nil {
		return c.Value
	}
	return ""
}

// ClientIP is the caller's address; X-Forwarded-For is believed only behind
// a trusted reverse proxy (it is trivially forged otherwise).
func ClientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if f := r.Header.Get("X-Forwarded-For"); f != "" {
			return strings.TrimSpace(strings.Split(f, ",")[0])
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// ------------------------------------------------------------------ limiter

// Limiter allows at most Max events per key in a sliding Window (per
// process, which is enough for one server).
type Limiter struct {
	Max    int
	Window time.Duration

	mu   sync.Mutex
	hits map[string][]time.Time
}

func NewLimiter(max int, window time.Duration) *Limiter {
	return &Limiter{Max: max, Window: window, hits: map[string][]time.Time{}}
}

func (l *Limiter) recent(key string, now time.Time) []time.Time {
	h := l.hits[key]
	i := 0
	for i < len(h) && now.Sub(h[i]) >= l.Window {
		i++
	}
	h = h[i:]
	if len(h) == 0 {
		delete(l.hits, key)
	} else {
		l.hits[key] = h
	}
	return h
}

// Blocked reports whether key has used up its allowance, and for how long.
func (l *Limiter) Blocked(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	h := l.recent(key, now)
	if len(h) < l.Max {
		return false, 0
	}
	return true, l.Window - now.Sub(h[0])
}

// Add records one event for key.
func (l *Limiter) Add(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if len(l.hits) > 50_000 { // drop stale keys now and then
		for k := range l.hits {
			l.recent(k, now)
		}
	}
	l.hits[key] = append(l.recent(key, now), now)
}

// Allow records an event and reports whether it was within the allowance.
func (l *Limiter) Allow(key string) (bool, time.Duration) {
	if b, wait := l.Blocked(key); b {
		return false, wait
	}
	l.Add(key)
	return true, 0
}

func (l *Limiter) Reset(key string) {
	l.mu.Lock()
	delete(l.hits, key)
	l.mu.Unlock()
}
