// Package pix — rpc_types.go mirrors pix-gateway/internal/rpc/types.go
// field-for-field. This is the wire contract with pix-gateway's outbound
// Lambda; pix-gateway is a SEPARATE Go module, so these types cannot be
// imported — they are kept in sync by hand. A field added on one side must be
// added here too.
package pix

import "encoding/json"

type rpcOp string

const (
	opCreateCharge  rpcOp = "CreateCharge"
	opQueryCharge   rpcOp = "QueryCharge"
	opTransfer      rpcOp = "Transfer"
	opQueryTransfer rpcOp = "QueryTransfer"
	opRefund        rpcOp = "Refund"
	opPing          rpcOp = "Ping"
	opGetToken      rpcOp = "GetToken"
)

// errUnauthorizedSentinel means Inter rejected the passed bearer (HTTP 401).
// LambdaPixClient force-refreshes the token and retries the op once.
const errUnauthorizedSentinel = "unauthorized"

// rpcRequest is the Lambda Invoke payload. OAuthToken is injected by
// LambdaPixClient from the InterTokenManager on every call and must never be
// logged.
type rpcRequest struct {
	Op         rpcOp           `json:"op"`
	OAuthToken string          `json:"oauth_token"`
	Payload    json.RawMessage `json:"payload"`
}

// rpcGetTokenResult is the payload of a GetToken response: the bearer and its
// lifetime in seconds, as reported by Inter.
type rpcGetTokenResult struct {
	Token     string `json:"token"`
	ExpiresIn int    `json:"expires_in"`
}

type rpcResponse struct {
	Error   string          `json:"error,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

type rpcCreateChargeArgs struct {
	Txid         string `json:"txid"`
	Amount       int64  `json:"amount"`
	PayerHintCPF string `json:"payer_hint_cpf"`
}

type rpcQueryChargeArgs struct {
	Txid string `json:"txid"`
}

type rpcChargeResult struct {
	Txid      string            `json:"txid"`
	Amount    int64             `json:"amount"`
	QRCode    string            `json:"qr_code"`
	QRCodeB64 string            `json:"qr_code_b64"`
	Status    string            `json:"status"`
	PayerCPF  string            `json:"payer_cpf"`
	E2EID     string            `json:"e2e_id"`
	Refunds   []rpcRefundResult  `json:"refunds,omitempty"`
	Payments  []rpcPaymentResult `json:"payments,omitempty"`
}

// rpcRefundResult mirrors rpc.RefundResult field-for-field.
type rpcRefundResult struct {
	RtrID  string `json:"rtr_id"`
	Amount int64  `json:"amount"`
	Status string `json:"status"`
}

// rpcPaymentResult mirrors rpc.PaymentResult field-for-field.
type rpcPaymentResult struct {
	E2EID    string            `json:"e2e_id"`
	Amount   int64             `json:"amount"`
	PayerCPF string            `json:"payer_cpf"`
	Refunds  []rpcRefundResult `json:"refunds,omitempty"`
}

type rpcTransferArgs struct {
	PixKey  string `json:"pix_key"`
	Amount  int64  `json:"amount"`
	IdemKey string `json:"idem_key"`
}

type rpcQueryTransferArgs struct {
	IdemKey string `json:"idem_key"`
}

type rpcRefundArgs struct {
	E2EID   string `json:"e2e_id"`
	Amount  int64  `json:"amount"`
	IdemKey string `json:"idem_key"`
}

type rpcTransferResult struct {
	E2EID  string `json:"e2e_id"`
	Status string `json:"status"`
}
