package rail

import (
	"context"
	"errors"
	"testing"
	"time"

	"gopkg.aoctech.app/wallet/api/internal/billingclient"
	"gopkg.aoctech.app/wallet/api/internal/pix"
)

type fakeBilling struct {
	invoice     *billingclient.Invoice
	invoiceErr  error
	customerID  string
	customerErr error
	created     []string // references
	opened      int
	refunds     []refundCall
	refundErr   error
	payout      *billingclient.Payout
	payoutErr   error
	payoutCalls []string
	lookupErr   error
}

type refundCall struct {
	invoiceID string
	amount    int64
	idem      string
}

func (f *fakeBilling) EnsureCustomer(_ context.Context, _, _, _, _ string) (string, error) {
	return f.customerID, f.customerErr
}

func (f *fakeBilling) CreateInvoice(_ context.Context, _, reference, _ string, amount int64) (*billingclient.Invoice, error) {
	f.created = append(f.created, reference)
	if f.invoiceErr != nil {
		return nil, f.invoiceErr
	}
	inv := *f.invoice
	inv.AmountCents = amount
	return &inv, nil
}

func (f *fakeBilling) InvoiceByReference(_ context.Context, _ string) (*billingclient.Invoice, error) {
	if f.invoiceErr != nil {
		return nil, f.invoiceErr
	}
	inv := *f.invoice
	return &inv, nil
}

func (f *fakeBilling) OpenPix(_ context.Context, inv *billingclient.Invoice) error {
	f.opened++
	inv.PixCopiaECola = "000201-pix"
	return nil
}

func (f *fakeBilling) RefundInvoice(_ context.Context, id string, amount int64, _, idem string) error {
	f.refunds = append(f.refunds, refundCall{id, amount, idem})
	return f.refundErr
}

func (f *fakeBilling) CreatePayout(_ context.Context, _ string, _ int64, reference, _ string) (*billingclient.Payout, error) {
	f.payoutCalls = append(f.payoutCalls, reference)
	return f.payout, f.payoutErr
}

func (f *fakeBilling) PayoutByReference(_ context.Context, _ string) (*billingclient.Payout, error) {
	if f.lookupErr != nil {
		return nil, f.lookupErr
	}
	return f.payout, nil
}

func paid(match string, payer *billingclient.Payer, paidCents int64) *billingclient.Invoice {
	return &billingclient.Invoice{ID: "in_1", Status: billingclient.StatusPaid, AmountCents: 5000, PaidCents: paidCents, Txid: "dep-1", Payer: payer, PayerMatch: match}
}

func TestOpenDepositMakesTheCustomerThenTheInvoiceByTheDepositIdAndReturnsTheQR(t *testing.T) {
	f := &fakeBilling{customerID: "cus_1", invoice: &billingclient.Invoice{ID: "in_1", Status: billingclient.StatusOpen, Txid: "dep-1"}}
	c, err := NewBilling(f).OpenDeposit(context.Background(), DepositRequest{UserID: "u", TaxID: "39053344705", Name: "N", Email: "e", Txid: "dep-1", AmountCents: 5000})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.created) != 1 || f.created[0] != "dep-1" {
		t.Fatalf("invoice references %v", f.created)
	}
	if c.Txid != "dep-1" || c.Amount != 5000 || c.QRCode != "000201-pix" || c.Status != pix.ChargeActive {
		t.Fatalf("charge %+v", c)
	}
}

func TestOpenDepositWithoutACPFOpensNothingAtBilling(t *testing.T) {
	f := &fakeBilling{customerID: "cus_1", invoice: &billingclient.Invoice{}}
	if _, err := NewBilling(f).OpenDeposit(context.Background(), DepositRequest{UserID: "u", Txid: "dep-1", AmountCents: 5000}); err == nil {
		t.Fatal("a user with no CPF on record cannot deposit through Billing")
	}
	if len(f.created) != 0 {
		t.Fatal("no invoice for a user we cannot match the payer of")
	}
}

func TestAPaidInvoiceWhosePayerMatchesIsACompletedChargeCarryingThePayersDocument(t *testing.T) {
	f := &fakeBilling{invoice: paid("match", &billingclient.Payer{Name: "F", Document: "39053344705"}, 5000)}
	c, err := NewBilling(f).QueryDeposit(context.Background(), "dep-1")
	if err != nil {
		t.Fatal(err)
	}
	if c.Status != pix.ChargeCompleted || c.Amount != 5000 || len(c.Payments) != 1 || c.Payments[0].PayerCPF != "39053344705" || c.PayerCPF != "39053344705" || c.E2EID == "" {
		t.Fatalf("charge %+v", c)
	}
}

func TestAMismatchedPayerCarriesAPayerNoCheckCanAccept(t *testing.T) {
	f := &fakeBilling{invoice: paid("mismatch", &billingclient.Payer{Name: "X", Document: "11144477735"}, 5000)}
	c, _ := NewBilling(f).QueryDeposit(context.Background(), "dep-1")
	if c.Status != pix.ChargeCompleted || c.PayerCPF != MismatchPayer || c.Payments[0].PayerCPF != MismatchPayer {
		t.Fatalf("charge %+v", c)
	}
}

func TestAPaidInvoiceWithNoPayerYetHasNoPayerIdentitySoItStaysPending(t *testing.T) {
	for _, inv := range []*billingclient.Invoice{
		paid("", nil, 5000),
		paid("unknown", &billingclient.Payer{Document: "***1"}, 5000),
		paid("match", nil, 5000), // a verdict with no payer behind it is not evidence
	} {
		c, _ := NewBilling(&fakeBilling{invoice: inv}).QueryDeposit(context.Background(), "dep-1")
		if c.Status != pix.ChargeCompleted || c.PayerCPF != "" || c.Payments[0].PayerCPF != "" {
			t.Fatalf("%+v: charge %+v", inv, c)
		}
	}
}

