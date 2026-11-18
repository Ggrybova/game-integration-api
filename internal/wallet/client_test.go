package wallet

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/shopspring/decimal"

	"github.com/Ggrybova/game-integration-api/internal/betting"
)

const testKey = "test-api-key"

// newClient starts a fake Wallet that answers with handler.
func newClient(t *testing.T, timeout time.Duration, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return New(srv.URL, testKey, timeout)
}

// reply answers every request with the given status and body.
func reply(status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
}

func walletReq() betting.WalletRequest {
	return betting.WalletRequest{
		UserID:    34633089486,
		Currency:  "USD",
		Amount:    decimal.RequireFromString("10.50"),
		BetID:     7001,
		Reference: uuid.MustParse("b3f1c2d4-5e6f-4a7b-8c9d-0e1f2a3b4c5d"),
	}
}

func isUnknown(err error) bool {
	return err != nil && !errors.Is(err, betting.ErrInsufficientFunds) && !errors.Is(err, betting.ErrWalletRejected)
}

// The request has the key, the JSON content type and the body the Wallet expects,
// with the amount as a JSON number.
func TestWithdrawRequest(t *testing.T) {
	var got struct {
		method, path, key, contentType string
		body                           map[string]any
	}
	c := newClient(t, time.Second, func(w http.ResponseWriter, r *http.Request) {
		got.method, got.path = r.Method, r.URL.Path
		got.key, got.contentType = r.Header.Get("x-api-key"), r.Header.Get("Content-Type")
		dec := json.NewDecoder(r.Body)
		dec.UseNumber() // keep numbers as text to see exactly what was sent
		_ = dec.Decode(&got.body)
		_, _ = w.Write([]byte(`{"balance":"4989.5","transactions":[{"id":7,"reference":"x"}]}`))
	})

	res, err := c.Withdraw(context.Background(), walletReq())
	if err != nil {
		t.Fatalf("Withdraw: %v", err)
	}
	if got.method != http.MethodPost || got.path != "/api/v1/withdraw" {
		t.Errorf("request = %s %s, want POST /api/v1/withdraw", got.method, got.path)
	}
	if got.key != testKey || got.contentType != "application/json" {
		t.Errorf("headers: key=%q content-type=%q", got.key, got.contentType)
	}
	if got.body["userId"] != json.Number("34633089486") || got.body["currency"] != "USD" {
		t.Errorf("body = %v", got.body)
	}
	items, _ := got.body["transactions"].([]any)
	if len(items) != 1 {
		t.Fatalf("transactions = %v, want one item", got.body["transactions"])
	}
	item, _ := items[0].(map[string]any)
	if item["amount"] != json.Number("10.5") || item["betId"] != json.Number("7001") ||
		item["reference"] != "b3f1c2d4-5e6f-4a7b-8c9d-0e1f2a3b4c5d" {
		t.Errorf("transaction item = %v", item)
	}

	if !res.NewBalance.Valid || !res.NewBalance.Decimal.Equal(decimal.RequireFromString("4989.5")) {
		t.Errorf("newBalance = %v, want 4989.5", res.NewBalance)
	}
	if res.WalletTransactionID == nil || *res.WalletTransactionID != 7 {
		t.Errorf("wallet transaction id = %v, want 7", res.WalletTransactionID)
	}
}

func TestDepositUsesDepositPath(t *testing.T) {
	var path string
	c := newClient(t, time.Second, func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_, _ = w.Write([]byte(`{"balance":"5010.5","transactions":[{"id":8}]}`))
	})
	if _, err := c.Deposit(context.Background(), walletReq()); err != nil {
		t.Fatalf("Deposit: %v", err)
	}
	if path != "/api/v1/deposit" {
		t.Errorf("path = %s, want /api/v1/deposit", path)
	}
}

