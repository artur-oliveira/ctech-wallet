// Package billingclient is the wallet's client for ctech-billing: customers,
// one-off invoices (deposits), credit notes that give money back, and payouts
// (withdrawals). It speaks the routes Billing's M2M surface serves, with the
// wallet's own OAuth client, and nothing else.
package billingclient

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"gopkg.aoctech.app/api-commons/oauth2client"
	"gopkg.aoctech.app/wallet/api/internal/config"
)

const (
	// Billing's invoice statuses, as it renders them.
	StatusPaid = "PAID"
	StatusOpen = "OPEN"

	// Payout states.
	PayoutProcessing = "processing"
	PayoutDone       = "done"
	PayoutFailed     = "failed"

	// ReasonKeyNotFound is a payout failure: the bank has no Pix key for the CPF.
	ReasonKeyNotFound = "key_not_found"

	// Payer matches Billing computes against the customer's registered CPF.
	MatchYes = "match"
	// MatchPartial: the payer comes from the bank's webhook, the document masked to
	// its middle digits and every shown digit equal to the customer's.
	MatchPartial = "partial"

	// MetadataTxid is how an invoice names the wallet deposit it belongs to.
	MetadataTxid = "txid"

	// OwnerKey is the routing key of the wallet's invoices and credentials.
	OwnerKey = "wallet"

	pathToken = "/v1.0/token"
	scopes    = "billing:customers:read billing:customers:write billing:invoices:read billing:invoices:write internal:billing:payouts-read internal:billing:payouts-write"

	// HeaderSignature and HeaderTimestamp are Billing's webhook headers.
	HeaderSignature = "X-Billing-Signature"
	HeaderTimestamp = "X-Billing-Timestamp"
)

var (
	// ErrNotFound: Billing has no such invoice, customer or payout.
	ErrNotFound = errors.New("billing: not found")
	// ErrConflict: an idempotency key or reference was reused for something else.
	ErrConflict = errors.New("billing: idempotency or reference conflict")
	// ErrNoPix: the invoice cannot be paid by Pix now (not open, or no checkout link).
	ErrNoPix = errors.New("billing: invoice has no payable Pix")
)

// Invoice is a one-off invoice as the wallet needs it.
type Invoice struct {
	ID          string
	Status      string
	AmountCents int64 // the invoice total
	PaidCents   int64
	Txid        string // the wallet's deposit id, from the invoice metadata
	CheckoutURL string

	// Set by OpenPix.
	PixCopiaECola string
	ExpiresAt     time.Time

	Payer      *Payer // nil until Billing has recorded who paid
	PayerMatch string // "match" | "mismatch" | "unknown" | "" (not recorded yet)
}

// Payer is who paid an invoice, from the bank statement.
type Payer struct {
	Name, Document, Institution string
}

// Payout is a withdrawal paid out through Billing.
type Payout struct {
	ID, Reference, Status, PaymentID, FailureReason string
	AmountCents                                     int64
}

// Client talks to Billing.
type Client struct {
	base   string
	http   *http.Client
	tokens *oauth2client.TokenManager
}

// New builds the client. It makes no request: the token is fetched on first use.
func New(cfg *config.Config) *Client {
	h := &http.Client{Timeout: 8 * time.Second}
	ctech := strings.TrimRight(cfg.CtechURL, "/")
	return &Client{
		base:   strings.TrimRight(cfg.BillingURL, "/"),
		http:   h,
		tokens: oauth2client.New(h, nil, ctech+pathToken, cfg.BillingClientID, cfg.BillingClientSecret, scopes),
	}
}

type problemBody struct {
	Detail string `json:"detail"`
	Code   string `json:"code"`
}

