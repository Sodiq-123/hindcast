package auth

import (
	"strings"
	"testing"
	"time"
)

func TestPasswordHashing(t *testing.T) {
	h, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Errorf("hash format: %s", h)
	}
	if !VerifyPassword("correct horse battery", h) || VerifyPassword("correct horse batterY", h) {
		t.Error("verify")
	}
	h2, _ := HashPassword("correct horse battery")
	if h == h2 {
		t.Error("same hash twice: salt not random")
	}
	for _, bad := range []string{"", "$argon2id$v=19$m=1,t=1,p=1$$", "$bcrypt$x", "$argon2id$v=19$m=999999999,t=2,p=1$c2FsdA$a2V5"} {
		if VerifyPassword("x", bad) {
			t.Errorf("accepted malformed hash %q", bad)
		}
	}
}

func TestCheckPassword(t *testing.T) {
	cases := map[string]bool{
		"short":                  false,
		"Password123":            false, // common, any case
		"me@example.com":         false, // the email itself
		"a long passphrase here": true,
		strings.Repeat("x", 129): false,
	}
	for pw, ok := range cases {
		if err := CheckPassword(pw, "me@example.com"); (err == nil) != ok {
			t.Errorf("%q: %v", pw, err)
		}
	}
}

func TestLimiter(t *testing.T) {
	l := NewLimiter(2, 50*time.Millisecond)
	for i, want := range []bool{true, true, false} {
		if ok, _ := l.Allow("k"); ok != want {
			t.Errorf("attempt %d: %v", i+1, ok)
		}
	}
	if ok, _ := l.Allow("other"); !ok {
		t.Error("keys must be independent")
	}
	time.Sleep(60 * time.Millisecond)
	if b, _ := l.Blocked("k"); b {
		t.Error("window did not slide")
	}
	l.Add("k")
	l.Add("k")
	l.Reset("k")
	if b, _ := l.Blocked("k"); b {
		t.Error("reset")
	}
}

func TestTokens(t *testing.T) {
	a, ha, _ := NewToken()
	b, _, _ := NewToken()
	if a == b || len(a) < 40 || string(HashToken(a)) != string(ha) {
		t.Errorf("tokens: %q %q", a, b)
	}
}
