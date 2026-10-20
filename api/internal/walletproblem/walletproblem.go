// Package walletproblem holds the wallet's own RFC 7807 problem types and
// constructors, built on api-commons/problem. Type URIs and bodies are the ones
// clients already branch on; do not rename them.
package walletproblem

import (
	"fmt"
	"net/http"
	"time"

	"gopkg.aoctech.app/api-commons/problem"
)

const (
	// see docs/specs/2026-07-10-wallet-design.md §F
	TypeInsufficientBalance = "/problems/insufficient-balance"
	TypeWalletBusy          = "/problems/wallet-busy"
	TypeWithdrawCPFMismatch = "/problems/withdraw-cpf-mismatch"
	TypeKYCNotVerified      = "/problems/kyc-not-verified"
	TypeIdempotencyConflict = "/problems/idempotency-conflict"
	TypeStepUpRequired      = "/problems/step-up-required"
	TypePixKeyNotFound      = "/problems/pix-key-not-found"
	TypeDepositOutOfRange   = "/problems/deposit-out-of-range"
	TypeAmountAboveLimit    = "/problems/amount-above-limit"

	TypeGamblingNotActivated  = "/problems/gambling-not-activated"
	TypeRouteRetired          = "/problems/route-retired"
	TypeGamblingTermsRequired = "/problems/gambling-terms-required"

	// responsible gambling (docs/specs/2026-07-19-responsible-gambling-design.md)
	TypeSelfExcluded            = "/problems/self-excluded"
	TypeLimitsNotConfigured     = "/problems/limits-not-configured"
	TypeDepositLimitExceeded    = "/problems/deposit-limit-exceeded"
	TypeDailyDepositLimit       = "/problems/daily-deposit-limit"
	TypeDailyWithdrawLimit      = "/problems/daily-withdraw-limit"
	TypeExclusionChangeRejected = "/problems/exclusion-change-rejected"

	TypeSandboxPurchaseUsed = "/problems/sandbox-purchase-used"

	TypeAccountErasurePending = "/problems/account-erasure-pending"
)

func newProblem(status int, typ, title, detail string) *problem.Problem {
	return problem.New(status, typ, title, detail)
}

// Validation is a 422 carrying field-level failures, with the wallet's detail text.
func Validation(errs []problem.FieldError) *problem.Problem {
	p := problem.Validation(errs)
	p.Detail = "the request body failed validation"
	return p
}

func InsufficientBalance() *problem.Problem {
	return newProblem(http.StatusConflict, TypeInsufficientBalance, "Insufficient Balance", "saldo insuficiente para a operação")
}

func WalletBusy() *problem.Problem {
	return newProblem(http.StatusConflict, TypeWalletBusy, "Wallet Busy", "já existe uma operação em andamento nesta carteira")
}

func WithdrawCPFMismatch() *problem.Problem {
	return newProblem(http.StatusForbidden, TypeWithdrawCPFMismatch, "Withdraw CPF Mismatch", "a chave PIX de destino pertence a outro CPF")
}

func KYCNotVerified() *problem.Problem {
	return newProblem(http.StatusForbidden, TypeKYCNotVerified, "KYC Not Verified", "verificação de identidade necessária para esta operação")
}

func IdempotencyConflict() *problem.Problem {
	return newProblem(http.StatusConflict, TypeIdempotencyConflict, "Idempotency Conflict", "mesma Idempotency-Key usada com payload diferente")
}

// PixKeyNotFound is a CLIENT error: the destination PIX key is not registered
// (usually a typo). Never report this as a 500, and never leak the bank's
// response body into the detail.
func PixKeyNotFound() *problem.Problem {
	return newProblem(http.StatusUnprocessableEntity, TypePixKeyNotFound, "PIX Key Not Found", "chave PIX não encontrada; confira e tente de novo")
}

// GamblingNotActivated: the caller has no game/sandbox wallet because they never
// opted in. Returned by every route inside the gambling ring-fence.
func GamblingNotActivated() *problem.Problem {
	return newProblem(http.StatusConflict, TypeGamblingNotActivated, "Gambling Not Activated",
		"ative a carteira de jogos antes de usar esta operação")
}

// RouteRetired: the operation no longer exists because the game wallet was
// merged into the real one (GAME_WALLET_MERGED). 410, so a stale client sees a
// clear end rather than a silent success or a 500.
func RouteRetired() *problem.Problem {
	return newProblem(http.StatusGone, TypeRouteRetired, "Route Retired",
		"esta operação não existe mais: o saldo de jogos foi unificado ao saldo real")
}

// RouteMovedToBilling: the charge, the Pix confirmation and the sale of Sandbox packs and goods
// left the wallet (Billing owns the Inter connection, Poker sells). Same 410 and type as RouteRetired.
func RouteMovedToBilling() *problem.Problem {
	return newProblem(http.StatusGone, TypeRouteRetired, "Route Retired",
		"esta operação foi aposentada: a cobrança e a venda agora passam pelo Billing")
}

// GamblingTermsRequired: the caller has not accepted the CURRENT gambling
// addendum. A re-gated user may still return money from game to real — only
// funding and play are blocked, so a terms bump never traps money.
func GamblingTermsRequired() *problem.Problem {
	return newProblem(http.StatusForbidden, TypeGamblingTermsRequired, "Gambling Terms Required",
		"aceite o termo de jogo responsável para continuar")
}

