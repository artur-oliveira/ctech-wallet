package v1

// Request bodies. Amounts are integer centavos; validation rejects non-positive.

type DepositRequest struct {
	Amount int64 `json:"amount" validate:"required,gt=0"`
}

type WithdrawRequest struct {
	Amount int64  `json:"amount" validate:"required,gt=0"`
	PixKey string `json:"pix_key" validate:"required"`
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
}

// SandboxOpRequest is the M2M body for internal sandbox credit/debit. The
// idempotency key travels in the body (e.g. wallet_id#round_id), not a header.
type SandboxOpRequest struct {
	UserID         string `json:"user_id" validate:"required"`
	Amount         int64  `json:"amount" validate:"required,gt=0"`
	IdempotencyKey string `json:"idempotency_key" validate:"required"`
	Reason         string `json:"reason"`
}

// ConfirmDepositRequest is pix-gateway's webhook-Lambda call after it has
// already re-queried the charge at Inter. Only the txid crosses this
// boundary — api re-derives amount/status/payer CPF itself via
// WalletService.ConfirmDeposit, which re-queries Inter again through
// LambdaPixClient. Neither this call nor the original webhook payload is ever
// trusted for money movement (Financial Safety Invariant 11).
type ConfirmDepositRequest struct {
	Txid string `json:"txid" validate:"required"`
}
