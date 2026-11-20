package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/mail"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/sodiq/financial-market/backend/internal/auth"
	mailer "github.com/sodiq/financial-market/backend/internal/mail"
	"github.com/sodiq/financial-market/backend/internal/store"
)

// Accounts configures sign-up, sign-in and per-person allowances.
type Accounts struct {
	Signup         string // invite | open
	SecureCookie   bool   // the site is served over HTTPS
	TrustProxy     bool
	Mail           *mailer.Sender
	AppURL         string
	DefaultWatch   []string
	MemberAIRuns   int // per day
	MemberResearch int // per calendar month

	// Bootstrap is the one-time code that creates the first (admin) account;
	// empty once anyone has signed up.
	mu        sync.Mutex
	bootstrap string

	loginIP, loginEmail, signupIP, forgotIP, forgotEmail, resetIP, heavy *auth.Limiter
}

func (a *Accounts) init() {
	a.loginIP = auth.NewLimiter(30, 15*time.Minute)   // failed sign-ins per address
	a.loginEmail = auth.NewLimiter(8, 15*time.Minute) // failed sign-ins per account
	a.signupIP = auth.NewLimiter(10, time.Hour)
	a.forgotIP = auth.NewLimiter(10, time.Hour)
	a.forgotEmail = auth.NewLimiter(3, time.Hour)
	a.resetIP = auth.NewLimiter(20, time.Hour)
	a.heavy = auth.NewLimiter(40, 10*time.Minute) // analyses per member
}

// SetBootstrap sets the first-account code (see cmd/server).
func (a *Accounts) SetBootstrap(code string) {
	a.mu.Lock()
	a.bootstrap = code
	a.mu.Unlock()
}

func (a *Accounts) bootstrapCode() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.bootstrap
}

// publicAPI can be called without signing in.
var publicAPI = map[string]bool{
	"/api/auth/config": true, "/api/auth/signup": true, "/api/auth/login": true, "/api/auth/logout": true,
	"/api/auth/forgot": true, "/api/auth/reset": true, "/api/auth/invite": true,
}

// csrfHeader must accompany every state-changing API call. Browsers only let
// a page set a custom header on requests to its own site (anything else
// needs a CORS preflight, which this server refuses), so a hostile page
// can't submit a request using a signed-in person's cookie.
const csrfHeader = "X-Requested-With"

// authn requires a valid session for every API route except the public ones,
// and puts the user on the request context.
func (s *Server) authn(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions && r.Header.Get(csrfHeader) != "market-oracle" {
			writeErr(w, 403, "missing "+csrfHeader+" header")
			return
		}
		if publicAPI[r.URL.Path] {
			next.ServeHTTP(w, r)
			return
		}
		tok := auth.SessionToken(r)
		if tok == "" {
			writeErr(w, 401, "please sign in")
			return
		}
		u, err := s.Store.SessionUser(r.Context(), auth.HashToken(tok))
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				auth.ClearSessionCookie(w, s.Accounts.SecureCookie)
				writeErr(w, 401, "your session has ended; please sign in again")
				return
			}
			writeErr(w, 500, err.Error())
			return
		}
		next.ServeHTTP(w, r.WithContext(auth.WithUser(r.Context(), u)))
	})
}

// admin restricts a handler to admins.
func (s *Server) admin(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth.UserFrom(r.Context()).Admin() {
			writeErr(w, 403, "only an admin can do this")
			return
		}
		h(w, r)
	}
}

func user(r *http.Request) *auth.User { return auth.UserFrom(r.Context()) }
func uid(r *http.Request) int64       { return user(r).ID }

// heavy limits how often a member can start costly analyses.
func (s *Server) heavy(w http.ResponseWriter, r *http.Request) bool {
	if user(r).Admin() {
		return true
	}
	if ok, wait := s.Accounts.heavy.Allow(fmt.Sprint(uid(r))); !ok {
		tooMany(w, "You've started a lot of analyses in a short time.", wait)
		return false
	}
	return true
}

func tooMany(w http.ResponseWriter, msg string, wait time.Duration) {
	mins := int(math.Ceil(wait.Minutes()))
	w.Header().Set("Retry-After", fmt.Sprint(int(math.Ceil(wait.Seconds()))))
	writeErr(w, 429, fmt.Sprintf("%s Try again in %d minute%s.", msg, mins, plural(mins)))
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin") // reset and invite links carry tokens
		if strings.HasPrefix(r.URL.Path, "/api/") {
			h.Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

// decode reads a small JSON body.
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(v); err != nil {
		writeErr(w, 400, "invalid request")
		return false
	}
	return true
}

// ------------------------------------------------------------------ handlers

func (s *Server) authConfig(w http.ResponseWriter, r *http.Request) {
	n, err := s.Store.CountUsers(r.Context())
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"signup": s.Accounts.Signup, "email": s.Accounts.Mail.Enabled(), "needsSetup": n == 0})
}

