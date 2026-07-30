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

// sandboxPurchaseTTLMinutes mirrors depositTTLMinutes's reasoning exactly:
// must outlast both the sweep interval and the charge's real validity so a
// pending purchase is always re-queried before the row is TTL-deleted.
const sandboxPurchaseTTLMinutes = depositTTLMinutes

// PurchaseSandboxDirect sells a fixed sandbox-credit pack for a fixed PIX
// price, charged via the existing Inter integration to CTech's own pooled
// account — never a wallet-to-wallet transfer, never Asaas (custody is not
// involved). No KYC gate: this is a product sale (CDC), not custody (Res.
// Conj. 16/2025) — plan §9.1. SKUs are a fixed, server-side table — never
// client-supplied, same "never trust the client with a money-shaped number"
// posture as every other amount in this codebase.
//
// purchaseID is deterministic ("sbxp#"+userID+"#"+idemKey) and doubles as
// both the idempotency guard and the Inter txid — reserved via a conditional
// write BEFORE any charge is opened (SEC-08-style: a retried request can
// never open a second charge), same precondition ReserveDepositIdem enforces
// for deposits via a separate guard table; here the deterministic ID makes a
// second table unnecessary.
func (s *WalletService) PurchaseSandboxDirect(ctx context.Context, userID, sku, idemKey string) (*wallet.SandboxPurchase, *pix.Charge, error) {
	skuDef, ok := wallet.SandboxSKUByID(sku)
	if !ok {
		return nil, nil, problem.BadRequest("sku inválido")
	}
	purchaseID := "sbxp#" + userID + "#" + idemKey
	now := repositories.NowStr()
	p := &wallet.SandboxPurchase{
		PurchaseID: purchaseID, UserID: userID, SKU: sku, AmountExpected: skuDef.PriceCentavos,
		CreditsGranted: skuDef.CreditsGranted, Status: wallet.SandboxPurchasePending,
		CreatedAt: now, UpdatedAt: now, TTL: time.Now().Add(sandboxPurchaseTTLMinutes * time.Minute).Unix(),
	}
	if err := s.sandboxPurchases.PutIfAbsent(ctx, p); err != nil {
		if !errors.Is(err, repositories.ErrSandboxPurchaseExists) {
			return nil, nil, err
		}
		// Idempotent replay: return the original purchase + its (re-queried) charge.
		existing, gerr := s.sandboxPurchases.Get(ctx, purchaseID)
		if gerr != nil {
			return nil, nil, gerr
		}
		charge, qerr := s.pix.QueryCharge(ctx, purchaseID)
		if qerr != nil {
			return nil, nil, qerr
		}
		return existing, charge, nil
	}

	charge, err := s.pix.CreateCharge(ctx, purchaseID, skuDef.PriceCentavos, "")
	if err != nil {
		return nil, nil, problem.InternalServer("falha ao criar cobrança PIX: " + err.Error())
	}
	return p, charge, nil
}

// ConfirmSandboxPurchase mirrors ConfirmDeposit's shape (re-query by txid,
// never trust the webhook body for money movement — Invariant #11) but for a
// sale, not custody: no CPF gate (no KYC, nothing to match against) and no
// `real` wallet — confirmed payment credits EnsureSandboxWallet(userID)
// exactly once (plan §9.3). sweep mirrors ConfirmDeposit's own sweep
// semantics for parity, though this flow has no CPF gate to skip either way.
func (s *WalletService) ConfirmSandboxPurchase(ctx context.Context, txid string, sweep bool) error {
	p, err := s.sandboxPurchases.Get(ctx, txid)
	if err != nil {
		return err
	}
	if p == nil {
		return nil // unknown — idempotent no-op, same posture as ConfirmDeposit's unknown-txid handling
	}
	if p.Status != wallet.SandboxPurchasePending {
		return nil // already resolved (confirmed/refunded/expired) — idempotent no-op
	}

	charge, err := s.pix.QueryCharge(ctx, txid)
	if err != nil {
		return err
	}
	if err := s.refundExcessPayments(ctx, txid, charge); err != nil {
		return err
	}
	if charge.Status != pix.ChargeCompleted {
		return nil // not paid yet — safe to be re-woken later
	}
	if charge.Amount != p.AmountExpected {
		slog.Error("ALARM sandbox purchase amount mismatch", "purchase_id", txid, "expected", p.AmountExpected, "paid", charge.Amount)
		return problem.InternalServer("valor pago não corresponde ao esperado; reconciliação manual necessária")
	}

	sandbox, err := s.repo.EnsureSandboxWallet(ctx, p.UserID)
	if err != nil {
		return err
	}
	release, ok, err := s.lock.Acquire(ctx, sandbox.WalletID)
	if err != nil {
		return err
	}
	if !ok {
		return problem.WalletBusy()
	}
	defer release()

	entry, _, err := s.repo.Credit(ctx, repositories.Mutation{
		WalletID: sandbox.WalletID, Amount: p.CreditsGranted, EntryType: wallet.EntrySandboxCredit,
		Ref: txid, IdempotencyKey: "sandbox_purchase#" + txid, ReqHash: reqHash(txid, p.CreditsGranted),
	})
	if err != nil {
		return err
	}
	return s.sandboxPurchases.Update(ctx, txid, map[string]any{
		"status": wallet.SandboxPurchaseConfirmed, "e2e_id": charge.E2EID, "credit_sk": entry.SK,
	})
}

