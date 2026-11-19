package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/shopspring/decimal"

	"github.com/Ggrybova/game-integration-api/internal/auth"
	"github.com/Ggrybova/game-integration-api/internal/betting"
)

func TestMain(m *testing.M) {
	decimal.MarshalJSONWithoutQuotes = true // as main does
	os.Exit(m.Run())
}

const player = int64(34633089486)

// stubAuth accepts player1/password1 and the token "good".
type stubAuth struct{}

func (stubAuth) Login(ctx context.Context, username, password string) (string, time.Time, error) {
	if username == "player1" && password == "password1" {
		return "good", time.Date(2026, 10, 27, 18, 30, 0, 0, time.UTC), nil
	}
	return "", time.Time{}, auth.ErrUnauthorized
}

func (stubAuth) UserID(token string) (int64, error) {
	if token == "good" {
		return player, nil
	}
	return 0, auth.ErrUnauthorized
}

// stubBets records the request and answers with res and err.
type stubBets struct {
	got *betting.WithdrawRequest
	res betting.WithdrawResult
	err error
}

func (b *stubBets) Withdraw(ctx context.Context, req betting.WithdrawRequest) (betting.WithdrawResult, error) {
	b.got = &req
	return b.res, b.err
}

// Player answers for the player of the token with a fixed balance, or b.err.
func (b *stubBets) Player(ctx context.Context, userID int64) (betting.Account, decimal.Decimal, error) {
	if b.err != nil {
		return betting.Account{}, decimal.Decimal{}, b.err
	}
	return betting.Account{UserID: userID, Currency: "USD"}, decimal.RequireFromString("5000.00"), nil
}

func do(t *testing.T, bets *stubBets, path, token, body string) (*http.Response, map[string]any, string) {
	t.Helper()
	return doMethod(t, http.MethodPost, bets, path, token, body)
}

func doMethod(t *testing.T, method string, bets *stubBets, path, token, body string) (*http.Response, map[string]any, string) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	New(stubAuth{}, bets, slog.New(slog.DiscardHandler)).ServeHTTP(rec, req)

	raw := rec.Body.String()
	var parsed map[string]any
	_ = json.Unmarshal([]byte(raw), &parsed)
	return rec.Result(), parsed, raw
}

const withdrawBody = `{"currency":"USD","amount":10.50,"providerTransactionId":"p-1"}`

func TestLogin(t *testing.T) {
	resp, body, _ := do(t, &stubBets{}, "/api/v1/auth/login", "", `{"username":"player1","password":"password1"}`)
	if resp.StatusCode != 200 || body["token"] != "good" || body["tokenType"] != "Bearer" ||
		body["expiresAt"] != "2026-10-27T18:30:00Z" {
		t.Errorf("status %d, body %v", resp.StatusCode, body)
	}

	resp, body, _ = do(t, &stubBets{}, "/api/v1/auth/login", "", `{"username":"player1","password":"nope"}`)
	if resp.StatusCode != 401 || body["code"] != "UNAUTHORIZED" {
		t.Errorf("wrong password: status %d, body %v", resp.StatusCode, body)
	}
}

func TestWithdrawNeedsAValidToken(t *testing.T) {
	for _, token := range []string{"", "bad"} {
		bets := &stubBets{}
		resp, body, _ := do(t, bets, "/api/v1/withdraw", token, withdrawBody)
		if resp.StatusCode != 401 || body["code"] != "UNAUTHORIZED" || bets.got != nil {
			t.Errorf("token %q: status %d, body %v, service called: %v", token, resp.StatusCode, body, bets.got != nil)
		}
	}
}

// The player comes from the token and the amount stays exact.
func TestWithdrawRequest(t *testing.T) {
	bets := &stubBets{res: betting.WithdrawResult{Outcome: betting.OutcomeCreated}}
	do(t, bets, "/api/v1/withdraw", "good", withdrawBody)
	if bets.got == nil || bets.got.UserID != player || bets.got.Currency != "USD" ||
		bets.got.Amount.String() != "10.5" || bets.got.ProviderTxID != "p-1" {
		t.Errorf("service got %+v", bets.got)
	}
}