func (s *Server) inviteCheck(w http.ResponseWriter, r *http.Request) {
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	ok := false
	if bc := s.Accounts.bootstrapCode(); bc != "" && subtle.ConstantTimeCompare([]byte(code), []byte(bc)) == 1 {
		ok = true
	} else if code != "" {
		ok, _ = s.Store.InviteValid(r.Context(), auth.HashToken(code))
	}
	writeJSON(w, 200, map[string]bool{"valid": ok})
}

func checkEmail(e string) error {
	e = strings.TrimSpace(e)
	a, err := mail.ParseAddress(e)
	if err != nil || a.Address != e || len(e) > 254 || !strings.Contains(e[strings.LastIndex(e, "@"):], ".") {
		return errors.New("enter a valid email address")
	}
	return nil
}

func checkName(n string) error {
	if utf8.RuneCountInString(strings.TrimSpace(n)) > 80 {
		return errors.New("keep the name under 80 characters")
	}
	return nil
}

func (s *Server) signup(w http.ResponseWriter, r *http.Request) {
	a := s.Accounts
	if ok, wait := a.signupIP.Allow(auth.ClientIP(r, a.TrustProxy)); !ok {
		tooMany(w, "Too many sign-ups from this network.", wait)
		return
	}
	var req struct{ Email, Name, Password, Invite string }
	if !decode(w, r, &req) {
		return
	}
	for _, err := range []error{checkEmail(req.Email), checkName(req.Name), auth.CheckPassword(req.Password, req.Email)} {
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
	}
	p := store.Signup{Email: req.Email, Name: req.Name}
	code := strings.TrimSpace(req.Invite)
	bc := a.bootstrapCode()
	switch {
	case bc != "":
		if subtle.ConstantTimeCompare([]byte(code), []byte(bc)) != 1 {
			writeErr(w, 403, "The first account needs the setup code printed in the server log.")
			return
		}
		p.Bootstrap = true
	case code != "":
		p.InviteHash = auth.HashToken(code)
	case a.Signup != "open":
		writeErr(w, 403, "Sign-up needs an invite code from an admin.")
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	p.PasswordHash = hash
	u, err := s.Store.CreateAccount(r.Context(), p)
	switch {
	case errors.Is(err, store.ErrEmailTaken):
		writeErr(w, 409, "An account with this email already exists. Sign in instead, or reset the password.")
		return
	case errors.Is(err, store.ErrInviteInvalid), errors.Is(err, store.ErrBootstrapUsed):
		writeErr(w, 403, err.Error())
		return
	case err != nil:
		writeErr(w, 500, err.Error())
		return
	}
	if p.Bootstrap {
		a.SetBootstrap("")
		s.Log.Info("first account created; it is the admin and owns the existing watchlist and journal", "id", u.ID)
	}
	if err := s.startSession(w, r, u); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	s.writeMe(w, r, u)
}

func (s *Server) startSession(w http.ResponseWriter, r *http.Request, u *auth.User) error {
	tok, hash, err := auth.NewToken()
	if err != nil {
		return err
	}
	if err := s.Store.CreateSession(r.Context(), hash, u.ID, r.UserAgent(), auth.ClientIP(r, s.Accounts.TrustProxy)); err != nil {
		return err
	}
	_ = s.Store.TouchLogin(r.Context(), u.ID)
	auth.SetSessionCookie(w, tok, s.Accounts.SecureCookie)
	return nil
}

// badLogin doesn't say which of the two was wrong, so the sign-in form can't
// be used to find out who has an account. The server log records the reason.
const badLogin = "That email and password don't match an account. Check the email for typos, and use the eye button to check the password."

// maskEmail keeps enough of an address to recognise it in the log.
func maskEmail(e string) string {
	at := strings.LastIndex(e, "@")
	if at < 1 {
		return "?"
	}
	keep := min(3, at)
	return e[:keep] + strings.Repeat("*", at-keep) + e[at:]
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	a := s.Accounts
	var req struct{ Email, Password string }
	if !decode(w, r, &req) {
		return
	}
	ip, email := auth.ClientIP(r, a.TrustProxy), store.NormEmail(req.Email)
	for _, l := range []struct {
		lim *auth.Limiter
		key string
	}{{a.loginIP, ip}, {a.loginEmail, email}} {
		if b, wait := l.lim.Blocked(l.key); b {
			tooMany(w, "Too many failed sign-in attempts.", wait)
			return
		}
	}
	fail := func(reason string) {
		a.loginIP.Add(ip)
		a.loginEmail.Add(email)
		s.Log.Warn("sign-in failed", "reason", reason, "email", maskEmail(email), "ip", ip)
		writeErr(w, 401, badLogin)
	}
	if len(req.Password) > 4*auth.MaxPassword {
		fail("password too long")
		return
	}
	u, hash, disabled, err := s.Store.Credentials(r.Context(), email)
	if errors.Is(err, store.ErrNotFound) {
		auth.SpendVerifyTime(req.Password)
		fail("no account with this email")
		return
	}
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if !auth.VerifyPassword(req.Password, hash) {
		fail("wrong password")
		return
	}
	if disabled {
		writeErr(w, 403, "This account has been switched off by an admin.")
		return
	}
	a.loginEmail.Reset(email)
	if err := s.startSession(w, r, u); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	s.writeMe(w, r, u)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if tok := auth.SessionToken(r); tok != "" {
		_ = s.Store.DeleteSession(r.Context(), auth.HashToken(tok))
	}
	auth.ClearSessionCookie(w, s.Accounts.SecureCookie)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) { s.writeMe(w, r, user(r)) }

// writeMe returns the user with their profile and what it changes.
func (s *Server) writeMe(w http.ResponseWriter, r *http.Request, u *auth.User) {
	p, err := s.Store.Profile(r.Context(), u.ID)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	prefs, err := s.Store.Prefs(r.Context(), u.ID)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"user": u, "profile": p, "settings": p.Settings(), "prefs": prefs})
}

