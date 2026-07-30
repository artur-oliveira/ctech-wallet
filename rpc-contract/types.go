// Package rpccontract defines the wire contract between api's LambdaPixClient
// and pix-gateway's outbound Lambda. Both modules import this package instead
// of hand-mirroring it (see docs/specs/2026-07-18-audit-remediation.md).
package rpccontract

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

	// Asaas BaaS custody ops (ctech-wallet-api plan
	// docs/plans/2026-07-30-asaas-baas-implementation-plan.md §2.2). Each
	// Asaas *Args struct below carries its own APIKey — unlike the Inter ops
	// above, there is no fleet-wide bearer: every call authenticates as a
	// specific subaccount (or the parent account).
	OpAsaasCreateAccount       Op = "AsaasCreateAccount"
	OpAsaasUploadDocument      Op = "AsaasUploadDocument"
	OpAsaasCreateStaticPixKey  Op = "AsaasCreateStaticPixKey"
	OpAsaasCreatePixQRCode     Op = "AsaasCreatePixQRCode"
	OpAsaasQueryPayment        Op = "AsaasQueryPayment"
	OpAsaasCreateTransfer      Op = "AsaasCreateTransfer"
	OpAsaasQueryTransfer       Op = "AsaasQueryTransfer"
	OpAsaasQueryAccountBalance Op = "AsaasQueryAccountBalance"
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
	Txid      string          `json:"txid"`
	Amount    int64           `json:"amount"`
	QRCode    string          `json:"qr_code"`
	QRCodeB64 string          `json:"qr_code_b64"`
	Status    string          `json:"status"`
	PayerCPF  string          `json:"payer_cpf"`
	E2EID     string          `json:"e2e_id"`
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

// --- Asaas BaaS custody wire types. Money fields are int64 centavos, same
// convention as every Inter type above — pix-gateway's asaas client converts
// to/from Asaas's own decimal-reais wire format internally. ---

type AsaasCreateAccountArgs struct {
	Name          string `json:"name"`
	CPF           string `json:"cpf"`
	Email         string `json:"email"`
	MobilePhone   string `json:"mobile_phone"`
	BirthDate     string `json:"birth_date"`
	Address       string `json:"address"`
	AddressNumber string `json:"address_number"`
	Complement    string `json:"complement"`
	Province      string `json:"province"`
	City          string `json:"city"`
	State         string `json:"state"`
	PostalCode    string `json:"postal_code"`
	IncomeValue   int64  `json:"income_value"`
}

type AsaasAccountResult struct {
	ID            string `json:"id"`
	WalletID      string `json:"wallet_id"`
	APIKey        string `json:"api_key"`
	Status        string `json:"status"`
	OnboardingURL string `json:"onboarding_url"`
}

type AsaasUploadDocumentArgs struct {
	APIKey     string `json:"api_key"`
	DocumentID string `json:"document_id"`
	File       []byte `json:"file"`
}

type AsaasCreateStaticPixKeyArgs struct {
	APIKey string `json:"api_key"`
}

type AsaasPixAddressKeyResult struct {
	Key    string `json:"key"`
	Status string `json:"status"`
}

type AsaasCreatePixQRCodeArgs struct {
	APIKey                 string `json:"api_key"`
	AddressKey             string `json:"address_key"`
	Value                  int64  `json:"value"`
	Format                 string `json:"format"`
	ExpirationSeconds      int    `json:"expiration_seconds"`
	AllowsMultiplePayments bool   `json:"allows_multiple_payments"`
	ExternalReference      string `json:"external_reference"`
}

type AsaasQRCodeResult struct {
	PixQRCodeID    string `json:"pix_qr_code_id"`
	Payload        string `json:"payload"`
	EncodedImage   string `json:"encoded_image"`
	ExpirationDate string `json:"expiration_date"`
}

type AsaasQueryPaymentArgs struct {
	APIKey    string `json:"api_key"`
	PaymentID string `json:"payment_id"`
}

type AsaasPaymentResult struct {
	ID                string `json:"id"`
	Value             int64  `json:"value"`
	Status            string `json:"status"`
	ExternalReference string `json:"external_reference"`
}

type AsaasCreateTransferArgs struct {
	APIKey            string `json:"api_key"`
	Value             int64  `json:"value"`
	PixAddressKey     string `json:"pix_address_key"`
	PixAddressKeyType string `json:"pix_address_key_type"`
	WalletID          string `json:"wallet_id"`
	ExternalReference string `json:"external_reference"`
}

type AsaasTransferResult struct {
	ID                string `json:"id"`
	Status            string `json:"status"`
	TransferFee       int64  `json:"transfer_fee"`
	ExternalReference string `json:"external_reference"`
}

type AsaasQueryTransferArgs struct {
	APIKey            string `json:"api_key"`
	ExternalReference string `json:"external_reference"`
}

type AsaasQueryAccountBalanceArgs struct {
	APIKey string `json:"api_key"`
}

type AsaasBalanceResult struct {
	Balance int64 `json:"balance"`
}
