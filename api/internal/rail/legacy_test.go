package rail

import (
	"context"
	"testing"

	"gopkg.aoctech.app/wallet/api/internal/pix"
)

func TestLegacyOpensThenQueriesTheSameCharge(t *testing.T) {
	f := pix.NewFake()
	r := NewLegacy(f)
	ctx := context.Background()
	opened, err := r.OpenDeposit(ctx, DepositRequest{UserID: "u", TaxID: "123", Txid: "tx1", AmountCents: 1000})
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.QueryDeposit(ctx, "tx1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Txid != opened.Txid || got.Amount != 1000 {
		t.Fatalf("queried %+v, opened %+v", got, opened)
	}
	if len(f.CreatedCharges) != 1 || f.CreatedCharges[0] != "tx1" {
		t.Fatalf("created %v", f.CreatedCharges)
	}
}

func TestLegacyPaysOutAndReportsItByTheKey(t *testing.T) {
	f := pix.NewFake()
	r := NewLegacy(f)
	ctx := context.Background()
	if _, err := r.Payout(ctx, "12345678909", 500, "idem1"); err != nil {
		t.Fatal(err)
	}
	if len(f.Transfers) != 1 || f.Transfers[0] != (pix.RecordedTransfer{PixKey: "12345678909", Amount: 500, IdemKey: "idem1"}) {
		t.Fatalf("transfers %+v", f.Transfers)
	}
	f.StageTransferStatus("idem1", pix.TransferDone)
	res, err := r.QueryPayout(ctx, "idem1")
	if err != nil || res.Status != pix.TransferDone {
		t.Fatalf("query: %+v %v", res, err)
	}
}

func TestLegacyRefundForwardsTheEndToEndIdAmountAndKey(t *testing.T) {
	f := pix.NewFake()
	r := NewLegacy(f)
	if _, err := r.RefundDeposit(context.Background(), "tx1", "E2E1", 250, "refund#tx1"); err != nil {
		t.Fatal(err)
	}
	if len(f.Refunds) != 1 || f.Refunds[0] != (pix.RecordedRefund{E2EID: "E2E1", Amount: 250, IdemKey: "refund#tx1"}) {
		t.Fatalf("refunds %+v", f.Refunds)
	}
}

func TestTheLegacyRailNeverAsksForAnIdentity(t *testing.T) {
	if NeedsIdentity(NewLegacy(pix.NewFake())) {
		t.Fatal("the legacy path must not make a KYC call it did not make before")
	}
}
