package services

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/api-commons/erasure"
	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
	"gopkg.aoctech.app/wallet/api/internal/rail"
)

// ErasureRepo is the persistence side of the purge
// (*repositories.ErasureRepository).
type ErasureRepo interface {
	DepositsForUser(ctx context.Context, userID string) ([]wallet.PixDeposit, error)
	WithdrawalsForUser(ctx context.Context, userID string) ([]wallet.Withdrawal, error)
	OpenHoldsForUser(ctx context.Context, userID string) ([]wallet.Hold, error)
	Retain(ctx context.Context, sub, source, sourceKey string, item any, until time.Time) error
	StripDepositPII(ctx context.Context, txid string) error
	StripWithdrawalPII(ctx context.Context, withdrawalID string) error
	EraseUserProfile(ctx context.Context, userID string) (bool, error)
	AnonymizeAudit(ctx context.Context, userID string) (int, error)
	PutCarriedSelfExclusion(ctx context.Context, cpfHMAC string, ex wallet.SelfExclusion, expiresAt time.Time) error
}

var (
	// DepositPending is deliberately absent: an abandoned QR stays pending
	// forever. paidDepositBlockers decides which pending rows block (R19).
	pendingDepositStatuses    = []string{wallet.DepositRefundPending, wallet.DepositRejectedCPF, wallet.DepositRefundFailed}
	pendingWithdrawalStatuses = []string{wallet.WithdrawProcessing, wallet.WithdrawRefundFail}

	errCPFHMACMissing = errors.New("erasure: ctech-account returned no cpf_hmac for a self-excluded user; retry once it serves the field")
)

const erasurePageSize = 100

// maxDepositChecks bounds the Inter calls one Blockers makes (R19).
const maxDepositChecks = 10

// ErasureService is the wallet's side of the account-deletion saga
// (docs/plans/2026-10-07-account-deletion-participant.md): blockers for the
// eligibility endpoint, and the idempotent, resumable purge.
type ErasureService struct {
	wallets   WalletStore
	users     UserRepo
	sandbox   SandboxPurchaseRepo
	products  ProductPurchaseRepo
	repo      ErasureRepo
	kyc       KYCClient
	walletURL string        // wallet UI home; action_url of a balance blocker
	charges   ChargeQuerier // R19: asks Inter whether an old pending deposit was paid
	now       func() time.Time
}

// ChargeQuerier is the one rail call the blockers make.
type ChargeQuerier interface {
	QueryDeposit(ctx context.Context, txid string) (*rail.Charge, error)
}

// WithCharges enables the paid-but-unconfirmed deposit check (R19).
func (e *ErasureService) WithCharges(c ChargeQuerier) *ErasureService {
	e.charges = c
	return e
}

func NewErasureService(wallets WalletStore, users UserRepo, sandbox SandboxPurchaseRepo, products ProductPurchaseRepo, repo ErasureRepo, kyc KYCClient, walletURL string) *ErasureService {
	return &ErasureService{wallets: wallets, users: users, sandbox: sandbox, products: products, repo: repo,
		kyc: kyc, walletURL: walletURL, now: time.Now}
}

