package inter

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"

	"gopkg.aoctech.app/pix-gateway/internal/secrets"
)

func TestCentavosReaisRoundTrip(t *testing.T) {
	cases := []int64{1, 99, 100, 12345, 5000, 100000}
	for _, c := range cases {
		if got := reaisToCentavos(centavosToReais(c)); got != c {
			t.Errorf("round-trip %d: got %d (via %q)", c, got, centavosToReais(c))
		}
	}
	if centavosToReais(12345) != "123.45" {
		t.Errorf("format: got %q", centavosToReais(12345))
	}
}

// newTestClient builds an InterClient against a plain (non-mTLS) httptest
// server, so tests never touch SSM or real Inter. Credentials are set directly
// for the GetToken op.
func newTestClient(base string, httpClient *http.Client) *InterClient {
	base = strings.TrimRight(base, "/")
	return &InterClient{
		base:         base,
		pixKey:       "test-pix-key",
		http:         httpClient,
		clientID:     "cid",
		clientSecret: "csec",
		scope:        tokenScope,
		tokenURL:     base + pathToken,
	}
}

func TestGetToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != pathToken {
			t.Errorf("unexpected token path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "AT", "expires_in": 3600})
	}))
	defer srv.Close()

	c := newTestClient(srv.URL, srv.Client())
	res, err := c.GetToken(context.Background())
	if err != nil {
		t.Fatalf("GetToken: %v", err)
	}
	if res.Token != "AT" || res.ExpiresIn != 3600 {
		t.Fatalf("bad token result: %+v", res)
	}
}

// fakeSecretsSSM counts GetParameter calls and returns a fixed value.
type fakeSecretsSSM struct {
	calls int
	value string
}

func (f *fakeSecretsSSM) GetParameter(_ context.Context, _ *ssm.GetParameterInput, _ ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
	f.calls++
	return &ssm.GetParameterOutput{Parameter: &types.Parameter{Value: aws.String(f.value)}}, nil
}

// TestGetTokenLoadsClientSecretLazilyAndCaches proves the Inter OAuth client
// secret is fetched from SSM only on first GetToken and cached afterwards — so
// cold start never reads it, and repeat GetToken calls don't re-hit SSM.
func TestGetTokenLoadsClientSecretLazilyAndCaches(t *testing.T) {
	var gotSecret string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotSecret = r.FormValue("client_secret")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "AT", "expires_in": 3600})
	}))
	defer srv.Close()

	fake := &fakeSecretsSSM{value: "ssm-secret"}
	c := &InterClient{
		base:     strings.TrimRight(srv.URL, "/"),
		pixKey:   "k",
		http:     srv.Client(),
		clientID: "cid",
		scope:    tokenScope,
		tokenURL: srv.URL + pathToken,
		secrets:  secrets.NewStore(fake, "dev"),
	}

	if _, err := c.GetToken(context.Background()); err != nil {
		t.Fatalf("GetToken: %v", err)
	}
	if gotSecret != "ssm-secret" {
		t.Fatalf("secret not forwarded from SSM: got %q", gotSecret)
	}
	if _, err := c.GetToken(context.Background()); err != nil {
		t.Fatalf("GetToken 2: %v", err)
	}
	if fake.calls != 1 {
		t.Fatalf("expected 1 SSM call (cached after first), got %d", fake.calls)
	}
}

// TestDoSetsBearer verifies the bearer api passes per call is forwarded as
// Authorization: Bearer <tok> — pix-gateway is a pure transport.
func TestDoSetsBearer(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"txid": "tx1", "status": ChargeActive, "pixCopiaECola": "EMV"})
	}))
	defer srv.Close()

	c := newTestClient(srv.URL, srv.Client())
	ctx := WithBearer(context.Background(), "BEARER123")
	ch, err := c.CreateCharge(ctx, "tx1", 500, "")
	if err != nil {
		t.Fatalf("CreateCharge: %v", err)
	}
	if gotAuth != "Bearer BEARER123" {
		t.Fatalf("expected Authorization Bearer BEARER123, got %q", gotAuth)
	}
	if ch.Txid != "tx1" || ch.Status != ChargeActive {
		t.Fatalf("bad charge: %+v", ch)
	}
	// Inter returns only the EMV string; the gateway must render it into a PNG
	// QR so the frontend <img> has something to show.
	if ch.QRCodeB64 == "" {
		t.Fatalf("expected QRCodeB64 to be populated from pixCopiaECola, got empty")
	}
	raw, err := base64.StdEncoding.DecodeString(ch.QRCodeB64)
	if err != nil {
		t.Fatalf("QRCodeB64 not valid base64: %v", err)
	}
	if len(raw) < 4 || string(raw[:4]) != "\x89PNG" {
		t.Fatalf("QRCodeB64 did not decode to a PNG (magic=%x)", raw[:min(4, len(raw))])
	}
}

