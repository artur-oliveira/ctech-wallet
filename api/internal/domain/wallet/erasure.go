package wallet

import "time"

// Account-deletion (LGPD) participant constants. Contract:
// ctech-account docs/specs/2026-10-06-account-deletion-saga-protocol.md;
// plan: docs/plans/2026-10-07-account-deletion-participant.md.
const (
	// ErasureServiceID is this service's name in the saga (message
	// `services[]`, SNS filter policy, ack `service`).
	ErasureServiceID = "wallet"

	// ErasureRetentionYears is the PLD retention horizon (Lei 9.613 art. 10)
	// counted from the purge, and the horizon of a carried indefinite
	// self-exclusion (D13).
	ErasureRetentionYears = 5

	// ErasureTombstoneTTL is erasure.NewStore's erasedTTL: never shorter than
	// ErasureRetentionYears calendar years (366-day years absorb leap days).
	ErasureTombstoneTTL = ErasureRetentionYears * 366 * 24 * time.Hour

	// ErasureStateTableSegment is appended to TABLE_PREFIX for
	// erasure.NewStore, so its {prefix}_erasure_state becomes the
	// wallet-owned {env}_wallet_erasure_state, never a name a sibling service
	// in the same AWS account could also claim.
	ErasureStateTableSegment = "wallet"

	// TableErasureRetention holds the PII copied out of operational rows at
	// purge. The API role may only PutItem it (IAM); support reads it.
	TableErasureRetention = "wallet_erasure_retention"

	// RetentionSubPrefix keys a retention partition by the erased sub.
	RetentionSubPrefix = "SUB#"

	// SelfExclusionCarryPrefix keys a self-exclusion that outlives its
	// account (D13) in wallet_users, by ctech-account's CPF keyed hash.
	SelfExclusionCarryPrefix = "SELFEXCL#"

	// ScopeAccountErasureAck is requested by the wallet's own M2M client to
	// ack a purge to ctech-account.
	ScopeAccountErasureAck = "internal:account:erasure-ack"
	// PathAccountErasureAck is appended to CTECH_URL.
	PathAccountErasureAck = "/v1.0/internal/erasure/ack"

	// ScopeAccountExportAck acks an uploaded part of the personal-data export
	// (ctech-account docs/specs/2026-10-17-data-export.md); an operator grants it
	// to the same M2M client before account is told "export": true for the wallet.
	ScopeAccountExportAck = "internal:account:export-ack"
	// PathAccountExportAck is appended to CTECH_URL.
	PathAccountExportAck = "/v1.0/internal/export/ack"

	// ActorErasureCarryOver is the audit actor of a carried self-exclusion
	// re-applied at activation.
	ActorErasureCarryOver = "erasure-carry-over"
)

// Blocker codes reported by GET /internal/erasure/eligibility/:sub. Stable:
// the ctech-account UI translates them.
const (
	BlockerBalanceNonzero    = "wallet.balance_nonzero"
	BlockerHoldOpen          = "wallet.hold_open"
	BlockerDepositPending    = "wallet.deposit_pending"
	BlockerWithdrawalPending = "wallet.withdrawal_pending"
	BlockerPurchasePending   = "wallet.purchase_pending"
	// BlockerDepositQuarantined: a deposit still `pending` that is payable or
	// that Inter reports paid without a credit (R19).
	BlockerDepositQuarantined = "wallet.deposit_quarantined"
)

// DepositChargeValidity is how long a deposit's PIX charge can be paid:
// pix-gateway opens it with a 300 s expiry (inter.go chargeExpirySec) plus a
// 5 min margin for clock skew and a late webhook. Keep it in step with that
// constant. Past it, a `pending` deposit is paid only if Inter says so (R19).
const DepositChargeValidity = 10 * time.Minute

// Ack count keys (saga protocol §4.3: per-store counts in the ack).
const (
	CountDepositsRetained     = "deposits_pii_retained"
	CountWithdrawalsRetained  = "withdrawals_pii_retained"
	CountUserProfileErased    = "wallet_users_erased"
	CountAuditAnonymized      = "wallet_audit_anonymized"
	CountSelfExclusionCarried = "self_exclusion_carried"
)

// CarriedSelfExclusion decides whether ex outlives the account (D13) and
// until when. A timed exclusion is kept until its end. An indefinite one, or
// one whose end cannot be parsed (fail closed, like User.SelfExcluded), is
// kept ErasureRetentionYears from now. An expired one carries nothing.
func CarriedSelfExclusion(ex *SelfExclusion, now time.Time) (SelfExclusion, time.Time, bool) {
	if ex == nil {
		return SelfExclusion{}, time.Time{}, false
	}
	if ex.Until == "" {
		return *ex, now.AddDate(ErasureRetentionYears, 0, 0), true
	}
	until, err := time.Parse(time.RFC3339, ex.Until)
	if err != nil {
		return *ex, now.AddDate(ErasureRetentionYears, 0, 0), true
	}
	if !now.Before(until) {
		return SelfExclusion{}, time.Time{}, false
	}
	return *ex, until, true
}
