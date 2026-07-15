// Package problem implements RFC 7807 Problem Details, matching the shape used
// by ctech-account and ctech-dfe so all services in the platform emit
// consistent error bodies.
package problem

import (
	"encoding/json"
	"net/http"

	"github.com/gofiber/fiber/v3"
)

const ContentType = "application/problem+json"

// Problem type URIs (RFC 7807 "type" member). Defined as constants so they are
// never scattered as raw string literals across the codebase.
const (
	TypeBadRequest          = "/problems/bad-request"
	TypeUnauthorized        = "/problems/unauthorized"
	TypeForbidden           = "/problems/forbidden"
	TypeNotFound            = "/problems/not-found"
	TypeConflict            = "/problems/conflict"
	TypeUnprocessableEntity = "/problems/unprocessable-entity"
	TypeValidation          = "/problems/validation-error"
	TypeTooManyRequests     = "/problems/too-many-requests"
	TypeInternalServer      = "/problems/internal-server-error"

	// wallet-specific codes (see docs/specs/2026-07-10-wallet-design.md §F)
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
	TypeGamblingTermsRequired = "/problems/gambling-terms-required"
)

// FieldError is a single field-level validation failure. It mirrors the shape
// the frontend Zod layer produces so the UI can map each error back to its input.
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
	Tag     string `json:"tag,omitempty"`
}

// Problem is the RFC 7807 response body. Errors carries field-level validation
// failures (only for validation problems). MaxAgeSeconds carries the step-up
// freshness window on step-up-required problems (mirrors ctech-account).
// MinAmount/MaxAmount carry the accepted range on deposit-out-of-range problems
// so the UI can state the bounds without hardcoding them.
type Problem struct {
	Type          string       `json:"type"`
	Title         string       `json:"title"`
	Status        int          `json:"status"`
	Detail        string       `json:"detail,omitempty"`
	Errors        []FieldError `json:"errors,omitempty"`
	MaxAgeSeconds int          `json:"max_age_seconds,omitempty"`
	MinAmount     int64        `json:"min_amount,omitempty"`
	MaxAmount     int64        `json:"max_amount,omitempty"`
}

// Error implements the error interface so problems can be returned as errors.
func (p *Problem) Error() string {
	if p.Detail != "" {
		return p.Detail
	}
	return p.Title
}

func (p *Problem) Send(c fiber.Ctx) error {
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	c.Status(p.Status)
	c.Set(fiber.HeaderContentType, ContentType)
	return c.Send(b)
}

func New(status int, typ, title, detail string) *Problem {
	return &Problem{Type: typ, Title: title, Status: status, Detail: detail}
}

func BadRequest(detail string) *Problem {
	return New(http.StatusBadRequest, TypeBadRequest, "Bad Request", detail)
}

func Unauthorized(detail string) *Problem {
	return New(http.StatusUnauthorized, TypeUnauthorized, "Unauthorized", detail)
}

func Forbidden(detail string) *Problem {
	return New(http.StatusForbidden, TypeForbidden, "Forbidden", detail)
}

func NotFound(detail string) *Problem {
	return New(http.StatusNotFound, TypeNotFound, "Not Found", detail)
}

// Validation returns a 422 problem carrying field-level validation failures.
func Validation(errs []FieldError) *Problem {
	p := New(http.StatusUnprocessableEntity, TypeValidation, "Validation Error",
		"the request body failed validation")
	p.Errors = errs
	return p
}

func Conflict(detail string) *Problem {
	return New(http.StatusConflict, TypeConflict, "Conflict", detail)
}

func UnprocessableEntity(detail string) *Problem {
	return New(http.StatusUnprocessableEntity, TypeUnprocessableEntity, "Unprocessable Entity", detail)
}

func TooManyRequests(detail string) *Problem {
	return New(http.StatusTooManyRequests, TypeTooManyRequests, "Too Many Requests", detail)
}

func InternalServer(detail string) *Problem {
	return New(http.StatusInternalServerError, TypeInternalServer, "Internal Server Error", detail)
}

func FromFiber(err *fiber.Error) *Problem {
	switch err.Code {
	case http.StatusNotFound:
		return NotFound(err.Error())
	default:
		return InternalServer(err.Error())
	}
}

// --- wallet-specific constructors ---

func InsufficientBalance() *Problem {
	return New(http.StatusConflict, TypeInsufficientBalance, "Insufficient Balance", "saldo insuficiente para a operação")
}

func WalletBusy() *Problem {
	return New(http.StatusConflict, TypeWalletBusy, "Wallet Busy", "já existe uma operação em andamento nesta carteira")
}

func WithdrawCPFMismatch() *Problem {
	return New(http.StatusForbidden, TypeWithdrawCPFMismatch, "Withdraw CPF Mismatch", "a chave PIX de destino pertence a outro CPF")
}

func KYCNotVerified() *Problem {
	return New(http.StatusForbidden, TypeKYCNotVerified, "KYC Not Verified", "verificação de identidade necessária para esta operação")
}

func IdempotencyConflict() *Problem {
	return New(http.StatusConflict, TypeIdempotencyConflict, "Idempotency Conflict", "mesma Idempotency-Key usada com payload diferente")
}

// PixKeyNotFound is a CLIENT error: the destination PIX key is not registered
// (usually a typo). Never report this as a 500, and never leak the bank's
// response body into the detail.
func PixKeyNotFound() *Problem {
	return New(http.StatusUnprocessableEntity, TypePixKeyNotFound, "PIX Key Not Found", "chave PIX não encontrada; confira e tente de novo")
}

// GamblingNotActivated: the caller has no game/sandbox wallet because they never
// opted in. Returned by every route inside the gambling ring-fence.
func GamblingNotActivated() *Problem {
	return New(http.StatusConflict, TypeGamblingNotActivated, "Gambling Not Activated",
		"ative a carteira de jogos antes de usar esta operação")
}

// GamblingTermsRequired: the caller has not accepted the CURRENT gambling
// addendum. A re-gated user may still return money from game to real — only
// funding and play are blocked, so a terms bump never traps money.
func GamblingTermsRequired() *Problem {
	return New(http.StatusForbidden, TypeGamblingTermsRequired, "Gambling Terms Required",
		"aceite o termo de jogo responsável para continuar")
}

// DepositOutOfRange is a CLIENT error: the requested deposit amount falls
// outside the wallet's accepted range. It carries the bounds so the UI can show
// them instead of duplicating the limits.
func DepositOutOfRange(minAmt, maxAmt int64) *Problem {
	p := New(http.StatusUnprocessableEntity, TypeDepositOutOfRange, "Deposit Out Of Range",
		"valor de depósito fora do limite permitido para esta carteira")
	p.MinAmount = minAmt
	p.MaxAmount = maxAmt
	return p
}

// AmountAboveLimit is a CLIENT error: the requested amount exceeds the
// absolute ceiling on an inbound operation (deposit / real→game fund).
// It carries MaxAmount so the UI can show the bound without hardcoding it.
func AmountAboveLimit(maxAmt int64) *Problem {
	p := New(http.StatusUnprocessableEntity, TypeAmountAboveLimit, "Amount Above Limit",
		"valor acima do limite máximo por operação")
	p.MaxAmount = maxAmt
	return p
}

// StepUpRequired mirrors ctech-account: a 403 carrying the max-age hint the
// client needs to know how fresh the MFA proof must be.
func StepUpRequired(maxAgeSeconds int) *Problem {
	p := New(http.StatusForbidden, TypeStepUpRequired, "Step-Up Required", "esta operação exige autenticação MFA recente")
	p.MaxAgeSeconds = maxAgeSeconds
	return p
}
