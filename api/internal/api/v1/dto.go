package v1

// Request bodies. Amounts are integer centavos; validation rejects non-positive.

const MaxIdempotencyKeyLength = 128

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

// SandboxPurchaseDirectRequest opens a direct PIX→sandbox-credits sale (plan
// §9.1/§9.3) — a fixed server-side SKU, never a client-supplied amount.
type SandboxPurchaseDirectRequest struct {
	SKU string `json:"sku" validate:"required,max=64"`
}

// M2MSandboxPurchaseRequest opens a direct PIX→sandbox-credits sale on a
// user's behalf (M2M, scope internal:wallet:sandbox-purchase) — same fixed
// server-side SKU catalog as the user-facing route, just caller-initiated
// (e.g. ctech-poker). The idempotency key travels in the body, not the
// Idempotency-Key header, matching every other M2M request body in this file.
type M2MSandboxPurchaseRequest struct {
	UserID         string `json:"user_id" validate:"required,max=128"`
	SKU            string `json:"sku" validate:"required,max=64"`
	IdempotencyKey string `json:"idempotency_key" validate:"required,max=128"`
	// Description is optional free-form display text for the purchase row, on
	// the same terms as MovementOpRequest.Description: never price authority.
	Description string `json:"description" validate:"max=255"`
}

// M2MRefundSandboxPurchaseRequest mirrors M2MSandboxPurchaseRequest's shape
// for the refund leg — the purchase id travels in the route path.
type M2MRefundSandboxPurchaseRequest struct {
	UserID         string `json:"user_id" validate:"required,max=128"`
	IdempotencyKey string `json:"idempotency_key" validate:"required,max=128"`
}

// M2MOpenChargeRequest opens a PIX charge for an amount the caller supplies
// (M2M, scope internal:wallet:charge-amount).
//
// It is the one request body in this file that names its own price, which is
// why it is a separate type under a separate scope rather than an optional
// field on M2MSandboxPurchaseRequest: a nullable amount on the shared body
// would be one forgotten scope check away from letting a catalogue client set
// it. The ceiling is server-side (services.M2MClient.MaxCharge) — `gt=0` here
// only rejects the nonsensical, never the excessive.
type M2MOpenChargeRequest struct {
	UserID      string `json:"user_id" validate:"required,max=128"`
	AmountCents int64  `json:"amount_cents" validate:"required,gt=0"`
	// Reference is the caller's own label for what is being paid — an invoice id,
	// for ctech-billing. Opaque to wallet.
	Reference      string `json:"reference" validate:"required,max=128"`
	IdempotencyKey string `json:"idempotency_key" validate:"required,max=128"`
	// PayerTaxID is an optional CPF the rail uses to match the payer. Not stored
	// for that purpose and not required for the charge to open.
	PayerTaxID string `json:"payer_tax_id" validate:"omitempty,max=14"`
	// Description is optional free-form display text for the charge row, on the
	// same terms as MovementOpRequest.Description: never price authority.
	Description string `json:"description" validate:"max=255"`
}

// ConfirmPurchaseRequest is pix-gateway's webhook-Lambda call for either
// direct-sale rail (sandbox credits or a generic product). It mirrors
// ConfirmDepositRequest's txid but has no payer identity because these sales
// have no KYC/CPF gate.
type ConfirmPurchaseRequest struct {
	Txid string `json:"txid" validate:"required"`
}

// GameTransferRequest is the body for both ring-fence edges (real → game and
// game → real). The idempotency key travels in the Idempotency-Key header.
type GameTransferRequest struct {
	Amount int64 `json:"amount" validate:"required,gt=0"`
}

// OnboardingRequest opens the caller's custody onboarding. IncomeValue is a
// provider cadastral field; it is persisted only because the subaccount is
// created later, once the verification fee clears — see
// BaasService.RequestCustodyAccount.
type OnboardingRequest struct {
	IncomeValue int64 `json:"income_value" validate:"required,gt=0"`
}

