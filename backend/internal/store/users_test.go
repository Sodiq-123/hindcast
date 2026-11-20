package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sodiq/financial-market/backend/internal/auth"
	"github.com/sodiq/financial-market/backend/internal/profile"
)

// testUser creates a member (sign-up open) and returns its id.
func testUser(t *testing.T, st *Store, email string) int64 {
	t.Helper()
	u, err := st.CreateAccount(context.Background(), Signup{Email: email, PasswordHash: "x"})
	if err != nil {
		t.Fatal(err)
	}
	return u.ID
}

func TestBootstrapClaimsExistingData(t *testing.T) {
	st := OpenTest(t)
	ctx := context.Background()
	// Data from before accounts existed.
	if _, err := st.db.Exec(`INSERT INTO watchlist(symbol, kind, added_at) VALUES('AAPL', 'asset', now())`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`INSERT INTO positions(symbol, kind, opened_at, entry, quantity) VALUES('PAID', 'asset', '2026-10-01', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting(ctx, "journal", map[string]float64{"account": 5000}); err != nil {
		t.Fatal(err)
	}
	admin, err := st.CreateAccount(ctx, Signup{Email: " Owner@Example.com ", Name: "Owner", PasswordHash: "x", Bootstrap: true})
	if err != nil {
		t.Fatal(err)
	}
	if admin.Role != auth.RoleAdmin || admin.Email != "owner@example.com" {
		t.Fatalf("admin: %+v", admin)
	}
	if wl, _ := st.Watchlist(ctx, admin.ID); len(wl) != 1 || wl[0] != "AAPL" {
		t.Errorf("watchlist not claimed: %v", wl)
	}
	if ps, _ := st.Positions(ctx, admin.ID); len(ps) != 1 || ps[0].Symbol != "PAID" {
		t.Errorf("positions not claimed: %v", ps)
	}
	var js map[string]float64
	if ok, _ := st.Setting(ctx, JournalSettingsKey(admin.ID), &js); !ok || js["account"] != 5000 {
		t.Errorf("journal settings not moved: %v %v", ok, js)
	}
	if _, err := st.CreateAccount(ctx, Signup{Email: "second@example.com", PasswordHash: "x", Bootstrap: true}); !errors.Is(err, ErrBootstrapUsed) {
		t.Errorf("second bootstrap: %v", err)
	}
	if _, err := st.CreateAccount(ctx, Signup{Email: "OWNER@example.com", PasswordHash: "x"}); !errors.Is(err, ErrEmailTaken) {
		t.Errorf("duplicate email (any case): %v", err)
	}
	if id, err := st.OwnerID(ctx); err != nil || id != admin.ID {
		t.Errorf("owner: %d %v", id, err)
	}
}

func TestInvites(t *testing.T) {
	st := OpenTest(t)
	ctx := context.Background()
	code, hash, _ := auth.NewToken()
	if _, err := st.CreateInvite(ctx, hash, "testers", auth.RoleMember, 2, time.Now().Add(time.Hour), 0); err == nil {
		t.Fatal("created_by 0 should violate the foreign key")
	}
	admin := testUser(t, st, "admin@example.com")
	id, err := st.CreateInvite(ctx, hash, "testers", auth.RoleMember, 2, time.Now().Add(time.Hour), admin)
	if err != nil {
		t.Fatal(err)
	}
	for i, email := range []string{"a@example.com", "b@example.com", "c@example.com"} {
		_, err := st.CreateAccount(ctx, Signup{Email: email, PasswordHash: "x", InviteHash: auth.HashToken(code)})
		if (i < 2) != (err == nil) {
			t.Errorf("use %d: %v", i+1, err)
		}
	}
	// A failed sign-up (taken email) must not use up the invite.
	_, hash2, _ := auth.NewToken()
	id2, _ := st.CreateInvite(ctx, hash2, "", auth.RoleMember, 1, time.Now().Add(time.Hour), admin)
	if _, err := st.CreateAccount(ctx, Signup{Email: "a@example.com", PasswordHash: "x", InviteHash: hash2}); !errors.Is(err, ErrEmailTaken) {
		t.Fatal(err)
	}
	if ok, _ := st.InviteValid(ctx, hash2); !ok {
		t.Error("invite consumed by a failed sign-up")
	}
	if err := st.RevokeInvite(ctx, id2); err != nil {
		t.Fatal(err)
	}
	if ok, _ := st.InviteValid(ctx, hash2); ok {
		t.Error("revoked invite still valid")
	}
	inv, _ := st.Invites(ctx)
	if len(inv) != 2 || inv[1].ID != id || inv[1].Uses != 2 || inv[1].CreatedBy != "admin@example.com" {
		t.Errorf("invites: %+v", inv)
	}
}

func TestSessionsAndPasswords(t *testing.T) {
	st := OpenTest(t)
	ctx := context.Background()
	u := testUser(t, st, "a@example.com")
	_, h1, _ := auth.NewToken()
	_, h2, _ := auth.NewToken()
	for _, h := range [][]byte{h1, h2} {
		if err := st.CreateSession(ctx, h, u, "test", "127.0.0.1"); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := st.SessionUser(ctx, h1); err != nil || got.ID != u {
		t.Fatalf("session: %+v %v", got, err)
	}
	ss, _ := st.Sessions(ctx, u, h1)
	if len(ss) != 2 || !(ss[0].Current || ss[1].Current) {
		t.Errorf("sessions: %+v", ss)
	}
	// A password change keeps this session and ends the others.
	if err := st.SetPassword(ctx, u, "y", h1); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SessionUser(ctx, h2); !errors.Is(err, ErrNotFound) {
		t.Errorf("other session survived a password change: %v", err)
	}
	if _, err := st.SessionUser(ctx, h1); err != nil {
		t.Errorf("current session ended: %v", err)
	}
	// Reset links work once.
	_, rh, _ := auth.NewToken()
	if err := st.CreateReset(ctx, rh, u, time.Hour); err != nil {
		t.Fatal(err)
	}
	if id, err := st.UseReset(ctx, rh, "z"); err != nil || id != u {
		t.Fatalf("reset: %d %v", id, err)
	}
	if _, err := st.UseReset(ctx, rh, "w"); !errors.Is(err, ErrResetInvalid) {
		t.Errorf("reset link reused: %v", err)
	}
	if h, _ := st.PasswordHash(ctx, u); h != "z" {
		t.Errorf("password after reset: %q", h)
	}
	if _, err := st.SessionUser(ctx, h1); !errors.Is(err, ErrNotFound) {
		t.Error("a reset must sign out every session")
	}
	// Disabled accounts can't use their sessions.
	_, h3, _ := auth.NewToken()
	_ = st.CreateSession(ctx, h3, u, "", "")
	if err := st.SetDisabled(ctx, u, true); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SessionUser(ctx, h3); !errors.Is(err, ErrNotFound) {
		t.Error("disabled user still signed in")
	}
}

func TestDataIsPrivate(t *testing.T) {
	st := OpenTest(t)
	ctx := context.Background()
	a, b := testUser(t, st, "a@example.com"), testUser(t, st, "b@example.com")
	_ = st.AddWatch(ctx, a, "AAPL")
	_ = st.AddWatch(ctx, b, "AAPL")
	_ = st.AddWatch(ctx, b, "NVDA")
	if wl, _ := st.Watchlist(ctx, a); len(wl) != 1 {
		t.Errorf("a sees %v", wl)
	}
	if all, _ := st.AllWatched(ctx); len(all) != 2 {
		t.Errorf("all watched: %v", all)
	}
	p := &Position{Symbol: "AAPL", Kind: "asset", OpenedAt: "2026-11-01", Entry: 100, Quantity: 1}
	if err := st.AddPosition(ctx, a, p); err != nil {
		t.Fatal(err)
	}
	if err := st.ClosePosition(ctx, b, p.ID, "2026-11-02", 110); !errors.Is(err, ErrNotFound) {
		t.Errorf("b closed a's position: %v", err)
	}
	if err := st.DeletePosition(ctx, b, p.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("b deleted a's position: %v", err)
	}
	if ps, _ := st.Positions(ctx, b); len(ps) != 0 {
		t.Errorf("b sees %v", ps)
	}
	if err := st.DeleteUser(ctx, a); err != nil {
		t.Fatal(err)
	}
	if ps, _ := st.Positions(ctx, a); len(ps) != 0 {
		t.Error("positions outlived their account")
	}
}

func TestLastAdmin(t *testing.T) {
	st := OpenTest(t)
	ctx := context.Background()
	admin, _ := st.CreateAccount(ctx, Signup{Email: "admin@example.com", PasswordHash: "x", Bootstrap: true})
	m := testUser(t, st, "m@example.com")
	for name, err := range map[string]error{
		"demote":  st.SetRole(ctx, admin.ID, auth.RoleMember),
		"disable": st.SetDisabled(ctx, admin.ID, true),
		"delete":  st.DeleteUser(ctx, admin.ID),
	} {
		if !errors.Is(err, ErrLastAdmin) {
			t.Errorf("%s the last admin: %v", name, err)
		}
	}
	if err := st.SetRole(ctx, m, auth.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	if err := st.SetRole(ctx, admin.ID, auth.RoleMember); err != nil {
		t.Errorf("demote with another admin: %v", err)
	}
	if err := st.SetRole(ctx, 9999, auth.RoleMember); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown user: %v", err)
	}
}

func TestProfileAndUsage(t *testing.T) {
	st := OpenTest(t)
	ctx := context.Background()
	u := testUser(t, st, "a@example.com")
	p, err := st.Profile(ctx, u)
	if err != nil || p.Status != "" {
		t.Fatalf("empty profile: %+v %v", p, err)
	}
	p = &profile.Profile{Experience: "new", Horizon: "1y", Interests: []string{"crypto"}, Status: "done"}
	if err := st.SaveProfile(ctx, u, p); err != nil {
		t.Fatal(err)
	}
	got, _ := st.Profile(ctx, u)
	if got.Horizon != "1y" || got.Status != "done" || got.UpdatedAt.IsZero() {
		t.Errorf("profile: %+v", got)
	}
	_ = st.CountAction(ctx, u, "research")
	_ = st.CountAction(ctx, u, "research")
	if n, _ := st.ActionsSince(ctx, u, "research", time.Now().Add(-time.Hour)); n != 2 {
		t.Errorf("usage: %d", n)
	}
}
