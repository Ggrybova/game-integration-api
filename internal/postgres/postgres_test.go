package postgres

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
	"golang.org/x/crypto/bcrypt"

	"github.com/Ggrybova/game-integration-api/internal/betting"
)

const (
	testDBName = "game_test"
	player1    = 34633089486 // from migrations/002_seed.sql
)

// newStore gives each test a fresh game_test database built from
// migrations/*.sql, so tests never touch the game database and never
// depend on each other. Set TEST_DATABASE_URL to any database on the
// server (for example the compose one); without it the test is skipped.
func newStore(t *testing.T) *Store {
	t.Helper()
	adminURL := os.Getenv("TEST_DATABASE_URL")
	if adminURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()

	admin, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer admin.Close(ctx)
	for _, sql := range []string{
		"DROP DATABASE IF EXISTS " + testDBName + " WITH (FORCE)",
		"CREATE DATABASE " + testDBName,
	} {
		if _, err := admin.Exec(ctx, sql); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}

	u, err := url.Parse(adminURL)
	if err != nil {
		t.Fatalf("parse TEST_DATABASE_URL: %v", err)
	}
	u.Path = "/" + testDBName
	testURL := u.String()

	conn, err := pgx.Connect(ctx, testURL)
	if err != nil {
		t.Fatalf("connect to %s: %v", testDBName, err)
	}
	defer conn.Close(ctx)
	files, err := filepath.Glob("../../migrations/*.sql") // sorted: 001, 002, ...
	if err != nil || len(files) == 0 {
		t.Fatalf("no migration files found: %v", err)
	}
	for _, f := range files {
		sql, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		if _, err := conn.Exec(ctx, string(sql)); err != nil {
			t.Fatalf("run %s: %v", f, err)
		}
	}

	s, err := New(ctx, testURL)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(s.Close)
	return s
}

func newWithdraw(providerTxID, amount, oldBalance string) betting.Transaction {
	return betting.Transaction{
		ID:                    uuid.New(),
		Type:                  betting.TxWithdraw,
		Status:                betting.TxPending,
		UserID:                player1,
		Amount:                decimal.RequireFromString(amount),
		ProviderTransactionID: providerTxID,
		OldBalance:            decimal.NewNullDecimal(decimal.RequireFromString(oldBalance)),
	}
}

func mustCreate(t *testing.T, s *Store, tx betting.Transaction) betting.Transaction {
	t.Helper()
	created, err := s.CreateWithdraw(context.Background(), tx)
	if err != nil {
		t.Fatalf("CreateWithdraw: %v", err)
	}
	return created
}

func mustFind(t *testing.T, s *Store, providerTxID string) betting.Transaction {
	t.Helper()
	tx, found, err := s.FindByProviderTxID(context.Background(), providerTxID)
	if err != nil || !found {
		t.Fatalf("FindByProviderTxID(%s): found=%v err=%v", providerTxID, found, err)
	}
	return tx
}

func assertDecimal(t *testing.T, name string, got decimal.NullDecimal, want string) {
	t.Helper()
	if !got.Valid || !got.Decimal.Equal(decimal.RequireFromString(want)) {
		t.Errorf("%s = %v, want %s", name, got, want)
	}
}

func TestGetAccount(t *testing.T) {
	s := newStore(t)
	a, err := s.GetAccount(context.Background(), player1)
	if err != nil || a.Currency != "USD" {
		t.Errorf("GetAccount = %+v, %v; want USD", a, err)
	}
}

// The seeded hash matches the password documented in README.md.
func TestFindByUsername(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	id, hash, found, err := s.FindByUsername(ctx, "player1")
	if err != nil || !found || id != player1 {
		t.Fatalf("FindByUsername = %d, found=%v, %v; want %d", id, found, err, int64(player1))
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte("password1")) != nil {
		t.Error("seeded hash does not match password1")
	}
	if _, _, found, err := s.FindByUsername(ctx, "nobody"); found || err != nil {
		t.Errorf("unknown user: found=%v err=%v; want false, nil", found, err)
	}
}

func TestFindMissingIsNotAnError(t *testing.T) {
	s := newStore(t)
	_, found, err := s.FindByProviderTxID(context.Background(), "nope")
	if err != nil || found {
		t.Errorf("found=%v err=%v; want false, nil", found, err)
	}
}

// A stored PENDING withdraw reads back unchanged, including a large
// fractional balance and NULL newBalance; its bet has no status yet.
func TestCreateWithdrawRoundTrip(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	tx := newWithdraw("p-1", "10.50", "9000000000.12")
	created := mustCreate(t, s, tx)

	got := mustFind(t, s, "p-1")
	if got.ID != tx.ID || got.Type != betting.TxWithdraw || got.Status != betting.TxPending ||
		got.UserID != player1 || got.BetID != created.BetID || !got.Amount.Equal(tx.Amount) {
		t.Errorf("read back %+v, want %+v", got, created)
	}
	assertDecimal(t, "oldBalance", got.OldBalance, "9000000000.12")
	if got.NewBalance.Valid || got.WalletTransactionID != nil {
		t.Errorf("newBalance=%v walletTxID=%v; want NULL while PENDING", got.NewBalance, got.WalletTransactionID)
	}

	bet, err := s.GetBet(ctx, created.BetID)
	if err != nil || bet.Status != "" {
		t.Errorf("bet = %+v, %v; want no status", bet, err)
	}
	lb, err := s.LastBalance(ctx, player1)
	if err != nil || lb.Valid {
		t.Errorf("LastBalance = %v, %v; want not valid while PENDING", lb, err)
	}
}

