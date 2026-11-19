package betting

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"testing/synctest"
	"uuid"

	"github.com/shopspring/decimal"
)

const (
	testUser  int64 = 34633089486
	otherUser int64 = 34673635133 // also USD, so only the owner differs
)

// fakeWallet returns scripted Withdraw replies in order and records every call.
// It is safe for concurrent use.
type fakeWallet struct {
	mu           sync.Mutex
	balance      decimal.Decimal
	balanceErr   error         // if set, Balance fails with it
	balances     []walletReply // if set, Balance answers with these in order first
	balanceCalls int
	withdraws    []walletReply
	calls        []WalletRequest
	block        chan struct{} // if set, Withdraw waits until it is closed
}

type walletReply struct {
	res WalletResult
	err error
}

// errWalletDown stands for a 500 or a dropped connection: outcome unknown.
var errWalletDown = errors.New("wallet: 500")

func applied(balance string, walletTxID int64) walletReply {
	return walletReply{res: WalletResult{
		WalletTransactionID: &walletTxID,
		NewBalance:          decimal.NewNullDecimal(decimal.RequireFromString(balance)),
	}}
}

// duplicate is the Wallet's 409: applied earlier, no balance in the response.
func duplicate() walletReply { return walletReply{} }

func failed(err error) walletReply { return walletReply{err: err} }

func (w *fakeWallet) Balance(ctx context.Context, userID int64) (decimal.Decimal, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.balanceCalls++
	if len(w.balances) > 0 {
		r := w.balances[0]
		w.balances = w.balances[1:]
		return r.res.NewBalance.Decimal, r.err
	}
	if w.balanceErr != nil {
		return decimal.Decimal{}, w.balanceErr
	}
	return w.balance, nil
}

func (w *fakeWallet) Withdraw(ctx context.Context, req WalletRequest) (WalletResult, error) {
	w.mu.Lock()
	w.calls = append(w.calls, req)
	w.mu.Unlock()

	if w.block != nil {
		<-w.block // waits outside mu, so other goroutines can still use the fake
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.withdraws) == 0 {
		return WalletResult{}, errors.New("fakeWallet: unexpected Withdraw call")
	}
	r := w.withdraws[0]
	w.withdraws = w.withdraws[1:]
	return r.res, r.err
}

func (w *fakeWallet) callCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.calls)
}

func (w *fakeWallet) Deposit(ctx context.Context, req WalletRequest) (WalletResult, error) {
	return WalletResult{}, errors.New("fakeWallet: Deposit not scripted")
}

// fakeRepo keeps everything in maps; order remembers creation order for LastBalance.
// It is safe for concurrent use.
type fakeRepo struct {
	mu          sync.Mutex
	completeErr error // if set, CompleteWithdraw fails with it
	accounts    map[int64]Account
	txs         map[uuid.UUID]Transaction
	bets        map[int64]Bet
	order       []uuid.UUID
	nextBet     int64
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		accounts: map[int64]Account{
			testUser:  {UserID: testUser, Currency: "USD"},
			otherUser: {UserID: otherUser, Currency: "USD"},
		},
		txs:  map[uuid.UUID]Transaction{},
		bets: map[int64]Bet{},
	}
}

func (r *fakeRepo) GetAccount(ctx context.Context, userID int64) (Account, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	a, ok := r.accounts[userID]
	if !ok {
		return Account{}, errors.New("account not found")
	}
	return a, nil
}

func (r *fakeRepo) FindByProviderTxID(ctx context.Context, providerTxID string) (Transaction, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	tx, found := r.findLocked(providerTxID)
	return tx, found, nil
}

// findLocked is FindByProviderTxID for callers that already hold r.mu.
func (r *fakeRepo) findLocked(providerTxID string) (Transaction, bool) {
	for _, tx := range r.txs {
		if tx.ProviderTransactionID == providerTxID {
			return tx, true
		}
	}
	return Transaction{}, false
}

