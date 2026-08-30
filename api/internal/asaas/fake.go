package asaas

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
)

// FakeAsaasClient is a programmable in-memory AsaasClient for tests. Mirrors
// pix.FakePixClient's shape: stage accounts/QR codes/transfers, force errors,
// inspect recorded calls.
// fakeAccountSerial makes fake provider account ids unique process-wide.
var fakeAccountSerial atomic.Int64

type FakeAsaasClient struct {
	mu sync.Mutex

	Accounts  map[string]*Account  // by CPF — CreateAccount is keyed by the request's CPF for lookup in tests
	Payments  map[string]*Payment  // by payment ID
	Customers map[string]*Customer // by customer ID
	Transfers map[string]*Transfer // by ExternalReference
	Balances  map[string]int64     // by apiKey — staged for QueryAccountBalance

	nextAccountID  int
	nextTransferID int

	CreateAccountErr       error
	CreateTransferErr      error
	QueryPaymentErr        error
	QueryCustomerErr       error
	QueryTransferErr       error
	QueryAccountBalanceErr error
	QueryAccountStatusErr  error

	// Staged registration status / pending documents, by apiKey. An unstaged
	// key answers PENDING with no documents — never approved by omission.
	AccountStatuses  map[string]*AccountStatus
	PendingDocuments map[string][]PendingDocument

	// Recorded calls for assertions.
	CreatedAccounts  []CreateAccountRequest
	CreatedTransfers []TransferRequest
	QueriedTransfers []string
	RefundedPayments []RefundPaymentRequest
}

type RefundPaymentRequest struct {
	PaymentID   string
	Amount      int64
	Description string
}

// NewFake returns an initialized FakeAsaasClient.
func NewFake() *FakeAsaasClient {
	return &FakeAsaasClient{
		Accounts:  make(map[string]*Account),
		Payments:  make(map[string]*Payment),
		Customers: make(map[string]*Customer),
		Transfers: make(map[string]*Transfer),
		Balances:  make(map[string]int64),
	}
}

func (f *FakeAsaasClient) QueryCustomer(_ context.Context, _ string, customerID string) (*Customer, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.QueryCustomerErr != nil {
		return nil, f.QueryCustomerErr
	}
	c, ok := f.Customers[customerID]
	if !ok {
		return nil, fmt.Errorf("asaas: customer %s not found", customerID)
	}
	return c, nil
}

func (f *FakeAsaasClient) QueryAccountBalance(_ context.Context, apiKey string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.QueryAccountBalanceErr != nil {
		return 0, f.QueryAccountBalanceErr
	}
	return f.Balances[apiKey], nil
}

// StageBalance sets a subaccount's fake Asaas balance, keyed by apiKey — for
// conservation-check tests.
// StageAccountStatus sets what QueryAccountStatus answers for apiKey.
func (f *FakeAsaasClient) StageAccountStatus(apiKey string, st *AccountStatus) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.AccountStatuses == nil {
		f.AccountStatuses = map[string]*AccountStatus{}
	}
	f.AccountStatuses[apiKey] = st
}

// StagePendingDocuments sets what ListPendingDocuments answers for apiKey.
func (f *FakeAsaasClient) StagePendingDocuments(apiKey string, docs []PendingDocument) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.PendingDocuments == nil {
		f.PendingDocuments = map[string][]PendingDocument{}
	}
	f.PendingDocuments[apiKey] = docs
}

func (f *FakeAsaasClient) QueryAccountStatus(_ context.Context, apiKey string) (*AccountStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.QueryAccountStatusErr != nil {
		return nil, f.QueryAccountStatusErr
	}
	if st, ok := f.AccountStatuses[apiKey]; ok {
		return st, nil
	}
	// Unstaged is PENDING, never approved: a fake that approves by default
	// would let a test pass that production would refuse.
	return &AccountStatus{
		CommercialInfo: RegistrationPending, BankAccountInfo: RegistrationPending,
		Documentation: RegistrationPending, General: RegistrationPending,
	}, nil
}

func (f *FakeAsaasClient) ListPendingDocuments(_ context.Context, apiKey string) ([]PendingDocument, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.PendingDocuments[apiKey], nil
}

func (f *FakeAsaasClient) StageBalance(apiKey string, balance int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Balances[apiKey] = balance
}

