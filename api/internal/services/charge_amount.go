package services

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
	"gopkg.aoctech.app/wallet/api/internal/pix"
	"gopkg.aoctech.app/wallet/api/internal/problem"
	"gopkg.aoctech.app/wallet/api/internal/repositories"
)

// The caller-supplied-amount charge (docs/specs, ctech-billing
// 2026-08-15-wallet-invoice-charge.md).
//
// It exists because an invoice total is not a catalogue price. Proration and
// metered usage make it arbitrary by construction, so ctech-billing cannot ask
// for a SKU — there is no finite set of amounts a subscription can produce.
//
// Everything else is PurchaseProductDirect's machinery, reused rather than
// copied: the same deterministic txid, the same idempotent reservation before
// the charge, the same confirm-by-re-query, the same notify-back and sweep. The
// one thing that changes is where the amount comes from, and therefore what
// bounds it.

// OpenChargeInput is one caller-supplied charge.
type OpenChargeInput struct {
	UserID string
	// AmountCents is the whole point of the route, and the whole risk of it.
	AmountCents int64
	// Reference is the caller's own opaque label — an invoice id, for billing.
	// Wallet stores it and hands it back; it means nothing here.
	Reference      string
	IdempotencyKey string
	// PayerHintCPF is optional. The rail uses it to match the payer; wallet does
	// not persist it for that purpose and the charge opens without it.
	PayerHintCPF string
	// RequestingClient is the AZP claim, never anything from the body. It is what
	// the ceiling, the txid and the ownership check are all keyed on.
	RequestingClient string
	// Description is optional free-form display text for the charge row. Display
	// metadata only — never part of the request hash, never price authority.
	Description string
}

// OpenCharge opens a PIX charge for an amount the caller names.
//
// Three defenses, and they only work together (ADR 0004 in ctech-billing):
// the dedicated scope keeps this route away from catalogue clients, the ceiling
// bounds what any one call can ask for, and the mandatory idempotency key makes
// a replay return the original charge instead of opening a second one. Any one
// of them alone is not enough.
func (s *WalletService) OpenCharge(ctx context.Context, in OpenChargeInput) (*wallet.ProductPurchase, *pix.Charge, error) {
	if in.AmountCents <= 0 {
		return nil, nil, problem.BadRequest("amount_cents deve ser positivo")
	}
	if in.Reference == "" {
		return nil, nil, problem.BadRequest("reference é obrigatório")
	}

	// Checked here, before the reservation, so a refused charge leaves no row
	// behind. A rejected request that still wrote state would burn its
	// idempotency key: the caller's retry with a corrected amount would collide
	// with a reservation for an amount nobody ever agreed to.
	ceiling := s.m2mClients[in.RequestingClient].MaxCharge()
	if in.AmountCents > ceiling {
		slog.WarnContext(ctx, "charge refused above the client ceiling",
			"client", in.RequestingClient, "amount", in.AmountCents, "ceiling", ceiling)
		return nil, nil, problem.UnprocessableEntity("valor acima do limite configurado para este cliente")
	}

	purchaseID := productPurchaseTxID(in.UserID, in.IdempotencyKey, in.RequestingClient)
	now := repositories.NowStr()
	p := &wallet.ProductPurchase{
		PurchaseID:  purchaseID,
		UserID:      in.UserID,
		SKU:         in.Reference,
		Kind:        wallet.ProductPurchaseKindCharge,
		Description: in.Description,
		// The hash binds the key to the amount **from the request**, not to a
		// catalogue price. Without that, replaying one idempotency key with a
		// bigger amount would return the original charge and read as success —
		// which is precisely the hole the catalogue used to close.
		AmountExpected:   in.AmountCents,
		RequestHash:      reqHash(in.RequestingClient+"#"+in.UserID+"#"+in.Reference, in.AmountCents),
		Status:           wallet.ProductPurchasePending,
		RequestingClient: in.RequestingClient,
		CreatedAt:        now,
		UpdatedAt:        now,
		TTL:              time.Now().Add(productPurchaseTTLMinutes * time.Minute).Unix(),
	}

	if err := s.productPurchases.PutIfAbsent(ctx, p); err != nil {
		if !errors.Is(err, repositories.ErrProductPurchaseExists) {
			return nil, nil, err
		}
		existing, gerr := s.productPurchases.Get(ctx, purchaseID)
		if gerr != nil {
			return nil, nil, gerr
		}
		if existing.RequestHash != p.RequestHash ||
			existing.UserID != in.UserID || existing.RequestingClient != in.RequestingClient {
			return nil, nil, problem.IdempotencyConflict()
		}
		// A genuine replay. Hand back the charge that already exists rather than
		// opening a second one against the same invoice — two live PIX charges for
		// one bill is a duplicate payment waiting for somebody to ask for it back.
		charge, qerr := s.pix.QueryCharge(ctx, purchaseID)
		if qerr != nil {
			charge, qerr = s.pix.CreateCharge(ctx, purchaseID, existing.AmountExpected, in.PayerHintCPF)
			if qerr != nil {
				return nil, nil, qerr
			}
		}
		return existing, charge, nil
	}

	charge, err := s.pix.CreateCharge(ctx, purchaseID, in.AmountCents, in.PayerHintCPF)
	if err != nil {
		slog.Error("charge creation failed", "purchase_id", purchaseID, "err", err)
		return nil, nil, problem.InternalServer("falha ao criar cobrança PIX")
	}
	return p, charge, nil
}

// GetCharge is the caller's read-back, and the only thing it may ever see is its
// own charge.
//
// It matters more here than on the catalogue route: this is the call the
// consumer makes before moving money on its side, so an id belonging to another
// client must be reported as not-found rather than described.
func (s *WalletService) GetCharge(ctx context.Context, purchaseID, requestingClient string) (*wallet.ProductPurchase, error) {
	p, err := s.productPurchases.Get(ctx, purchaseID)
	if err != nil {
		return nil, err
	}
	if p == nil || p.RequestingClient != requestingClient || p.Kind != wallet.ProductPurchaseKindCharge {
		return nil, problem.NotFound("cobrança não encontrada")
	}
	return p, nil
}
