package pix

import (
	"context"
	"encoding/json"
	"testing"
)

// fakeLambdaInvoker stands in for *lambda.Client — LambdaPixClient depends on
// a small interface (lambdaInvoker) so this test never touches AWS.
type fakeLambdaInvoker struct {
	// respond is keyed by the decoded rpcRequest.Op string.
	respond map[string]rpcResponse
}

func (f *fakeLambdaInvoker) invoke(_ context.Context, payload []byte) ([]byte, error) {
	var req rpcRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return nil, err
	}
	resp := f.respond[string(req.Op)]
	return json.Marshal(resp)
}

func TestLambdaPixClientCreateCharge(t *testing.T) {
	chargeJSON, _ := json.Marshal(rpcChargeResult{Txid: "tx1", Amount: 500, Status: ChargeActive, QRCode: "EMV"})
	f := &fakeLambdaInvoker{respond: map[string]rpcResponse{
		string(opCreateCharge): {Payload: chargeJSON},
	}}
	c := &LambdaPixClient{invoker: f}
	ch, err := c.CreateCharge(context.Background(), "tx1", 500, "")
	if err != nil {
		t.Fatalf("CreateCharge: %v", err)
	}
	if ch.Txid != "tx1" || ch.Amount != 500 || ch.Status != ChargeActive || ch.QRCode != "EMV" {
		t.Fatalf("bad charge: %+v", ch)
	}
}

func TestLambdaPixClientDictLookupNotFound(t *testing.T) {
	f := &fakeLambdaInvoker{respond: map[string]rpcResponse{
		string(opDictLookup): {Error: errKeyNotFoundSentinel},
	}}
	c := &LambdaPixClient{invoker: f}
	_, err := c.DictLookup(context.Background(), "some-key")
	if err != ErrKeyNotFound {
		t.Fatalf("expected ErrKeyNotFound, got %v", err)
	}
}

func TestLambdaPixClientGenericError(t *testing.T) {
	f := &fakeLambdaInvoker{respond: map[string]rpcResponse{
		string(opPing): {Error: "bank unreachable"},
	}}
	c := &LambdaPixClient{invoker: f}
	err := c.Ping(context.Background())
	if err == nil || err.Error() != "bank unreachable" {
		t.Fatalf("expected passthrough error, got %v", err)
	}
}