func (f *FakeAsaasClient) CreateAccount(_ context.Context, _ string, req CreateAccountRequest) (*Account, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.CreatedAccounts = append(f.CreatedAccounts, req)
	if f.CreateAccountErr != nil {
		return nil, f.CreateAccountErr
	}
	// Unique across fake INSTANCES, not just within one. Integration tests build
	// a fake per harness but share one database, and provider account ids are
	// looked up through a global index — a per-instance counter makes two users
	// collide on "acc_fake_1" and the index then resolves one user's webhook to
	// the other's account.
	f.nextAccountID++
	serial := fmt.Sprintf("%d_%d", fakeAccountSerial.Add(1), f.nextAccountID)
	acc := &Account{
		ID:       "acc_fake_" + serial,
		WalletID: "wallet_fake_" + serial,
		APIKey:   "apikey_fake_" + serial,
		Status:   AccountStatusPending,
	}
	f.Accounts[req.CPF] = acc
	return acc, nil
}

func (f *FakeAsaasClient) UploadDocument(_ context.Context, _ /* subaccountAPIKey */, _ /* documentID */ string, _ []byte) error {
	return nil
}

func (f *FakeAsaasClient) CreateStaticPixKey(_ context.Context, subaccountAPIKey string) (*PixAddressKey, error) {
	return &PixAddressKey{Key: "evp-fake-" + subaccountAPIKey, Status: "ACTIVE"}, nil
}

func (f *FakeAsaasClient) CreatePixQRCode(_ context.Context, _ string, req QRCodeRequest) (*QRCode, error) {
	return &QRCode{
		PixQRCodeID:    "qr_fake_" + req.ExternalReference,
		Payload:        "000201-fake-" + req.ExternalReference,
		EncodedImage:   "",
		ExpirationDate: "",
	}, nil
}

func (f *FakeAsaasClient) QueryPayment(_ context.Context, _ string, paymentID string) (*Payment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.QueryPaymentErr != nil {
		return nil, f.QueryPaymentErr
	}
	p, ok := f.Payments[paymentID]
	if !ok {
		return nil, fmt.Errorf("asaas: payment %s not found", paymentID)
	}
	return p, nil
}

func (f *FakeAsaasClient) RefundPayment(_ context.Context, _ string, paymentID string, amount int64, description string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.Payments[paymentID]
	if !ok {
		return fmt.Errorf("asaas: payment %s not found", paymentID)
	}
	f.RefundedPayments = append(f.RefundedPayments, RefundPaymentRequest{PaymentID: paymentID, Amount: amount, Description: description})
	p.Status = PaymentRefunded
	return nil
}

func (f *FakeAsaasClient) CreateTransfer(_ context.Context, _ string, req TransferRequest) (*Transfer, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.CreatedTransfers = append(f.CreatedTransfers, req)
	if f.CreateTransferErr != nil {
		return nil, f.CreateTransferErr
	}
	f.nextTransferID++
	t := &Transfer{
		ID:                fmt.Sprintf("transfer_fake_%d", f.nextTransferID),
		Status:            TransferDone,
		ExternalReference: req.ExternalReference,
	}
	f.Transfers[req.ExternalReference] = t
	return t, nil
}

// QueryTransfer looks up by ExternalReference (matching how CreateTransfer
// stores into f.Transfers, and how Asaas's real GET
// /v3/transfers?externalReference=... query works — plan §6) rather than by
// Asaas's own opaque transfer ID.
func (f *FakeAsaasClient) QueryTransfer(_ context.Context, _ string, externalReference string) (*Transfer, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.QueriedTransfers = append(f.QueriedTransfers, externalReference)
	if f.QueryTransferErr != nil {
		return nil, f.QueryTransferErr
	}
	if t, ok := f.Transfers[externalReference]; ok {
		return t, nil
	}
	return nil, ErrTransferNotFound
}

// StagePayment marks a payment as received — for deposit-confirm tests.
func (f *FakeAsaasClient) StagePayment(paymentID, pixQRCodeID string, amount int64, status, externalRef string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Payments[paymentID] = &Payment{
		ID: paymentID, PixQRCodeID: pixQRCodeID, Value: amount, Status: status, ExternalReference: externalRef,
	}
}

// StagePayer attaches a payer to a staged payment, the way a real receipt does:
// the deposit's CPF check reads it from the linked customer, never from the
// webhook body.
func (f *FakeAsaasClient) StagePayer(paymentID, customerID, cpfCNPJ, name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if p, ok := f.Payments[paymentID]; ok {
		p.CustomerID = customerID
	}
	f.Customers[customerID] = &Customer{ID: customerID, Name: name, CPFCNPJ: cpfCNPJ}
}

// StageTransferStatus overrides a transfer's status by ExternalReference —
// for reconcile/authorization-webhook tests that need a pending/cancelled leg.
func (f *FakeAsaasClient) StageTransferStatus(externalReference, status string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.Transfers[externalReference]
	if !ok {
		t = &Transfer{ID: fmt.Sprintf("transfer_fake_staged_%s", externalReference), ExternalReference: externalReference}
		f.Transfers[externalReference] = t
	}
	t.Status = status
}

var _ AsaasClient = (*FakeAsaasClient)(nil)
