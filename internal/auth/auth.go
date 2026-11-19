// Package auth checks passwords and issues and verifies JWTs (assignment 8.1).
package auth

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

// ErrUnauthorized is the same for an unknown user, a wrong password and a bad token.
var ErrUnauthorized = errors.New("unauthorized")

// Accounts finds a player by username. found is false when there is none.
type Accounts interface {
	FindByUsername(ctx context.Context, username string) (userID int64, passwordHash string, found bool, err error)
}

// dummyHash is checked for unknown users, so they take as long as a wrong password
// and response times do not reveal which usernames exist.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("dummy"), bcrypt.DefaultCost)

type Service struct {
	accounts Accounts
	secret   []byte
	ttl      time.Duration
}

func New(accounts Accounts, secret string, ttl time.Duration) *Service {
	return &Service{accounts: accounts, secret: []byte(secret), ttl: ttl}
}

// Login returns a signed token with the wallet user ID as subject and an expiry.
func (s *Service) Login(ctx context.Context, username, password string) (token string, expiresAt time.Time, err error) {
	userID, hash, found, err := s.accounts.FindByUsername(ctx, username)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("find account: %w", err)
	}
	if !found {
		hash = string(dummyHash)
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil || !found {
		return "", time.Time{}, ErrUnauthorized
	}

	expiresAt = time.Now().Add(s.ttl).UTC().Truncate(time.Second) // JWT stores whole seconds
	claims := jwt.RegisteredClaims{
		Subject:   strconv.FormatInt(userID, 10),
		ExpiresAt: jwt.NewNumericDate(expiresAt),
	}
	token, err = jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(s.secret)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("sign token: %w", err)
	}
	return token, expiresAt, nil
}

// UserID checks the token's signature, algorithm and expiry and returns its subject.
func (s *Service) UserID(token string) (int64, error) {
	var claims jwt.RegisteredClaims
	_, err := jwt.ParseWithClaims(token, &claims,
		func(*jwt.Token) (any, error) { return s.secret, nil },
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}), // rejects "none" and other algorithms
		jwt.WithExpirationRequired(),
	)
	if err != nil {
		return 0, ErrUnauthorized
	}
	userID, err := strconv.ParseInt(claims.Subject, 10, 64)
	if err != nil {
		return 0, ErrUnauthorized
	}
	return userID, nil
}