func (r *fakeRepo) GetBet(ctx context.Context, betID int64) (Bet, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	b, ok := r.bets[betID]
	if !ok {
		return Bet{}, errors.New("bet not found")
	}
	return b, nil
}

func (r *fakeRepo) LastBalance(ctx context.Context, userID int64) (decimal.NullDecimal, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := len(r.order) - 1; i >= 0; i-- {
		tx := r.txs[r.order[i]]
		if tx.UserID != userID || tx.Status == TxFailed {
			continue
		}
		return tx.NewBalance, nil // Valid=false while PENDING
	}
	return decimal.NullDecimal{}, nil
}

func (r *fakeRepo) CreateWithdraw(ctx context.Context, tx Transaction) (Transaction, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, found := r.findLocked(tx.ProviderTransactionID); found {
		return Transaction{}, ErrDuplicateProviderTxID
	}
	r.nextBet++
	tx.BetID = r.nextBet
	r.bets[tx.BetID] = Bet{ID: tx.BetID}
	r.txs[tx.ID] = tx
	r.order = append(r.order, tx.ID)
	return tx, nil
}

func (r *fakeRepo) CompleteWithdraw(ctx context.Context, txID uuid.UUID, walletTxID *int64, newBalance decimal.Decimal) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.completeErr != nil {
		return r.completeErr
	}
	tx := r.txs[txID]
	tx.Status = TxCompleted
	tx.WalletTransactionID = walletTxID
	tx.NewBalance = decimal.NewNullDecimal(newBalance)
	r.txs[txID] = tx

	bet := r.bets[tx.BetID]
	bet.Status = BetPlaced
	r.bets[tx.BetID] = bet
	return nil
}

func (r *fakeRepo) FailTransaction(ctx context.Context, txID uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	tx := r.txs[txID]
	tx.Status = TxFailed
	r.txs[txID] = tx
	return nil
}

func newTestService(w *fakeWallet) (*Service, *fakeRepo) {
	repo := newFakeRepo()
	return NewService(repo, w, slog.New(slog.DiscardHandler)), repo
}

func withdrawReq(providerTxID, amount string) WithdrawRequest {
	return WithdrawRequest{
		UserID:       testUser,
		Currency:     "USD",
		Amount:       decimal.RequireFromString(amount),
		ProviderTxID: providerTxID,
	}
}

func assertDecimal(t *testing.T, name string, got decimal.Decimal, want string) {
	t.Helper()
	if !got.Equal(decimal.RequireFromString(want)) {
		t.Errorf("%s = %s, want %s", name, got, want)
	}
}

func TestWithdrawCompleted(t *testing.T) {
	w := &fakeWallet{balance: decimal.RequireFromString("5000"), withdraws: []walletReply{applied("4989.50", 1)}}
	svc, repo := newTestService(w)

	res, err := svc.Withdraw(context.Background(), withdrawReq("p-1", "10.50"))
	if err != nil {
		t.Fatalf("Withdraw: %v", err)
	}
	if res.Outcome != OutcomeCreated || res.Transaction.Status != TxCompleted || res.BetStatus != BetPlaced {
		t.Errorf("got outcome %v, status %s, bet %s; want created, COMPLETED, PLACED",
			res.Outcome, res.Transaction.Status, res.BetStatus)
	}
	assertDecimal(t, "oldBalance", res.Transaction.OldBalance.Decimal, "5000")
	assertDecimal(t, "newBalance", res.Transaction.NewBalance.Decimal, "4989.50")
	if got := repo.txs[res.Transaction.ID].Status; got != TxCompleted {
		t.Errorf("stored status = %s, want COMPLETED", got)
	}
}

