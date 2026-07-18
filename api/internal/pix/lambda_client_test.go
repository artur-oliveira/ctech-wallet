package pix

import (
	"context"
	"encoding/json"
	"testing"

	rpccontract "gopkg.aoctech.app/wallet/rpc-contract"
)

// fakeLambdaInvoker stands in for *lambda.Client — LambdaPixClient depends on
// a small interface (lambdaInvoker) so this test never touches AWS.
type fakeLambdaInvoker struct {
	// respond is keyed by the decoded rpccontract.Request.Op string.
	respond map[string]rpccontract.Response
	// calls counts invocations per op (for retry assertions).
	calls map[string]int
}

func (f *fakeLambdaInvoker) invoke(_ context.Context, payload []byte) ([]byte, error) {
	var req rpccontract.Request
	if err := json.Unmarshal(payload, &req); err != nil {
		return nil, err
	}
	f.calls[string(req.Op)]++
	if req.Op != rpccontract.OpGetToken && req.OAuthToken == "" {
		// Every non-token op must carry a bearer from the token manager.
		return json.Marshal(rpccontract.Response{Error: "missing oauth_token"})
	}
	resp := f.respond[string(req.Op)]
	return json.Marshal(resp)
}

// newTestLambdaPixClient wires a LambdaPixClient + InterTokenManager over a
// fake invoker (which must answer rpccontract.OpGetToken with a token).
func newTestLambdaPixClient(f lambdaInvoker) *LambdaPixClient {
	mgr := newTestTokenMgr(f) // nil locker: safe (skips cross-replica guard)
	return &LambdaPixClient{invoker: f, tokenMgr: mgr}
}

func TestLambdaPixClientCreateCharge(t *testing.T) {
	chargeJSON, _ := json.Marshal(rpccontract.ChargeResult{Txid: "tx1", Amount: 500, Status: ChargeActive, QRCode: "EMV"})
	f := &fakeLambdaInvoker{
		calls: map[string]int{},
		respond: map[string]rpccontract.Response{
			string(rpccontract.OpGetToken):     {Payload: mustJSON(rpccontract.GetTokenResult{Token: "TOK", ExpiresIn: 3600})},
			string(rpccontract.OpCreateCharge): {Payload: chargeJSON},
		},
	}
	c := newTestLambdaPixClient(f)
	ch, err := c.CreateCharge(context.Background(), "tx1", 500, "")
	if err != nil {
		t.Fatalf("CreateCharge: %v", err)
	}
	if ch.Txid != "tx1" || ch.Amount != 500 || ch.Status != ChargeActive || ch.QRCode != "EMV" {
		t.Fatalf("bad charge: %+v", ch)
	}
	if f.calls[string(rpccontract.OpCreateCharge)] != 1 {
		t.Fatalf("expected 1 CreateCharge call, got %d", f.calls[string(rpccontract.OpCreateCharge)])
	}
}

func TestLambdaPixClientGenericError(t *testing.T) {
	f := &fakeLambdaInvoker{
		calls: map[string]int{},
		respond: map[string]rpccontract.Response{
			string(rpccontract.OpGetToken): {Payload: mustJSON(rpccontract.GetTokenResult{Token: "TOK", ExpiresIn: 3600})},
			string(rpccontract.OpPing):     {Error: "bank unreachable"},
		},
	}
	c := newTestLambdaPixClient(f)
	err := c.Ping(context.Background())
	if err == nil || err.Error() != "bank unreachable" {
		t.Fatalf("expected passthrough error, got %v", err)
	}
}

// TestLambdaPixClientUnauthorizedRetry: a stale bearer yields a 401 from
// Inter; LambdaPixClient force-refreshes and retries the op exactly once.
func TestLambdaPixClientUnauthorizedRetry(t *testing.T) {
	chargeJSON, _ := json.Marshal(rpccontract.ChargeResult{Txid: "tx1", Amount: 500, Status: ChargeActive})
	// First CreateCharge returns unauthorized; the retried one succeeds.
	rf := &retryFake{
		okPayload: chargeJSON,
		calls:     map[string]int{},
	}
	c := newTestLambdaPixClient(rf)
	ch, err := c.CreateCharge(context.Background(), "tx1", 500, "")
	if err != nil {
		t.Fatalf("expected success after retry, got: %v", err)
	}
	if ch.Txid != "tx1" {
		t.Fatalf("bad charge after retry: %+v", ch)
	}
	// 1 forced token fetch + 2 CreateCharge attempts (initial + retry) = 2 ops.
	if rf.calls[string(rpccontract.OpCreateCharge)] != 2 {
		t.Fatalf("expected 2 CreateCharge calls (initial + retry), got %d", rf.calls[string(rpccontract.OpCreateCharge)])
	}
}

// retryFake fails the first CreateCharge with unauthorized, then succeeds.
// It implements lambdaInvoker directly (no embedding) so call counting is exact.
type retryFake struct {
	okPayload json.RawMessage
	calls     map[string]int
}

func (r *retryFake) invoke(_ context.Context, payload []byte) ([]byte, error) {
	var req rpccontract.Request
	if err := json.Unmarshal(payload, &req); err != nil {
		return nil, err
	}
	r.calls[string(req.Op)]++
	if req.Op != rpccontract.OpGetToken && req.OAuthToken == "" {
		return json.Marshal(rpccontract.Response{Error: "missing oauth_token"})
	}
	switch req.Op {
	case rpccontract.OpGetToken:
		return json.Marshal(rpccontract.Response{Payload: mustJSON(rpccontract.GetTokenResult{Token: "TOK", ExpiresIn: 3600})})
	case rpccontract.OpCreateCharge:
		if r.calls[string(rpccontract.OpCreateCharge)] == 1 {
			return json.Marshal(rpccontract.Response{Error: rpccontract.ErrUnauthorizedSentinel})
		}
		return json.Marshal(rpccontract.Response{Payload: r.okPayload})
	default:
		return json.Marshal(rpccontract.Response{})
	}
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
