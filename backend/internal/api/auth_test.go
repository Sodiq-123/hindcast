package api

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"testing"

	"github.com/sodiq/financial-market/backend/internal/learn"
	"github.com/sodiq/financial-market/backend/internal/store"
)

type client struct {
	t    *testing.T
	base string
	c    *http.Client
}

func newClient(t *testing.T, base string) *client {
	jar, _ := cookiejar.New(nil)
	return &client{t, base, &http.Client{Jar: jar}}
}

// do sends a request (with the anti-forgery header unless noCSRF) and
// decodes the JSON reply into out.
func (c *client) do(method, path string, body any, out any, noCSRF ...bool) int {
	c.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.base+path, rd)
	req.Header.Set("Content-Type", "application/json")
	if len(noCSRF) == 0 {
		req.Header.Set(csrfHeader, "market-oracle")
	}
	res, err := c.c.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer res.Body.Close()
	if out != nil {
		_ = json.NewDecoder(res.Body).Decode(out)
	}
	return res.StatusCode
}

func TestAccountsEndToEnd(t *testing.T) {
	st := store.OpenTest(t)
	acc := &Accounts{Signup: "invite", AppURL: "http://oracle.test", DefaultWatch: []string{"SPY"}, MemberResearch: 2, MemberAIRuns: 10}
	acc.SetBootstrap("first-account-code")
	srv := &Server{Store: st, Learn: &learn.Service{Store: st}, Accounts: acc, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	anon := newClient(t, ts.URL)
	if code := anon.do("GET", "/api/watchlist", nil, nil); code != 401 {
		t.Fatalf("signed out: %d", code)
	}
	var cfg map[string]any
	anon.do("GET", "/api/auth/config", nil, &cfg)
	if cfg["needsSetup"] != true || cfg["signup"] != "invite" {
		t.Errorf("config: %v", cfg)
	}

	// The first account needs the code from the server log.
	owner := newClient(t, ts.URL)
	signup := map[string]string{"email": "owner@example.com", "name": "Owner", "password": "a long passphrase", "invite": "first-account-code"}
	if code := owner.do("POST", "/api/auth/signup", signup, nil, true); code != 403 {
		t.Errorf("no anti-forgery header: %d", code)
	}
	if code := owner.do("POST", "/api/auth/signup", map[string]string{"email": "x@example.com", "password": "a long passphrase", "invite": "guess"}, nil); code != 403 {
		t.Errorf("wrong setup code: %d", code)
	}
	var me struct {
		User     struct{ Role string }
		Settings struct{ Horizon string }
	}
	if code := owner.do("POST", "/api/auth/signup", signup, &me); code != 200 || me.User.Role != "admin" {
		t.Fatalf("bootstrap: %d %+v", code, me)
	}
	if code := anon.do("POST", "/api/auth/signup", signup, nil); code != 403 {
		t.Errorf("setup code reused: %d", code)
	}

	// Members join with an invite; admin-only routes refuse them.
	var inv struct{ Code, Link string }
	if code := owner.do("POST", "/api/admin/invites", map[string]any{"note": "testers", "maxUses": 5, "days": 7}, &inv); code != 200 || inv.Code == "" {
		t.Fatalf("invite: %d %+v", code, inv)
	}
	if inv.Link != "http://oracle.test/signup?invite="+inv.Code {
		t.Errorf("link: %s", inv.Link)
	}
	member := newClient(t, ts.URL)
	if code := member.do("POST", "/api/auth/signup", map[string]string{"email": "m@example.com", "password": "short", "invite": inv.Code}, nil); code != 400 {
		t.Errorf("weak password: %d", code)
	}
	if code := member.do("POST", "/api/auth/signup", map[string]string{"email": "m@example.com", "password": "another long one", "invite": inv.Code}, &me); code != 200 || me.User.Role != "member" {
		t.Fatalf("member sign-up: %d %+v", code, me)
	}
	for _, path := range []string{"/api/run-all", "/api/scan", "/api/models/train", "/api/admin/invites"} {
		if code := member.do("POST", path, map[string]any{}, nil); code != 403 {
			t.Errorf("member POST %s: %d", path, code)
		}
	}
	if code := member.do("GET", "/api/admin/users", nil, nil); code != 403 {
		t.Errorf("member lists users: %d", code)
	}

	// Finishing setup fills an empty watchlist from the stated interests.
	var saved struct{ Starter []string }
	if code := member.do("PUT", "/api/profile", map[string]any{"interests": []string{"funds"}, "horizon": "1y", "status": "done",
		"quizAnswers": map[string]int{"pe": 1}}, &saved); code != 200 || len(saved.Starter) != 2 {
		t.Fatalf("profile: %d %+v", code, saved)
	}
	var wl []struct{ Symbol string }
	member.do("GET", "/api/watchlist", nil, &wl)
	if len(wl) != 2 || wl[0].Symbol != "SPY" {
		t.Errorf("member watchlist: %+v", wl)
	}
	owner.do("GET", "/api/watchlist", nil, &wl)
	if len(wl) != 0 {
		t.Errorf("owner sees the member's watchlist: %+v", wl)
	}
	member.do("GET", "/api/auth/me", nil, &me)
	if me.Settings.Horizon != "1y" {
		t.Errorf("settings: %+v", me)
	}

	// Look and feel, and the Today screen.
	if code := member.do("PUT", "/api/account/prefs", map[string]string{"theme": "neon", "accent": "teal"}, nil); code != 400 {
		t.Errorf("bad theme: %d", code)
	}
	if code := member.do("PUT", "/api/account/prefs", map[string]string{"theme": "dark", "accent": "plum"}, nil); code != 200 {
		t.Errorf("save prefs: %d", code)
	}
	var withPrefs struct {
		Prefs struct{ Theme, Accent string }
	}
	member.do("GET", "/api/auth/me", nil, &withPrefs)
	if withPrefs.Prefs.Theme != "dark" || withPrefs.Prefs.Accent != "plum" {
		t.Errorf("prefs in me: %+v", withPrefs)
	}
	var td struct {
		Lines []any
		Rows  []struct{ Symbol string }
		Step  *struct {
			ID      string
			Skipped bool
		}
	}
	if code := member.do("GET", "/api/today", nil, &td); code != 200 || len(td.Rows) != 2 || len(td.Lines) != 1 {
		t.Fatalf("today with starter funds still being analysed: %d %+v", code, td)
	}
	// The owner's list is empty, so the suggestion is to add something.
	if code := owner.do("GET", "/api/today", nil, &td); code != 200 || td.Step == nil || td.Step.ID != "watch:first" {
		t.Fatalf("today (empty list): %d %+v", code, td)
	}
	first := td.Step.ID
	if code := owner.do("POST", "/api/today/step", map[string]string{"id": first, "action": "later"}, nil); code != 400 {
		t.Errorf("bad step action: %d", code)
	}
	owner.do("POST", "/api/today/step", map[string]string{"id": first, "action": "skip"}, nil)
	owner.do("GET", "/api/today", nil, &td)
	if td.Step == nil || td.Step.ID != first || !td.Step.Skipped {
		t.Errorf("after skip: %+v", td.Step)
	}
	owner.do("POST", "/api/today/step", map[string]string{"id": first, "action": "undo"}, nil)
	owner.do("GET", "/api/today", nil, &td)
	if td.Step == nil || td.Step.Skipped {
		t.Errorf("after undo: %+v", td.Step)
	}

	// Sign-in: wrong passwords are limited per account.
	other := newClient(t, ts.URL)
	codes := []int{}
	for range 9 {
		codes = append(codes, other.do("POST", "/api/auth/login", map[string]string{"email": "m@example.com", "password": "wrong password"}, nil))
	}
	if codes[0] != 401 || codes[8] != 429 {
		t.Errorf("login attempts: %v", codes)
	}
	if code := other.do("POST", "/api/auth/login", map[string]string{"email": "nobody@example.com", "password": "whatever it is"}, nil); code != 401 {
		t.Errorf("unknown email: %d", code)
	}

	// Signing out ends the session on the server, not just the cookie.
	if code := owner.do("POST", "/api/auth/login", map[string]string{"email": "OWNER@example.com", "password": "a long passphrase"}, nil); code != 200 {
		t.Errorf("login (email case-insensitive): %d", code)
	}
	member.do("POST", "/api/auth/logout", nil, nil)
	if code := member.do("GET", "/api/watchlist", nil, nil); code != 401 {
		t.Errorf("after logout: %d", code)
	}

	// Removing the last admin is refused.
	var users []struct {
		ID   int64
		Role string
	}
	owner.do("GET", "/api/admin/users", nil, &users)
	if len(users) != 2 {
		t.Fatalf("users: %+v", users)
	}
	if code := owner.do("PUT", "/api/admin/users/1", map[string]any{"role": "member"}, nil); code != 409 {
		t.Errorf("demote last admin: %d", code)
	}
}