func TestWithdrawReplayDoesNotCallWallet(t *testing.T) {
	w := &fakeWallet{balance: decimal.RequireFromString("5000"), withdraws: []walletReply{applied("4989.50", 1)}}
	svc, _ := newTestService(w)
	ctx := context.Background()

	first, err := svc.Withdraw(ctx, withdrawReq("p-1", "10.50"))
	if err != nil {
		t.Fatalf("first Withdraw: %v", err)
	}
	second, err := svc.Withdraw(ctx, withdrawReq("p-1", "10.50"))
	if err != nil {
		t.Fatalf("second Withdraw: %v", err)
	}
	if second.Outcome != OutcomeReplay {
		t.Errorf("outcome = %v, want replay", second.Outcome)
	}
	if second.Transaction.ID != first.Transaction.ID {
		t.Errorf("transactionId changed on replay")
	}
	if len(w.calls) != 1 {
		t.Errorf("Wallet calls = %d, want 1", len(w.calls))
	}
}

func TestWithdrawRetriesUnknownOutcomeWithSameIDs(t *testing.T) {
	w := &fakeWallet{
		balance:   decimal.RequireFromString("5000"),
		withdraws: []walletReply{failed(errWalletDown), applied("4989.50", 1)},
	}
	svc, _ := newTestService(w)

	res, err := svc.Withdraw(context.Background(), withdrawReq("p-1", "10.50"))
	if err != nil {
		t.Fatalf("Withdraw: %v", err)
	}
	if res.Outcome != OutcomeCreated {
		t.Errorf("outcome = %v, want created", res.Outcome)
	}
	if len(w.calls) != 2 {
		t.Fatalf("Wallet calls = %d, want 2", len(w.calls))
	}
	if w.calls[0].BetID != w.calls[1].BetID || w.calls[0].Reference != w.calls[1].Reference {
		t.Errorf("retry changed betId or reference: %+v vs %+v", w.calls[0], w.calls[1])
	}
}

func TestWithdrawPendingIsResolvedByProviderRetry(t *testing.T) {
	w := &fakeWallet{
		balance:   decimal.RequireFromString("5000"),
		withdraws: []walletReply{failed(errWalletDown), failed(errWalletDown)},
	}
	svc, repo := newTestService(w)
	ctx := context.Background()

	res, err := svc.Withdraw(ctx, withdrawReq("p-1", "10.50"))
	if err != nil {
		t.Fatalf("first Withdraw: %v", err)
	}
	if res.Outcome != OutcomePending || res.Transaction.NewBalance.Valid || res.BetStatus != "" {
		t.Errorf("got outcome %v, newBalance %v, bet %q; want pending, null, empty",
			res.Outcome, res.Transaction.NewBalance, res.BetStatus)
	}
	if got := repo.txs[res.Transaction.ID].Status; got != TxPending {
		t.Errorf("stored status = %s, want PENDING", got)
	}

	// The provider retries; the Wallet says it was applied earlier (409).
	w.withdraws = []walletReply{duplicate()}
	res, err = svc.Withdraw(ctx, withdrawReq("p-1", "10.50"))
	if err != nil {
		t.Fatalf("retry Withdraw: %v", err)
	}
	if res.Outcome != OutcomeCreated || res.BetStatus != BetPlaced {
		t.Errorf("got outcome %v, bet %s; want created, PLACED", res.Outcome, res.BetStatus)
	}
	assertDecimal(t, "newBalance", res.Transaction.NewBalance.Decimal, "4989.50") // oldBalance - amount
	if len(w.calls) != 3 {
		t.Fatalf("Wallet calls = %d, want 3", len(w.calls))
	}
	for i, c := range w.calls {
		if c.BetID != w.calls[0].BetID || c.Reference != w.calls[0].Reference {
			t.Errorf("call %d changed betId or reference", i)
		}
	}
}