func TestWithdrawResponses(t *testing.T) {
	tx := betting.Transaction{
		ID:                    uuid.MustParse("b3f1c2d4-5e6f-4a7b-8c9d-0e1f2a3b4c5d"),
		ProviderTransactionID: "p-1",
		Status:                betting.TxCompleted,
		OldBalance:            decimal.NewNullDecimal(decimal.RequireFromString("5000.00")),
		NewBalance:            decimal.NewNullDecimal(decimal.RequireFromString("4989.50")),
	}
	pending := tx
	pending.Status, pending.NewBalance = betting.TxPending, decimal.NullDecimal{}

	tests := []struct {
		name   string
		res    betting.WithdrawResult
		status int
		want   string // the exact JSON body
	}{
		{"created", betting.WithdrawResult{Transaction: tx, BetStatus: betting.BetPlaced, Outcome: betting.OutcomeCreated}, 201,
			`{"transactionId":"b3f1c2d4-5e6f-4a7b-8c9d-0e1f2a3b4c5d","providerTransactionId":"p-1","oldBalance":5000,"newBalance":4989.5,"status":"COMPLETED","betStatus":"PLACED"}`},
		{"replay", betting.WithdrawResult{Transaction: tx, BetStatus: betting.BetPlaced, Outcome: betting.OutcomeReplay}, 200,
			`{"transactionId":"b3f1c2d4-5e6f-4a7b-8c9d-0e1f2a3b4c5d","providerTransactionId":"p-1","oldBalance":5000,"newBalance":4989.5,"status":"COMPLETED","betStatus":"PLACED"}`},
		{"pending", betting.WithdrawResult{Transaction: pending, Outcome: betting.OutcomePending}, 202,
			`{"transactionId":"b3f1c2d4-5e6f-4a7b-8c9d-0e1f2a3b4c5d","providerTransactionId":"p-1","oldBalance":5000,"newBalance":null,"status":"PENDING","betStatus":null}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, _, raw := do(t, &stubBets{res: tt.res}, "/api/v1/withdraw", "good", withdrawBody)
			if resp.StatusCode != tt.status || strings.TrimSpace(raw) != tt.want {
				t.Errorf("status %d, body %s\nwant %d, %s", resp.StatusCode, raw, tt.status, tt.want)
			}
		})
	}
}

// Business errors map to the codes of assignment 7.3.
func TestWithdrawErrors(t *testing.T) {
	tests := []struct {
		err    error
		status int
		code   string
	}{
		{fmt.Errorf("%w: bad amount", betting.ErrInvalidRequest), 400, "INVALID_REQUEST"},
		{betting.ErrNotFound, 404, "NOT_FOUND"},
		{fmt.Errorf("%w: different payload", betting.ErrConflict), 409, "CONFLICT"},
		{fmt.Errorf("wallet withdraw: %w", betting.ErrInsufficientFunds), 422, "INSUFFICIENT_FUNDS"},
		{fmt.Errorf("wallet withdraw: %w", betting.ErrWalletRejected), 502, "WALLET_ERROR"},
		{fmt.Errorf("%w: read balance", betting.ErrWalletUnavailable), 502, "WALLET_ERROR"},
		{errors.New("db down"), 500, "INTERNAL_ERROR"},
	}
	for _, tt := range tests {
		resp, body, _ := do(t, &stubBets{err: tt.err}, "/api/v1/withdraw", "good", withdrawBody)
		if resp.StatusCode != tt.status || body["code"] != tt.code {
			t.Errorf("%v: status %d, body %v; want %d %s", tt.err, resp.StatusCode, body, tt.status, tt.code)
		}
	}
}

func TestMalformedBody(t *testing.T) {
	resp, body, _ := do(t, &stubBets{}, "/api/v1/withdraw", "good", `{"amount":`)
	if resp.StatusCode != 400 || body["code"] != "INVALID_REQUEST" {
		t.Errorf("status %d, body %v", resp.StatusCode, body)
	}
}

// GET /player (8.2): the player from the token, the balance as a number,
// 502 or 504 when the Wallet cannot be read, never a stale balance.
func TestPlayer(t *testing.T) {
	resp, _, raw := doMethod(t, http.MethodGet, &stubBets{}, "/api/v1/player", "good", "")
	if want := `{"userId":34633089486,"balance":5000,"currency":"USD"}`; resp.StatusCode != 200 || strings.TrimSpace(raw) != want {
		t.Errorf("status %d, body %s; want 200 %s", resp.StatusCode, raw, want)
	}

	for _, tt := range []struct {
		name   string
		token  string
		err    error
		status int
		code   string
	}{
		{"no token", "", nil, 401, "UNAUTHORIZED"},
		{"wallet error", "good", fmt.Errorf("%w: read balance", betting.ErrWalletUnavailable), 502, "WALLET_ERROR"},
		{"wallet timeout", "good", fmt.Errorf("%w: read balance", betting.ErrWalletTimeout), 504, "WALLET_TIMEOUT"},
	} {
		resp, body, _ := doMethod(t, http.MethodGet, &stubBets{err: tt.err}, "/api/v1/player", tt.token, "")
		if resp.StatusCode != tt.status || body["code"] != tt.code {
			t.Errorf("%s: status %d, body %v; want %d %s", tt.name, resp.StatusCode, body, tt.status, tt.code)
		}
	}
}
