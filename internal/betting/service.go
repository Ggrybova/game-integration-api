package betting

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"uuid"

	"github.com/shopspring/decimal"
)

// Outcome tells the HTTP layer which success code to use (7.2).
type Outcome int

const (
	OutcomeCreated Outcome = iota // 201: completed by this request
	OutcomeReplay                 // 200: already COMPLETED, no Wallet call
	OutcomePending                // 202: outcome at the Wallet unknown
)

type WithdrawRequest struct {
	UserID       int64 // from the JWT subject, never from the body
	Currency     string
	Amount       decimal.Decimal
	ProviderTxID string
}

type WithdrawResult struct {
	Transaction Transaction
	BetStatus   BetStatus // "" while the withdraw is PENDING
	Outcome     Outcome
}

type Service struct {
	repo   Repository
	wallet Wallet
	log    *slog.Logger
	locks  *playerLocks
}

func NewService(repo Repository, wallet Wallet, log *slog.Logger) *Service {
	return &Service{repo: repo, wallet: wallet, log: log, locks: newPlayerLocks()}
}

// Player returns the player's account and the balance read from the Wallet
// now (8.2); a stale balance is never returned.
func (s *Service) Player(ctx context.Context, userID int64) (Account, decimal.Decimal, error) {
	account, err := s.repo.GetAccount(ctx, userID)
	if err != nil {
		return Account{}, decimal.Decimal{}, fmt.Errorf("get account: %w", err)
	}
	balance, err := s.wallet.Balance(ctx, userID)
	if errors.Is(err, context.DeadlineExceeded) {
		return Account{}, decimal.Decimal{}, fmt.Errorf("%w: read balance: %w", ErrWalletTimeout, err)
	}
	if err != nil {
		return Account{}, decimal.Decimal{}, fmt.Errorf("%w: read balance: %w", ErrWalletUnavailable, err)
	}
	return account, balance, nil
}

func (s *Service) Withdraw(ctx context.Context, req WithdrawRequest) (WithdrawResult, error) {
	// Step 1: validate the fields.
	if err := validateWithdraw(req); err != nil {
		return WithdrawResult{}, err
	}

	// Step 2: one operation per player at a time.
	unlock, err := s.locks.lock(ctx, req.UserID)
	if err != nil {
		return WithdrawResult{}, err
	}
	defer unlock() // step 8, also on every error path

	account, err := s.repo.GetAccount(ctx, req.UserID)
	if err != nil {
		return WithdrawResult{}, fmt.Errorf("get account: %w", err)
	}

	// Step 3: idempotency check.
	existing, found, err := s.repo.FindByProviderTxID(ctx, req.ProviderTxID)
	if err != nil {
		return WithdrawResult{}, fmt.Errorf("find transaction: %w", err)
	}
	if found {
		return s.withdrawExisting(ctx, req, account, existing)
	}

	// Step 4: currency of the player's wallet and oldBalance.
	if req.Currency != account.Currency {
		return WithdrawResult{}, fmt.Errorf("%w: currency does not match the player's wallet", ErrInvalidRequest)
	}
	oldBalance, err := s.oldBalance(ctx, req.UserID)
	if err != nil {
		return WithdrawResult{}, err
	}

	return s.createAndCall(ctx, req, account, oldBalance)
}

func validateWithdraw(req WithdrawRequest) error {
	switch req.Currency {
	case "USD", "EUR", "KES":
	default:
		return fmt.Errorf("%w: unsupported currency", ErrInvalidRequest)
	}
	if !req.Amount.IsPositive() {
		return fmt.Errorf("%w: amount must be greater than zero", ErrInvalidRequest)
	}
	if !req.Amount.Equal(req.Amount.Round(2)) {
		return fmt.Errorf("%w: amount has more than two decimals", ErrInvalidRequest)
	}
	if req.ProviderTxID == "" {
		return fmt.Errorf("%w: providerTransactionId is required", ErrInvalidRequest)
	}
	return nil
}

