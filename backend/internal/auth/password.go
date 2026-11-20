// Package auth holds the security pieces of user accounts: password hashing,
// session tokens, the signed-in user on a request's context, and rate limits
// on guessable endpoints. Storage is in package store; HTTP handlers in api.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// Argon2id with OWASP's recommended minimum (19 MiB, 2 passes, 1 lane).
const (
	argonTime    = 2
	argonMemory  = 19 * 1024
	argonThreads = 1
	argonKeyLen  = 32
	saltLen      = 16
)

const (
	MinPassword = 10
	MaxPassword = 128 // hashing cost grows with length; cap it
)

// hashSlots bounds concurrent hashes so a burst of logins can't exhaust memory.
var hashSlots = make(chan struct{}, 4)

// CheckPassword returns a user-facing reason a new password is unacceptable.
func CheckPassword(pw, email string) error {
	n := utf8.RuneCountInString(pw)
	switch {
	case n < MinPassword:
		return fmt.Errorf("use at least %d characters", MinPassword)
	case n > MaxPassword:
		return fmt.Errorf("use at most %d characters", MaxPassword)
	case email != "" && strings.EqualFold(pw, email):
		return errors.New("don't use your email address as the password")
	case common[strings.ToLower(pw)]:
		return errors.New("that password is too common; pick something less guessable")
	}
	return nil
}

var common = map[string]bool{
	"password12": true, "password123": true, "password1234": true, "1234567890": true, "12345678910": true,
	"qwertyuiop": true, "qwerty12345": true, "iloveyou12": true, "letmein123": true, "welcome123": true,
	"administrator": true, "passw0rd123": true, "abcdefghij": true, "0987654321": true, "1111111111": true,
	"marketoracle": true, "hindcast": true, "hindcast123": true, "trading123": true, "bitcoin123": true,
}

// HashPassword returns a self-describing argon2id hash
// ($argon2id$v=19$m=…,t=…,p=…$salt$key).
func HashPassword(pw string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	hashSlots <- struct{}{}
	key := argon2.IDKey([]byte(pw), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	<-hashSlots
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, argonMemory, argonTime, argonThreads,
		b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

// VerifyPassword reports whether pw matches the stored hash.
func VerifyPassword(pw, hash string) bool {
	parts := strings.Split(hash, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var m, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil || m == 0 || m > 256*1024 || t == 0 || t > 10 || p == 0 {
		return false
	}
	b64 := base64.RawStdEncoding
	salt, err1 := b64.DecodeString(parts[4])
	want, err2 := b64.DecodeString(parts[5])
	if err1 != nil || err2 != nil || len(want) == 0 {
		return false
	}
	hashSlots <- struct{}{}
	got := argon2.IDKey([]byte(pw), salt, t, m, p, uint32(len(want)))
	<-hashSlots
	return subtle.ConstantTimeCompare(got, want) == 1
}

// dummyHash is verified against when an email has no account, so a login
// takes as long whether or not the account exists.
var dummyHash, _ = HashPassword("not-a-real-password-just-timing")

func SpendVerifyTime(pw string) { VerifyPassword(pw, dummyHash) }

// NewToken returns a random URL-safe token (for sessions, invites and reset
// links) and the SHA-256 hash that is stored instead of it.
func NewToken() (token string, hash []byte, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", nil, err
	}
	token = base64.RawURLEncoding.EncodeToString(b)
	return token, HashToken(token), nil
}

func HashToken(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}