// RefundSandboxPurchase reverses an unused sandbox purchase per §9.2:
// eligible iff no debit has landed on the sandbox wallet since this
// purchase's credit. Revokes exactly the credits this purchase granted, then
// refunds the PIX payment — never a sandbox→game/real conversion (Invariant
// #6 untouched): this is a debit that zeroes out the entitlement, not a
// conversion — the credits are simply revoked, they never become game or
// real money.
func (s *WalletService) RefundSandboxPurchase(ctx context.Context, userID, purchaseID, idemKey string) (*wallet.SandboxPurchase, error) {
	p, err := s.sandboxPurchases.Get(ctx, purchaseID)
	if err != nil {
		return nil, err
	}
	if p == nil || p.UserID != userID {
		return nil, problem.NotFound("compra não encontrada")
	}
	if p.Status == wallet.SandboxPurchaseRefunded {
		return p, nil // idempotent replay
	}
	if p.Status != wallet.SandboxPurchaseConfirmed {
		return nil, problem.Conflict("compra ainda não confirmada")
	}

	sandbox, err := s.repo.EnsureSandboxWallet(ctx, userID)
	if err != nil {
		return nil, err
	}
	if used, err := s.repo.AnyDebitSince(ctx, sandbox.WalletID, p.CreditSK); err != nil {
		return nil, err
	} else if used {
		return nil, problem.SandboxPurchaseUsed()
	}

	release, ok, err := s.lock.Acquire(ctx, sandbox.WalletID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, problem.WalletBusy()
	}
	defer release()

	// Re-check under the lock: a concurrent debit may have won the race
	// between the check above and acquiring the lock.
	if used, err := s.repo.AnyDebitSince(ctx, sandbox.WalletID, p.CreditSK); err != nil {
		return nil, err
	} else if used {
		return nil, problem.SandboxPurchaseUsed()
	}

	if _, _, err := s.repo.Debit(ctx, repositories.Mutation{
		WalletID: sandbox.WalletID, Amount: p.CreditsGranted, EntryType: wallet.EntrySandboxRefundReversal,
		Ref: purchaseID, IdempotencyKey: "sandbox_refund#" + purchaseID, ReqHash: reqHash(purchaseID, p.CreditsGranted),
	}); err != nil {
		return nil, err
	}

	if _, err := s.pix.Refund(ctx, p.E2EID, p.AmountExpected, "sandbox_refund#"+purchaseID); err != nil {
		slog.Error("ALARM sandbox purchase refund failed", "purchase_id", purchaseID, "e2e_id", p.E2EID, "err", err)
		return nil, problem.InternalServer("estorno da compra falhou; reconciliação manual necessária")
	}

	if err := s.sandboxPurchases.Update(ctx, purchaseID, map[string]any{"status": wallet.SandboxPurchaseRefunded}); err != nil {
		return nil, err
	}
	p.Status = wallet.SandboxPurchaseRefunded
	return p, nil
}

// SweepPendingSandboxPurchases re-queries Inter once for every pending
// purchase approaching its TTL — the sandbox-purchase counterpart to
// SweepPendingDeposits, reusing ConfirmSandboxPurchase's own idempotent
// credit logic.
func (s *WalletService) SweepPendingSandboxPurchases(ctx context.Context) (swept int, err error) {
	cutoff := time.Now().Add(-sweepAgeThreshold)
	purchases, err := s.sandboxPurchases.ListPendingOlderThan(ctx, cutoff, reconcileBatch)
	if err != nil {
		return 0, err
	}
	for i := range purchases {
		p := purchases[i]
		if err := s.ConfirmSandboxPurchase(ctx, p.PurchaseID, true); err != nil {
			slog.Warn("sweep: confirm-sandbox-purchase failed, will retry next run", "purchase_id", p.PurchaseID, "err", err)
			continue
		}
		swept++
	}
	return swept, nil
}