func TestWithdrawInsufficientFunds(t *testing.T) {
	w := &fakeWallet{
		balance:   decimal.RequireFromString("5"),
		withdraws: []walletReply{failed(fmt.Errorf("%w: InsufficientBalance", ErrInsufficientFunds))},
	}
	svc, repo := newTestService(w)
	ctx := context.Background()

	_, err := svc.Withdraw(ctx, withdrawReq("p-1", "10.50"))
	if !errors.Is(err, ErrInsufficientFunds) {
		t.Fatalf("err = %v, want ErrInsufficientFunds", err)
	}
	tx, found, _ := repo.FindByProviderTxID(ctx, "p-1")
	if !found || tx.Status != TxFailed {
		t.Errorf("stored transaction found=%v status=%s, want FAILED", found, tx.Status)
	}
	if repo.bets[tx.BetID].Status != "" {
		t.Errorf("bet status = %s, want not placed", repo.bets[tx.BetID].Status)
	}

	// A retry of a FAILED withdraw is 409 and does not call the Wallet.
	_, err = svc.Withdraw(ctx, withdrawReq("p-1", "10.50"))
	if !errors.Is(err, ErrConflict) {
		t.Errorf("retry err = %v, want ErrConflict", err)
	}
	if len(w.calls) != 1 {
		t.Errorf("Wallet calls = %d, want 1 (no retry of a definite failure)", len(w.calls))
	}
}

func TestWithdrawSameKeyDifferentAmountIsConflict(t *testing.T) {
	w := &fakeWallet{balance: decimal.RequireFromString("5000"), withdraws: []walletReply{applied("4989.50", 1)}}
	svc, _ := newTestService(w)
	ctx := context.Background()

	if _, err := svc.Withdraw(ctx, withdrawReq("p-1", "10.50")); err != nil {
		t.Fatalf("first Withdraw: %v", err)
	}
	_, err := svc.Withdraw(ctx, withdrawReq("p-1", "20.00"))
	if !errors.Is(err, ErrConflict) {
		t.Errorf("err = %v, want ErrConflict", err)
	}
	if len(w.calls) != 1 {
		t.Errorf("Wallet calls = %d, want 1", len(w.calls))
	}
}

func TestWithdrawValidation(t *testing.T) {
	tests := []struct {
		name string
		req  WithdrawRequest
	}{
		{"zero amount", withdrawReq("p-1", "0")},
		{"negative amount", withdrawReq("p-1", "-1")},
		{"three decimals", withdrawReq("p-1", "1.005")},
		{"empty providerTransactionId", withdrawReq("", "1")},
		{"unknown currency", WithdrawRequest{UserID: testUser, Currency: "GBP", Amount: decimal.NewFromInt(1), ProviderTxID: "p-1"}},
		{"currency of another wallet", WithdrawRequest{UserID: testUser, Currency: "EUR", Amount: decimal.NewFromInt(1), ProviderTxID: "p-1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := &fakeWallet{balance: decimal.RequireFromString("5000")}
			svc, _ := newTestService(w)

			_, err := svc.Withdraw(context.Background(), tt.req)
			if !errors.Is(err, ErrInvalidRequest) {
				t.Errorf("err = %v, want ErrInvalidRequest", err)
			}
			if len(w.calls) != 0 {
				t.Errorf("Wallet calls = %d, want 0", len(w.calls))
			}
		})
	}
}

// Identical requests sent at the same time reach the Wallet once (9.2).
// synctest makes the race deterministic: the first request is held inside
// the Wallet call until all the others are waiting for the player lock.
func TestWithdrawConcurrentIdenticalRequests(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := &fakeWallet{
			balance:   decimal.RequireFromString("5000"),
			withdraws: []walletReply{applied("4989.50", 1)},
			block:     make(chan struct{}),
		}
		svc, _ := newTestService(w)

		const n = 10
		results := make([]WithdrawResult, n)
		errs := make([]error, n)
		var wg sync.WaitGroup
		for i := range n {
			wg.Go(func() {
				results[i], errs[i] = svc.Withdraw(context.Background(), withdrawReq("p-1", "10.50"))
			})
		}

		// Every goroutine is blocked now: one inside the Wallet call,
		// the others on the player lock.
		synctest.Wait()
		if got := w.callCount(); got != 1 {
			t.Errorf("Wallet calls while the first one is in flight = %d, want 1", got)
		}
		close(w.block)
		wg.Wait()

		created, replays := 0, 0
		for i := range n {
			if errs[i] != nil {
				t.Errorf("request %d: %v", i, errs[i])
				continue
			}
			switch results[i].Outcome {
			case OutcomeCreated:
				created++
			case OutcomeReplay:
				replays++
			}
			if results[i].Transaction.ID != results[0].Transaction.ID {
				t.Errorf("request %d: transactionId differs", i)
			}
		}
		if created != 1 || replays != n-1 {
			t.Errorf("created = %d, replays = %d; want 1 and %d", created, replays, n-1)
		}
		if got := w.callCount(); got != 1 {
			t.Errorf("Wallet calls = %d, want 1", got)
		}
	})
}

