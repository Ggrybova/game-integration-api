// Package wallet is the HTTP client of the external Wallet service.
// It implements betting.Wallet and turns Wallet answers into the three
// outcomes the business rules need: applied, definitely not applied, unknown.
// See docs/wallet_api.md for the observed behaviour this is built on.
package wallet

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/Ggrybova/game-integration-api/internal/betting"
)

// maxBody bounds how much of a Wallet response is read.
const maxBody = 1 << 20

type Client struct {
	baseURL string
	apiKey  string
	timeout time.Duration
	http    *http.Client
}

// The compiler checks that Client implements betting.Wallet.
var _ betting.Wallet = (*Client)(nil)

// New returns a client; timeout bounds every call, including reading the answer (9.5).
func New(baseURL, apiKey string, timeout time.Duration) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		timeout: timeout,
		http:    &http.Client{},
	}
}

func (c *Client) Balance(ctx context.Context, userID int64) (decimal.Decimal, error) {
	status, body, err := c.do(ctx, http.MethodGet, "/api/v1/balance/"+strconv.FormatInt(userID, 10), nil)
	if err != nil {
		return decimal.Decimal{}, fmt.Errorf("wallet balance: %w", err)
	}
	if status != http.StatusOK {
		return decimal.Decimal{}, fmt.Errorf("wallet balance: %s", describe(status, body))
	}
	var resp struct {
		Balance decimal.Decimal `json:"balance"` // a string in practice; a number is accepted too
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return decimal.Decimal{}, fmt.Errorf("wallet balance: unreadable answer: %w", err)
	}
	return resp.Balance, nil
}

func (c *Client) Withdraw(ctx context.Context, req betting.WalletRequest) (betting.WalletResult, error) {
	return c.move(ctx, "withdraw", req)
}

func (c *Client) Deposit(ctx context.Context, req betting.WalletRequest) (betting.WalletResult, error) {
	return c.move(ctx, "deposit", req)
}

type moveRequest struct {
	UserID       int64      `json:"userId"`
	Currency     string     `json:"currency"`
	Transactions []moveItem `json:"transactions"`
}

type moveItem struct {
	Amount    json.Number `json:"amount"` // written as a JSON number, from the exact decimal
	BetID     int64       `json:"betId"`
	Reference string      `json:"reference"`
}

// move sends one withdraw or deposit and classifies the answer.
// Only answers that prove the operation was not applied are errors that
// lead to FAILED; anything we do not recognise is an unknown outcome.
func (c *Client) move(ctx context.Context, op string, req betting.WalletRequest) (betting.WalletResult, error) {
	payload := moveRequest{
		UserID:   req.UserID,
		Currency: req.Currency,
		Transactions: []moveItem{{
			Amount:    json.Number(req.Amount.String()),
			BetID:     req.BetID,
			Reference: req.Reference.String(),
		}},
	}
	status, body, err := c.do(ctx, http.MethodPost, "/api/v1/"+op, payload)
	if err != nil {
		// Timeout or dropped connection: the Wallet may have applied it.
		return betting.WalletResult{}, fmt.Errorf("wallet %s: outcome unknown: %w", op, err)
	}

	code := errorCode(body)
	switch {
	case status == http.StatusOK || status == http.StatusCreated: // the mock sends 200, Swagger says 201
		var resp struct {
			Balance      decimal.Decimal `json:"balance"`
			Transactions []struct {
				ID int64 `json:"id"`
			} `json:"transactions"`
		}
		if err := json.Unmarshal(body, &resp); err != nil || len(resp.Transactions) == 0 {
			// Applied, but we cannot read the result: a retry gets 409 and completes it.
			return betting.WalletResult{}, fmt.Errorf("wallet %s: outcome unknown: unreadable answer", op)
		}
		id := resp.Transactions[0].ID
		return betting.WalletResult{
			WalletTransactionID: &id,
			NewBalance:          decimal.NewNullDecimal(resp.Balance),
		}, nil

	case status == http.StatusConflict && code == "DuplicatedTransaction":
		// Applied earlier with this reference and betId; the answer has no balance.
		return betting.WalletResult{}, nil

	case status == http.StatusBadRequest && code == "InsufficientBalance":
		return betting.WalletResult{}, fmt.Errorf("wallet %s: %w (%s)", op, betting.ErrInsufficientFunds, describe(status, body))

	case status == http.StatusBadRequest || status == http.StatusUnauthorized:
		// Validation and authentication happen before anything is applied.
		return betting.WalletResult{}, fmt.Errorf("wallet %s: %w (%s)", op, betting.ErrWalletRejected, describe(status, body))

	default:
		// 500, an unexpected status or body: we cannot tell, so it stays PENDING.
		return betting.WalletResult{}, fmt.Errorf("wallet %s: outcome unknown: %s", op, describe(status, body))
	}
}

// do sends one request and reads the answer. A non-nil error means no
// complete answer arrived (timeout, dropped connection).
func (c *Client) do(ctx context.Context, method, path string, payload any) (status int, body []byte, err error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	var reqBody io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return 0, nil, fmt.Errorf("encode request: %w", err)
		}
		reqBody = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reqBody)
	if err != nil {
		return 0, nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("x-api-key", c.apiKey)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err = io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return 0, nil, fmt.Errorf("read answer: %w", err)
	}
	return resp.StatusCode, body, nil
}

// errorCode returns the "code" of a Wallet error body, or "" if the body
// is not JSON (for example the plain-text 404).
func errorCode(body []byte) string {
	var e struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(body, &e)
	return e.Code
}

// describe is a short text for logs: the status and the Wallet's code and msg.
func describe(status int, body []byte) string {
	var e struct {
		Code string `json:"code"`
		Msg  string `json:"msg"`
	}
	if json.Unmarshal(body, &e) != nil || e.Code == "" {
		return "status " + strconv.Itoa(status)
	}
	return fmt.Sprintf("status %d, code %s: %s", status, e.Code, e.Msg)
}
