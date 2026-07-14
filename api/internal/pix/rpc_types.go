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
	opDictLookup    rpcOp = "DictLookup"
	opTransfer      rpcOp = "Transfer"
	opQueryTransfer rpcOp = "QueryTransfer"
	opRefund        rpcOp = "Refund"
	opPing          rpcOp = "Ping"
	opGetToken      rpcOp = "GetToken"
)

const errKeyNotFoundSentinel = "key_not_found"

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
	Txid      string `json:"txid"`
	Amount    int64  `json:"amount"`
	QRCode    string `json:"qr_code"`
	QRCodeB64 string `json:"qr_code_b64"`
	Status    string `json:"status"`
	PayerCPF  string `json:"payer_cpf"`
	E2EID     string `json:"e2e_id"`
}

type rpcDictLookupArgs struct {
	PixKey string `json:"pix_key"`
}

type rpcDictResult struct {
	Key  string `json:"key"`
	CPF  string `json:"cpf"`
	Name string `json:"name"`
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
