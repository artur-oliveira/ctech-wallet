package billingclient

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"gopkg.aoctech.app/wallet/api/internal/config"
)

// stub stands in for both ctech-account's token endpoint and Billing.
type stub struct {
	t      *testing.T
	srv    *httptest.Server
	mu     sync.Mutex
	calls  []call
	routes map[string]func(w http.ResponseWriter, r *http.Request, body []byte)
}

type call struct {
	method, path, idem, auth string
	body                     []byte
}

func newStub(t *testing.T) (*stub, *Client) {
	t.Helper()
	s := &stub{t: t, routes: map[string]func(http.ResponseWriter, *http.Request, []byte){}}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == pathToken {
			_, _ = w.Write([]byte(`{"access_token":"tok-1","token_type":"Bearer","expires_in":3600}`))
			return
		}
		body, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.calls = append(s.calls, call{r.Method, r.URL.Path, r.Header.Get("Idempotency-Key"), r.Header.Get("Authorization"), body})
		h := s.routes[r.Method+" "+r.URL.Path]
		s.mu.Unlock()
		if h == nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		h(w, r, body)
	}))
	t.Cleanup(s.srv.Close)
	cfg := &config.Config{BillingURL: s.srv.URL, CtechURL: s.srv.URL, BillingClientID: "wallet", BillingClientSecret: "s"}
	return s, New(cfg)
}

func (s *stub) on(route string, status int, body string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.routes[route] = func(w http.ResponseWriter, _ *http.Request, _ []byte) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
}

func (s *stub) last() call {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[len(s.calls)-1]
}

func TestCreateInvoiceSendsTheReferenceAsTheIdempotencyKeyAndNamesTheDeposit(t *testing.T) {
	s, c := newStub(t)
	s.on("POST /v1.0/invoices", 201, `{"id":"in_1","status":"OPEN","total":5000,"amount_paid":0,"checkout_url":"https://x/checkout?token=T","metadata":{"txid":"dep-1"}}`)
	inv, err := c.CreateInvoice(context.Background(), "cus_1", "dep-1", "Depósito", 5000)
	if err != nil {
		t.Fatal(err)
	}
	got := s.last()
	if got.idem != "dep-1" || got.auth != "Bearer tok-1" {
		t.Fatalf("headers: idem %q auth %q", got.idem, got.auth)
	}
	var sent map[string]any
	_ = json.Unmarshal(got.body, &sent)
	meta, _ := sent["metadata"].(map[string]any)
	if sent["customer_id"] != "cus_1" || sent["reference"] != "dep-1" || sent["amount_cents"].(float64) != 5000 ||
		sent["owner_key"] != OwnerKey || meta[MetadataTxid] != "dep-1" {
		t.Fatalf("body: %s", got.body)
	}
	if inv.ID != "in_1" || inv.Txid != "dep-1" || inv.AmountCents != 5000 || inv.Status != StatusOpen {
		t.Fatalf("invoice %+v", inv)
	}
}

func TestAReusedReferenceIsAConflictNotACreditableInvoice(t *testing.T) {
	s, c := newStub(t)
	s.on("POST /v1.0/invoices", 409, `{"title":"Conflict"}`)
	if _, err := c.CreateInvoice(context.Background(), "cus_1", "dep-1", "x", 1); !errors.Is(err, ErrConflict) {
		t.Fatalf("want ErrConflict, got %v", err)
	}
}

func TestInvoiceByReferenceReadsThePayerAndTheVerdict(t *testing.T) {
	s, c := newStub(t)
	s.on("GET /v1.0/invoices/by-reference/dep-1", 200,
		`{"id":"in_1","status":"PAID","total":5000,"amount_paid":5000,"metadata":{"txid":"dep-1"},"payer":{"name":"Fulano","document":"39053344705","institution":"Banco"},"payer_match":"match"}`)
	inv, err := c.InvoiceByReference(context.Background(), "dep-1")
	if err != nil {
		t.Fatal(err)
	}
	if inv.Status != StatusPaid || inv.PaidCents != 5000 || inv.PayerMatch != MatchYes || inv.Payer == nil || inv.Payer.Document != "39053344705" {
		t.Fatalf("invoice %+v", inv)
	}
}

