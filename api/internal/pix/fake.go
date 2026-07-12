package pix

import (
	"context"
	"fmt"
	"sync"
)

// FakePixClient is a programmable in-memory PixClient for tests. Stage charges
// and DICT results, force transfer/refund errors, and inspect recorded calls.
type FakePixClient struct {
	mu sync.Mutex

	Charges        map[string]*Charge         // by txid
	Dict           map[string]*DictAccount    // by pix key
	TransferStatus map[string]*TransferResult // by idemKey — reconciliation lookups

	TransferErr error
	RefundErr   error
	PingErr     error

	// Recorded calls for assertions.
	CreatedCharges []string // txids
	Transfers      []RecordedTransfer
	Refunds        []RecordedRefund
	Queried        []string
}

type RecordedTransfer struct {
	PixKey  string
	Amount  int64
	IdemKey string
}

type RecordedRefund struct {
	E2EID   string
	Amount  int64
	IdemKey string
}

// NewFake returns an initialized FakePixClient.
func NewFake() *FakePixClient {
	return &FakePixClient{
		Charges:        make(map[string]*Charge),
		Dict:           make(map[string]*DictAccount),
		TransferStatus: make(map[string]*TransferResult),
	}
}

func (f *FakePixClient) CreateCharge(_ context.Context, txid string, amount int64, _ string) (*Charge, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := &Charge{Txid: txid, Amount: amount, QRCode: "000201-fake-" + txid, Status: ChargeActive}
	f.Charges[txid] = c
	f.CreatedCharges = append(f.CreatedCharges, txid)
	return c, nil
}

func (f *FakePixClient) QueryCharge(_ context.Context, txid string) (*Charge, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Queried = append(f.Queried, txid)
	c, ok := f.Charges[txid]
	if !ok {
		return nil, fmt.Errorf("charge %s not found", txid)
	}
	return c, nil
}

func (f *FakePixClient) DictLookup(_ context.Context, pixKey string) (*DictAccount, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.Dict[pixKey]
	if !ok {
		return nil, ErrKeyNotFound
	}
	return d, nil
}

func (f *FakePixClient) Transfer(_ context.Context, pixKey string, amount int64, idemKey string) (*TransferResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Transfers = append(f.Transfers, RecordedTransfer{PixKey: pixKey, Amount: amount, IdemKey: idemKey})
	if f.TransferErr != nil {
		return nil, f.TransferErr
	}
	return &TransferResult{E2EID: "E2E-" + idemKey, Status: "EFETIVADO"}, nil
}

func (f *FakePixClient) Refund(_ context.Context, e2eID string, amount int64, idemKey string) (*TransferResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Refunds = append(f.Refunds, RecordedRefund{E2EID: e2eID, Amount: amount, IdemKey: idemKey})
	if f.RefundErr != nil {
		return nil, f.RefundErr
	}
	return &TransferResult{E2EID: e2eID, Status: "DEVOLVIDO"}, nil
}

func (f *FakePixClient) QueryTransfer(_ context.Context, idemKey string) (*TransferResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r, ok := f.TransferStatus[idemKey]; ok {
		return r, nil
	}
	return &TransferResult{Status: TransferNotFound}, nil
}

func (f *FakePixClient) Ping(_ context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.PingErr
}

// StageTransferStatus stages the reconciliation view of a payout — for tests.
func (f *FakePixClient) StageTransferStatus(idemKey, status string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.TransferStatus[idemKey] = &TransferResult{E2EID: "E2E-" + idemKey, Status: status}
}

// StageCharge marks a charge as paid with a payer CPF — for deposit-confirm tests.
func (f *FakePixClient) StageCharge(txid string, amount int64, status, payerCPF, e2eID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Charges[txid] = &Charge{Txid: txid, Amount: amount, Status: status, PayerCPF: payerCPF, E2EID: e2eID}
}

// StageDict registers a DICT owner for a key — for withdrawal tests.
func (f *FakePixClient) StageDict(pixKey, cpf, name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Dict[pixKey] = &DictAccount{Key: pixKey, CPF: cpf, Name: name}
}

var _ PixClient = (*FakePixClient)(nil)