// Every Wallet answer from docs/wallet_api.md maps to the right outcome.
func TestMoveAnswers(t *testing.T) {
	const (
		applied        = "applied"
		appliedEarlier = "applied earlier"
		insufficient   = "insufficient funds"
		rejected       = "rejected"
		unknown        = "unknown"
	)
	tests := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"200", 200, `{"balance":"4989.5","transactions":[{"id":7}]}`, applied},
		{"201 as in Swagger", 201, `{"balance":4989.5,"transactions":[{"id":7}]}`, applied},
		{"409 duplicate", 409, `{"msg":"Transaction already exists.","code":"DuplicatedTransaction"}`, appliedEarlier},
		{"400 insufficient", 400, `{"msg":"Insufficient balance for the user.","code":"InsufficientBalance"}`, insufficient},
		{"400 currency", 400, `{"msg":"Currency mismatch for the user.","code":"InvalidCurrency"}`, rejected},
		{"400 unknown user", 400, `{"msg":"User does not exists.","code":"UserNotFound"}`, rejected},
		{"400 invalid", 400, `{"msg":"Request is invalid.","code":"InvalidRequest"}`, rejected},
		{"401 key", 401, `{"msg":"Authentication error, see logs for more details.","code":"AuthenticatonError"}`, rejected},
		{"500", 500, `{"msg":"Internal server response.","code":"666"}`, unknown},
		{"404 plain text", 404, `404 page not found`, unknown},
		{"409 other code", 409, `{"code":"Something"}`, unknown},
		{"200 unreadable", 200, `not json`, unknown},
		{"200 without transactions", 200, `{"balance":"1"}`, unknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newClient(t, time.Second, reply(tt.status, tt.body))
			res, err := c.Withdraw(context.Background(), walletReq())

			var got string
			switch {
			case err == nil && res.NewBalance.Valid:
				got = applied
			case err == nil:
				got = appliedEarlier
			case errors.Is(err, betting.ErrInsufficientFunds):
				got = insufficient
			case errors.Is(err, betting.ErrWalletRejected):
				got = rejected
			default:
				got = unknown
			}
			if got != tt.want {
				t.Errorf("got %s (err %v), want %s", got, err, tt.want)
			}
		})
	}
}

func TestTimeoutIsUnknown(t *testing.T) {
	c := newClient(t, 50*time.Millisecond, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(300 * time.Millisecond): // longer than the client timeout
		}
	})
	_, err := c.Withdraw(context.Background(), walletReq())
	if !isUnknown(err) || !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want an unknown outcome caused by the timeout", err)
	}
}

func TestDroppedConnectionIsUnknown(t *testing.T) {
	c := newClient(t, time.Second, func(w http.ResponseWriter, r *http.Request) {
		conn, _, err := http.NewResponseController(w).Hijack()
		if err != nil {
			t.Errorf("hijack: %v", err)
			return
		}
		_ = conn.Close() // close without an answer, like the mock does
	})
	_, err := c.Withdraw(context.Background(), walletReq())
	if !isUnknown(err) {
		t.Errorf("err = %v, want an unknown outcome", err)
	}
}

func TestBalance(t *testing.T) {
	for _, body := range []string{`{"balance":"4996.995","currency":"USD"}`, `{"balance":4996.995,"currency":"USD"}`} {
		var path, key string
		c := newClient(t, time.Second, func(w http.ResponseWriter, r *http.Request) {
			path, key = r.URL.Path, r.Header.Get("x-api-key")
			_, _ = w.Write([]byte(body))
		})
		got, err := c.Balance(context.Background(), 34633089486)
		if err != nil || !got.Equal(decimal.RequireFromString("4996.995")) {
			t.Errorf("body %s: Balance = %s, %v; want 4996.995", body, got, err)
		}
		if path != "/api/v1/balance/34633089486" || key != testKey {
			t.Errorf("request path=%s key=%q", path, key)
		}
	}
}

func TestBalanceErrors(t *testing.T) {
	for _, tt := range []struct {
		name   string
		status int
		body   string
	}{
		{"500", 500, `{"msg":"Internal server response.","code":"666"}`},
		{"401", 401, `{"code":"AuthenticatonError"}`},
		{"unreadable", 200, `not json`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := newClient(t, time.Second, reply(tt.status, tt.body))
			if _, err := c.Balance(context.Background(), 1); err == nil {
				t.Error("Balance succeeded, want an error")
			}
		})
	}
}

// Errors go to the logs, so they must never contain the API key (9.7).
func TestErrorsDoNotContainAPIKey(t *testing.T) {
	c := newClient(t, time.Second, reply(401, `{"code":"AuthenticatonError","msg":"Authentication error"}`))
	_, err := c.Withdraw(context.Background(), walletReq())
	if err == nil || strings.Contains(err.Error(), testKey) {
		t.Errorf("err = %v; must be an error without the key", err)
	}
	if !bytes.Contains([]byte(err.Error()), []byte("AuthenticatonError")) {
		t.Errorf("err = %v; want the Wallet code for the logs", err)
	}
}
