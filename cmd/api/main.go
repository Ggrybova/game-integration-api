package main

import (
	"log/slog"
	"net/http"
	"os"

	"github.com/Ggrybova/game-integration-api/internal/config"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}
	_ = cfg // passed to the adapters in step 6

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})

	slog.Info("starting server", "addr", ":8080")
	if err := http.ListenAndServe(":8080", mux); err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}