// OnboardingResponse tells the client exactly one next step. Which field is
// populated depends on Status, and the client renders that rather than
// re-deriving the step from the status string.
type OnboardingResponse struct {
	Status string `json:"status"`
	// Fee is present while the verification fee is outstanding.
	Fee *OnboardingFee `json:"fee,omitempty"`
	// OnboardingURL is present when the provider wants documents sent through
	// its own hosted flow. It is the only way those documents may be sent.
	OnboardingURL string `json:"onboarding_url,omitempty"`
	// PendingDocuments names what the provider is waiting on. Populated even
	// when OnboardingURL is absent — the provider does return requirements with
	// no link, and "under review" with nothing to read is a dead end.
	PendingDocuments []string `json:"pending_documents,omitempty"`
}

// OnboardingFee is the PIX charge for the one-off verification fee. It is a
// purchase, not a deposit: nothing is credited to any wallet, and it is not
// refunded if the provider later refuses the registration.
type OnboardingFee struct {
	Amount     int64  `json:"amount"`
	QRCode     string `json:"qr_code"`
	QRCodeB64  string `json:"qr_code_base64,omitempty"`
	Refundable bool   `json:"refundable"`
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

// SelfExcludeRequest picks the self-exclusion period.
type SelfExcludeRequest struct {
	Period string `json:"period" validate:"required,oneof=30d 90d indefinite"`
}

// GameLimitsRequest carries the three personal deposit limits (centavos).
type GameLimitsRequest struct {
	DailyLimit   int64 `json:"daily_limit" validate:"required,gt=0"`
	WeeklyLimit  int64 `json:"weekly_limit" validate:"required,gt=0"`
	MonthlyLimit int64 `json:"monthly_limit" validate:"required,gt=0"`
}

// MovementOpRequest is the M2M body for internal credit/debit. The
// idempotency key travels in the body (e.g. wallet_id#round_id), not a header.
type MovementOpRequest struct {
	UserID         string `json:"user_id" validate:"required,max=128"`
	Amount         int64  `json:"amount" validate:"required,gt=0"`
	IdempotencyKey string `json:"idempotency_key" validate:"required,max=128"`
	Reason         string `json:"reason" validate:"max=256"`
	// Description is optional free-form text the calling service wants the user
	// to read on their statement ("Recompensa diária"). Stored verbatim on the
	// ledger entry, never parsed, never authority for the amount. Reason stays
	// the machine-readable key (`ref`); this is the human sentence.
	Description string `json:"description" validate:"max=255"`
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
	UserID         string `json:"user_id" validate:"required,max=128"`
	Amount         int64  `json:"amount" validate:"required,gt=0"`
	TableRef       string `json:"table_ref" validate:"required,max=256"`
	IdempotencyKey string `json:"idempotency_key" validate:"required,max=128"`
}

// ReleaseRequest refunds a `held` hold in full (M2M, scope
// internal:wallet:game-hold). The hold id travels in the route path. UserID is
// required so the service can verify the hold actually belongs to the named user
// before releasing it (SEC-07) — a hold id alone is not proof of ownership.
type ReleaseRequest struct {
	UserID         string `json:"user_id" validate:"required,max=128"`
	IdempotencyKey string `json:"idempotency_key" validate:"required,max=128"`
}

// CashoutRequest credits the caller's final stack (M2M, scope
// internal:wallet:game-cashout). The service requires every hold to be held,
// owned by this user and table, and the amount not to exceed their total.
type CashoutRequest struct {
	UserID         string   `json:"user_id" validate:"required,max=128"`
	Amount         int64    `json:"amount" validate:"required,gt=0"`
	TableRef       string   `json:"table_ref" validate:"required,max=256"`
	HoldIDs        []string `json:"hold_ids" validate:"required,min=1,max=20,dive,max=256"`
	IdempotencyKey string   `json:"idempotency_key" validate:"required,max=128"`
}
