package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"time"

	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
	"gopkg.aoctech.app/wallet/api/internal/pix"
	"gopkg.aoctech.app/wallet/api/internal/problem"
	"gopkg.aoctech.app/wallet/api/internal/repositories"
)

// productPurchaseTTLMinutes mirrors sandboxPurchaseTTLMinutes's reasoning
// exactly — must outlast both the sweep interval and the charge's real
// validity.
const productPurchaseTTLMinutes = sandboxPurchaseTTLMinutes

const (
	productPurchaseTxIDPrefix       = "prdp"
	productPurchaseTxIDDigestLength = sandboxPurchaseTxIDDigestLength
)

// productPurchaseTxID mirrors sandboxPurchaseTxID exactly, but with the
// "prdp" prefix so pix-gateway's webhook and poker's own dispatch can route
// it distinctly from a sandbox-credits purchase ("sbxp").
func productPurchaseTxID(userID, idemKey, requestingClient string) string {
	identity := requestingClient + sandboxPurchaseIDSeparator + userID + sandboxPurchaseIDSeparator + idemKey
	digest := sha256.Sum256([]byte(identity))
	return productPurchaseTxIDPrefix + hex.EncodeToString(digest[:])[:productPurchaseTxIDDigestLength]
}

// ProductPurchaseRepo is the persistence seam PurchaseProductDirect/
// ConfirmProductPurchase/RefundProductPurchase depend on — satisfied by
// *repositories.ProductPurchaseRepository in production, a fake in tests.
type ProductPurchaseRepo interface {
	PutIfAbsent(ctx context.Context, p *wallet.ProductPurchase) error
	Get(ctx context.Context, purchaseID string) (*wallet.ProductPurchase, error)
	TransitionStatus(ctx context.Context, purchaseID, fromStatus, toStatus string) (bool, error)
	Update(ctx context.Context, purchaseID string, updates map[string]any) error
	ListPendingOlderThan(ctx context.Context, cutoff time.Time, limit int) ([]wallet.ProductPurchase, error)
	ListWebhookFailedOlderThan(ctx context.Context, cutoff time.Time, limit int) ([]wallet.ProductPurchase, error)
}

// PurchaseProductDirect sells a fixed-price digital good for real PIX money —
// no KYC gate (product sale, not custody), no ledger effect whatsoever
// (docs/specs/2026-08-12-product-purchase-skus.md). Mirrors
// PurchaseSandboxDirect's idempotent-reservation-before-charge shape exactly.
func (s *WalletService) PurchaseProductDirect(ctx context.Context, userID, sku, idemKey, requestingClient string) (*wallet.ProductPurchase, *pix.Charge, error) {
	skuDef, ok := wallet.ProductSKUByID(sku)
	if !ok {
		return nil, nil, problem.BadRequest("sku inválido")
	}
	purchaseID := productPurchaseTxID(userID, idemKey, requestingClient)
	now := repositories.NowStr()
	p := &wallet.ProductPurchase{
		PurchaseID:       purchaseID,
		UserID:           userID,
		SKU:              sku,
		AmountExpected:   skuDef.PriceCents,
		RequestHash:      reqHash(requestingClient+"#"+userID+"#"+sku, skuDef.PriceCents),
		Status:           wallet.ProductPurchasePending,
		RequestingClient: requestingClient,
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
		if (existing.RequestHash != "" && existing.RequestHash != p.RequestHash) ||
			existing.UserID != userID || existing.SKU != sku || existing.RequestingClient != requestingClient {
			return nil, nil, problem.IdempotencyConflict()
		}
		charge, qerr := s.pix.QueryCharge(ctx, purchaseID)
		if qerr != nil {
			charge, qerr = s.pix.CreateCharge(ctx, purchaseID, existing.AmountExpected, "")
			if qerr != nil {
				return nil, nil, qerr
			}
		}
		return existing, charge, nil
	}

	charge, err := s.pix.CreateCharge(ctx, purchaseID, skuDef.PriceCents, "")
	if err != nil {
		slog.Error("product purchase charge creation failed", "purchase_id", purchaseID, "err", err)
		return nil, nil, problem.InternalServer("falha ao criar cobrança PIX")
	}
	return p, charge, nil
}

