// Package inter abstracts the PIX partner bank (Inter). pix-gateway talks to the
// bank only through the PixClient interface.
package inter

import (
	"context"
)

// Inter immediate-charge (cob) statuses relevant to deposits.
const (
	ChargeActive    = "ATIVA"
	ChargeCompleted = "CONCLUIDA"
	ChargeRemoved   = "REMOVIDA_PELO_USUARIO_RECEBEDOR"
)

// PIX payout (transfer) statuses used by reconciliation.
const (
	TransferDone     = "EFETIVADO"
	TransferNotFound = "NAO_ENCONTRADO"
)

// RefundCompleted is Inter's terminal status for a devolução (PIX refund) —
// the payer got the money back.
const RefundCompleted = "DEVOLVIDO"

// Charge is an immediate PIX charge (cobrança imediata).
type Charge struct {
	Txid      string
	Amount    int64  // centavos
	QRCode    string // copia-e-cola (EMV payload)
	QRCodeB64 string // base64 PNG (optional)
	Status    string // one of the Charge* constants
	PayerCPF  string // set only once paid; Inter's charge query no longer returns this — always empty in practice, kept for interface compatibility
	E2EID     string // end-to-end id of the received payment
	Refunds   []Refund
	// Payments lists every PIX actually received against this txid — normally
	// one, but a QR code can be scanned and paid by two different people at the
	// same time, landing two. Payments[0] mirrors E2EID/PayerCPF/Refunds above;
	// api credits only it and refunds everything from Payments[1:] as excess.
	Payments []Payment
}

// Payment is one PIX actually received against a charge.
type Payment struct {
	E2EID    string
	Amount   int64 // centavos
	PayerCPF string
	Refunds  []Refund
}

// Refund is a devolução against a received PIX payment, as reported inside a
// charge's pix[].devolucoes[] entries on re-query — never trusted from the
// webhook body (api's Invariant 11).
type Refund struct {
	RtrID  string // devolução's own end-to-end id — unique, used as idempotency key
	Amount int64  // centavos
	Status string // one of the Refund* constants
}

// DictAccount is the owner of a PIX key resolved via DICT.
type DictAccount struct {
	Key  string
	CPF  string // owner CPF (for withdrawal same-owner matching)
	Name string
}

// TransferResult is the outcome of a PIX payout or refund.
type TransferResult struct {
	E2EID  string
	Status string
}

// TokenResult is a freshly issued OAuth bearer.
type TokenResult struct {
	Token     string
	ExpiresIn int // seconds
}

// PixClient is the partner-bank contract. The real implementation talks to
// Inter over mTLS.
type PixClient interface {
	// CreateCharge opens an immediate charge for the given txid and amount.
	CreateCharge(ctx context.Context, txid string, amount int64, payerHintCPF string) (*Charge, error)
	// QueryCharge re-reads a charge by txid — the source of truth for a deposit,
	// never the webhook payload.
	QueryCharge(ctx context.Context, txid string) (*Charge, error)
	// Transfer sends a PIX payout to a key. idemKey deduplicates at the bank.
	Transfer(ctx context.Context, pixKey string, amount int64, idemKey string) (*TransferResult, error)
	// QueryTransfer reports the status of a payout by its idempotency key, so the
	// reconciliation job can tell whether a payout whose call failed actually went
	// through. A not-found result means the payout was never accepted.
	QueryTransfer(ctx context.Context, idemKey string) (*TransferResult, error)
	// Refund issues a devolução for a received payment identified by e2eID.
	Refund(ctx context.Context, e2eID string, amount int64, idemKey string) (*TransferResult, error)
	// GetToken fetches a fresh OAuth bearer using pix-gateway's own client
	// credentials. api invokes this; pix-gateway is the only place that talks to
	// Inter's token endpoint.
	GetToken(ctx context.Context) (TokenResult, error)
	// Ping reports whether the partner bank is reachable. The bearer is supplied
	// per call; Ping only validates it is present (no Inter call).
	Ping(ctx context.Context) error
}