func TestAnUnknownReferenceIsNotFound(t *testing.T) {
	_, c := newStub(t)
	if _, err := c.InvoiceByReference(context.Background(), "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestOpenPixUsesTheCheckoutTokenWithoutABearer(t *testing.T) {
	s, c := newStub(t)
	exp := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	s.on("POST /v1.0/checkout/T0K/pay", 200, `{"payment":{"method":"PIX","pix_code":"000201...","expires_at":"`+exp.Format(time.RFC3339)+`"}}`)
	inv := &Invoice{Status: StatusOpen, CheckoutURL: "https://billing.aoctech.app/checkout?token=T0K"}
	if err := c.OpenPix(context.Background(), inv); err != nil {
		t.Fatal(err)
	}
	if inv.PixCopiaECola != "000201..." || !inv.ExpiresAt.Equal(exp) {
		t.Fatalf("pix %q expires %v", inv.PixCopiaECola, inv.ExpiresAt)
	}
	if got := s.last(); got.auth != "" {
		t.Fatalf("the public route is authenticated by its token alone, sent %q", got.auth)
	}
}

func TestOpenPixRefusesAnInvoiceThatCannotBePaid(t *testing.T) {
	_, c := newStub(t)
	for _, inv := range []*Invoice{
		{Status: StatusPaid, CheckoutURL: "https://x/checkout?token=T"},
		{Status: StatusOpen},
		{Status: StatusOpen, CheckoutURL: "https://x/checkout"},
	} {
		if err := c.OpenPix(context.Background(), inv); !errors.Is(err, ErrNoPix) {
			t.Fatalf("%+v: want ErrNoPix, got %v", inv, err)
		}
	}
}

func TestEnsureCustomerReusesAnExistingCustomerOfThatUser(t *testing.T) {
	s, c := newStub(t)
	s.on("GET /v1.0/customers/by-user/u1", 200, `{"id":"cus_existing"}`)
	id, err := c.EnsureCustomer(context.Background(), "u1", "39053344705", "Fulano", "f@x.com")
	if err != nil || id != "cus_existing" {
		t.Fatalf("id %q err %v", id, err)
	}
	for _, call := range s.calls {
		if call.method == http.MethodPost {
			t.Fatal("no customer is created for a user who already is one")
		}
	}
}

func TestEnsureCustomerCreatesOnFirstUseWithTheUserAndTheirCPF(t *testing.T) {
	s, c := newStub(t)
	s.on("POST /v1.0/customers", 201, `{"id":"cus_new"}`)
	id, err := c.EnsureCustomer(context.Background(), "u1", "39053344705", "Fulano", "f@x.com")
	if err != nil || id != "cus_new" {
		t.Fatalf("id %q err %v", id, err)
	}
	got := s.last()
	var sent map[string]string
	_ = json.Unmarshal(got.body, &sent)
	if got.idem != "wallet-customer-u1" || sent["user_id"] != "u1" || sent["tax_id"] != "39053344705" || sent["name"] != "Fulano" {
		t.Fatalf("call %+v body %s", got, got.body)
	}
}

func TestEnsureCustomerSurvivesALostRace(t *testing.T) {
	s, c := newStub(t)
	n := 0
	s.mu.Lock()
	s.routes["GET /v1.0/customers/by-user/u1"] = func(w http.ResponseWriter, _ *http.Request, _ []byte) {
		n++
		if n == 1 {
			w.WriteHeader(404)
			return
		}
		_, _ = w.Write([]byte(`{"id":"cus_raced"}`))
	}
	s.mu.Unlock()
	s.on("POST /v1.0/customers", 409, `{}`)
	id, err := c.EnsureCustomer(context.Background(), "u1", "1", "n", "e")
	if err != nil || id != "cus_raced" {
		t.Fatalf("id %q err %v", id, err)
	}
}

func TestRefundInvoiceIsACreditNoteRefundedByPix(t *testing.T) {
	s, c := newStub(t)
	s.on("POST /v1.0/invoices/in_1/credit-notes", 201, `{"id":"cn_1"}`)
	if err := c.RefundInvoice(context.Background(), "in_1", 5000, "payer mismatch", "refund#dep-1"); err != nil {
		t.Fatal(err)
	}
	got := s.last()
	var sent map[string]any
	_ = json.Unmarshal(got.body, &sent)
	if got.idem != "refund#dep-1" || sent["amount"].(float64) != 5000 || sent["refund_via_pix"] != true || sent["reason"] == "" {
		t.Fatalf("call %+v", got)
	}
}

func TestCreatePayoutCarriesTheReferenceAndNeverASuppliedDestinationBeyondTheCPF(t *testing.T) {
	s, c := newStub(t)
	s.on("POST /v1.0/payouts", 201, `{"id":"po_1","reference":"w-1","status":"processing","amount_cents":700}`)
	p, err := c.CreatePayout(context.Background(), "39053344705", 700, "w-1", "Saque")
	if err != nil || p.Status != PayoutProcessing {
		t.Fatalf("payout %+v err %v", p, err)
	}
	got := s.last()
	if got.idem != "w-1" || !strings.Contains(string(got.body), `"cpf":"39053344705"`) || !strings.Contains(string(got.body), `"reference":"w-1"`) {
		t.Fatalf("call %+v body %s", got, got.body)
	}
}

func TestAPayoutFailureCarriesItsReason(t *testing.T) {
	s, c := newStub(t)
	s.on("GET /v1.0/payouts/by-reference/w-1", 200, `{"id":"po_1","reference":"w-1","status":"failed","failure_reason":"key_not_found"}`)
	p, err := c.PayoutByReference(context.Background(), "w-1")
	if err != nil || p.Status != PayoutFailed || p.FailureReason != ReasonKeyNotFound {
		t.Fatalf("payout %+v err %v", p, err)
	}
}

func TestATransportFailureIsAnErrorAndNeverNotFound(t *testing.T) {
	s, c := newStub(t)
	s.srv.Close()
	_, err := c.PayoutByReference(context.Background(), "w-1")
	if err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("a dead Billing is not 'no such payout': %v", err)
	}
}

