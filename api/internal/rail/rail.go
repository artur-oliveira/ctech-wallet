// Package rail is the seam between the wallet's deposit and withdrawal flows
// and whatever moves the money: today the Inter Pix client directly
// (LegacyRail), and behind a flag Billing, which owns the Inter connection.
// Everything past the seam (locks, idempotency, limits, quarantine, reconcile)
// is the wallet's own and does not know which rail is in use.
package rail

import (
	"context"

	"gopkg.aoctech.app/wallet/api/internal/pix"
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
	OpenDeposit(ctx context.Context, in DepositRequest) (*pix.Charge, error)
	QueryDeposit(ctx context.Context, txid string) (*pix.Charge, error)
	RefundDeposit(ctx context.Context, txid, e2eID string, amountCents int64, idemKey string) (*pix.TransferResult, error)
	// Payout sends to the destination, which is always the KYC owner's CPF.
	Payout(ctx context.Context, cpf string, amountCents int64, idemKey string) (*pix.TransferResult, error)
	// QueryPayout reports a payout by its idempotency key. A transport failure is
	// an error; only a payout the rail never accepted is TransferNotFound.
	QueryPayout(ctx context.Context, idemKey string) (*pix.TransferResult, error)
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
