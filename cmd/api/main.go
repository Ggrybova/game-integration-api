package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/shopspring/decimal"

	"github.com/Ggrybova/game-integration-api/internal/auth"
	"github.com/Ggrybova/game-integration-api/internal/betting"
	"github.com/Ggrybova/game-integration-api/internal/config"
	"github.com/Ggrybova/game-integration-api/internal/httpapi"
	"github.com/Ggrybova/game-integration-api/internal/postgres"
	"github.com/Ggrybova/game-integration-api/internal/wallet"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	cfg, err := config.Load()
	if err != nil {
		log.Error("load config", "error", err)
		os.Exit(1)
	}

	// Money is written as JSON numbers, not strings (decisions.md).
	decimal.MarshalJSONWithoutQuotes = true

	store, err := postgres.New(context.Background(), cfg.DatabaseURL)
	if err != nil {
		log.Error("connect to database", "error", err)
		os.Exit(1)
	}
	defer store.Close()

	walletClient := wallet.New(cfg.WalletURL, cfg.WalletAPIKey, cfg.WalletTimeout)
	bets := betting.NewService(store, walletClient, log)
	logins := auth.New(store, cfg.JWTSecret, cfg.JWTTTL)

	srv := &http.Server{
		Addr:              ":8080",
		Handler:           httpapi.New(logins, bets, log),
		ReadHeaderTimeout: 5 * time.Second, // a client cannot hold a connection by sending headers slowly
	}
	log.Info("starting server", "addr", srv.Addr)
	if err := srv.ListenAndServe(); err != nil {
		log.Error("server stopped", "error", err)
		os.Exit(1)
	}
}
