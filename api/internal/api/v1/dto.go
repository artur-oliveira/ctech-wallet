package v1

// Request bodies. Amounts are integer centavos; validation rejects non-positive.

type DepositRequest struct {
	Amount int64 `json:"amount" validate:"required,gt=0"`
}

// WithdrawRequest carries only the amount — the PIX destination is always the
// CPF on the caller's KYC record, never a client-supplied key (see
// WalletService.Withdraw).
type WithdrawRequest struct {
	Amount int64 `json:"amount" validate:"required,gt=0"`
}

type SandboxPurchaseRequest struct {
	Amount int64 `json:"amount" validate:"required,gt=0"`
}

// GameTransferRequest is the body for both ring-fence edges (real → game and
// game → real). The idempotency key travels in the Idempotency-Key header.
type GameTransferRequest struct {
	Amount int64 `json:"amount" validate:"required,gt=0"`
}

// ActivateGamblingRequest carries the explicit consent. AcceptAddendum must be
// true: activation is opt-in, and a defaulted-true field would not be consent.
type ActivateGamblingRequest struct {
	AcceptAddendum bool `json:"accept_addendum" validate:"required"`
	// Mandatory personal deposit limits (centavos) — activation without limits
	// is impossible by design. Ignored (may be zero) only when the caller is
	// already activated with limits configured (idempotent replay).
	DailyLimit   int64 `json:"daily_limit"`
	WeeklyLimit  int64 `json:"weekly_limit"`
	MonthlyLimit int64 `json:"monthly_limit"`
}

// MovementOpRequest is the M2M body for internal credit/debit. The
// idempotency key travels in the body (e.g. wallet_id#round_id), not a header.
type MovementOpRequest struct {
	UserID         string `json:"user_id" validate:"required"`
	Amount         int64  `json:"amount" validate:"required,gt=0"`
	IdempotencyKey string `json:"idempotency_key" validate:"required"`
	Reason         string `json:"reason"`
}

// ConfirmDepositRequest is pix-gateway's webhook-Lambda call. api re-derives
// amount/status/devolução itself via WalletService.ConfirmDeposit, which
// re-queries Inter again through LambdaPixClient — neither this call nor the
// original webhook payload is ever trusted for money movement (Financial
// Safety Invariant 11). PayerCPF/PayerName are the exception: Inter's charge
// re-query no longer returns the payer, so the webhook body is their only
// source — they cross here to be persisted on the deposit and used for the
// CPF-match anti-fraud check only, never for crediting. PayerCPF may be
// partially masked by Inter (e.g. "***137303**") and is absent on a
// devolução-only webhook call for an already-confirmed deposit.
type ConfirmDepositRequest struct {
	Txid      string `json:"txid" validate:"required"`
	PayerCPF  string `json:"payer_cpf"`
	PayerName string `json:"payer_name"`
}

// HoldRequest reserves a player's buy-in against their game wallet (M2M,
// scope internal:wallet:game-hold). TableRef is an opaque caller-supplied
// session identifier (e.g. table_id:seat) — the wallet never interprets it.
type HoldRequest struct {
	UserID         string `json:"user_id" validate:"required"`
	Amount         int64  `json:"amount" validate:"required,gt=0"`
	TableRef       string `json:"table_ref" validate:"required"`
	IdempotencyKey string `json:"idempotency_key" validate:"required"`
}

// ReleaseRequest refunds a `held` hold in full (M2M, scope
// internal:wallet:game-hold). The hold id travels in the route path.
type ReleaseRequest struct {
	IdempotencyKey string `json:"idempotency_key" validate:"required"`
}

// CashoutRequest credits the caller's final stack (M2M, scope
// internal:wallet:game-cashout). Amount is NEVER validated against the sum of
// HoldIDs — see WalletService.CashoutGame.
type CashoutRequest struct {
	UserID         string   `json:"user_id" validate:"required"`
	Amount         int64    `json:"amount" validate:"required,gt=0"`
	TableRef       string   `json:"table_ref" validate:"required"`
	HoldIDs        []string `json:"hold_ids" validate:"required,min=1"`
	IdempotencyKey string   `json:"idempotency_key" validate:"required"`
}
