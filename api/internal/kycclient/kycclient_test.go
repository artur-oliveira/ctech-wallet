package kycclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"gopkg.aoctech.app/wallet/api/internal/config"
)

func newTestClient(t *testing.T, kyc http.HandlerFunc) (*Client, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == PathToken {
			_, _ = w.Write([]byte(`{"access_token":"t","token_type":"Bearer","expires_in":3600}`))
			return
		}
		hits.Add(1)
		kyc(w, r)
	}))
	t.Cleanup(srv.Close)
	return New(&config.Config{CtechURL: srv.URL, WalletClientID: "w", WalletClientSecret: "s"}, nil), &hits
}

func TestKYCNotFoundFailsClosedAndIsNotCached(t *testing.T) {
	c, hits := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) })
	for range 2 {
		if k, err := c.Get(context.Background(), "u1"); err == nil || k != nil {
			t.Fatalf("a 404 must be an error, got %+v %v", k, err)
		}
	}
	if hits.Load() != 2 {
		t.Fatalf("each lookup must reach the account, got %d", hits.Load())
	}
}

func TestKYCRetriesA5xxThenSucceeds(t *testing.T) {
	var n atomic.Int32
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		if n.Add(1) == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte(`{"level":"verified","cpf":"39053344705"}`))
	})
	k, err := c.Get(context.Background(), "u1")
	if err != nil || k.CPF != "39053344705" {
		t.Fatalf("kyc %+v err %v", k, err)
	}
}

func TestKYCPersistent5xxIsAnErrorNeverAnEmptyRecord(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) })
	if k, err := c.Get(context.Background(), "u1"); err == nil || k != nil {
		t.Fatalf("want an error, got %+v %v", k, err)
	}
}

func TestKYC429IsNotRetried(t *testing.T) {
	c, hits := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTooManyRequests) })
	if _, err := c.Get(context.Background(), "u1"); err == nil {
		t.Fatal("want an error")
	}
	if hits.Load() != 1 {
		t.Fatalf("a 429 must not be retried, got %d", hits.Load())
	}
}