// ConfirmProductPurchase re-queries the PIX charge (never trusts the webhook
// body — Invariant #11), and on success just transitions pending→confirmed
// and dispatches the M2M webhook. No wallet lock, no Credit, no ledger entry
// — this is the entire generalization versus ConfirmSandboxPurchase
// (docs/specs/2026-08-12-product-purchase-skus.md).
func (s *WalletService) ConfirmProductPurchase(ctx context.Context, txid string, sweep bool) error {
	p, err := s.productPurchases.Get(ctx, txid)
	if err != nil {
		return err
	}
	if p == nil {
		return nil
	}
	if p.Status != wallet.ProductPurchasePending {
		return nil
	}

	charge, err := s.pix.QueryCharge(ctx, txid)
	if err != nil {
		return err
	}
	if charge.Status != pix.ChargeCompleted {
		return nil
	}
	if charge.Amount != p.AmountExpected {
		slog.Error("ALARM product purchase amount mismatch", "purchase_id", txid, "expected", p.AmountExpected, "paid", charge.Amount)
		return problem.InternalServer("valor pago não corresponde ao esperado; reconciliação manual necessária")
	}

	changed, err := s.productPurchases.TransitionStatus(ctx, txid, wallet.ProductPurchasePending, wallet.ProductPurchaseConfirmed)
	if err != nil {
		return err
	}
	if !changed {
		return nil // lost a race with a concurrent confirm — already handled
	}
	p.Status = wallet.ProductPurchaseConfirmed
	p.E2EID = charge.E2EID
	s.dispatchM2MWebhookProduct(ctx, p)
	return nil
}

// RefundProductPurchase reverses a confirmed purchase. No usage-eligibility
// check here — that is the caller's domain fact (docs/specs/2026-08-12-
// product-purchase-skus.md Non-goals), unlike RefundSandboxPurchase's
// AnyDebitSince check. requestingClient isolates cross-client access: a
// purchase opened by a different client (or the user-direct route) is
// reported as not-found, never leaked.
func (s *WalletService) RefundProductPurchase(ctx context.Context, userID, purchaseID, idemKey, requestingClient string) (*wallet.ProductPurchase, error) {
	p, err := s.productPurchases.Get(ctx, purchaseID)
	if err != nil {
		return nil, err
	}
	if p == nil || p.UserID != userID || (requestingClient != "" && p.RequestingClient != requestingClient) {
		return nil, problem.NotFound("compra não encontrada")
	}
	if p.Status == wallet.ProductPurchaseRefunded {
		return p, nil
	}
	if p.Status != wallet.ProductPurchaseConfirmed {
		return nil, problem.Conflict("compra ainda não confirmada")
	}

	if _, err := s.pix.Refund(ctx, p.E2EID, p.AmountExpected, "product_refund#"+purchaseID); err != nil {
		slog.Error("ALARM product purchase refund failed", "purchase_id", purchaseID, "e2e_id", p.E2EID, "err", err)
		return nil, problem.InternalServer("estorno da compra falhou; nova tentativa agendada")
	}
	changed, err := s.productPurchases.TransitionStatus(ctx, purchaseID, wallet.ProductPurchaseConfirmed, wallet.ProductPurchaseRefunded)
	if err != nil {
		return nil, err
	}
	if !changed {
		current, err := s.productPurchases.Get(ctx, purchaseID)
		if err != nil {
			return nil, err
		}
		return current, nil
	}
	p.Status = wallet.ProductPurchaseRefunded
	s.dispatchM2MWebhookProduct(ctx, p)
	return p, nil
}

// GetProductPurchase is the M2M poll endpoint's read path — never the
// webhook body alone. Ownership enforced identically to
// RefundProductPurchase.
func (s *WalletService) GetProductPurchase(ctx context.Context, purchaseID, requestingClient string) (*wallet.ProductPurchase, error) {
	p, err := s.productPurchases.Get(ctx, purchaseID)
	if err != nil {
		return nil, err
	}
	if p == nil || p.RequestingClient != requestingClient {
		return nil, problem.NotFound("compra não encontrada")
	}
	return p, nil
}

// SweepPendingProductPurchases re-queries the PIX provider once for every
// pending purchase approaching its TTL — mirrors SweepPendingSandboxPurchases,
// reusing ConfirmProductPurchase's own idempotent confirm logic. No
// SweepRefundPendingProductPurchases counterpart exists: a refund has no
// pending stage to resume (docs/specs/2026-08-12-product-purchase-skus.md).
func (s *WalletService) SweepPendingProductPurchases(ctx context.Context) (swept int, err error) {
	cutoff := time.Now().Add(-sweepAgeThreshold)
	purchases, err := s.productPurchases.ListPendingOlderThan(ctx, cutoff, reconcileBatch)
	if err != nil {
		return 0, err
	}
	for i := range purchases {
		p := purchases[i]
		if err := s.ConfirmProductPurchase(ctx, p.PurchaseID, true); err != nil {
			slog.Warn("sweep: confirm-product-purchase failed, will retry next run", "purchase_id", p.PurchaseID, "err", err)
			continue
		}
		swept++
	}
	return swept, nil
}

// SetProductPurchases wires the generic product-purchase repository after
// construction — same setter pattern as SetSandboxPurchases. Unset,
// PurchaseProductDirect/ConfirmProductPurchase/RefundProductPurchase panic on
// first use — cmd/server and cmd/reconcile must always call this.
func (s *WalletService) SetProductPurchases(r ProductPurchaseRepo) {
	s.productPurchases = r
}