func (c *Client) do(ctx context.Context, method, path string, idemKey string, in, out any, authed bool) error {
	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return err
	}
	if authed {
		tok, err := c.tokens.Get(ctx)
		if err != nil {
			return fmt.Errorf("billing token: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if idemKey != "" {
		req.Header.Set("Idempotency-Key", idemKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return ErrNotFound
	case resp.StatusCode == http.StatusConflict:
		var p problemBody
		_ = json.Unmarshal(raw, &p)
		return fmt.Errorf("%w: %s %s", ErrConflict, p.Code, p.Detail)
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		var p problemBody
		_ = json.Unmarshal(raw, &p)
		return fmt.Errorf("billing %s %s: status %d %s %s", method, path, resp.StatusCode, p.Code, p.Detail)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(raw, out)
}

// ── customers ────────────────────────────────────────────────────────────────

type customerJSON struct {
	ID          string `json:"id"`
	TaxIDMasked string `json:"tax_id_masked"`
}

// EnsureCustomer returns the Billing customer of a wallet user, creating it on
// first use. The user may already be a customer for another reason (a DF-e
// plan), in which case that customer is reused.
func (c *Client) EnsureCustomer(ctx context.Context, userID, taxID, name, email string) (string, error) {
	var found customerJSON
	err := c.do(ctx, http.MethodGet, "/v1.0/customers/by-user/"+url.PathEscape(userID), "", nil, &found, true)
	if err == nil {
		// A customer that exists for another reason (a DF-e plan) may have been
		// created without a CPF, and Billing cannot open a Pix charge for one. The
		// wallet holds the CPF, so it gives the customer that one, once; Billing
		// never replaces a CPF that is set.
		if found.TaxIDMasked == "" {
			if err := c.do(ctx, http.MethodPut, "/v1.0/customers/"+url.PathEscape(found.ID)+"/tax-id", "wallet-taxid-"+found.ID, map[string]string{"tax_id": taxID}, nil, true); err != nil {
				return "", fmt.Errorf("billing customer %s: setting the CPF: %w", found.ID, err)
			}
		}
		return found.ID, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return "", err
	}
	var made customerJSON
	err = c.do(ctx, http.MethodPost, "/v1.0/customers", "wallet-customer-"+userID, map[string]string{
		"user_id": userID, "external_ref": "wallet:" + userID, "name": name, "email": email, "tax_id": taxID,
	}, &made, true)
	if errors.Is(err, ErrConflict) { // created by a concurrent call in between
		err = c.do(ctx, http.MethodGet, "/v1.0/customers/by-user/"+url.PathEscape(userID), "", nil, &made, true)
	}
	if err != nil {
		return "", err
	}
	return made.ID, nil
}

// ── invoices ─────────────────────────────────────────────────────────────────

type invoiceJSON struct {
	ID          string            `json:"id"`
	Status      string            `json:"status"`
	Total       int64             `json:"total"`
	AmountPaid  int64             `json:"amount_paid"`
	CheckoutURL string            `json:"checkout_url"`
	Metadata    map[string]string `json:"metadata"`
	Payer       *struct {
		Name        string `json:"name"`
		Document    string `json:"document"`
		Institution string `json:"institution"`
	} `json:"payer"`
	PayerMatch string `json:"payer_match"`
}

func (j *invoiceJSON) invoice() *Invoice {
	inv := &Invoice{
		ID: j.ID, Status: j.Status, AmountCents: j.Total, PaidCents: j.AmountPaid,
		Txid: j.Metadata[MetadataTxid], CheckoutURL: j.CheckoutURL, PayerMatch: j.PayerMatch,
	}
	if j.Payer != nil {
		inv.Payer = &Payer{Name: j.Payer.Name, Document: j.Payer.Document, Institution: j.Payer.Institution}
	}
	return inv
}

// CreateInvoice issues the deposit's invoice. The reference is the wallet's
// deposit id and the idempotency key, so a retry returns the same invoice.
func (c *Client) CreateInvoice(ctx context.Context, customerID, reference, description string, amountCents int64) (*Invoice, error) {
	var out invoiceJSON
	err := c.do(ctx, http.MethodPost, "/v1.0/invoices", reference, map[string]any{
		"customer_id": customerID, "description": description, "amount_cents": amountCents,
		"reference": reference, "owner_key": OwnerKey, "metadata": map[string]string{MetadataTxid: reference},
	}, &out, true)
	if err != nil {
		return nil, err
	}
	return out.invoice(), nil
}

// InvoiceByReference reads the invoice the wallet issued under a reference.
func (c *Client) InvoiceByReference(ctx context.Context, reference string) (*Invoice, error) {
	var out invoiceJSON
	if err := c.do(ctx, http.MethodGet, "/v1.0/invoices/by-reference/"+url.PathEscape(reference), "", nil, &out, true); err != nil {
		return nil, err
	}
	return out.invoice(), nil
}

// InvoiceByID reads an invoice by Billing's id (what an event names).
func (c *Client) InvoiceByID(ctx context.Context, id string) (*Invoice, error) {
	var out invoiceJSON
	if err := c.do(ctx, http.MethodGet, "/v1.0/invoices/"+url.PathEscape(id), "", nil, &out, true); err != nil {
		return nil, err
	}
	return out.invoice(), nil
}

// OpenPix opens (or returns the still-live) Pix charge of an open invoice, through
// the public checkout link Billing put on it, and fills the copia-e-cola and its
// expiry. The link is the credential of that route, so no bearer is sent.
func (c *Client) OpenPix(ctx context.Context, inv *Invoice) error {
	if inv.Status != StatusOpen || inv.CheckoutURL == "" {
		return ErrNoPix
	}
	u, err := url.Parse(inv.CheckoutURL)
	if err != nil || u.Query().Get("token") == "" {
		return fmt.Errorf("%w: checkout link without a token", ErrNoPix)
	}
	var out struct {
		Payment *struct {
			PixCode   string    `json:"pix_code"`
			ExpiresAt time.Time `json:"expires_at"`
		} `json:"payment"`
	}
	if err := c.do(ctx, http.MethodPost, "/v1.0/checkout/"+url.PathEscape(u.Query().Get("token"))+"/pay", "", nil, &out, false); err != nil {
		return err
	}
	if out.Payment == nil || out.Payment.PixCode == "" {
		return ErrNoPix
	}
	inv.PixCopiaECola, inv.ExpiresAt = out.Payment.PixCode, out.Payment.ExpiresAt
	return nil
}

// RefundInvoice gives money back to the payer of a paid invoice: a credit note
// with the refund by Pix, which Billing does against the payment it holds.
func (c *Client) RefundInvoice(ctx context.Context, invoiceID string, amountCents int64, reason, idemKey string) error {
	return c.do(ctx, http.MethodPost, "/v1.0/invoices/"+url.PathEscape(invoiceID)+"/credit-notes", idemKey, map[string]any{
		"amount": amountCents, "reason": reason, "refund_via_pix": true,
	}, nil, true)
}

// ── payouts ──────────────────────────────────────────────────────────────────

type payoutJSON struct {
	ID            string `json:"id"`
	Reference     string `json:"reference"`
	Status        string `json:"status"`
	AmountCents   int64  `json:"amount_cents"`
	PaymentID     string `json:"payment_id"`
	FailureReason string `json:"failure_reason"`
}

func (j *payoutJSON) payout() *Payout {
	return &Payout{ID: j.ID, Reference: j.Reference, Status: j.Status, AmountCents: j.AmountCents, PaymentID: j.PaymentID, FailureReason: j.FailureReason}
}

// CreatePayout asks Billing to pay a CPF. The reference is the wallet's
// idempotency key. A transport failure is an error; the caller must not treat it
// as "not paid" (QueryPayout answers that).
func (c *Client) CreatePayout(ctx context.Context, cpf string, amountCents int64, reference, message string) (*Payout, error) {
	var out payoutJSON
	err := c.do(ctx, http.MethodPost, "/v1.0/payouts", reference, map[string]any{
		"reference": reference, "cpf": cpf, "amount_cents": amountCents, "message": message,
	}, &out, true)
	if err != nil {
		return nil, err
	}
	return out.payout(), nil
}

// PayoutByReference reads a payout by the wallet's reference. ErrNotFound means
// Billing never accepted it.
func (c *Client) PayoutByReference(ctx context.Context, reference string) (*Payout, error) {
	var out payoutJSON
	if err := c.do(ctx, http.MethodGet, "/v1.0/payouts/by-reference/"+url.PathEscape(reference), "", nil, &out, true); err != nil {
		return nil, err
	}
	return out.payout(), nil
}

// ── events ───────────────────────────────────────────────────────────────────

// Sign renders Billing's delivery signature: HMAC-SHA256 over "timestamp.body".
func Sign(secret, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// VerifySignature checks a delivery: the header is "v1=<hex>", the timestamp is
// unix seconds within skew of now, and the signature covers both.
func VerifySignature(secret, header, timestamp string, body []byte, now time.Time, skew time.Duration) bool {
	sig, ok := strings.CutPrefix(header, "v1=")
	if !ok || secret == "" {
		return false
	}
	sec, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return false
	}
	at := time.Unix(sec, 0)
	if now.Sub(at) > skew || at.Sub(now) > skew {
		return false
	}
	return hmac.Equal([]byte(sig), []byte(Sign(secret, timestamp, body)))
}
