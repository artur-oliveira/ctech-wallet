package rail

import (
	"context"
	"fmt"
	"sync"
)

// Fake is a programmable in-memory Rail for tests. Stage
// charges, force transfer/refund errors, and inspect recorded calls.
type Fake struct {
	mu sync.Mutex

	Charges        map[string]*Charge         // by txid
	TransferStatus map[string]*TransferResult // by idemKey — reconciliation lookups

	TransferErr error
	RefundErr   error

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

// NewFake returns an initialized Fake.
func NewFake() *Fake {
	return &Fake{
		Charges:        make(map[string]*Charge),
		TransferStatus: make(map[string]*TransferResult),
	}
}

func (f *Fake) CreateCharge(_ context.Context, txid string, amount int64, _ string) (*Charge, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := &Charge{Txid: txid, Amount: amount, QRCode: "000201-fake-" + txid, Status: ChargeActive}
	f.Charges[txid] = c
	f.CreatedCharges = append(f.CreatedCharges, txid)
	return c, nil
}

func (f *Fake) QueryCharge(_ context.Context, txid string) (*Charge, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Queried = append(f.Queried, txid)
	c, ok := f.Charges[txid]
	if !ok {
		return nil, fmt.Errorf("charge %s not found", txid)
	}
	return c, nil
}

func (f *Fake) Transfer(_ context.Context, pixKey string, amount int64, idemKey string) (*TransferResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Transfers = append(f.Transfers, RecordedTransfer{PixKey: pixKey, Amount: amount, IdemKey: idemKey})
	if f.TransferErr != nil {
		return nil, f.TransferErr
	}
	return &TransferResult{E2EID: "E2E-" + idemKey, Status: "EFETIVADO"}, nil
}

func (f *Fake) Refund(_ context.Context, e2eID string, amount int64, idemKey string) (*TransferResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Refunds = append(f.Refunds, RecordedRefund{E2EID: e2eID, Amount: amount, IdemKey: idemKey})
	if f.RefundErr != nil {
		return nil, f.RefundErr
	}
	return &TransferResult{E2EID: e2eID, Status: "DEVOLVIDO"}, nil
}

func (f *Fake) QueryTransfer(_ context.Context, idemKey string) (*TransferResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r, ok := f.TransferStatus[idemKey]; ok {
		return r, nil
	}
	return &TransferResult{Status: TransferNotFound}, nil
}

// StageTransferStatus stages the reconciliation view of a payout — for tests.
func (f *Fake) StageTransferStatus(idemKey, status string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.TransferStatus[idemKey] = &TransferResult{E2EID: "E2E-" + idemKey, Status: status}
}

// StageCharge marks a charge as paid with a payer CPF — for deposit-confirm tests.
func (f *Fake) StageCharge(txid string, amount int64, status, payerCPF, e2eID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Charges[txid] = &Charge{
		Txid: txid, Amount: amount, Status: status, PayerCPF: payerCPF, E2EID: e2eID,
		Payments: []Payment{{E2EID: e2eID, Amount: amount, PayerCPF: payerCPF}},
	}
}

// StageChargeRefund appends a devolução to an already-staged charge — for
// deposit-refund-reversal tests.
func (f *Fake) StageChargeRefund(txid, rtrID string, amount int64, status string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if c, ok := f.Charges[txid]; ok {
		c.Refunds = append(c.Refunds, Refund{RtrID: rtrID, Amount: amount, Status: status})
	}
}

// StageChargePayment appends an extra PIX payment to an already-staged charge —
// for excess-payment-refund tests (the same QR code paid by two people).
func (f *Fake) StageChargePayment(txid, e2eID string, amount int64, payerCPF string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if c, ok := f.Charges[txid]; ok {
		c.Payments = append(c.Payments, Payment{E2EID: e2eID, Amount: amount, PayerCPF: payerCPF})
	}
}

// The Rail surface, over the charge/transfer helpers above.
func (f *Fake) OpenDeposit(ctx context.Context, in DepositRequest) (*Charge, error) {
	return f.CreateCharge(ctx, in.Txid, in.AmountCents, "")
}
func (f *Fake) QueryDeposit(ctx context.Context, txid string) (*Charge, error) {
	return f.QueryCharge(ctx, txid)
}
func (f *Fake) RefundDeposit(ctx context.Context, _, e2eID string, amountCents int64, idemKey string) (*TransferResult, error) {
	return f.Refund(ctx, e2eID, amountCents, idemKey)
}
func (f *Fake) Payout(ctx context.Context, cpf string, amountCents int64, idemKey string) (*TransferResult, error) {
	return f.Transfer(ctx, cpf, amountCents, idemKey)
}
func (f *Fake) QueryPayout(ctx context.Context, idemKey string) (*TransferResult, error) {
	return f.QueryTransfer(ctx, idemKey)
}

var _ Rail = (*Fake)(nil)
