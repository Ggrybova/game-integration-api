// Package betting holds the business rules for bets and transactions.
// It does not depend on HTTP, the database or the Wallet client.
package betting

import (
	"uuid"

	"github.com/shopspring/decimal"
)

type TxType string

const (
	TxWithdraw TxType = "WITHDRAW"
	TxDeposit  TxType = "DEPOSIT"
	TxCancel   TxType = "CANCEL"
)

type TxStatus string

const (
	TxPending   TxStatus = "PENDING"
	TxCompleted TxStatus = "COMPLETED"
	TxFailed    TxStatus = "FAILED"
	TxCancelled TxStatus = "CANCELLED"
)

type BetStatus string

const (
	BetPlaced    BetStatus = "PLACED"
	BetWon       BetStatus = "WON"
	BetLost      BetStatus = "LOST"
	BetCancelled BetStatus = "CANCELLED"
)

// Account is a player linked to exactly one wallet user.
type Account struct {
	UserID   int64
	Currency string
}

type Transaction struct {
	ID                    uuid.UUID // our transactionId, sent to the Wallet as reference
	Type                  TxType
	Status                TxStatus
	UserID                int64
	BetID                 int64
	Amount                decimal.Decimal
	ProviderTransactionID string              // "" for CANCEL
	StornoTransactionID   string              // "" unless CANCEL
	WalletTransactionID   *int64              // nil until the Wallet confirms
	OldBalance            decimal.NullDecimal // Valid=false if not read
	NewBalance            decimal.NullDecimal // Valid=false while PENDING
}

type Bet struct {
	ID     int64     // betId sent to the Wallet
	Status BetStatus // "" until the withdraw is COMPLETED
}