// A taken providerTransactionId is reported as such, and the bet inserted
// in the same DB transaction is rolled back with it.
func TestCreateWithdrawDuplicateKeyStoresNothing(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	mustCreate(t, s, newWithdraw("p-1", "10.50", "5000"))

	_, err := s.CreateWithdraw(ctx, newWithdraw("p-1", "10.50", "5000"))
	if !errors.Is(err, betting.ErrDuplicateProviderTxID) {
		t.Fatalf("err = %v, want ErrDuplicateProviderTxID", err)
	}
	var bets int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM bets`).Scan(&bets); err != nil {
		t.Fatal(err)
	}
	if bets != 1 {
		t.Errorf("bets = %d, want 1 (the second insert must be rolled back)", bets)
	}
}

func TestCompleteWithdraw(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	tx := mustCreate(t, s, newWithdraw("p-1", "10.50", "5000"))

	walletTxID := int64(42)
	if err := s.CompleteWithdraw(ctx, tx.ID, &walletTxID, decimal.RequireFromString("4989.50")); err != nil {
		t.Fatalf("CompleteWithdraw: %v", err)
	}

	got := mustFind(t, s, "p-1")
	if got.Status != betting.TxCompleted || got.WalletTransactionID == nil || *got.WalletTransactionID != 42 {
		t.Errorf("status=%s walletTxID=%v; want COMPLETED, 42", got.Status, got.WalletTransactionID)
	}
	assertDecimal(t, "newBalance", got.NewBalance, "4989.50")
	if bet, _ := s.GetBet(ctx, tx.BetID); bet.Status != betting.BetPlaced {
		t.Errorf("bet status = %q, want PLACED", bet.Status)
	}
	lb, _ := s.LastBalance(ctx, player1)
	assertDecimal(t, "LastBalance", lb, "4989.50")
}

// Only a PENDING row can be completed or failed: a finished one is never overwritten.
func TestFinishedTransactionIsNotOverwritten(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	tx := mustCreate(t, s, newWithdraw("p-1", "10.50", "5000"))
	if err := s.CompleteWithdraw(ctx, tx.ID, nil, decimal.RequireFromString("4989.50")); err != nil {
		t.Fatalf("CompleteWithdraw: %v", err)
	}

	if err := s.CompleteWithdraw(ctx, tx.ID, nil, decimal.Zero); err == nil {
		t.Error("second CompleteWithdraw succeeded, want an error")
	}
	if err := s.FailTransaction(ctx, tx.ID); err == nil {
		t.Error("FailTransaction on COMPLETED succeeded, want an error")
	}
	got := mustFind(t, s, "p-1")
	if got.Status != betting.TxCompleted {
		t.Errorf("status = %s, want COMPLETED", got.Status)
	}
	assertDecimal(t, "newBalance", got.NewBalance, "4989.50")
}

func TestFailTransactionIsSkippedByLastBalance(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	first := mustCreate(t, s, newWithdraw("p-1", "10.50", "5000"))
	if err := s.CompleteWithdraw(ctx, first.ID, nil, decimal.RequireFromString("4989.50")); err != nil {
		t.Fatalf("CompleteWithdraw: %v", err)
	}
	second := mustCreate(t, s, newWithdraw("p-2", "99999", "4989.50"))
	if err := s.FailTransaction(ctx, second.ID); err != nil {
		t.Fatalf("FailTransaction: %v", err)
	}

	if got := mustFind(t, s, "p-2"); got.Status != betting.TxFailed {
		t.Errorf("status = %s, want FAILED", got.Status)
	}
	lb, _ := s.LastBalance(ctx, player1)
	assertDecimal(t, "LastBalance", lb, "4989.50")
}

// A withdraw resolved late must not hide the balance of a newer one (decisions.md).
func TestLastBalanceUsesNewestCreatedNotNewestUpdated(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	older := mustCreate(t, s, newWithdraw("p-1", "10", "100"))
	newer := mustCreate(t, s, newWithdraw("p-2", "20", "90"))

	if err := s.CompleteWithdraw(ctx, newer.ID, nil, decimal.RequireFromString("70")); err != nil {
		t.Fatalf("complete newer: %v", err)
	}
	if err := s.CompleteWithdraw(ctx, older.ID, nil, decimal.RequireFromString("90")); err != nil {
		t.Fatalf("complete older: %v", err)
	}

	lb, _ := s.LastBalance(ctx, player1)
	assertDecimal(t, "LastBalance", lb, "70")
}
