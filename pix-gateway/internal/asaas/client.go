// Package asaas is the production client for Asaas's BaaS custody API,
// mirroring pix-gateway/internal/inter's role for Banco Inter: the ONLY place
// this codebase talks to Asaas over the network. api never calls Asaas
// directly — it invokes pix-gateway's outbound Lambda (see
// docs/plans/2026-07-30-asaas-baas-implementation-plan.md §2.2), same shape
// as every existing Inter op.
//
// IMPORTANT: endpoint paths and JSON field names below follow Asaas's
// documented v3 API (https://docs.asaas.com), fetched and cited per method.
// A few specifics could not be confirmed from the public docs excerpts alone
// and are flagged inline with "VERIFY" — confirm each against a live
// Asaas-sandbox call before enabling real money, exactly the same
// external-contract verification posture inter.go already carries for Inter.
package asaas

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"time"
)

var ErrTransferNotFound = errors.New("asaas: transfer not found")

// AsaasClient is the production client talking to Asaas's v3 REST API over
// plain HTTPS (no mTLS — Asaas authenticates via a per-call header, not a
// client certificate).
type AsaasClient struct {
	base string
	http *http.Client
}

// NewAsaasClient builds the client. baseURL is
// https://api-sandbox.asaas.com or https://api.asaas.com (config.AsaasBaseURL).
func NewAsaasClient(baseURL string) *AsaasClient {
	return &AsaasClient{base: baseURL, http: &http.Client{Timeout: 20 * time.Second}}
}

// Asaas API paths (centralized — no scattered literals, mirrors inter.go's
// own convention).
const (
	pathAccounts      = "/v3/accounts"
	pathMyDocuments   = "/v3/myAccount/documents/%s"         // POST, multipart — operates on the CALLING (subaccount) account
	pathAddressKeys   = "/v3/pix/addressKeys"                // POST — create a new EVP static PIX key for the calling account
	pathQRCodesStatic = "/v3/pix/qrCodes/static"             // POST — VERIFY: response shape (id/payload/encodedImage field names) not confirmed from public docs excerpt
	pathPayments      = "/v3/payments/%s"                    // GET — query a payment by id
	pathTransfers     = "/v3/transfers"                      // POST — both PIX-key and Asaas-wallet transfers use this same endpoint
	pathTransfersList = "/v3/transfers?externalReference=%s" // GET — list/filter, used for QueryTransfer (no GET-by-externalReference on the single-transfer endpoint)
	pathBalance       = "/v3/finance/balance"                // GET
)

// authHeader is the header Asaas expects the API key under on every call —
// a plain per-account key, never "Authorization: Bearer" (Asaas's stable,
// long-documented convention — confirmed independently of the specific
// pages fetched for this package, which did not show a curl example).
const authHeader = "access_token"

func (c *AsaasClient) do(ctx context.Context, method, path, apiKey string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set(authHeader, apiKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("asaas: %s %s: status %d: %s", method, path, resp.StatusCode, string(raw))
	}
	if out != nil && len(raw) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}

// wireAccount mirrors Asaas's POST /v3/accounts response fields exactly
// (confirmed via docs.asaas.com/reference/criar-subconta).
type wireAccount struct {
	ID       string `json:"id"`
	APIKey   string `json:"apiKey"`
	WalletID string `json:"walletId"`
	Status   string `json:"status"`
}

func (c *AsaasClient) CreateAccount(ctx context.Context, parentAPIKey string, args CreateAccountArgs) (*wireAccount, error) {
	body := map[string]any{
		"name": args.Name, "email": args.Email, "cpfCnpj": args.CPF,
		"birthDate": args.BirthDate, "mobilePhone": args.MobilePhone,
		"incomeValue": centavosToReais(args.IncomeValue),
		"address":     args.Address, "addressNumber": args.AddressNumber,
		"complement": args.Complement, "province": args.Province,
		"postalCode": args.PostalCode,
	}
	var out wireAccount
	if err := c.do(ctx, http.MethodPost, pathAccounts, parentAPIKey, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateAccountArgs mirrors rpccontract.AsaasCreateAccountArgs — kept as a
// distinct type in this package (not importing rpc-contract) so this client
// package has no dependency on the wire-transport package, mirroring how
// internal/inter's Charge/TransferResult types are independent of
// rpc-contract's ChargeResult/TransferResult (cmd/outbound does the mapping).
type CreateAccountArgs struct {
	Name, CPF, Email, MobilePhone, BirthDate                              string
	Address, AddressNumber, Complement, Province, City, State, PostalCode string
	IncomeValue                                                           int64
}

// UploadDocument sends a KYC document for the CALLING account (the api_key's
// own subaccount) — POST /v3/myAccount/documents/{documentID}, multipart.
func (c *AsaasClient) UploadDocument(ctx context.Context, apiKey, documentID string, file []byte) error {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile("documentFile", documentID)
	if err != nil {
		return err
	}
	if _, err := fw.Write(file); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+fmt.Sprintf(pathMyDocuments, documentID), &buf)
	if err != nil {
		return err
	}
	req.Header.Set(authHeader, apiKey)
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("asaas: upload document: status %d: %s", resp.StatusCode, string(raw))
	}
	return nil
}

type wirePixAddressKey struct {
	Key    string `json:"key"`
	Status string `json:"status"`
}

