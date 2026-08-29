package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
	"gopkg.aoctech.app/wallet/api/internal/pix"
	"gopkg.aoctech.app/wallet/api/internal/problem"
	"gopkg.aoctech.app/wallet/api/internal/repositories"
)

// sandboxPurchaseTTLMinutes mirrors depositTTLMinutes's reasoning exactly:
// must outlast both the sweep interval and the charge's real validity so a
// pending purchase is always re-queried before the row is TTL-deleted.
const sandboxPurchaseTTLMinutes = depositTTLMinutes

const (
	sandboxPurchaseTxIDPrefix       = "sbxp"
	sandboxPurchaseTxIDDigestLength = 31
	sandboxPurchaseIDSeparator      = "\x00"
)

// SandboxPurchaseHistory is the ownership-scoped read path used by the
// caller's wallet UI. It remains available without gambling activation and
// performs no wallet creation or mutation.
func (s *WalletService) SandboxPurchaseHistory(ctx context.Context, userID string, limit int, startKey map[string]types.AttributeValue) (*repositories.Page[wallet.SandboxPurchase], error) {
	return s.sandboxPurchases.ListByUser(ctx, userID, limit, startKey)
}

// sandboxPurchaseTxID returns the stable identifier used both as the purchase
// primary key and as Inter's txid. Inter only accepts 26-35 ASCII
// alphanumeric characters, so the caller-controlled values must never be
// embedded verbatim. The sbxp prefix lets pix-gateway route the webhook while
// the truncated SHA-256 digest keeps the user-direct and per-M2M-client
// idempotency namespaces disjoint. NUL separators make the tuple encoding
// unambiguous before hashing.
func sandboxPurchaseTxID(userID, idemKey, requestingClient string) string {
	identity := requestingClient + sandboxPurchaseIDSeparator + userID + sandboxPurchaseIDSeparator + idemKey
	digest := sha256.Sum256([]byte(identity))
	return sandboxPurchaseTxIDPrefix + hex.EncodeToString(digest[:])[:sandboxPurchaseTxIDDigestLength]
}

