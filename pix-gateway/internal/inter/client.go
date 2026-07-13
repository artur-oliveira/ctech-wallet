// Package inter abstracts the PIX partner bank (Inter). pix-gateway talks to the
// bank only through the PixClient interface.
package inter

import (
	"context"
	"errors"
)

// ErrKeyNotFound means the DICT lookup found no owner for the PIX key — the user
// mistyped it, or it isn't registered. It is a CLIENT error and must never be
// reported as a 500; anything else from DictLookup is a bank/transport failure.
var ErrKeyNotFound = errors.New("pix: dict key not found")

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

// Charge is an immediate PIX charge (cobrança imediata).
type Charge struct {
	Txid      string
	Amount    int64  // centavos
	QRCode    string // copia-e-cola (EMV payload)
	QRCodeB64 string // base64 PNG (optional)
	Status    string // one of the Charge* constants
	PayerCPF  string // set only once paid
	E2EID     string // end-to-end id of the received payment
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

// PixClient is the partner-bank contract. The real implementation talks to
// Inter over mTLS.
type PixClient interface {
	// CreateCharge opens an immediate charge for the given txid and amount.
	CreateCharge(ctx context.Context, txid string, amount int64, payerHintCPF string) (*Charge, error)
	// QueryCharge re-reads a charge by txid — the source of truth for a deposit,
	// never the webhook payload.
	QueryCharge(ctx context.Context, txid string) (*Charge, error)
	// DictLookup resolves the owner of a destination PIX key.
	DictLookup(ctx context.Context, pixKey string) (*DictAccount, error)
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