// CreateStaticPixKey creates the calling subaccount's EVP PIX key — POST
// /v3/pix/addressKeys, no body — created once, ever, per subaccount (BCB
// rate limit: plan §3.2 step 6, "1 creation/minute" per subaccount).
func (c *AsaasClient) CreateStaticPixKey(ctx context.Context, apiKey string) (*wirePixAddressKey, error) {
	var out wirePixAddressKey
	if err := c.do(ctx, http.MethodPost, pathAddressKeys, apiKey, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreatePixQRCodeArgs mirrors rpccontract.AsaasCreatePixQRCodeArgs (minus APIKey).
type CreatePixQRCodeArgs struct {
	AddressKey             string
	Value                  int64
	Format                 string
	ExpirationSeconds      int
	AllowsMultiplePayments bool
	ExternalReference      string
}

// wireQRCode mirrors POST /v3/pix/qrCodes/static's response. VERIFY: field
// names (id/payload/encodedImage/expirationDate) are the standard Asaas QR
// response shape used elsewhere in their API (e.g. GET .../pixQrCode), but
// this exact endpoint's response body was not shown in the fetched docs
// excerpt — confirm against a live Asaas-sandbox call before relying on it.
type wireQRCode struct {
	ID             string `json:"id"`
	Payload        string `json:"payload"`
	EncodedImage   string `json:"encodedImage"`
	ExpirationDate string `json:"expirationDate"`
}

func (c *AsaasClient) CreatePixQRCode(ctx context.Context, apiKey string, args CreatePixQRCodeArgs) (*wireQRCode, error) {
	body := map[string]any{
		"addressKey": args.AddressKey, "value": centavosToReais(args.Value),
		"format": args.Format, "expirationSeconds": args.ExpirationSeconds,
		"allowsMultiplePayments": args.AllowsMultiplePayments,
		"externalReference":      args.ExternalReference,
	}
	var out wireQRCode
	if err := c.do(ctx, http.MethodPost, pathQRCodesStatic, apiKey, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// wirePayment mirrors GET /v3/payments/{id}'s response (confirmed field
// names from the PAYMENT_RECEIVED webhook example: id/value/status/
// externalReference — docs.asaas.com/docs/webhook-para-cobrancas — the
// direct GET response was not itself shown, but Asaas payment objects are
// documented to carry the same fields across webhook and query responses).
type wirePayment struct {
	ID                string  `json:"id"`
	Value             float64 `json:"value"`
	Status            string  `json:"status"`
	ExternalReference string  `json:"externalReference"`
}

func (c *AsaasClient) QueryPayment(ctx context.Context, apiKey, paymentID string) (*wirePayment, error) {
	var out wirePayment
	if err := c.do(ctx, http.MethodGet, fmt.Sprintf(pathPayments, paymentID), apiKey, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateTransferArgs mirrors rpccontract.AsaasCreateTransferArgs (minus APIKey).
type CreateTransferArgs struct {
	Value                            int64
	PixAddressKey, PixAddressKeyType string
	WalletID                         string
	ExternalReference                string
}

// wireTransfer mirrors POST/GET /v3/transfers's response. VERIFY: the exact
// fee field name — the design research for this plan found "transferFee"
// referenced but not independently confirmed from a live response (same gap
// the plan's own §5.2 already names).
type wireTransfer struct {
	ID                string  `json:"id"`
	Status            string  `json:"status"`
	TransferFee       float64 `json:"transferFee"`
	ExternalReference string  `json:"externalReference"`
}

// CreateTransfer handles both transfer shapes through the same Asaas
// endpoint: a PIX-key payout (PixAddressKey set) or an Asaas-wallet-to-wallet
// move (WalletID set) — exactly one of the two per call, matching
// api's own asaas.TransferRequest contract.
func (c *AsaasClient) CreateTransfer(ctx context.Context, apiKey string, args CreateTransferArgs) (*wireTransfer, error) {
	body := map[string]any{
		"value": centavosToReais(args.Value), "externalReference": args.ExternalReference,
	}
	switch {
	case args.WalletID != "":
		body["walletId"] = args.WalletID
	case args.PixAddressKey != "":
		body["pixAddressKey"] = args.PixAddressKey
		body["pixAddressKeyType"] = args.PixAddressKeyType
	default:
		return nil, errors.New("asaas: CreateTransfer requires either WalletID or PixAddressKey")
	}
	var out wireTransfer
	if err := c.do(ctx, http.MethodPost, pathTransfers, apiKey, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// QueryTransfer looks a transfer up by ExternalReference via the list/filter
// endpoint (GET /v3/transfers?externalReference=...) — the single-transfer
// GET (/v3/transfers/{id}) only supports Asaas's own opaque id, not our
// reference, per docs.asaas.com/reference/recuperar-uma-unica-transferencia.
// Every caller in this codebase already knows the reference it submitted and
// nothing else, so the list endpoint (taking the first/only match) is the
// correct lookup, not the single-transfer GET.
func (c *AsaasClient) QueryTransfer(ctx context.Context, apiKey, externalReference string) (*wireTransfer, error) {
	var page struct {
		Data []wireTransfer `json:"data"`
	}
	path := fmt.Sprintf(pathTransfersList, externalReference)
	if err := c.do(ctx, http.MethodGet, path, apiKey, nil, &page); err != nil {
		return nil, err
	}
	if len(page.Data) == 0 {
		return nil, fmt.Errorf("%w: externalReference %q", ErrTransferNotFound, externalReference)
	}
	return &page.Data[0], nil
}

// QueryAccountBalance — GET /v3/finance/balance. VERIFY: the response
// field name "balance" is the well-established Asaas convention (used
// consistently across third-party Asaas SDKs) but was not directly shown in
// the fetched docs excerpt.
func (c *AsaasClient) QueryAccountBalance(ctx context.Context, apiKey string) (int64, error) {
	var out struct {
		Balance float64 `json:"balance"`
	}
	if err := c.do(ctx, http.MethodGet, pathBalance, apiKey, nil, &out); err != nil {
		return 0, err
	}
	return reaisToCentavos(out.Balance), nil
}
