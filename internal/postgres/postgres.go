// Package postgres implements betting.Repository on PostgreSQL.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/Ggrybova/game-integration-api/internal/betting"
)

// Store is the PostgreSQL storage. It is safe for concurrent use.
type Store struct {
	pool *pgxpool.Pool
}

// The compiler checks that Store implements betting.Repository.
var _ betting.Repository = (*Store)(nil)

// New connects to the database and checks the connection,
// so a wrong DATABASE_URL stops the service at start.
func New(ctx context.Context, databaseURL string) (*Store, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Close() {
	s.pool.Close()
}

func (s *Store) GetAccount(ctx context.Context, userID int64) (betting.Account, error) {
	a := betting.Account{UserID: userID}
	err := s.pool.QueryRow(ctx,
		`SELECT currency FROM accounts WHERE user_id = $1`, userID,
	).Scan(&a.Currency)
	if err != nil {
		return betting.Account{}, fmt.Errorf("get account %d: %w", userID, err)
	}
	return a, nil
}

func (s *Store) FindByProviderTxID(ctx context.Context, providerTxID string) (betting.Transaction, bool, error) {
	var (
		tx     betting.Transaction
		id     string
		storno *string // NULL unless CANCEL
	)
	err := s.pool.QueryRow(ctx,
		`SELECT id, type, status, user_id, bet_id, amount, provider_transaction_id,
		        storno_transaction_id, wallet_transaction_id, old_balance, new_balance
		 FROM transactions
		 WHERE provider_transaction_id = $1`, providerTxID,
	).Scan(&id, &tx.Type, &tx.Status, &tx.UserID, &tx.BetID, &tx.Amount, &tx.ProviderTransactionID,
		&storno, &tx.WalletTransactionID, &tx.OldBalance, &tx.NewBalance)
	if errors.Is(err, pgx.ErrNoRows) {
		return betting.Transaction{}, false, nil
	}
	if err != nil {
		return betting.Transaction{}, false, fmt.Errorf("find transaction: %w", err)
	}

	if tx.ID, err = uuid.Parse(id); err != nil {
		return betting.Transaction{}, false, fmt.Errorf("parse transaction id: %w", err)
	}
	if storno != nil {
		tx.StornoTransactionID = *storno
	}
	return tx, true, nil
}

func (s *Store) GetBet(ctx context.Context, betID int64) (betting.Bet, error) {
	b := betting.Bet{ID: betID}
	var status *string // NULL until the withdraw is COMPLETED
	err := s.pool.QueryRow(ctx,
		`SELECT status FROM bets WHERE id = $1`, betID,
	).Scan(&status)
	if err != nil {
		return betting.Bet{}, fmt.Errorf("get bet %d: %w", betID, err)
	}
	if status != nil {
		b.Status = betting.BetStatus(*status)
	}
	return b, nil
}

// LastBalance orders by created_dt, not updated_dt: a withdraw resolved late
// must not hide the balance of a newer transaction that already includes it.
func (s *Store) LastBalance(ctx context.Context, userID int64) (decimal.NullDecimal, error) {
	var balance decimal.NullDecimal // NULL while the latest one is PENDING
	err := s.pool.QueryRow(ctx,
		`SELECT new_balance FROM transactions
		 WHERE user_id = $1 AND status <> 'FAILED'
		 ORDER BY created_dt DESC
		 LIMIT 1`, userID,
	).Scan(&balance)
	if errors.Is(err, pgx.ErrNoRows) {
		return decimal.NullDecimal{}, nil
	}
	if err != nil {
		return decimal.NullDecimal{}, fmt.Errorf("last balance: %w", err)
	}
	return balance, nil
}

// CreateWithdraw inserts the bet and the PENDING transaction together:
// either both are stored or neither.
// IDs are passed as strings: uuid.UUID from the standard library
// has no database encoding of its own.
func (s *Store) CreateWithdraw(ctx context.Context, tx betting.Transaction) (betting.Transaction, error) {
	err := pgx.BeginFunc(ctx, s.pool, func(dbTx pgx.Tx) error {
		err := dbTx.QueryRow(ctx,
			`INSERT INTO bets DEFAULT VALUES RETURNING id`,
		).Scan(&tx.BetID)
		if err != nil {
			return fmt.Errorf("insert bet: %w", err)
		}

		_, err = dbTx.Exec(ctx,
			`INSERT INTO transactions
			   (id, type, status, user_id, bet_id, amount, provider_transaction_id, old_balance)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			tx.ID.String(), tx.Type, tx.Status, tx.UserID, tx.BetID, tx.Amount,
			tx.ProviderTransactionID, tx.OldBalance)
		if err != nil {
			return fmt.Errorf("insert transaction: %w", err)
		}
		return nil
	})

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" &&
		pgErr.ConstraintName == "transactions_provider_transaction_id_key" {
		return betting.Transaction{}, betting.ErrDuplicateProviderTxID
	}
	if err != nil {
		return betting.Transaction{}, fmt.Errorf("create withdraw: %w", err)
	}
	return tx, nil
}

// CompleteWithdraw changes only a PENDING row, so a finished one is never overwritten.
func (s *Store) CompleteWithdraw(ctx context.Context, txID uuid.UUID, walletTxID *int64, newBalance decimal.Decimal) error {
	return pgx.BeginFunc(ctx, s.pool, func(dbTx pgx.Tx) error {
		var betID int64
		err := dbTx.QueryRow(ctx,
			`UPDATE transactions
			 SET status = 'COMPLETED', wallet_transaction_id = $2, new_balance = $3, updated_dt = now()
			 WHERE id = $1 AND status = 'PENDING'
			 RETURNING bet_id`,
			txID.String(), walletTxID, newBalance,
		).Scan(&betID)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("complete withdraw %s: transaction is not PENDING", txID)
		}
		if err != nil {
			return fmt.Errorf("complete withdraw %s: %w", txID, err)
		}

		_, err = dbTx.Exec(ctx,
			`UPDATE bets SET status = 'PLACED', updated_dt = now() WHERE id = $1`, betID)
		if err != nil {
			return fmt.Errorf("place bet %d: %w", betID, err)
		}
		return nil
	})
}

// FailTransaction changes only a PENDING row, like CompleteWithdraw.
func (s *Store) FailTransaction(ctx context.Context, txID uuid.UUID) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE transactions SET status = 'FAILED', updated_dt = now()
		 WHERE id = $1 AND status = 'PENDING'`, txID.String())
	if err != nil {
		return fmt.Errorf("fail transaction %s: %w", txID, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("fail transaction %s: transaction is not PENDING", txID)
	}
	return nil
}
