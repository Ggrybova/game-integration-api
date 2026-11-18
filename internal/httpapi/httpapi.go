// Package httpapi exposes the service over HTTP (assignment 7 and 8).
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/Ggrybova/game-integration-api/internal/auth"
	"github.com/Ggrybova/game-integration-api/internal/betting"
)

// Auth and Bets are what the handlers need; *auth.Service and
// *betting.Service implement them, and tests use small stubs.
type Auth interface {
	Login(ctx context.Context, username, password string) (token string, expiresAt time.Time, err error)
	UserID(token string) (int64, error)
}

type Bets interface {
	Withdraw(ctx context.Context, req betting.WithdrawRequest) (betting.WithdrawResult, error)
}

type server struct {
	auth Auth
	bets Bets
	log  *slog.Logger
}

// New returns the router. Money is written as JSON numbers because main sets
// decimal.MarshalJSONWithoutQuotes (decisions.md).
func New(a Auth, b Bets, log *slog.Logger) http.Handler {
	s := &server{auth: a, bets: b, log: log}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("POST /api/v1/auth/login", s.login)
	mux.HandleFunc("POST /api/v1/withdraw", s.requireAuth(s.withdraw))
	return mux
}

type userIDKey struct{}

// requireAuth lets the request through only with a valid Bearer token and
// passes the token's wallet user ID on; the body never chooses the player.
func (s *server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok {
			writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "missing Bearer token")
			return
		}
		userID, err := s.auth.UserID(token)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "invalid or expired token")
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), userIDKey{}, userID)))
	}
}

func (s *server) login(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decode(w, r, &req) {
		return
	}
	token, expiresAt, err := s.auth.Login(r.Context(), req.Username, req.Password)
	if errors.Is(err, auth.ErrUnauthorized) {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "wrong username or password")
		return
	}
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"token": token, "tokenType": "Bearer", "expiresAt": expiresAt})
}

func (s *server) withdraw(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Currency              string          `json:"currency"`
		Amount                decimal.Decimal `json:"amount"`
		ProviderTransactionID string          `json:"providerTransactionId"`
	}
	if !decode(w, r, &req) {
		return
	}
	res, err := s.bets.Withdraw(r.Context(), betting.WithdrawRequest{
		UserID:       r.Context().Value(userIDKey{}).(int64),
		Currency:     req.Currency,
		Amount:       req.Amount,
		ProviderTxID: req.ProviderTransactionID,
	})
	if err != nil {
		s.bettingError(w, err)
		return
	}

	status := map[betting.Outcome]int{
		betting.OutcomeCreated: http.StatusCreated,
		betting.OutcomeReplay:  http.StatusOK,
		betting.OutcomePending: http.StatusAccepted,
	}[res.Outcome]
	writeJSON(w, status, transactionResponse(res.Transaction, res.BetStatus))
}

// transactionResponse holds the fields of assignment 7.4.
func transactionResponse(tx betting.Transaction, bet betting.BetStatus) any {
	var betStatus *betting.BetStatus // null while the withdraw is PENDING
	if bet != "" {
		betStatus = &bet
	}
	return struct {
		TransactionID         string              `json:"transactionId"`
		ProviderTransactionID string              `json:"providerTransactionId"`
		OldBalance            decimal.Decimal     `json:"oldBalance"`
		NewBalance            decimal.NullDecimal `json:"newBalance"` // null while PENDING
		Status                betting.TxStatus    `json:"status"`
		BetStatus             *betting.BetStatus  `json:"betStatus"`
	}{tx.ID.String(), tx.ProviderTransactionID, tx.OldBalance.Decimal, tx.NewBalance, tx.Status, betStatus}
}

// bettingError maps business errors to the codes of assignment 7.3.
func (s *server) bettingError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, betting.ErrInvalidRequest):
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
	case errors.Is(err, betting.ErrNotFound):
		writeError(w, http.StatusNotFound, "NOT_FOUND", "transaction not found")
	case errors.Is(err, betting.ErrConflict):
		writeError(w, http.StatusConflict, "CONFLICT", err.Error())
	case errors.Is(err, betting.ErrInsufficientFunds):
		writeError(w, http.StatusUnprocessableEntity, "INSUFFICIENT_FUNDS", "insufficient funds")
	case errors.Is(err, betting.ErrWalletRejected), errors.Is(err, betting.ErrWalletUnavailable):
		s.log.Warn("wallet error", "error", err)
		writeError(w, http.StatusBadGateway, "WALLET_ERROR", "the wallet did not apply the operation")
	default:
		s.internalError(w, err)
	}
}

func (s *server) internalError(w http.ResponseWriter, err error) {
	s.log.Error("internal error", "error", err)
	writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal error")
}

// decode reads a JSON body of at most 1 MB; on failure it answers 400.
func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "malformed JSON body")
		return false
	}
	return true
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{"code": code, "message": message})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
