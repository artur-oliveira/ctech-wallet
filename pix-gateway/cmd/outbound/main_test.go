package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/artur-oliveira/ctech-wallet/pix-gateway/internal/inter"
	"github.com/artur-oliveira/ctech-wallet/pix-gateway/internal/rpc"
)

// fakePix is a minimal stand-in — pix-gateway has no dependency on api's fake,
// it defines its own (different module, and this one only needs to exercise
// the handler's marshal/unmarshal, not real business behavior).
type fakePix struct {
	dictErr error
}

func (f *fakePix) CreateCharge(_ context.Context, txid string, amount int64, _ string) (*inter.Charge, error) {
	return &inter.Charge{Txid: txid, Amount: amount, Status: inter.ChargeActive, QRCode: "EMV"}, nil
}
func (f *fakePix) QueryCharge(_ context.Context, txid string) (*inter.Charge, error) {
	return &inter.Charge{Txid: txid, Status: inter.ChargeCompleted, Amount: 500, PayerCPF: "111"}, nil
}
func (f *fakePix) DictLookup(_ context.Context, key string) (*inter.DictAccount, error) {
	if f.dictErr != nil {
		return nil, f.dictErr
	}
	return &inter.DictAccount{Key: key, CPF: "222", Name: "Fulano"}, nil
}
func (f *fakePix) Transfer(_ context.Context, key string, amount int64, idem string) (*inter.TransferResult, error) {
	return &inter.TransferResult{E2EID: "E2E-" + idem, Status: inter.TransferDone}, nil
}
func (f *fakePix) QueryTransfer(_ context.Context, idem string) (*inter.TransferResult, error) {
	return &inter.TransferResult{Status: inter.TransferNotFound}, nil
}
func (f *fakePix) Refund(_ context.Context, e2e string, amount int64, idem string) (*inter.TransferResult, error) {
	return &inter.TransferResult{E2EID: e2e, Status: "DEVOLVIDO"}, nil
}
func (f *fakePix) Ping(_ context.Context) error { return nil }

func TestHandleCreateCharge(t *testing.T) {
	h := &handler{pix: &fakePix{}}
	payload, _ := json.Marshal(rpc.CreateChargeArgs{Txid: "tx1", Amount: 12345})
	resp := h.handle(context.Background(), rpc.Request{Op: rpc.OpCreateCharge, Payload: payload})
	if resp.Error != "" {
		t.Fatalf("unexpected error: %s", resp.Error)
	}
	var got rpc.ChargeResult
	if err := json.Unmarshal(resp.Payload, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Txid != "tx1" || got.Amount != 12345 || got.Status != inter.ChargeActive {
		t.Fatalf("bad result: %+v", got)
	}
}

func TestHandleDictLookupNotFound(t *testing.T) {
	h := &handler{pix: &fakePix{dictErr: inter.ErrKeyNotFound}}
	payload, _ := json.Marshal(rpc.DictLookupArgs{PixKey: "some-key"})
	resp := h.handle(context.Background(), rpc.Request{Op: rpc.OpDictLookup, Payload: payload})
	if resp.Error != rpc.ErrKeyNotFoundSentinel {
		t.Fatalf("expected sentinel %q, got %q", rpc.ErrKeyNotFoundSentinel, resp.Error)
	}
}

func TestHandleUnknownOp(t *testing.T) {
	h := &handler{pix: &fakePix{}}
	resp := h.handle(context.Background(), rpc.Request{Op: "Bogus"})
	if resp.Error == "" {
		t.Fatal("expected an error for an unknown op")
	}
}

func TestHandlePing(t *testing.T) {
	h := &handler{pix: &fakePix{}}
	resp := h.handle(context.Background(), rpc.Request{Op: rpc.OpPing})
	if resp.Error != "" {
		t.Fatalf("unexpected error: %s", resp.Error)
	}
}

var _ = errors.New // silences unused import if a future edit removes an error path