// A Wallet 400 other than insufficient funds is a definite failure:
// FAILED record, the error is returned, no retry.
func TestWithdrawWalletRejected(t *testing.T) {
	w := &fakeWallet{
		balance:   decimal.RequireFromString("5000"),
		withdraws: []walletReply{failed(fmt.Errorf("%w: InvalidCurrency", ErrWalletRejected))},
	}
	svc, repo := newTestService(w)
	ctx := context.Background()

	_, err := svc.Withdraw(ctx, withdrawReq("p-1", "10.50"))
	if !errors.Is(err, ErrWalletRejected) {
		t.Fatalf("err = %v, want ErrWalletRejected", err)
	}
	tx, found, _ := repo.FindByProviderTxID(ctx, "p-1")
	if !found || tx.Status != TxFailed {
		t.Errorf("stored transaction found=%v status=%s, want FAILED", found, tx.Status)
	}
	if len(w.calls) != 1 {
		t.Errorf("Wallet calls = %d, want 1 (no retry of a definite failure)", len(w.calls))
	}
}

// A providerTransactionId used by another player is 404 (7.3), with no Wallet call.
func TestWithdrawKeyOfAnotherPlayerIsNotFound(t *testing.T) {
	w := &fakeWallet{balance: decimal.RequireFromString("5000"), withdraws: []walletReply{applied("4989.50", 1)}}
	svc, _ := newTestService(w)
	ctx := context.Background()

	if _, err := svc.Withdraw(ctx, withdrawReq("p-1", "10.50")); err != nil {
		t.Fatalf("first Withdraw: %v", err)
	}
	req := withdrawReq("p-1", "10.50")
	req.UserID = otherUser
	_, err := svc.Withdraw(ctx, req)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
	if len(w.calls) != 1 {
		t.Errorf("Wallet calls = %d, want 1", len(w.calls))
	}
}

// The Wallet applied the withdraw but our DB update failed: the answer is
// PENDING (what we recorded), not an error, and the row stays PENDING.
func TestWithdrawAppliedButNotRecordedStaysPending(t *testing.T) {
	w := &fakeWallet{balance: decimal.RequireFromString("5000"), withdraws: []walletReply{applied("4989.50", 1)}}
	svc, repo := newTestService(w)
	repo.completeErr = errors.New("db down")

	res, err := svc.Withdraw(context.Background(), withdrawReq("p-1", "10.50"))
	if err != nil {
		t.Fatalf("Withdraw: %v", err)
	}
	if res.Outcome != OutcomePending || res.Transaction.NewBalance.Valid || res.BetStatus != "" {
		t.Errorf("got outcome %v, newBalance %v, bet %q; want pending, null, empty",
			res.Outcome, res.Transaction.NewBalance, res.BetStatus)
	}
	if got := repo.txs[res.Transaction.ID].Status; got != TxPending {
		t.Errorf("stored status = %s, want PENDING", got)
	}
}

