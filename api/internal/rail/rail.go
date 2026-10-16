// Package rail is the seam between the wallet's deposit and withdrawal flows
// and whatever moves the money: Billing, which owns the Inter connection.
// Everything past the seam (locks, idempotency, limits, quarantine, reconcile)
// is the wallet's own and does not know which rail is in use.
package rail

import (
	"context"
	"time"
)

// DepositRequest is what opening a deposit needs. The identity fields are the
// KYC record's and are filled only when the rail asks for them.
type DepositRequest struct {
	UserID, TaxID, Name, Email string // TaxID is the CPF
	Txid                       string // the wallet's deterministic deposit id, also the rail's reference
	AmountCents                int64
}

// Rail opens and reads deposits, refunds them, and pays out.
type Rail interface {
	OpenDeposit(ctx context.Context, in DepositRequest) (*Charge, error)
	QueryDeposit(ctx context.Context, txid string) (*Charge, error)
	RefundDeposit(ctx context.Context, txid, e2eID string, amountCents int64, idemKey string) (*TransferResult, error)
	// Payout sends to the destination, which is always the KYC owner's CPF.
	Payout(ctx context.Context, cpf string, amountCents int64, idemKey string) (*TransferResult, error)
	// QueryPayout reports a payout by its idempotency key. A transport failure is
	// an error; only a payout the rail never accepted is TransferNotFound.
	QueryPayout(ctx context.Context, idemKey string) (*TransferResult, error)
}

// IdentityRail is implemented by a rail that needs the KYC identity to open a
// deposit. A rail that does not implement it is never handed one, so the legacy
// path makes no KYC call it did not make before.
type IdentityRail interface {
	NeedsIdentity() bool
}

// NeedsIdentity reports whether OpenDeposit wants the KYC identity filled in.
func NeedsIdentity(r Rail) bool {
	i, ok := r.(IdentityRail)
	return ok && i.NeedsIdentity()
}

// GraceRail is implemented by a rail that cannot tell "never accepted" from "not
// yet visible" the instant a payout is asked for. Reconcile does not reverse a
// withdrawal the rail reports as unknown until it is that old.
type GraceRail interface {
	PayoutGrace() time.Duration
}

// PayoutGrace is how old a processing withdrawal must be before "unknown to the
// rail" is believed. Zero for the Inter path, as it always was.
func PayoutGrace(r Rail) time.Duration {
	if g, ok := r.(GraceRail); ok {
		return g.PayoutGrace()
	}
	return 0
}