// DepositOutOfRange is a CLIENT error: the requested deposit amount falls
// outside the wallet's accepted range. It carries the bounds so the UI can show
// them instead of duplicating the limits.
func DepositOutOfRange(minAmt, maxAmt int64) *problem.Problem {
	p := newProblem(http.StatusUnprocessableEntity, TypeDepositOutOfRange, "Deposit Out Of Range",
		"valor de depósito fora do limite permitido para esta carteira")
	p.MinAmount = minAmt
	p.MaxAmount = maxAmt
	return p
}

// AmountAboveLimit is a CLIENT error: the requested amount exceeds the
// absolute ceiling on an inbound operation (deposit / real→game fund).
// It carries MaxAmount so the UI can show the bound without hardcoding it.
func AmountAboveLimit(maxAmt int64) *problem.Problem {
	p := newProblem(http.StatusUnprocessableEntity, TypeAmountAboveLimit, "Amount Above Limit",
		"valor acima do limite máximo por operação")
	p.MaxAmount = maxAmt
	return p
}

// StepUpRequired mirrors ctech-account: a 403 carrying the max-age hint the
// client needs to know how fresh the MFA proof must be.
func StepUpRequired(maxAgeSeconds int) *problem.Problem {
	p := newProblem(http.StatusForbidden, TypeStepUpRequired, "Step-Up Required", "esta operação exige autenticação MFA recente")
	p.MaxAgeSeconds = maxAgeSeconds
	return p
}

// SelfExcluded: the user self-excluded from real-money gambling. until is
// RFC3339, or "" for an indefinite exclusion; the UI shows it via Detail.
func SelfExcluded(until string) *problem.Problem {
	detail := "você se autoexcluiu do jogo por tempo indeterminado"
	if until != "" {
		detail = "você se autoexcluiu do jogo até " + until
	}
	return newProblem(http.StatusConflict, TypeSelfExcluded, "Self-Excluded", detail)
}

// LimitsNotConfigured: gambling is activated but the mandatory deposit limits
// were never set (pre-limits activation) — the user must configure them first.
func LimitsNotConfigured() *problem.Problem {
	return newProblem(http.StatusConflict, TypeLimitsNotConfigured, "Limits Not Configured",
		"defina seus limites de depósito (diário, semanal e mensal) antes de continuar")
}

// DepositLimitExceeded: the deposit would overflow a responsible-gambling
// window. MaxAmount carries the window's limit; Detail says which window and
// when it resets (RFC3339) so the UI need not recompute calendars.
func DepositLimitExceeded(window string, limit, used int64, resetsAt time.Time) *problem.Problem {
	names := map[string]string{"daily": "diário", "weekly": "semanal", "monthly": "mensal"}
	p := newProblem(http.StatusConflict, TypeDepositLimitExceeded, "Deposit Limit Exceeded",
		fmt.Sprintf("limite %s de depósito atingido; renova em %s", names[window], resetsAt.Format(time.RFC3339)))
	p.MaxAmount = limit
	return p
}

// DailyDepositLimit: the deposit would overflow the wallet's daily PIX deposit
// cap. MaxAmount carries the cap; Detail says when the BRT day resets.
func DailyDepositLimit(limit, used int64, resetsAt time.Time) *problem.Problem {
	p := newProblem(http.StatusConflict, TypeDailyDepositLimit, "Daily Deposit Limit",
		fmt.Sprintf("limite diário de depósito atingido; renova em %s", resetsAt.Format(time.RFC3339)))
	p.MaxAmount = limit
	return p
}

// dailyWithdrawCountKind mirrors wallet.DailyBreachWithdrawCount; this package
// cannot import the wallet domain (import cycle).
const dailyWithdrawCountKind = "withdraw_count"

// DailyWithdrawLimit: the withdrawal would exceed the wallet's daily count or
// amount cap. kind is wallet.DailyBreachWithdrawCount or DailyBreachWithdrawCap.
func DailyWithdrawLimit(kind string, limit, used int64, resetsAt time.Time) *problem.Problem {
	what := "valor"
	if kind == dailyWithdrawCountKind {
		what = "quantidade de saques"
	}
	p := newProblem(http.StatusConflict, TypeDailyWithdrawLimit, "Daily Withdraw Limit",
		fmt.Sprintf("limite diário de %s atingido; renova em %s", what, resetsAt.Format(time.RFC3339)))
	p.MaxAmount = limit
	return p
}

// ExclusionChangeRejected: revoke too early, or a shortening re-exclusion.
func ExclusionChangeRejected(detail string) *problem.Problem {
	return newProblem(http.StatusConflict, TypeExclusionChangeRejected, "Exclusion Change Rejected", detail)
}

// SandboxPurchaseUsed: the §9.2/§9.1a eligibility check failed — the sandbox
// wallet has had at least one debit since the purchase's credit landed, so it
// is no longer refundable/reversible.
func SandboxPurchaseUsed() *problem.Problem {
	return newProblem(http.StatusConflict, TypeSandboxPurchaseUsed, "Sandbox Purchase Used",
		"os créditos desta compra já foram utilizados; não é mais possível reverter")
}

// AccountErasurePending refuses a state-changing operation for a user whose
// account deletion is in grace or done (saga protocol §4.2, decision D1).
func AccountErasurePending() *problem.Problem {
	return newProblem(http.StatusConflict, TypeAccountErasurePending, "Account Erasure Pending",
		"a conta está em processo de exclusão; nenhuma operação pode ser feita")
}