// forgot emails a reset link. The reply is the same whether or not the
// email has an account, so it can't be used to find out who is registered.
func (s *Server) forgot(w http.ResponseWriter, r *http.Request) {
	a := s.Accounts
	if ok, wait := a.forgotIP.Allow(auth.ClientIP(r, a.TrustProxy)); !ok {
		tooMany(w, "Too many reset requests from this network.", wait)
		return
	}
	var req struct{ Email string }
	if !decode(w, r, &req) {
		return
	}
	out := map[string]any{"ok": true, "email": a.Mail.Enabled()}
	email := store.NormEmail(req.Email)
	if !a.Mail.Enabled() || email == "" {
		writeJSON(w, 200, out)
		return
	}
	if ok, _ := a.forgotEmail.Allow(email); !ok {
		writeJSON(w, 200, out)
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		u, _, disabled, err := s.Store.Credentials(ctx, email)
		if err != nil || disabled {
			return
		}
		link, err := s.resetLink(ctx, u.ID, time.Hour)
		if err != nil {
			s.Log.Warn("reset link", "err", err)
			return
		}
		body := "Someone (hopefully you) asked to reset the password for your Hindcast account.\n\n" +
			"Choose a new password here (the link works once, for one hour):\n" + link +
			"\n\nIf you didn't ask, ignore this email: your password stays the same."
		if err := a.Mail.Send(u.Email, "Reset your Hindcast password", body); err != nil {
			s.Log.Warn("reset email not sent", "err", err)
		}
	}()
	writeJSON(w, 200, out)
}

func (s *Server) resetLink(ctx context.Context, uid int64, ttl time.Duration) (string, error) {
	tok, hash, err := auth.NewToken()
	if err != nil {
		return "", err
	}
	if err := s.Store.CreateReset(ctx, hash, uid, ttl); err != nil {
		return "", err
	}
	return s.Accounts.AppURL + "/reset?token=" + tok, nil
}

func (s *Server) reset(w http.ResponseWriter, r *http.Request) {
	a := s.Accounts
	if ok, wait := a.resetIP.Allow(auth.ClientIP(r, a.TrustProxy)); !ok {
		tooMany(w, "Too many attempts.", wait)
		return
	}
	var req struct{ Token, Password string }
	if !decode(w, r, &req) {
		return
	}
	if err := auth.CheckPassword(req.Password, ""); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if _, err := s.Store.UseReset(r.Context(), auth.HashToken(strings.TrimSpace(req.Token)), hash); err != nil {
		if errors.Is(err, store.ErrResetInvalid) {
			writeErr(w, 400, err.Error())
			return
		}
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
