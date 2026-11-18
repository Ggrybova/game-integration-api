package betting

import (
	"context"
	"uuid"

	"github.com/shopspring/decimal"
)

// Wallet is the external Wallet service.
// A nil error means the operation was applied. ErrInsufficientFunds and
// ErrWalletRejected mean it was definitely not applied. Any other error
// means the outcome is unknown and the transaction stays PENDING.
type Wallet interface {
	Balance(ctx context.Context, userID int64) (decimal.Decimal, error)
	Withdraw(ctx context.Context, req WalletRequest) (WalletResult, error)
	Deposit(ctx context.Context, req WalletRequest) (WalletResult, error)
}

type WalletRequest struct {
	UserID    int64
	Currency  string
	Amount    decimal.Decimal
	BetID     int64
	Reference uuid.UUID // our transactionId
}

type WalletResult struct {
	WalletTransactionID *int64              // nil when the Wallet answered "already applied"
	NewBalance          decimal.NullDecimal // Valid=false when the Wallet answered "already applied"
}

// Repository stores accounts, bets and transactions.
type Repository interface {
	// GetAccount returns the player's account (step 4).
	GetAccount(ctx context.Context, userID int64) (Account, error)

	// FindByProviderTxID looks up a WITHDRAW or DEPOSIT by its
	// providerTransactionId (step 3). found is false when there is no row.
	FindByProviderTxID(ctx context.Context, providerTxID string) (tx Transaction, found bool, err error)

	// GetBet returns a bet; needed for betStatus in a replay (step 3).
	GetBet(ctx context.Context, betID int64) (Bet, error)

	// LastBalance returns new_balance of the player's latest non-FAILED
	// transaction. Valid=false when there is none or it is PENDING:
	// then the caller reads the balance from the Wallet (step 4).
	LastBalance(ctx context.Context, userID int64) (decimal.NullDecimal, error)

	// CreateWithdraw inserts the bet (status NULL) and the PENDING
	// transaction in one DB transaction and returns tx with BetID set
	// (step 5). Returns ErrDuplicateProviderTxID if the key is taken.
	CreateWithdraw(ctx context.Context, tx Transaction) (Transaction, error)

	// CompleteWithdraw sets the transaction COMPLETED with the Wallet's
	// result and the bet PLACED, in one DB transaction (step 7).
	CompleteWithdraw(ctx context.Context, txID uuid.UUID, walletTxID *int64, newBalance decimal.Decimal) error

	// FailTransaction sets the transaction FAILED (step 6, 400/401).
	FailTransaction(ctx context.Context, txID uuid.UUID) error
}