// PurchaseSandboxDirect sells a fixed sandbox-credit pack for a fixed PIX
// price, charged via the existing Inter integration to CTech's own pooled
// account — never a wallet-to-wallet transfer, never Asaas (custody is not
// involved). No KYC gate: this is a product sale (CDC), not custody (Res.
// Conj. 16/2025) — plan §9.1. SKUs are a fixed, server-side table — never
// client-supplied, same "never trust the client with a money-shaped number"
// posture as every other amount in this codebase.
//
// purchaseID is a deterministic, Inter-compatible digest of the caller
// namespace, userID, and idemKey. It doubles as both the idempotency guard and
// the Inter txid —
// reserved via a conditional write BEFORE any charge is opened (SEC-08-style:
// a retried request can never open a second charge), same precondition
// ReserveDepositIdem enforces for deposits via a separate guard table; here
// the deterministic ID makes a second table unnecessary. The requestingClient
// prefix keeps the M2M namespace disjoint from the user-direct one and from
// other M2M clients, so two different callers can never collide on the same
// (userID, idemKey) pair.
//
// requestingClient is the caller's AZP for an M2M-opened purchase (plan: M2M
// sandbox-purchase integration), or "" for the user-facing route. Threaded
// through to ConfirmSandboxPurchase's notify-back and to
// RefundSandboxPurchase/GetSandboxPurchase's ownership check.
func (s *WalletService) PurchaseSandboxDirect(ctx context.Context, userID, sku, idemKey, requestingClient, description string) (*wallet.SandboxPurchase, *pix.Charge, error) {
	skuDef, ok := wallet.SandboxSKUByID(sku)
	if !ok {
		return nil, nil, problem.BadRequest("sku inválido")
	}
	purchaseID := sandboxPurchaseTxID(userID, idemKey, requestingClient)
	now := repositories.NowStr()
	p := &wallet.SandboxPurchase{
		PurchaseID:       purchaseID,
		UserID:           userID,
		SKU:              sku,
		AmountExpected:   skuDef.PriceCents,
		CreditsGranted:   skuDef.TotalCredits(),
		RequestHash:      reqHash(requestingClient+"#"+userID+"#"+sku, skuDef.PriceCents),
		Status:           wallet.SandboxPurchasePending,
		Description:      description,
		RequestingClient: requestingClient,
		CreatedAt:        now,
		UpdatedAt:        now,
		TTL:              time.Now().Add(sandboxPurchaseTTLMinutes * time.Minute).Unix(),
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
		if (existing.RequestHash != "" && existing.RequestHash != p.RequestHash) ||
			existing.UserID != userID || existing.SKU != sku || existing.RequestingClient != requestingClient {
			return nil, nil, problem.IdempotencyConflict()
		}
		charge, qerr := s.pix.QueryCharge(ctx, purchaseID)
		if qerr != nil {
			// Recover a crash/failure after the durable reservation but before
			// CreateCharge. Inter's txid is unique, so retrying creation with the
			// same deterministic txid cannot open a second charge.
			charge, qerr = s.pix.CreateCharge(ctx, purchaseID, existing.AmountExpected, "")
			if qerr != nil {
				return nil, nil, qerr
			}
		}
		return existing, charge, nil
	}

	charge, err := s.pix.CreateCharge(ctx, purchaseID, skuDef.PriceCents, "")
	if err != nil {
		slog.Error("sandbox purchase charge creation failed", "purchase_id", purchaseID, "err", err)
		return nil, nil, problem.InternalServer("falha ao criar cobrança PIX")
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
	release, err := acquireWallet(ctx, s.lock, sandbox.WalletID)
	if err != nil {
		return err
	}
	defer release()

	entry, _, err := s.repo.Credit(ctx, repositories.Mutation{
		WalletID: sandbox.WalletID, Amount: p.CreditsGranted, EntryType: wallet.EntrySandboxCredit,
		Ref: txid, IdempotencyKey: "sandbox_purchase#" + txid, ReqHash: reqHash(txid, p.CreditsGranted),
		Extra: func(entry *wallet.LedgerEntry) []types.TransactWriteItem {
			return []types.TransactWriteItem{s.sandboxPurchases.BuildConfirmTx(txid, charge.E2EID, entry.SK)}
		},
	})
	if err != nil {
		return err
	}
	p.Status = wallet.SandboxPurchaseConfirmed
	p.E2EID = charge.E2EID
	p.CreditSK = entry.SK
	s.dispatchM2MWebhook(ctx, p)
	return nil
}

// RefundSandboxPurchase reverses an unused sandbox purchase per §9.2:
// eligible iff no debit has landed on the sandbox wallet since this
// purchase's credit. Revokes exactly the credits this purchase granted, then
// refunds the PIX payment — never a sandbox→game/real conversion (Invariant
// #6 untouched): this is a debit that zeroes out the entitlement, not a
// conversion — the credits are simply revoked, they never become game or
// real money.
// requestingClient mirrors PurchaseSandboxDirect's own parameter: "" for the
// user-facing route, or the caller's AZP for the M2M route — in which case a
// purchase opened by a DIFFERENT client (or opened directly by the user) is
// reported as not-found rather than leaking whose it is.
func (s *WalletService) RefundSandboxPurchase(ctx context.Context, userID, purchaseID, idemKey, requestingClient string) (*wallet.SandboxPurchase, error) {
	p, err := s.sandboxPurchases.Get(ctx, purchaseID)
	if err != nil {
		return nil, err
	}
	if p == nil || p.UserID != userID || (requestingClient != "" && p.RequestingClient != requestingClient) {
		return nil, problem.NotFound("compra não encontrada")
	}
	if p.Status == wallet.SandboxPurchaseRefunded {
		return p, nil
	}
	if p.Status != wallet.SandboxPurchaseConfirmed && p.Status != wallet.SandboxPurchaseRefundPending {
		return nil, problem.Conflict("compra ainda não confirmada")
	}

	if p.Status == wallet.SandboxPurchaseConfirmed {
		sandbox, err := s.repo.EnsureSandboxWallet(ctx, userID)
		if err != nil {
			return nil, err
		}
		release, err := acquireWallet(ctx, s.lock, sandbox.WalletID)
		if err != nil {
			return nil, err
		}

		// Re-read and validate under the wallet lock. The debit and durable
		// refund_pending claim then commit in the same transaction.
		current, err := s.sandboxPurchases.Get(ctx, purchaseID)
		if err != nil {
			release()
			return nil, err
		}
		if current.Status == wallet.SandboxPurchaseConfirmed {
			if used, err := s.repo.AnyDebitSince(ctx, sandbox.WalletID, current.CreditSK); err != nil {
				release()
				return nil, err
			} else if used {
				release()
				return nil, problem.SandboxPurchaseUsed()
			}
			_, _, err = s.repo.Debit(ctx, repositories.Mutation{
				WalletID: sandbox.WalletID, Amount: current.CreditsGranted, EntryType: wallet.EntrySandboxRefundReversal,
				Ref: purchaseID, IdempotencyKey: "sandbox_refund#" + purchaseID, ReqHash: reqHash(purchaseID, current.CreditsGranted),
			}, s.sandboxPurchases.BuildRefundClaimTx(purchaseID))
			if err != nil {
				release()
				return nil, err
			}
			current.Status = wallet.SandboxPurchaseRefundPending
		} else if current.Status != wallet.SandboxPurchaseRefundPending && current.Status != wallet.SandboxPurchaseRefunded {
			release()
			return nil, problem.Conflict("estado de estorno da compra inconsistente")
		}
		release()
		p = current
	}
	if p.Status == wallet.SandboxPurchaseRefunded {
		return p, nil
	}

	// The provider key is stable. Failure leaves refund_pending durable, so the
	// scheduled reconciler or an API replay resumes without debiting again.
	if _, err := s.pix.Refund(ctx, p.E2EID, p.AmountExpected, "sandbox_refund#"+purchaseID); err != nil {
		slog.Error("ALARM sandbox purchase refund failed; scheduled retry retained", "purchase_id", purchaseID, "e2e_id", p.E2EID, "err", err)
		return nil, problem.InternalServer("estorno da compra falhou; nova tentativa agendada")
	}
	changed, err := s.sandboxPurchases.TransitionStatus(ctx, purchaseID, wallet.SandboxPurchaseRefundPending, wallet.SandboxPurchaseRefunded)
	if err != nil {
		return nil, err
	}
	if !changed {
		current, err := s.sandboxPurchases.Get(ctx, purchaseID)
		if err != nil {
			return nil, err
		}
		if current == nil || current.Status != wallet.SandboxPurchaseRefunded {
			return nil, problem.InternalServer("estorno concluído no provedor aguardando reconciliação local")
		}
		p = current
	}
	p.Status = wallet.SandboxPurchaseRefunded
	s.dispatchM2MWebhook(ctx, p)
	return p, nil
}

// GetSandboxPurchase is the M2M poll endpoint's read path (plan: M2M
// sandbox-purchase integration) — the receiver's mandated way to confirm a
// purchase before crediting anything itself, never the webhook body alone
// (Invariant #11's own posture, mirrored for the M2M integration). Ownership
// is enforced the same way as RefundSandboxPurchase: a purchase this client
// did not open is reported as not-found, never leaked.
func (s *WalletService) GetSandboxPurchase(ctx context.Context, purchaseID, requestingClient string) (*wallet.SandboxPurchase, error) {
	p, err := s.sandboxPurchases.Get(ctx, purchaseID)
	if err != nil {
		return nil, err
	}
	if p == nil || p.RequestingClient != requestingClient {
		return nil, problem.NotFound("compra não encontrada")
	}
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

// SweepRefundPendingSandboxPurchases resumes provider refunds after a process
// crash or transient provider failure. The credit reversal was already committed
// with refund_pending, so retries never run usage validation or debit twice.
func (s *WalletService) SweepRefundPendingSandboxPurchases(ctx context.Context) (swept int, err error) {
	cutoff := time.Now().Add(-sweepAgeThreshold)
	purchases, err := s.sandboxPurchases.ListRefundPendingOlderThan(ctx, cutoff, reconcileBatch)
	if err != nil {
		return 0, err
	}
	for i := range purchases {
		p := purchases[i]
		if _, err := s.RefundSandboxPurchase(ctx, p.UserID, p.PurchaseID, "reconcile#"+p.PurchaseID, p.RequestingClient); err != nil {
			slog.Warn("sweep: sandbox refund failed, will retry next run", "purchase_id", p.PurchaseID, "err", err)
			continue
		}
		swept++
	}
	return swept, nil
}
