// Package asaas abstracts the Asaas BaaS custody provider, mirroring how
// internal/pix abstracts Inter — the wallet talks to Asaas only through the
// AsaasClient interface, so custody flows are tested against a fake and never
// depend on a live connection. See
// docs/plans/2026-07-30-asaas-baas-implementation-plan.md §2.1.
package asaas

import rpccontract "gopkg.aoctech.app/wallet/rpc-contract"

// Account (subaccount) status values, per Asaas's onboarding webhook. Field
// names/values here are best-effort against the design research in the
// implementation plan — confirm against a live Asaas-sandbox response before
// the asaas_sandbox integration tests (plan §3.4) are trusted as ground truth.
const (
	AccountStatusPending          = "ACCOUNT_STATUS_PENDING"
	AccountStatusApproved         = "ACCOUNT_STATUS_APPROVED"
	AccountStatusRejected         = "ACCOUNT_STATUS_REJECTED"
	AccountStatusAwaitingApproval = "ACCOUNT_STATUS_AWAITING_APPROVAL"
)

// Registration status values returned by QueryAccountStatus. Distinct from the
// ACCOUNT_STATUS_* webhook event names above: those are wake-up labels, these
// are the authoritative state.
const (
	RegistrationPending          = rpccontract.AsaasRegistrationPending
	RegistrationApproved         = rpccontract.AsaasRegistrationApproved
	RegistrationRejected         = rpccontract.AsaasRegistrationRejected
	RegistrationAwaitingApproval = rpccontract.AsaasRegistrationAwaitingApproval
)

// AccountStatus is the provider's own answer to "is this subaccount usable".
// Approved means General == RegistrationApproved and nothing else: the three
// component fields exist to name the outstanding step, never to be combined
// into an approval of our own.
type AccountStatus struct {
	ID              string
	CommercialInfo  string
	BankAccountInfo string
	Documentation   string
	General         string
}

// Approved reports full registration approval.
func (s *AccountStatus) Approved() bool {
	return s != nil && s.General == RegistrationApproved
}

// Rejected reports a refused registration. Not terminal: the user re-sends
// documents on the same subaccount, and the verification fee is never charged
// twice (docs/specs/2026-08-30-asaas-only-deposits.md).
func (s *AccountStatus) Rejected() bool {
	return s != nil && (s.General == RegistrationRejected || s.Documentation == RegistrationRejected)
}

// PendingDocument is one document the provider is still waiting on.
//
// OnboardingURL decides the delivery route and the provider enforces it:
// a document that has one can only be sent through that hosted flow, and an
// API upload of it is refused.
type PendingDocument struct {
	ID            string
	Type          string
	Status        string
	OnboardingURL string
}

// Transfer status values. The plan's own §5.2/§6/§9.1a prose treats "DONE" as
// the terminal success status to poll for — kept verbatim rather than guessing
// the full enum.
const (
	TransferDone      = "DONE"
	TransferPending   = "PENDING"
	TransferCancelled = "CANCELLED"
	TransferFailed    = "FAILED"
)

// Payment (PIX charge) status values.
const (
	PaymentPending  = "PENDING"
	PaymentReceived = "RECEIVED" // paid — analogous to pix.ChargeCompleted
	PaymentOverdue  = "OVERDUE"
	PaymentRefunded = "REFUNDED"
)

// PIX key types accepted by CreateTransfer's PixAddressKeyType.
const (
	PixKeyTypeCPF = "CPF"
)

// CreateAccountRequest opens an Asaas subaccount for a user. Fields sourced
// from ctech-account's KYC record (name/cpfCnpj/birthDate/email/phone/address)
// plus IncomeValue, collected by the wallet's own activation step and never
// persisted (plan §3.1 — no documented LGPD retention need).
type CreateAccountRequest struct {
	Name          string
	CPF           string
	Email         string
	MobilePhone   string
	BirthDate     string // YYYY-MM-DD
	Address       string
	AddressNumber string
	Complement    string
	Province      string // bairro
	City          string
	State         string
	PostalCode    string
	IncomeValue   int64 // centavos; not persisted by the caller
}

// Account is the Asaas subaccount created for a user.
type Account struct {
	ID            string // account.id — stored as BaasAccount.ProviderAccountID
	WalletID      string // account.walletId — stored as BaasAccount.ProviderWalletID
	APIKey        string // the subaccount's own API key — encrypted at rest (plan §3.3)
	Status        string
	OnboardingURL string // present when Asaas routes onboarding through a hosted flow
}

// PixAddressKey is the subaccount's static EVP PIX key — created once, ever,
// per subaccount (plan §3.2 step 6).
type PixAddressKey struct {
	Key    string
	Status string
}

// QRCodeRequest opens a dynamic PIX QR code for a deposit.
type QRCodeRequest struct {
	AddressKey             string
	Value                  int64 // centavos
	Format                 string
	ExpirationSeconds      int
	AllowsMultiplePayments bool
	ExternalReference      string // the deposit's txid
}

// QRCode is the created PIX QR code. PixQRCodeID is the value the payment
// webhook reports back (payment.pixQrCodeId) — deposit resolution is
// PixQRCodeID → txid, not the other way round (plan §4.2).
type QRCode struct {
	PixQRCodeID    string
	Payload        string // copia-e-cola
	EncodedImage   string // base64 PNG
	ExpirationDate string
}

// Payment is the result of QueryPayment — the source of truth for a deposit,
// never the webhook payload (Invariant #11). Payer CPF is deliberately absent:
// the design research found Asaas's payment query does not reliably expose the
// actual PIX payer's CPF (only a merchant-side `customer` record we create
// ourselves) — the CPF anti-fraud gate must come from the webhook body, same
// as Inter today (plan §4.3). Confirm this against a live Asaas-sandbox
// response before relying on it either way.
type Payment struct {
	ID                string
	PixQRCodeID       string
	Value             int64
	Status            string
	ExternalReference string
	CustomerID        string
}

// Customer is the Asaas customer record referenced by a confirmed payment.
// Its CPF is retrieved from Asaas after the payment re-query; the webhook's
// customer field is used only as a routing hint and is never credited directly.
type Customer struct {
	ID      string
	Name    string
	CPFCNPJ string
}

// TransferRequest sends money out of a subaccount (or the parent account) —
// either to a PIX key (a withdrawal payout) or to another Asaas wallet (a fee
// sweep, a settlement leg, a sandbox-purchase settlement/reversal leg).
// Exactly one of PixAddressKey or WalletID is set.
type TransferRequest struct {
	Value             int64 // centavos
	PixAddressKey     string
	PixAddressKeyType string // PixKeyType* — set only alongside PixAddressKey
	WalletID          string // destination Asaas wallet — set only for wallet-to-wallet transfers
	ExternalReference string
}

// Transfer is the result of CreateTransfer/QueryTransfer. TransferFee is
// Asaas's own cost for the leg, read back from the response — needed by §5.2's
// two-leg withdrawal math (leg 2's amount is fee − TransferFee). Field name
// not independently verified against a live response; confirm before build
// per plan §5.2.
type Transfer struct {
	ID                string
	Status            string
	TransferFee       int64
	ExternalReference string
}
