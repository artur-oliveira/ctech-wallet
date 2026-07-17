// Package rpc defines the wire contract between api's LambdaPixClient and
// pix-gateway's outbound Lambda. Both sides mirror these types independently
// (separate Go modules — internal/ packages cannot be imported across module
// boundaries), so a field added here must be added in
// api/internal/pix/rpc_types.go too.
package rpc

import "encoding/json"

// Op names the PixClient method being invoked. One Lambda function handles all
// of them so api makes exactly one kind of Invoke call.
type Op string

const (
	OpCreateCharge  Op = "CreateCharge"
	OpQueryCharge   Op = "QueryCharge"
	OpTransfer      Op = "Transfer"
	OpQueryTransfer Op = "QueryTransfer"
	OpRefund        Op = "Refund"
	OpPing          Op = "Ping"
	OpGetToken      Op = "GetToken"
)

// ErrKeyNotFoundSentinel is the Response.Error value that means
// inter.ErrKeyNotFound — the one PixClient error callers must distinguish from
// a generic bank/transport failure.
const ErrKeyNotFoundSentinel = "key_not_found"

// ErrUnauthorizedSentinel is the Response.Error value that means Inter rejected
// the passed bearer (HTTP 401). api force-refreshes and retries once.
const ErrUnauthorizedSentinel = "unauthorized"

// Request is the Lambda Invoke payload. OAuthToken is supplied by api's
// InterTokenManager on every call and must never be logged. Payload is
// re-decoded per Op into the matching *Args struct below.
type Request struct {
	Op         Op              `json:"op"`
	OAuthToken string          `json:"oauth_token"`
	Payload    json.RawMessage `json:"payload"`
}

// GetTokenResult is the payload of a GetToken response: the bearer and its
// lifetime in seconds, as reported by Inter.
type GetTokenResult struct {
	Token     string `json:"token"`
	ExpiresIn int    `json:"expires_in"`
}

// Response is the Lambda Invoke result. Error is empty on success; Payload is
// empty on error. A non-sentinel Error string means a bank/transport failure —
// api surfaces it as problem.InternalServer, matching InterClient's own error
// contract (opaque error, no special handling) today.
type Response struct {
	Error   string          `json:"error,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

type CreateChargeArgs struct {
	Txid         string `json:"txid"`
	Amount       int64  `json:"amount"`
	PayerHintCPF string `json:"payer_hint_cpf"`
}

type QueryChargeArgs struct {
	Txid string `json:"txid"`
}

// ChargeResult mirrors inter.Charge field-for-field.
type ChargeResult struct {
	Txid      string         `json:"txid"`
	Amount    int64          `json:"amount"`
	QRCode    string         `json:"qr_code"`
	QRCodeB64 string         `json:"qr_code_b64"`
	Status    string         `json:"status"`
	PayerCPF  string         `json:"payer_cpf"`
	E2EID     string         `json:"e2e_id"`
	Refunds   []RefundResult  `json:"refunds,omitempty"`
	Payments  []PaymentResult `json:"payments,omitempty"`
}

// RefundResult mirrors inter.Refund field-for-field.
type RefundResult struct {
	RtrID  string `json:"rtr_id"`
	Amount int64  `json:"amount"`
	Status string `json:"status"`
}

// PaymentResult mirrors inter.Payment field-for-field.
type PaymentResult struct {
	E2EID    string         `json:"e2e_id"`
	Amount   int64          `json:"amount"`
	PayerCPF string         `json:"payer_cpf"`
	Refunds  []RefundResult `json:"refunds,omitempty"`
}

type DictLookupArgs struct {
	PixKey string `json:"pix_key"`
}

// DictResult mirrors inter.DictAccount field-for-field.
type DictResult struct {
	Key  string `json:"key"`
	CPF  string `json:"cpf"`
	Name string `json:"name"`
}

type TransferArgs struct {
	PixKey  string `json:"pix_key"`
	Amount  int64  `json:"amount"`
	IdemKey string `json:"idem_key"`
}

type QueryTransferArgs struct {
	IdemKey string `json:"idem_key"`
}

type RefundArgs struct {
	E2EID   string `json:"e2e_id"`
	Amount  int64  `json:"amount"`
	IdemKey string `json:"idem_key"`
}

// TransferResult mirrors inter.TransferResult field-for-field.
type TransferResult struct {
	E2EID  string `json:"e2e_id"`
	Status string `json:"status"`
}