// withdrawExisting handles a request whose providerTransactionId is already stored.
func (s *Service) withdrawExisting(ctx context.Context, req WithdrawRequest, account Account, tx Transaction) (WithdrawResult, error) {
	if tx.UserID != req.UserID {
		return WithdrawResult{}, ErrNotFound
	}
	if tx.Type != TxWithdraw {
		return WithdrawResult{}, fmt.Errorf("%w: providerTransactionId is used by another operation", ErrConflict)
	}
	// A transaction has no currency of its own: it is the account's currency.
	if !tx.Amount.Equal(req.Amount) || req.Currency != account.Currency {
		return WithdrawResult{}, fmt.Errorf("%w: same providerTransactionId with a different payload", ErrConflict)
	}

	switch tx.Status {
	case TxPending:
		// Step 10 first: the earlier attempt may already be applied.
		return s.callWallet(ctx, account, tx, true)
	case TxCompleted:
		bet, err := s.repo.GetBet(ctx, tx.BetID)
		if err != nil {
			return WithdrawResult{}, fmt.Errorf("get bet: %w", err)
		}
		return WithdrawResult{Transaction: tx, BetStatus: bet.Status, Outcome: OutcomeReplay}, nil
	default: // FAILED, CANCELLED
		return WithdrawResult{}, fmt.Errorf("%w: transaction is %s", ErrConflict, tx.Status)
	}
}

// oldBalance takes the balance from our DB and reads the Wallet only when needed.
func (s *Service) oldBalance(ctx context.Context, userID int64) (decimal.Decimal, error) {
	last, err := s.repo.LastBalance(ctx, userID)
	if err != nil {
		return decimal.Decimal{}, fmt.Errorf("last balance: %w", err)
	}
	if last.Valid {
		return last.Decimal, nil
	}
	balance, err := s.wallet.Balance(ctx, userID)
	if err != nil {
		// Nothing is written or sent yet, so this is a plain 502.
		return decimal.Decimal{}, fmt.Errorf("%w: read balance: %w", ErrWalletUnavailable, err)
	}
	return balance, nil
}

// createAndCall runs steps 5-7 for a new withdraw.
func (s *Service) createAndCall(ctx context.Context, req WithdrawRequest, account Account, oldBalance decimal.Decimal) (WithdrawResult, error) {
	// Step 5: the PENDING row is committed before any Wallet call,
	// so a crash after the call cannot lose it (9.1).
	tx, err := s.repo.CreateWithdraw(ctx, Transaction{
		ID:                    uuid.New(),
		Type:                  TxWithdraw,
		Status:                TxPending,
		UserID:                req.UserID,
		Amount:                req.Amount,
		ProviderTransactionID: req.ProviderTxID,
		OldBalance:            decimal.NewNullDecimal(oldBalance),
	})
	if errors.Is(err, ErrDuplicateProviderTxID) {
		// Our lock covers one player, so the key was taken concurrently
		// by another player: same answer as in step 3.
		return WithdrawResult{}, ErrNotFound
	}
	if err != nil {
		return WithdrawResult{}, fmt.Errorf("create withdraw: %w", err)
	}
	return s.callWallet(ctx, account, tx, false)
}

// maxWithdrawCalls bounds the Wallet withdraw calls of one request (9.5).
const maxWithdrawCalls = 2

// callWallet runs steps 6, 10 and 7 for a stored PENDING withdraw.
// checkFirst is true when an earlier request already got an unknown outcome.
func (s *Service) callWallet(ctx context.Context, account Account, tx Transaction, checkFirst bool) (WithdrawResult, error) {
	walletReq := WalletRequest{
		UserID:    tx.UserID,
		Currency:  account.Currency,
		Amount:    tx.Amount,
		BetID:     tx.BetID, // the stored betId, never a new one
		Reference: tx.ID,
	}

	// DB writes after a Wallet call must not be dropped because the
	// provider closed the connection.
	dbCtx := context.WithoutCancel(ctx)
	pending := WithdrawResult{Transaction: tx, Outcome: OutcomePending}

	needCheck := checkFirst
	for calls := 0; ; {
		if needCheck {
			// Step 10: after an unknown outcome, the balance tells whether
			// the earlier call was applied.
			switch state, balance := s.checkBalance(ctx, tx); state {
			case balanceApplied:
				return s.completeWithdraw(dbCtx, tx, WalletResult{NewBalance: decimal.NewNullDecimal(balance)})
			case balanceUnclear:
				return pending, nil
			}
			// balanceNotApplied: call the Wallet (again).
		}
		if calls == maxWithdrawCalls {
			return pending, nil
		}

		// Step 6.
		res, err := s.wallet.Withdraw(ctx, walletReq)
		calls++
		s.logWalletCall(tx, calls, err)
		switch {
		case err == nil:
			return s.completeWithdraw(dbCtx, tx, res)
		case isRejected(err):
			// Not applied: keep a FAILED record (8.3, 9.3).
			if ferr := s.repo.FailTransaction(dbCtx, tx.ID); ferr != nil {
				return WithdrawResult{}, fmt.Errorf("fail transaction: %w", ferr)
			}
			return WithdrawResult{}, err
		}
		needCheck = true // unknown outcome
	}
}

