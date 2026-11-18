package betting

import "errors"

var (
	ErrInsufficientFunds = errors.New("insufficient funds")
	ErrWalletRejected    = errors.New("wallet rejected the operation")

	ErrDuplicateProviderTxID = errors.New("provider transaction id already used")

	ErrInvalidRequest    = errors.New("invalid request")
	ErrNotFound          = errors.New("not found")
	ErrConflict          = errors.New("conflict")
	ErrWalletUnavailable = errors.New("wallet unavailable")
)
