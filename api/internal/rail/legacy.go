package rail

import (
	"context"

	"gopkg.aoctech.app/wallet/api/internal/pix"
)

// LegacyRail is today's behaviour: the Inter Pix client, unchanged.
type LegacyRail struct{ p pix.PixClient }

func NewLegacy(p pix.PixClient) Rail { return &LegacyRail{p: p} }

func (l *LegacyRail) OpenDeposit(ctx context.Context, in DepositRequest) (*pix.Charge, error) {
	return l.p.CreateCharge(ctx, in.Txid, in.AmountCents, "")
}

func (l *LegacyRail) QueryDeposit(ctx context.Context, txid string) (*pix.Charge, error) {
	return l.p.QueryCharge(ctx, txid)
}

func (l *LegacyRail) RefundDeposit(ctx context.Context, _, e2eID string, amountCents int64, idemKey string) (*pix.TransferResult, error) {
	return l.p.Refund(ctx, e2eID, amountCents, idemKey)
}

func (l *LegacyRail) Payout(ctx context.Context, cpf string, amountCents int64, idemKey string) (*pix.TransferResult, error) {
	return l.p.Transfer(ctx, cpf, amountCents, idemKey)
}

func (l *LegacyRail) QueryPayout(ctx context.Context, idemKey string) (*pix.TransferResult, error) {
	return l.p.QueryTransfer(ctx, idemKey)
}
