package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

const (
	secret = "test-secret-of-at-least-32-bytes!!"
	userID = int64(34633089486)
)

type fakeAccounts map[string]string // username -> bcrypt hash; every user has userID

func (f fakeAccounts) FindByUsername(ctx context.Context, username string) (int64, string, bool, error) {
	hash, ok := f[username]
	return userID, hash, ok, nil
}

func newService(t *testing.T, ttl time.Duration) *Service {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte("password1"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	return New(fakeAccounts{"player1": string(hash)}, secret, ttl)
}

func TestLoginIssuesTokenForTheUser(t *testing.T) {
	s := newService(t, time.Hour)
	token, expiresAt, err := s.Login(context.Background(), "player1", "password1")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if d := time.Until(expiresAt); d < 59*time.Minute || d > time.Hour {
		t.Errorf("expiresAt in %v, want about 1h", d)
	}
	got, err := s.UserID(token)
	if err != nil || got != userID {
		t.Errorf("UserID = %d, %v; want %d", got, err, userID)
	}
}

// Unknown user and wrong password give the same error (8.1).
func TestLoginFailuresLookTheSame(t *testing.T) {
	s := newService(t, time.Hour)
	_, _, wrongPassword := s.Login(context.Background(), "player1", "nope")
	_, _, unknownUser := s.Login(context.Background(), "nobody", "password1")
	if !errors.Is(wrongPassword, ErrUnauthorized) || wrongPassword != unknownUser {
		t.Errorf("wrong password: %v, unknown user: %v; want the same ErrUnauthorized", wrongPassword, unknownUser)
	}
	// The password of the dummy hash must not open an unknown user.
	if _, _, err := s.Login(context.Background(), "nobody", "dummy"); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("unknown user with the dummy password: err = %v, want ErrUnauthorized", err)
	}
}

func TestUserIDRejectsBadTokens(t *testing.T) {
	s := newService(t, time.Hour)
	claims := jwt.RegisteredClaims{Subject: "34633089486", ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))}

	expired, _, err := newService(t, -time.Minute).Login(context.Background(), "player1", "password1")
	if err != nil {
		t.Fatal(err)
	}
	otherSecret, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte("another-secret-of-at-least-32-bytes"))
	unsigned, _ := jwt.NewWithClaims(jwt.SigningMethodNone, claims).SignedString(jwt.UnsafeAllowNoneSignatureType)
	noExpiry, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{Subject: "34633089486"}).SignedString([]byte(secret))
	claims.Subject = "abc"
	badSubject, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))

	for name, token := range map[string]string{
		"expired":      expired,
		"other secret": otherSecret,
		"alg none":     unsigned,
		"no expiry":    noExpiry,
		"bad subject":  badSubject,
		"garbage":      "not.a.token",
		"empty":        "",
	} {
		if _, err := s.UserID(token); !errors.Is(err, ErrUnauthorized) {
			t.Errorf("%s: err = %v, want ErrUnauthorized", name, err)
		}
	}
}
