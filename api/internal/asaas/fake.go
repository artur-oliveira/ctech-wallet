package asaas

import (
	"context"
	"fmt"
	"sync"
)

// FakeAsaasClient is a programmable in-memory AsaasClient for tests. Mirrors
// pix.FakePixClient's shape: stage accounts/QR codes/transfers, force errors,
// inspect recorded calls.
type FakeAsaasClient struct {
	mu sync.Mutex

	Accounts  map[string]*Account  // by CPF — CreateAccount is keyed by the request's CPF for lookup in tests
	Payments  map[string]*Payment  // by payment ID
	Transfers map[string]*Transfer // by ExternalReference
	Balances  map[string]int64     // by apiKey — staged for QueryAccountBalance

	nextAccountID  int
	nextTransferID int

	CreateAccountErr       error
	CreateTransferErr      error
	QueryPaymentErr        error
	QueryAccountBalanceErr error

	// Recorded calls for assertions.
	CreatedAccounts  []CreateAccountRequest
	CreatedTransfers []TransferRequest
	QueriedTransfers []string
}

// NewFake returns an initialized FakeAsaasClient.
func NewFake() *FakeAsaasClient {
	return &FakeAsaasClient{
		Accounts:  make(map[string]*Account),
		Payments:  make(map[string]*Payment),
		Transfers: make(map[string]*Transfer),
		Balances:  make(map[string]int64),
	}
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
func (f *FakeAsaasClient) StageBalance(apiKey string, balance int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Balances[apiKey] = balance
}

func (f *FakeAsaasClient) CreateAccount(_ context.Context, req CreateAccountRequest) (*Account, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.CreatedAccounts = append(f.CreatedAccounts, req)
	if f.CreateAccountErr != nil {
		return nil, f.CreateAccountErr
	}
	f.nextAccountID++
	acc := &Account{
		ID:       fmt.Sprintf("acc_fake_%d", f.nextAccountID),
		WalletID: fmt.Sprintf("wallet_fake_%d", f.nextAccountID),
		APIKey:   fmt.Sprintf("apikey_fake_%d", f.nextAccountID),
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
	if t, ok := f.Transfers[externalReference]; ok {
		return t, nil
	}
	return nil, fmt.Errorf("asaas: transfer %s not found", externalReference)
}

// StagePayment marks a payment as received — for deposit-confirm tests.
func (f *FakeAsaasClient) StagePayment(paymentID, pixQRCodeID string, amount int64, status, externalRef string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Payments[paymentID] = &Payment{
		ID: paymentID, PixQRCodeID: pixQRCodeID, Value: amount, Status: status, ExternalReference: externalRef,
	}
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