func TestAnUnderpaidInvoiceReportsWhatWasPaidSoTheAmountCheckRefundsIt(t *testing.T) {
	f := &fakeBilling{invoice: paid("match", &billingclient.Payer{Document: "39053344705"}, 4000)}
	c, _ := NewBilling(f).QueryDeposit(context.Background(), "dep-1")
	if c.Amount != 4000 || c.Payments[0].Amount != 4000 {
		t.Fatalf("charge %+v", c)
	}
}

func TestAVoidInvoiceIsNotACompletedCharge(t *testing.T) {
	f := &fakeBilling{invoice: &billingclient.Invoice{ID: "in_1", Status: "VOID", AmountCents: 5000, Txid: "dep-1"}}
	c, _ := NewBilling(f).QueryDeposit(context.Background(), "dep-1")
	if c.Status == pix.ChargeCompleted {
		t.Fatalf("charge %+v", c)
	}
}

func TestRefundDepositCreditsTheInvoiceFoundByTheDepositWithTheCallersKey(t *testing.T) {
	f := &fakeBilling{invoice: paid("mismatch", nil, 5000)}
	res, err := NewBilling(f).RefundDeposit(context.Background(), "dep-1", "ignored-e2e", 5000, "refund#dep-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(f.refunds) != 1 || f.refunds[0] != (refundCall{"in_1", 5000, "refund#dep-1"}) {
		t.Fatalf("refunds %+v", f.refunds)
	}
	if res.Status != pix.TransferDone {
		t.Fatalf("result %+v", res)
	}
}

func TestARefundThatFailsIsAnError(t *testing.T) {
	f := &fakeBilling{invoice: paid("mismatch", nil, 5000), refundErr: errors.New("bank said no")}
	if _, err := NewBilling(f).RefundDeposit(context.Background(), "dep-1", "", 5000, "refund#dep-1"); err == nil {
		t.Fatal("the deposit flow must see the failure and keep the refund pending")
	}
}

func TestPayoutIsAcceptedAsProcessingAndNeverAsDoneByTheCall(t *testing.T) {
	f := &fakeBilling{payout: &billingclient.Payout{ID: "po_1", Status: billingclient.PayoutProcessing}}
	res, err := NewBilling(f).Payout(context.Background(), "39053344705", 700, "w-1")
	if err != nil || res.Status != pix.TransferProcessing {
		t.Fatalf("result %+v err %v", res, err)
	}
	if len(f.payoutCalls) != 1 || f.payoutCalls[0] != "w-1" {
		t.Fatalf("calls %v", f.payoutCalls)
	}
}

func TestAPayoutTheBankHasNoKeyForIsErrKeyNotFound(t *testing.T) {
	f := &fakeBilling{payout: &billingclient.Payout{Status: billingclient.PayoutFailed, FailureReason: billingclient.ReasonKeyNotFound}}
	if _, err := NewBilling(f).Payout(context.Background(), "1", 1, "w"); !errors.Is(err, pix.ErrKeyNotFound) {
		t.Fatalf("want ErrKeyNotFound, got %v", err)
	}
}

func TestAPayoutTimeoutIsAnErrorNotAFailure(t *testing.T) {
	f := &fakeBilling{payoutErr: context.DeadlineExceeded}
	if _, err := NewBilling(f).Payout(context.Background(), "1", 1, "w"); err == nil || errors.Is(err, pix.ErrKeyNotFound) {
		t.Fatalf("a timeout leaves the withdrawal processing: %v", err)
	}
}

func TestQueryPayoutStates(t *testing.T) {
	cases := []struct {
		name   string
		f      *fakeBilling
		status string
		err    bool
	}{
		{"done", &fakeBilling{payout: &billingclient.Payout{Status: billingclient.PayoutDone, PaymentID: "E2E1"}}, pix.TransferDone, false},
		{"processing", &fakeBilling{payout: &billingclient.Payout{Status: billingclient.PayoutProcessing}}, pix.TransferProcessing, false},
		{"refused", &fakeBilling{payout: &billingclient.Payout{Status: billingclient.PayoutFailed, FailureReason: "refused"}}, pix.TransferNotFound, false},
		{"never accepted", &fakeBilling{lookupErr: billingclient.ErrNotFound}, pix.TransferNotFound, false},
		{"transport", &fakeBilling{lookupErr: errors.New("connection reset")}, "", true},
	}
	for _, tc := range cases {
		res, err := NewBilling(tc.f).QueryPayout(context.Background(), "w-1")
		if (err != nil) != tc.err {
			t.Fatalf("%s: err %v", tc.name, err)
		}
		if !tc.err && res.Status != tc.status {
			t.Errorf("%s: status %q, want %q", tc.name, res.Status, tc.status)
		}
	}
}

func TestBillingRailAsksForTheIdentityAndAGraceBeforeBelievingUnknown(t *testing.T) {
	r := NewBilling(&fakeBilling{})
	if !NeedsIdentity(r) {
		t.Fatal("Billing needs the customer's identity")
	}
	if PayoutGrace(r) < 5*time.Minute {
		t.Fatalf("grace %v", PayoutGrace(r))
	}
	if PayoutGrace(NewLegacy(pix.NewFake())) != 0 {
		t.Fatal("the Inter path keeps its behaviour: no grace")
	}
}
