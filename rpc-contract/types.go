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
	// Asaas credentials travel only in Request.OAuthToken. They MUST NOT be
	// duplicated into Payload. Payloads can contain financial data and are never
	// operationally logged.
	OpAsaasCreateAccount        Op = "AsaasCreateAccount"
	OpAsaasUploadDocument       Op = "AsaasUploadDocument"
	OpAsaasCreateStaticPixKey   Op = "AsaasCreateStaticPixKey"
	OpAsaasCreatePixQRCode      Op = "AsaasCreatePixQRCode"
	OpAsaasQueryPayment         Op = "AsaasQueryPayment"
	OpAsaasQueryCustomer        Op = "AsaasQueryCustomer"
	OpAsaasRefundPayment        Op = "AsaasRefundPayment"
	OpAsaasCreateTransfer       Op = "AsaasCreateTransfer"
	OpAsaasQueryTransfer        Op = "AsaasQueryTransfer"
	OpAsaasQueryAccountBalance  Op = "AsaasQueryAccountBalance"
	OpAsaasQueryAccountStatus   Op = "AsaasQueryAccountStatus"
	OpAsaasListPendingDocuments Op = "AsaasListPendingDocuments"
)

// ErrKeyNotFoundSentinel is the Response.Error value that means
// inter.ErrKeyNotFound — the one PixClient error callers must distinguish from
// a generic bank/transport failure.
const ErrKeyNotFoundSentinel = "key_not_found"

// ErrUnauthorizedSentinel is the Response.Error value that means Inter rejected
// the passed bearer (HTTP 401). api force-refreshes and retries once.
const ErrUnauthorizedSentinel = "unauthorized"

// ErrTransferNotFoundSentinel means a provider query succeeded and proved no
// transfer exists for the supplied external reference. It is deliberately
// distinct from a query/transport error: only this result permits resubmission.
const ErrTransferNotFoundSentinel = "transfer_not_found"

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
	DocumentID string `json:"document_id"`
	File       []byte `json:"file"`
}

type AsaasCreateStaticPixKeyArgs struct{}

type AsaasPixAddressKeyResult struct {
	Key    string `json:"key"`
	Status string `json:"status"`
}

type AsaasCreatePixQRCodeArgs struct {
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
	PaymentID string `json:"payment_id"`
}

type AsaasPaymentResult struct {
	ID                string `json:"id"`
	Value             int64  `json:"value"`
	Status            string `json:"status"`
	ExternalReference string `json:"external_reference"`
	CustomerID        string `json:"customer_id"`
}

type AsaasQueryCustomerArgs struct {
	CustomerID string `json:"customer_id"`
}

// AsaasRefundPaymentArgs refunds the PIX payment that originally received the
// money. Amount stays in centavos across the RPC boundary; pix-gateway
// converts it to Asaas's decimal-reais API format.
type AsaasRefundPaymentArgs struct {
	PaymentID   string `json:"payment_id"`
	Amount      int64  `json:"amount"`
	Description string `json:"description"`
}

type AsaasCustomerResult struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	CPFCNPJ string `json:"cpf_cnpj"`
}

type AsaasCreateTransferArgs struct {
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
	ExternalReference string `json:"external_reference"`
}

type AsaasQueryAccountBalanceArgs struct{}

type AsaasBalanceResult struct {
	Balance int64 `json:"balance"`
}

// AsaasQueryAccountStatusArgs reads the registration status of whichever
// account the supplied API key belongs to (GET /v3/myAccount/status). No
// arguments: the credential IS the selector.
type AsaasQueryAccountStatusArgs struct{}

// Registration status values, shared by every field of AsaasAccountStatusResult.
const (
	AsaasRegistrationPending          = "PENDING"
	AsaasRegistrationApproved         = "APPROVED"
	AsaasRegistrationRejected         = "REJECTED"
	AsaasRegistrationAwaitingApproval = "AWAITING_APPROVAL"
)

// AsaasAccountStatusResult is the authoritative answer to "is this subaccount
// usable yet". The account is fully approved only when General is APPROVED —
// the other three fields exist to tell the user WHICH step is outstanding, and
// must never be combined into an approval decision of their own.
type AsaasAccountStatusResult struct {
	ID              string `json:"id"`
	CommercialInfo  string `json:"commercial_info"`
	BankAccountInfo string `json:"bank_account_info"`
	Documentation   string `json:"documentation"`
	General         string `json:"general"`
}

// AsaasListPendingDocumentsArgs lists the documents the provider is still
// waiting on for the account the API key belongs to
// (GET /v3/myAccount/documents).
type AsaasListPendingDocumentsArgs struct{}

// AsaasPendingDocument is one outstanding document requirement.
//
// OnboardingURL decides how it must be sent, and the choice is not ours: a
// document that carries one can ONLY be sent through that provider-hosted
// flow, and an API upload of it is rejected. One without it is uploadable by
// ID.
type AsaasPendingDocument struct {
	ID            string `json:"id"`
	Type          string `json:"type"`
	Status        string `json:"status"`
	OnboardingURL string `json:"onboarding_url,omitempty"`
}

type AsaasPendingDocumentsResult struct {
	Documents []AsaasPendingDocument `json:"documents"`
}
