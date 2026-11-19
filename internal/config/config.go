// Package config reads the service configuration from environment variables.
package config

import (
	"errors"
	"fmt"
	"os"
	"time"
)

type Config struct {
	DatabaseURL   string
	WalletURL     string
	WalletAPIKey  string
	WalletTimeout time.Duration
	JWTSecret     string
	JWTTTL        time.Duration
}

func Load() (Config, error) {
	walletTimeout, err := time.ParseDuration(os.Getenv("WALLET_TIMEOUT"))
	if err != nil {
		return Config{}, fmt.Errorf("WALLET_TIMEOUT: %w", err)
	}
	jwtTTL, err := time.ParseDuration(os.Getenv("JWT_TTL"))
	if err != nil {
		return Config{}, fmt.Errorf("JWT_TTL: %w", err)
	}

	c := Config{
		DatabaseURL:   os.Getenv("DATABASE_URL"),
		WalletURL:     os.Getenv("WALLET_URL"),
		WalletAPIKey:  os.Getenv("WALLET_API_KEY"),
		WalletTimeout: walletTimeout,
		JWTSecret:     os.Getenv("JWT_SECRET"),
		JWTTTL:        jwtTTL,
	}
	if c.DatabaseURL == "" || c.WalletURL == "" || c.WalletAPIKey == "" || c.JWTSecret == "" {
		return Config{}, errors.New("DATABASE_URL, WALLET_URL, WALLET_API_KEY and JWT_SECRET are required")
	}
	return c, nil
}