// The second withdraw takes oldBalance from our DB, not from the Wallet.
func TestWithdrawOldBalanceFromLastTransaction(t *testing.T) {
	w := &fakeWallet{
		balance:   decimal.RequireFromString("5000"),
		withdraws: []walletReply{applied("4989.50", 1), applied("4979.00", 2)},
	}
	svc, _ := newTestService(w)
	ctx := context.Background()

	if _, err := svc.Withdraw(ctx, withdrawReq("p-1", "10.50")); err != nil {
		t.Fatalf("first Withdraw: %v", err)
	}
	res, err := svc.Withdraw(ctx, withdrawReq("p-2", "10.50"))
	if err != nil {
		t.Fatalf("second Withdraw: %v", err)
	}
	assertDecimal(t, "oldBalance", res.Transaction.OldBalance.Decimal, "4989.50")
	if w.balanceCalls != 1 {
		t.Errorf("Wallet balance reads = %d, want 1 (only for the first withdraw)", w.balanceCalls)
	}
}

// If oldBalance cannot be read, nothing is stored and the Wallet is not
// asked to withdraw: a plain 502.
func TestWithdrawBalanceReadFails(t *testing.T) {
	w := &fakeWallet{balanceErr: errWalletDown}
	svc, repo := newTestService(w)
	ctx := context.Background()

	_, err := svc.Withdraw(ctx, withdrawReq("p-1", "10.50"))
	if !errors.Is(err, ErrWalletUnavailable) {
		t.Fatalf("err = %v, want ErrWalletUnavailable", err)
	}
	if _, found, _ := repo.FindByProviderTxID(ctx, "p-1"); found {
		t.Errorf("a transaction was stored, want none")
	}
	if len(w.calls) != 0 {
		t.Errorf("Wallet withdraw calls = %d, want 0", len(w.calls))
	}
}

// balanceIs and balanceFails script one Balance answer.
func balanceIs(v string) walletReply {
	return walletReply{res: WalletResult{NewBalance: decimal.NewNullDecimal(decimal.RequireFromString(v))}}
}

func balanceFails() walletReply { return walletReply{err: errWalletDown} }

// The my-4 case: the call was applied but the connection dropped. The
// balance shows oldBalance - amount, so the withdraw is COMPLETED and the
// Wallet is not called again (a retry could be answered 400).
func TestWithdrawUnknownThenBalanceShowsApplied(t *testing.T) {
	w := &fakeWallet{
		balances: []walletReply{balanceIs("3888.50"), balanceIs("888")},
		// What the real Wallet answered: a drop although applied, then 400 on
		// the retry because it checks the balance before the duplicate key.
		withdraws: []walletReply{failed(errWalletDown), failed(fmt.Errorf("%w: InsufficientBalance", ErrInsufficientFunds))},
	}
	svc, repo := newTestService(w)

	res, err := svc.Withdraw(context.Background(), withdrawReq("p-1", "3000.50"))
	if err != nil {
		t.Fatalf("Withdraw: %v", err)
	}
	if res.Outcome != OutcomeCreated || res.BetStatus != BetPlaced {
		t.Errorf("got outcome %v, bet %q; want created, PLACED", res.Outcome, res.BetStatus)
	}
	assertDecimal(t, "newBalance", res.Transaction.NewBalance.Decimal, "888")
	if got := repo.txs[res.Transaction.ID].Status; got != TxCompleted {
		t.Errorf("stored status = %s, want COMPLETED", got)
	}
	if len(w.calls) != 1 {
		t.Errorf("Wallet withdraw calls = %d, want 1 (no retry once the balance proves it)", len(w.calls))
	}
}

// A balance that matches neither oldBalance nor oldBalance - amount proves
// nothing: PENDING, no retry.
func TestWithdrawUnknownThenUnrelatedBalanceStaysPending(t *testing.T) {
	w := &fakeWallet{
		balances:  []walletReply{balanceIs("5000"), balanceIs("4000")},
		withdraws: []walletReply{failed(errWalletDown)},
	}
	svc, repo := newTestService(w)

	res, err := svc.Withdraw(context.Background(), withdrawReq("p-1", "10.50"))
	if err != nil || res.Outcome != OutcomePending {
		t.Fatalf("got %v, %v; want pending", res.Outcome, err)
	}
	if got := repo.txs[res.Transaction.ID].Status; got != TxPending {
		t.Errorf("stored status = %s, want PENDING", got)
	}
	if len(w.calls) != 1 {
		t.Errorf("Wallet withdraw calls = %d, want 1", len(w.calls))
	}
}