func TestAServerErrorIsAnErrorAndNeverNotFound(t *testing.T) {
	s, c := newStub(t)
	s.on("GET /v1.0/payouts/by-reference/w-1", 503, `{"detail":"down"}`)
	_, err := c.PayoutByReference(context.Background(), "w-1")
	if err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("503 is not 'no such payout': %v", err)
	}
}

func TestVerifySignature(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	ts := strconv.FormatInt(now.Unix(), 10)
	body := []byte(`{"type":"invoice.paid"}`)
	good := "v1=" + Sign("s3cret", ts, body)

	if !VerifySignature("s3cret", good, ts, body, now, 5*time.Minute) {
		t.Fatal("a genuine delivery must verify")
	}
	flipped := append([]byte(nil), body...)
	flipped[2] ^= 1
	cases := map[string]bool{
		"flipped byte":    VerifySignature("s3cret", good, ts, flipped, now, 5*time.Minute),
		"wrong secret":    VerifySignature("other", good, ts, body, now, 5*time.Minute),
		"no v1 prefix":    VerifySignature("s3cret", Sign("s3cret", ts, body), ts, body, now, 5*time.Minute),
		"empty secret":    VerifySignature("", good, ts, body, now, 5*time.Minute),
		"stale":           VerifySignature("s3cret", good, ts, body, now.Add(6*time.Minute), 5*time.Minute),
		"from the future": VerifySignature("s3cret", good, ts, body, now.Add(-6*time.Minute), 5*time.Minute),
		"bad timestamp":   VerifySignature("s3cret", good, "soon", body, now, 5*time.Minute),
	}
	for name, ok := range cases {
		if ok {
			t.Errorf("%s verified", name)
		}
	}
}
