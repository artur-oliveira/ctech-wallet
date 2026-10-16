package rail

import (
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
	// TransferProcessing: accepted, not settled. Only a rail that pays
	// asynchronously (Billing) answers it; the Inter gateway settles inside the call.
	TransferProcessing = "EM_PROCESSAMENTO"
	// TransferFailed: the rail says the payout did not happen and never will (the
	// bank refused it, or has no Pix key for the CPF). Money can go back at once.
	TransferFailed = "FALHOU"
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
	PayerName string // the payer's name when the rail knows it (Billing); empty on the Inter path
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
	E2EID     string
	Amount    int64 // centavos
	PayerCPF  string
	PayerName string
	Refunds   []Refund
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

// ErrKeyNotFound means a payout's destination PIX key is not registered at the
// bank — the caller must distinguish this from a generic bank/transport
// failure so the withdrawal is refunded immediately instead of left
// processing for reconciliation. Check with errors.Is.
var ErrKeyNotFound = errors.New("pix: destination key not registered")
