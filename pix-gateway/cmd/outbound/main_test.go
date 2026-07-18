package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/aws/aws-lambda-go/lambda"

	"gopkg.aoctech.app/pix-gateway/internal/inter"
	"gopkg.aoctech.app/pix-gateway/internal/rpc"
)

// fakePix is a minimal stand-in — pix-gateway has no dependency on api's fake,
// it defines its own (different module, and this one only needs to exercise
// the handler's marshal/unmarshal, not real business behavior).
type fakePix struct {
	dictErr     error
	transferErr error
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
	if f.transferErr != nil {
		return nil, f.transferErr
	}
	return &inter.TransferResult{E2EID: "E2E-" + idem, Status: inter.TransferDone}, nil
}
func (f *fakePix) QueryTransfer(_ context.Context, idem string) (*inter.TransferResult, error) {
	return &inter.TransferResult{Status: inter.TransferNotFound}, nil
}
func (f *fakePix) Refund(_ context.Context, e2e string, amount int64, idem string) (*inter.TransferResult, error) {
	return &inter.TransferResult{E2EID: e2e, Status: "DEVOLVIDO"}, nil
}
func (f *fakePix) GetToken(_ context.Context) (inter.TokenResult, error) {
	return inter.TokenResult{Token: "FAKE", ExpiresIn: 3600}, nil
}
func (f *fakePix) Ping(_ context.Context) error { return nil }

func TestHandleCreateCharge(t *testing.T) {
	h := &handler{pix: &fakePix{}}
	payload, _ := json.Marshal(rpc.CreateChargeArgs{Txid: "tx1", Amount: 12345})
	resp, _ := h.handle(context.Background(), rpc.Request{Op: rpc.OpCreateCharge, Payload: payload})
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

// TestHandleTransferKeyNotFound proves the outbound Lambda surfaces
// rpc.ErrKeyNotFoundSentinel (not a raw error string) when Transfer fails
// because the destination PIX key is unregistered — api relies on this exact
// sentinel to refund the withdrawal immediately instead of leaving it
// processing for reconciliation.
func TestHandleTransferKeyNotFound(t *testing.T) {
	h := &handler{pix: &fakePix{transferErr: inter.ErrKeyNotFound}}
	payload, _ := json.Marshal(rpc.TransferArgs{PixKey: "unknown", Amount: 1000, IdemKey: "idem1"})
	resp, _ := h.handle(context.Background(), rpc.Request{Op: rpc.OpTransfer, Payload: payload})
	if resp.Error != rpc.ErrKeyNotFoundSentinel {
		t.Fatalf("expected error %q, got %q", rpc.ErrKeyNotFoundSentinel, resp.Error)
	}
}

func TestHandleUnknownOp(t *testing.T) {
	h := &handler{pix: &fakePix{}}
	resp, _ := h.handle(context.Background(), rpc.Request{Op: "Bogus"})
	if resp.Error == "" {
		t.Fatal("expected an error for an unknown op")
	}
}

func TestHandlePing(t *testing.T) {
	h := &handler{pix: &fakePix{}}
	// Bearer validation lives in InterClient.Ping (covered in inter_test.go);
	// here we only confirm the handler dispatches Ping and forwards the bearer.
	resp, _ := h.handle(context.Background(), rpc.Request{Op: rpc.OpPing, OAuthToken: "x"})
	if resp.Error != "" {
		t.Fatalf("unexpected error: %s", resp.Error)
	}
}

func TestHandleGetToken(t *testing.T) {
	h := &handler{pix: &fakePix{}}
	resp, _ := h.handle(context.Background(), rpc.Request{Op: rpc.OpGetToken})
	if resp.Error != "" {
		t.Fatalf("unexpected error: %s", resp.Error)
	}
	var got rpc.GetTokenResult
	if err := json.Unmarshal(resp.Payload, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Token != "FAKE" || got.ExpiresIn != 3600 {
		t.Fatalf("bad token result: %+v", got)
	}
}

var _ = errors.New // silences unused import if a future edit removes an error path

// TestHandleValidLambdaHandler guards against regressing handle's
// (rpc.Response, error) return signature. aws-lambda-go rejects a handler that
// returns a single non-error value, but that failure only surfaces at runtime
// (lambda.Start → NewHandler → validateReturns), never in a direct unit call —
// so a single-value return compiled and "passed tests" while failing in Lambda
// with "handler returns a single value, but it does not implement error".
func TestHandleValidLambdaHandler(t *testing.T) {
	h := &handler{pix: &fakePix{}}
	handler := lambda.NewHandler(h.handle)
	payload, _ := json.Marshal(rpc.Request{Op: rpc.OpPing, OAuthToken: "x"})
	if _, err := handler.Invoke(context.Background(), payload); err != nil {
		t.Fatalf("handle is not a valid lambda handler: %v", err)
	}
}