// TestQueryChargeParsesDevolucoes covers Inter's real cob-query response shape
// for a payment that was later returned to the payer — the devolução is
// nested under pix[].devolucoes[], never a top-level field.
func TestQueryChargeParsesDevolucoes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"txid": "01KXPR34BKVF7E5SWP1TE2R0K7",
			"status": "CONCLUIDA",
			"valor": {"original": "1.00"},
			"pix": [{
				"endToEndId": "E10573521202607170037OukqTjpOvAA",
				"valor": "1.00",
				"devolucoes": [{
					"id": "refund",
					"rtrId": "D00416968202607170059Yy0QJaJ31i1",
					"valor": "1.00",
					"status": "DEVOLVIDO",
					"motivo": ""
				}]
			}]
		}`))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL, srv.Client())
	ctx := WithBearer(context.Background(), "BEARER123")
	ch, err := c.QueryCharge(ctx, "01KXPR34BKVF7E5SWP1TE2R0K7")
	if err != nil {
		t.Fatalf("QueryCharge: %v", err)
	}
	if len(ch.Refunds) != 1 {
		t.Fatalf("expected 1 refund, got %+v", ch.Refunds)
	}
	r := ch.Refunds[0]
	if r.RtrID != "D00416968202607170059Yy0QJaJ31i1" || r.Amount != 100 || r.Status != RefundCompleted {
		t.Fatalf("bad refund: %+v", r)
	}
}

// TestQueryChargeParsesMultiplePayments covers the same QR code being scanned
// and paid by two different people — Inter reports both under pix[], each
// with its own endToEndId/valor/pagador.
func TestQueryChargeParsesMultiplePayments(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"txid": "tx1",
			"status": "CONCLUIDA",
			"valor": {"original": "50.00"},
			"pix": [
				{"endToEndId": "E1", "valor": "50.00", "pagador": {"cpf": "11111111111"}},
				{"endToEndId": "E2", "valor": "50.00", "pagador": {"cpf": "22222222222"}}
			]
		}`))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL, srv.Client())
	ctx := WithBearer(context.Background(), "BEARER123")
	ch, err := c.QueryCharge(ctx, "tx1")
	if err != nil {
		t.Fatalf("QueryCharge: %v", err)
	}
	if len(ch.Payments) != 2 {
		t.Fatalf("expected 2 payments, got %+v", ch.Payments)
	}
	if ch.Payments[0].E2EID != "E1" || ch.Payments[1].E2EID != "E2" {
		t.Fatalf("bad payment order: %+v", ch.Payments)
	}
	if ch.Payments[1].Amount != 5000 || ch.Payments[1].PayerCPF != "22222222222" {
		t.Fatalf("bad excess payment: %+v", ch.Payments[1])
	}
	// Legacy top-level fields still mirror the FIRST payment.
	if ch.E2EID != "E1" || ch.PayerCPF != "11111111111" {
		t.Fatalf("top-level fields should mirror payments[0]: e2e=%q cpf=%q", ch.E2EID, ch.PayerCPF)
	}
}

// TestDoMissingBearer proves the tokenManager is gone: without a supplied
// bearer, do refuses instead of fetching one.
func TestDoMissingBearer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()

	c := newTestClient(srv.URL, srv.Client())
	_, err := c.CreateCharge(context.Background(), "tx1", 500, "")
	if err == nil || !strings.Contains(err.Error(), "missing OAuth bearer") {
		t.Fatalf("expected missing-bearer error, got %v", err)
	}
}

// TestTransferKeyNotFound proves a 404 from Inter's payout endpoint (unknown
// destination PIX key) is classified as ErrKeyNotFound, not a generic error —
// this is what lets api refund the withdrawal immediately instead of leaving
// it stuck in processing for reconciliation.
func TestTransferKeyNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"title":"chave não encontrada"}`))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL, srv.Client())
	ctx := WithBearer(context.Background(), "BEARER123")
	_, err := c.Transfer(ctx, "unknown-key", 1000, "idem1")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !IsKeyNotFound(err) {
		t.Fatalf("expected IsKeyNotFound(err) to be true, got err=%v", err)
	}
}

// TestTransferOtherErrorNotClassifiedAsKeyNotFound proves a non-404 failure
// (e.g. a transient 500) is NOT classified as ErrKeyNotFound — only an exact
// 404 means "key not registered"; anything else must stay an opaque
// bank/transport failure so reconciliation still retries it.
func TestTransferOtherErrorNotClassifiedAsKeyNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL, srv.Client())
	ctx := WithBearer(context.Background(), "BEARER123")
	_, err := c.Transfer(ctx, "some-key", 1000, "idem2")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if IsKeyNotFound(err) {
		t.Fatalf("500 must not be classified as key-not-found: %v", err)
	}
}

func TestPingValidatesBearer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()

	c := newTestClient(srv.URL, srv.Client())
	if err := c.Ping(WithBearer(context.Background(), "X")); err != nil {
		t.Fatalf("Ping with bearer should succeed: %v", err)
	}
	if err := c.Ping(context.Background()); err == nil {
		t.Fatal("Ping without bearer should fail")
	}
}
