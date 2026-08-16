package asaas

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func newTestClient(t *testing.T, fn roundTripFunc) *AsaasClient {
	t.Helper()
	return &AsaasClient{base: "https://asaas.example", http: &http.Client{Transport: fn}}
}

func jsonResponse(body string) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func TestQueryPaymentIncludesCustomerID(t *testing.T) {
	c := newTestClient(t, func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || r.URL.Path != "/v3/payments/pay_1" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get(authHeader); got != "subaccount-key" {
			t.Fatalf("access token = %q", got)
		}
		return jsonResponse(`{"id":"pay_1","value":10,"status":"RECEIVED","customer":"cus_1","externalReference":"tx_1"}`), nil
	})

	p, err := c.QueryPayment(context.Background(), "subaccount-key", "pay_1")
	if err != nil {
		t.Fatalf("QueryPayment: %v", err)
	}
	if p.CustomerID != "cus_1" || p.ExternalReference != "tx_1" {
		t.Fatalf("payment = %+v", p)
	}
}

func TestRefundPaymentUsesPaymentRefundEndpointAndCentavos(t *testing.T) {
	c := newTestClient(t, func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Path != "/v3/payments/pay_1/refund" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		if got, want := string(body), `{"description":"CPF divergente","value":10.25}`; got != want {
			t.Fatalf("body = %s, want %s", got, want)
		}
		return jsonResponse(`{}`), nil
	})

	if err := c.RefundPayment(context.Background(), "subaccount-key", "pay_1", 1025, "CPF divergente"); err != nil {
		t.Fatalf("RefundPayment: %v", err)
	}
}

func TestQueryCustomerReturnsCPFWithoutLoggingIt(t *testing.T) {
	c := newTestClient(t, func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || r.URL.Path != "/v3/customers/cus_1" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		return jsonResponse(`{"id":"cus_1","name":"Pessoa","cpfCnpj":"12345678901"}`), nil
	})

	customer, err := c.QueryCustomer(context.Background(), "subaccount-key", "cus_1")
	if err != nil {
		t.Fatalf("QueryCustomer: %v", err)
	}
	if customer.CPFCNPJ != "12345678901" {
		t.Fatalf("customer CPF = %q", customer.CPFCNPJ)
	}
}