type balanceState int

const (
	balanceNotApplied balanceState = iota // balance = oldBalance
	balanceApplied                        // balance = oldBalance - amount
	balanceUnclear                        // anything else, or the balance could not be read
)

// checkBalance is step 10: it reads the Wallet balance (at most two reads)
// and compares it with the balance stored before the first call. The player
// lock is held, so no other operation of ours changes it meanwhile.
func (s *Service) checkBalance(ctx context.Context, tx Transaction) (balanceState, decimal.Decimal) {
	var balance decimal.Decimal
	var err error
	for read := 1; read <= 2; read++ {
		balance, err = s.wallet.Balance(ctx, tx.UserID)
		s.log.Info("wallet balance check", "transactionId", tx.ID, "providerTransactionId", tx.ProviderTransactionID,
			"userId", tx.UserID, "read", read, "balance", balance, "error", err)
		if err == nil {
			break
		}
	}
	switch {
	case err != nil:
		return balanceUnclear, decimal.Decimal{}
	case balance.Equal(tx.OldBalance.Decimal.Sub(tx.Amount)):
		return balanceApplied, balance
	case balance.Equal(tx.OldBalance.Decimal):
		return balanceNotApplied, balance
	default:
		return balanceUnclear, balance
	}
}

// completeWithdraw is step 7 after the Wallet applied the withdraw.
func (s *Service) completeWithdraw(dbCtx context.Context, tx Transaction, res WalletResult) (WithdrawResult, error) {
	newBalance := res.NewBalance
	if !newBalance.Valid {
		// "Already applied" (409) has no balance. The Wallet confirmed this
		// operation with this amount, so the balance right after it is
		// oldBalance - amount (decisions.md).
		newBalance = decimal.NewNullDecimal(tx.OldBalance.Decimal.Sub(tx.Amount))
	}

	if tx.OldBalance.Valid && !newBalance.Decimal.Equal(tx.OldBalance.Decimal.Sub(tx.Amount)) {
		s.log.Warn("balance changed outside our service",
			"transactionId", tx.ID, "oldBalance", tx.OldBalance.Decimal, "amount", tx.Amount, "newBalance", newBalance.Decimal)
	}

	if err := s.repo.CompleteWithdraw(dbCtx, tx.ID, res.WalletTransactionID, newBalance.Decimal); err != nil {
		// The Wallet applied it but our row is still PENDING: report what we
		// recorded; the retry gets 409 from the Wallet and completes it.
		s.log.Error("withdraw applied but not recorded, stays PENDING",
			"transactionId", tx.ID, "providerTransactionId", tx.ProviderTransactionID, "error", err)
		return WithdrawResult{Transaction: tx, Outcome: OutcomePending}, nil
	}

	tx.Status = TxCompleted
	tx.NewBalance = newBalance
	tx.WalletTransactionID = res.WalletTransactionID
	return WithdrawResult{Transaction: tx, BetStatus: BetPlaced, Outcome: OutcomeCreated}, nil
}

// isRejected reports a Wallet error that means "definitely not applied".
func isRejected(err error) bool {
	return errors.Is(err, ErrInsufficientFunds) || errors.Is(err, ErrWalletRejected)
}

// logWalletCall logs every Wallet call with its identifiers and outcome (9.7).
func (s *Service) logWalletCall(tx Transaction, attempt int, err error) {
	attrs := []any{
		"transactionId", tx.ID,
		"providerTransactionId", tx.ProviderTransactionID,
		"betId", tx.BetID,
		"userId", tx.UserID,
		"attempt", attempt,
	}
	switch {
	case err == nil:
		s.log.Info("wallet withdraw applied", attrs...)
	case isRejected(err):
		s.log.Warn("wallet withdraw rejected", append(attrs, "error", err)...)
	default:
		s.log.Warn("wallet withdraw outcome unknown", append(attrs, "error", err)...)
	}
}