// Blockers lists what stops sub's data from being erased (inventory §4). It
// never creates a wallet: an unknown sub has no blockers.
func (e *ErasureService) Blockers(ctx context.Context, sub string) ([]erasure.Blocker, error) {
	var out []erasure.Blocker
	realW, gameW, _, err := e.wallets.LoadWallets(ctx, sub)
	if err != nil {
		return nil, err
	}
	// sandbox is virtual (Invariant #6): forfeited, never a blocker.
	for _, w := range []*wallet.Wallet{realW, gameW} {
		if w == nil || w.Balance == 0 {
			continue
		}
		b := erasure.Blocker{Code: wallet.BlockerBalanceNonzero, Detail: map[string]any{"wallet": w.Type, "amount_cents": w.Balance}}
		// game → real is always open (Invariant #9) and the withdrawal dialog is
		// on the wallet home: both balances link there (R8).
		if e.walletURL != "" {
			b.ActionURL = e.walletURL
		}
		if w.Type == wallet.TypeReal {
			// R20: the closing withdrawal is subject to the daily limits.
			lim := wallet.EffectiveDailyLimits(w)
			b.Detail["daily_withdraw_cap_cents"] = lim.WithdrawCap
			b.Detail["daily_withdraw_count"] = lim.WithdrawCount
			b.Detail["days_needed"] = withdrawDaysNeeded(w.Balance, lim)
		}
		out = append(out, b)
	}

	holds, err := e.repo.OpenHoldsForUser(ctx, sub)
	if err != nil {
		return nil, err
	}
	if len(holds) > 0 {
		out = append(out, erasure.Blocker{Code: wallet.BlockerHoldOpen, Detail: map[string]any{"count": len(holds)}})
	}

	deps, err := e.repo.DepositsForUser(ctx, sub)
	if err != nil {
		return nil, err
	}
	if n := countIn(deps, func(d wallet.PixDeposit) string { return d.Status }, pendingDepositStatuses); n > 0 {
		out = append(out, erasure.Blocker{Code: wallet.BlockerDepositPending, Detail: map[string]any{"count": n}})
	}
	q, err := e.paidDepositBlockers(ctx, deps)
	if err != nil {
		return nil, err
	}
	if q != nil {
		out = append(out, *q)
	}

	wds, err := e.repo.WithdrawalsForUser(ctx, sub)
	if err != nil {
		return nil, err
	}
	if n := countIn(wds, func(w wallet.Withdrawal) string { return w.Status }, pendingWithdrawalStatuses); n > 0 {
		out = append(out, erasure.Blocker{Code: wallet.BlockerWithdrawalPending, Detail: map[string]any{"count": n}})
	}

	n, err := e.pendingPurchases(ctx, sub)
	if err != nil {
		return nil, err
	}
	if n > 0 {
		out = append(out, erasure.Blocker{Code: wallet.BlockerPurchasePending, Detail: map[string]any{"count": n}})
	}
	return out, nil
}

// withdrawDaysNeeded is how many São Paulo days it takes to empty balance under
// the daily withdrawal limits: ceil(ceil(balance / cap) / count). Guidance for
// the account UI (R20), not a promise.
func withdrawDaysNeeded(balance int64, lim wallet.DailyLimits) int64 {
	if balance <= 0 || lim.WithdrawCap <= 0 || lim.WithdrawCount <= 0 {
		return 0
	}
	withdrawals := (balance + lim.WithdrawCap - 1) / lim.WithdrawCap
	return (withdrawals + lim.WithdrawCount - 1) / lim.WithdrawCount
}

// paidDepositBlockers implements R19: a deposit still `pending` blocks while
// its charge can be paid, or when the provider says it was paid but the credit
// never happened (quarantine). At most maxDepositChecks old rows are asked.
func (e *ErasureService) paidDepositBlockers(ctx context.Context, deps []wallet.PixDeposit) (*erasure.Blocker, error) {
	validUntil := e.now().Add(-wallet.DepositChargeValidity)
	var payable, paid, unchecked int
	for _, d := range deps {
		if d.Status != wallet.DepositPending {
			continue
		}
		created, err := time.Parse(time.RFC3339Nano, d.CreatedAt)
		if err == nil && created.After(validUntil) {
			payable++
			continue
		}
		if e.charges == nil || paid+unchecked >= maxDepositChecks {
			unchecked++
			continue
		}
		ch, err := e.charges.QueryDeposit(ctx, d.Txid)
		if err != nil {
			return nil, err
		}
		if ch.Status == rail.ChargeCompleted {
			paid++
		}
	}
	if payable+paid+unchecked == 0 {
		return nil, nil
	}
	return &erasure.Blocker{Code: wallet.BlockerDepositQuarantined,
		Detail: map[string]any{"payable": payable, "paid_unconfirmed": paid, "unchecked": unchecked}}, nil
}

func countIn[T any](rows []T, status func(T) string, statuses []string) int {
	n := 0
	for _, r := range rows {
		if slices.Contains(statuses, status(r)) {
			n++
		}
	}
	return n
}

// pendingPurchases counts purchases whose money may still move: a charge that
// can still be paid (within its TTL; product rows never leave `pending` when
// unpaid) and a sandbox refund still in flight.
func (e *ErasureService) pendingPurchases(ctx context.Context, sub string) (int, error) {
	payableSince := e.now().Add(-sandboxPurchaseTTLMinutes * time.Minute)
	n := 0
	var start map[string]types.AttributeValue
	for {
		page, err := e.sandbox.ListByUser(ctx, sub, erasurePageSize, start)
		if err != nil {
			return 0, err
		}
		for _, p := range page.Items {
			if p.Status == wallet.SandboxPurchaseRefundPending ||
				(p.Status == wallet.SandboxPurchasePending && createdAfter(p.CreatedAt, payableSince)) {
				n++
			}
		}
		if len(page.LastEvaluatedKey) == 0 {
			break
		}
		start = page.LastEvaluatedKey
	}
	start = nil
	for {
		page, err := e.products.ListByUser(ctx, sub, erasurePageSize, start)
		if err != nil {
			return 0, err
		}
		for _, p := range page.Items {
			if p.Status == wallet.ProductPurchasePending && createdAfter(p.CreatedAt, payableSince) {
				n++
			}
		}
		if len(page.LastEvaluatedKey) == 0 {
			break
		}
		start = page.LastEvaluatedKey
	}
	return n, nil
}

