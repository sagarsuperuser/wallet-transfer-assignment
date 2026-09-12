// Package domain holds the entities, state transitions, and validation rules of
// the wallet transfer service. It knows nothing about HTTP or SQL: everything
// here is true regardless of how a transfer arrives or where it is stored.
package domain

import (
	"errors"
	"fmt"
)

// Validation errors. Each describes a request that is malformed on its face,
// independent of any state in the database, so the handler maps all of them to
// 400 without needing to know which one it received.
var (
	ErrMissingIdempotencyKey = errors.New("idempotencyKey is required")
	ErrIdempotencyKeyTooLong = fmt.Errorf("idempotencyKey must be at most %d characters", MaxIdempotencyKeyLength)
	ErrMissingFromWallet     = errors.New("fromWalletId is required")
	ErrMissingToWallet       = errors.New("toWalletId is required")
	ErrSameWallet            = errors.New("fromWalletId and toWalletId must differ")
	ErrNonPositiveAmount     = errors.New("amount must be greater than zero")
)

// Outcome errors. These depend on stored state, and each maps to its own status
// code: see docs/design.md.
var (
	// ErrInsufficientFunds is recorded on the transfer and committed, so a
	// replay of the same idempotency key reports the same failure.
	ErrInsufficientFunds = errors.New("insufficient funds")

	// ErrWalletNotFound reports a wallet that does not exist. Match it with
	// errors.Is; use WalletNotFoundError to learn which wallet.
	ErrWalletNotFound = errors.New("wallet not found")

	// ErrIdempotencyKeyConflict reports a key replayed with different
	// parameters than it was first used with.
	ErrIdempotencyKeyConflict = errors.New("idempotency key already used with different parameters")

	// ErrInvalidStateTransition reports an attempt to move a transfer out of a
	// state it is not in. It should be unreachable: every transition is guarded
	// in SQL as well, and it exists so a logic bug surfaces as an error rather
	// than as a silently skipped update.
	ErrInvalidStateTransition = errors.New("invalid state transition")
)

// WalletNotFoundError names the wallet that was missing, so the handler can say
// which one rather than making the caller guess.
type WalletNotFoundError struct {
	WalletID string
}

func (e *WalletNotFoundError) Error() string {
	return fmt.Sprintf("wallet %s does not exist", e.WalletID)
}

// Is reports WalletNotFoundError as ErrWalletNotFound, so callers can match the
// category with errors.Is and still recover the wallet id with errors.As.
func (e *WalletNotFoundError) Is(target error) bool {
	return target == ErrWalletNotFound
}
