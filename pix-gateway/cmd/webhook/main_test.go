package main

import (
	"context"
	"testing"

	"github.com/aws/aws-lambda-go/events"
)

type fakeConfirmer struct {
	calls []string
	err   error
}

func (f *fakeConfirmer) ConfirmDeposit(_ context.Context, txid string) error {
	f.calls = append(f.calls, txid)
	return f.err
}

func TestHandleWebhookForwardsEveryTxid(t *testing.T) {
	f := &fakeConfirmer{}
	h := &handler{confirmer: f}
	body := `{"txid":"tx1"}`
	req := events.APIGatewayV2HTTPRequest{Body: body}
	resp, err := h.handle(context.Background(), req)
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body: %s", resp.StatusCode, resp.Body)
	}
	if len(f.calls) != 1 || f.calls[0] != "tx1" {
		t.Fatalf("calls: %v", f.calls)
	}
}

func TestHandleWebhookMalformedBody(t *testing.T) {
	h := &handler{confirmer: &fakeConfirmer{}}
	resp, err := h.handle(context.Background(), events.APIGatewayV2HTTPRequest{Body: "not json"})
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	if resp.StatusCode != 400 {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestHandleWebhookConfirmFailureReturns500(t *testing.T) {
	f := &fakeConfirmer{err: context.DeadlineExceeded}
	h := &handler{confirmer: f}
	body := `{"txid":"tx1"}`
	resp, err := h.handle(context.Background(), events.APIGatewayV2HTTPRequest{Body: body})
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	// Non-200 so Inter retries the whole payload — ConfirmDeposit is idempotent
	// per txid (DepositPending guard + idempotency key), so a retry after a
	// partial failure is always safe.
	if resp.StatusCode != 500 {
		t.Fatalf("expected 500, got %d", resp.StatusCode)
	}
}
