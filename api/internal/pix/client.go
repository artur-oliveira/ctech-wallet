// Package pix abstracts the PIX partner bank (Inter). The wallet talks to the
// bank only through the PixClient interface, so the deposit/withdraw flows are
// tested against a fake and never depend on a live bank connection.
package pix

import (
	"context"
	"errors"
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
	// only it is ever credited (Invariant: Amount is the charge's nominal value,
	// never the sum of payments) — everything from Payments[1:] is an excess
	// payment that must be refunded to its own payer, never credited.
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
// webhook body (Invariant 11).
type Refund struct {
	RtrID  string // devolução's own end-to-end id — unique, used as idempotency key
	Amount int64  // centavos
	Status string // one of the Refund* constants
}

// TransferResult is the outcome of a PIX payout or refund.
type TransferResult struct {
	E2EID  string
	Status string
}

// ErrKeyNotFound means Transfer's destination PIX key is not registered at the
// bank — the caller must distinguish this from a generic bank/transport
// failure so the withdrawal is refunded immediately instead of left
// processing for reconciliation. Check with errors.Is.
var ErrKeyNotFound = errors.New("pix: destination key not registered")

// PixClient is the partner-bank contract. The real implementation talks to
// Inter over mTLS; the fake is used in tests.
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
	// Ping reports whether the partner bank is reachable and the credentials are
	// accepted. It performs no money movement and is used by the health check.
	Ping(ctx context.Context) error
}
