package lambdarpc

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	rpccontract "gopkg.aoctech.app/wallet/rpc-contract"
)

type stubInvoker struct {
	payload []byte
	err     error
	request rpccontract.Request
}

func (s *stubInvoker) Invoke(_ context.Context, payload []byte) ([]byte, error) {
	if err := json.Unmarshal(payload, &s.request); err != nil {
		return nil, err
	}
	return s.payload, s.err
}

func TestCallBuildsContractRequestAndDecodesResponse(t *testing.T) {
	responsePayload, err := json.Marshal(struct {
		Value string `json:"value"`
	}{Value: "result"})
	if err != nil {
		t.Fatal(err)
	}
	wireResponse, err := json.Marshal(rpccontract.Response{Payload: responsePayload})
	if err != nil {
		t.Fatal(err)
	}
	invoker := &stubInvoker{payload: wireResponse}

	response, err := Call(context.Background(), invoker, rpccontract.OpPing, "credential", struct {
		ID string `json:"id"`
	}{ID: "request"})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if invoker.request.Op != rpccontract.OpPing || invoker.request.OAuthToken != "credential" {
		t.Fatalf("unexpected request metadata: %+v", invoker.request)
	}
	var args struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(invoker.request.Payload, &args); err != nil || args.ID != "request" {
		t.Fatalf("unexpected request payload: %s (%v)", invoker.request.Payload, err)
	}
	var result struct {
		Value string `json:"value"`
	}
	if err := DecodePayload(response, &result); err != nil || result.Value != "result" {
		t.Fatalf("unexpected result: %+v (%v)", result, err)
	}
}

func TestCallPropagatesTransportError(t *testing.T) {
	want := errors.New("invoke failed")
	_, err := Call(context.Background(), &stubInvoker{err: want}, rpccontract.OpPing, "", struct{}{})
	if !errors.Is(err, want) {
		t.Fatalf("expected transport error %v, got %v", want, err)
	}
}

func TestDecodePayloadAcceptsEmptyCommandResponse(t *testing.T) {
	if err := DecodePayload(rpccontract.Response{}, nil); err != nil {
		t.Fatalf("empty command response: %v", err)
	}
}