// createdAfter fails closed: an unparseable timestamp counts as recent.
func createdAfter(createdAt string, t time.Time) bool {
	c, err := time.Parse(time.RFC3339Nano, createdAt)
	return err != nil || c.After(t)
}

// Purge is the erasure.PurgeFunc. Idempotent and resumable: every step
// re-reads from DynamoDB, retention is write-once, and the self-exclusion is
// carried BEFORE the profile that holds it is erased. It moves no money and
// never touches the ledger (Invariant #2). m.Organizations is ignored: the
// wallet holds no organization data.
func (e *ErasureService) Purge(ctx context.Context, m erasure.Message) (erasure.Ack, error) {
	blockers, err := e.Blockers(ctx, m.Sub)
	if err != nil {
		return erasure.Ack{}, err
	}
	if len(blockers) > 0 {
		return erasure.Ack{Result: erasure.ResultBlocked, Blockers: blockers}, nil
	}

	now := e.now()
	until := now.AddDate(wallet.ErasureRetentionYears, 0, 0)
	counts := map[string]int{}

	carried, err := e.carrySelfExclusion(ctx, m.Sub, now)
	if err != nil {
		return erasure.Ack{}, err
	}
	if carried {
		counts[wallet.CountSelfExclusionCarried] = 1
	}

	deps, err := e.repo.DepositsForUser(ctx, m.Sub)
	if err != nil {
		return erasure.Ack{}, err
	}
	for _, d := range deps {
		if d.PayerCPF == "" && d.PayerName == "" {
			continue
		}
		if err := e.repo.Retain(ctx, m.Sub, wallet.TablePixDeposits, d.Txid, d, until); err != nil {
			return erasure.Ack{}, err
		}
		if err := e.repo.StripDepositPII(ctx, d.Txid); err != nil {
			return erasure.Ack{}, err
		}
		counts[wallet.CountDepositsRetained]++
	}

	wds, err := e.repo.WithdrawalsForUser(ctx, m.Sub)
	if err != nil {
		return erasure.Ack{}, err
	}
	for _, w := range wds {
		if w.PixKey == "" {
			continue
		}
		if err := e.repo.Retain(ctx, m.Sub, wallet.TableWithdrawals, w.WithdrawalID, w, until); err != nil {
			return erasure.Ack{}, err
		}
		if err := e.repo.StripWithdrawalPII(ctx, w.WithdrawalID); err != nil {
			return erasure.Ack{}, err
		}
		counts[wallet.CountWithdrawalsRetained]++
	}

	erased, err := e.repo.EraseUserProfile(ctx, m.Sub)
	if err != nil {
		return erasure.Ack{}, err
	}
	if erased {
		counts[wallet.CountUserProfileErased] = 1
	}
	n, err := e.repo.AnonymizeAudit(ctx, m.Sub)
	if err != nil {
		return erasure.Ack{}, err
	}
	counts[wallet.CountAuditAnonymized] = n
	return erasure.Ack{Result: erasure.ResultDone, Counts: counts}, nil
}

// carrySelfExclusion keeps an active self-exclusion past the erasure, keyed
// by ctech-account's CPF keyed hash (D13). A user who never gave a CPF has
// nothing to key it on.
func (e *ErasureService) carrySelfExclusion(ctx context.Context, sub string, now time.Time) (bool, error) {
	u, err := e.users.Get(ctx, sub)
	if err != nil {
		return false, err
	}
	var cur *wallet.SelfExclusion
	if u != nil {
		cur = u.SelfExclusion
	}
	ex, expiresAt, ok := wallet.CarriedSelfExclusion(cur, now)
	if !ok {
		return false, nil
	}
	k, err := e.kyc.Get(ctx, sub)
	if err != nil {
		return false, fmt.Errorf("erasure: kyc for self-exclusion carry-over: %w", err)
	}
	if k == nil || k.CPF == "" {
		return false, nil
	}
	if k.CPFHMAC == "" {
		return false, errCPFHMACMissing
	}
	return true, e.repo.PutCarriedSelfExclusion(ctx, k.CPFHMAC, ex, expiresAt)
}