// The balance check reads twice at most; if both reads fail, the row stays PENDING.
func TestWithdrawUnknownThenBalanceUnreadableStaysPending(t *testing.T) {
	w := &fakeWallet{
		balances:  []walletReply{balanceIs("5000"), balanceFails(), balanceFails()},
		withdraws: []walletReply{failed(errWalletDown)},
	}
	svc, _ := newTestService(w)

	res, err := svc.Withdraw(context.Background(), withdrawReq("p-1", "10.50"))
	if err != nil || res.Outcome != OutcomePending {
		t.Fatalf("got %v, %v; want pending", res.Outcome, err)
	}
	if w.balanceCalls != 3 || len(w.calls) != 1 {
		t.Errorf("balance reads = %d, withdraw calls = %d; want 3 and 1", w.balanceCalls, len(w.calls))
	}
}

// A failed balance read is retried once.
func TestWithdrawBalanceCheckRetriesRead(t *testing.T) {
	w := &fakeWallet{
		balances:  []walletReply{balanceIs("5000"), balanceFails(), balanceIs("4989.50")},
		withdraws: []walletReply{failed(errWalletDown)},
	}
	svc, _ := newTestService(w)

	res, err := svc.Withdraw(context.Background(), withdrawReq("p-1", "10.50"))
	if err != nil || res.Outcome != OutcomeCreated {
		t.Fatalf("got %v, %v; want created", res.Outcome, err)
	}
}

// A provider retry of a PENDING withdraw checks the balance first and
// completes it without a new withdraw call when the balance proves it.
func TestWithdrawPendingResolvedByBalance(t *testing.T) {
	w := &fakeWallet{
		balance:   decimal.RequireFromString("5000"), // not applied yet, as far as the first request sees
		withdraws: []walletReply{failed(errWalletDown), failed(errWalletDown)},
	}
	svc, repo := newTestService(w)
	ctx := context.Background()

	res, err := svc.Withdraw(ctx, withdrawReq("p-1", "10.50"))
	if err != nil || res.Outcome != OutcomePending {
		t.Fatalf("first request: %v, %v; want pending", res.Outcome, err)
	}

	w.balance = decimal.RequireFromString("4989.50") // the Wallet applied it after all
	res, err = svc.Withdraw(ctx, withdrawReq("p-1", "10.50"))
	if err != nil || res.Outcome != OutcomeCreated || res.BetStatus != BetPlaced {
		t.Fatalf("retry: %v, %q, %v; want created, PLACED", res.Outcome, res.BetStatus, err)
	}
	if got := repo.txs[res.Transaction.ID].Status; got != TxCompleted {
		t.Errorf("stored status = %s, want COMPLETED", got)
	}
	if len(w.calls) != 2 {
		t.Errorf("Wallet withdraw calls = %d, want 2 (none on the retry)", len(w.calls))
	}
}

// Player reads the balance from the Wallet at request time and tells a
// timeout (504) from another failure (502).
func TestPlayer(t *testing.T) {
	w := &fakeWallet{balance: decimal.RequireFromString("4996.995")}
	svc, _ := newTestService(w)
	account, balance, err := svc.Player(context.Background(), testUser)
	if err != nil || account.Currency != "USD" || !balance.Equal(decimal.RequireFromString("4996.995")) {
		t.Errorf("Player = %+v, %s, %v", account, balance, err)
	}

	w.balanceErr = errWalletDown
	if _, _, err := svc.Player(context.Background(), testUser); !errors.Is(err, ErrWalletUnavailable) {
		t.Errorf("err = %v, want ErrWalletUnavailable", err)
	}
	w.balanceErr = fmt.Errorf("wallet balance: %w", context.DeadlineExceeded)
	if _, _, err := svc.Player(context.Background(), testUser); !errors.Is(err, ErrWalletTimeout) {
		t.Errorf("err = %v, want ErrWalletTimeout", err)
	}
}
