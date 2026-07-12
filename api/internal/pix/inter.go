package pix

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/artur-oliveira/ctech-wallet/api/internal/config"
	"github.com/artur-oliveira/ctech-wallet/api/internal/secrets"
)

// InterClient is the production PixClient talking to Banco Inter's PIX/Banking
// APIs over mTLS with an OAuth2 client_credentials bearer token.
//
// IMPORTANT: the endpoint paths and JSON field names below follow Inter's
// documented v2 PIX/Banking API. Confirm each request/response shape against
// Inter's current API reference (and the sandbox) before enabling real money —
// this is an external-contract verification step, not a stub.
type InterClient struct {
	base   string
	pixKey string
	http   *http.Client
	tokens *tokenManager
}

// Inter API paths (centralized — no scattered literals).
const (
	pathToken      = "/oauth/v2/token"
	pathCob        = "/pix/v2/cob/%s"              // PUT create / GET query, by txid
	pathBankingPix = "/banking/v2/pix"             // POST payout
	pathDict       = "/banking/v2/pix/dict/%s"     // GET key owner lookup
	pathDevolucao  = "/pix/v2/pix/%s/devolucao/%s" // PUT refund by e2eid + devolucao id

	tokenScope      = "cob.read cob.write pix.read pix.write banking pix.pagamento"
	chargeExpirySec = 900 // 15 min, mirrors the pix_deposits TTL
)

// NewInterClient builds the client. The mTLS keypair is already in memory
// (loaded from SSM SecureString — see internal/secrets) and never touches the
// filesystem; the OAuth client secret comes from config (env, exported by
// start.sh from SSM).
func NewInterClient(cfg *config.Config, kp *secrets.MTLSKeypair) (*InterClient, error) {
	cert, err := tls.X509KeyPair(kp.CertPEM, kp.KeyPEM)
	if err != nil {
		return nil, fmt.Errorf("inter: parse mTLS keypair: %w", err)
	}
	httpClient := &http.Client{
		Timeout: 20 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				Certificates: []tls.Certificate{cert},
				MinVersion:   tls.VersionTLS12,
			},
		},
	}
	c := &InterClient{
		base:   strings.TrimRight(cfg.InterBaseURL, "/"),
		pixKey: cfg.InterPixKey,
		http:   httpClient,
	}
	c.tokens = &tokenManager{
		client:       httpClient,
		tokenURL:     c.base + pathToken,
		clientID:     cfg.InterClientID,
		clientSecret: cfg.InterClientSecret,
		scope:        tokenScope,
	}
	return c, nil
}

func (c *InterClient) CreateCharge(ctx context.Context, txid string, amount int64, payerHintCPF string) (*Charge, error) {
	body := map[string]any{
		"calendario": map[string]any{"expiracao": chargeExpirySec},
		"valor":      map[string]any{"original": centavosToReais(amount)},
		"chave":      c.pixKey,
	}
	var resp struct {
		Txid          string `json:"txid"`
		Status        string `json:"status"`
		PixCopiaECola string `json:"pixCopiaECola"`
	}
	if err := c.do(ctx, http.MethodPut, fmt.Sprintf(pathCob, txid), body, &resp); err != nil {
		return nil, err
	}
	return &Charge{Txid: txid, Amount: amount, QRCode: resp.PixCopiaECola, Status: resp.Status}, nil
}

func (c *InterClient) QueryCharge(ctx context.Context, txid string) (*Charge, error) {
	var resp struct {
		Status string `json:"status"`
		Valor  struct {
			Original string `json:"original"`
		} `json:"valor"`
		Pix []struct {
			EndToEndID string `json:"endToEndId"`
			Pagador    struct {
				CPF string `json:"cpf"`
			} `json:"pagador"`
		} `json:"pix"`
	}
	if err := c.do(ctx, http.MethodGet, fmt.Sprintf(pathCob, txid), nil, &resp); err != nil {
		return nil, err
	}
	ch := &Charge{Txid: txid, Status: resp.Status, Amount: reaisToCentavos(resp.Valor.Original)}
	if len(resp.Pix) > 0 {
		ch.PayerCPF = onlyDigits(resp.Pix[0].Pagador.CPF)
		ch.E2EID = resp.Pix[0].EndToEndID
	}
	return ch, nil
}

func (c *InterClient) DictLookup(ctx context.Context, pixKey string) (*DictAccount, error) {
	var resp struct {
		Chave   string `json:"chave"`
		Titular struct {
			CPFCNPJ string `json:"cpfCnpj"`
			Nome    string `json:"nome"`
		} `json:"titular"`
	}
	if err := c.do(ctx, http.MethodGet, fmt.Sprintf(pathDict, url.PathEscape(pixKey)), nil, &resp); err != nil {
		// An unregistered/mistyped key is a client error, not a bank outage.
		if isStatus(err, http.StatusNotFound) {
			return nil, ErrKeyNotFound
		}
		return nil, err
	}
	if resp.Titular.CPFCNPJ == "" {
		return nil, ErrKeyNotFound
	}
	return &DictAccount{Key: pixKey, CPF: onlyDigits(resp.Titular.CPFCNPJ), Name: resp.Titular.Nome}, nil
}

func (c *InterClient) Transfer(ctx context.Context, pixKey string, amount int64, idemKey string) (*TransferResult, error) {
	body := map[string]any{
		"valor":        centavosToReais(amount),
		"descricao":    "saque wallet",
		"destinatario": map[string]any{"tipo": "CHAVE", "chave": pixKey},
	}
	var resp struct {
		CodigoSolicitacao string `json:"codigoSolicitacao"`
		EndToEndID        string `json:"endToEndId"`
		Status            string `json:"status"`
	}
	if err := c.doIdem(ctx, http.MethodPost, pathBankingPix, body, &resp, idemKey); err != nil {
		return nil, err
	}
	e2e := resp.EndToEndID
	if e2e == "" {
		e2e = resp.CodigoSolicitacao
	}
	return &TransferResult{E2EID: e2e, Status: resp.Status}, nil
}

// QueryTransfer looks up a payout by its client idempotency key. Confirm the
// exact status endpoint/field names against Inter's Banking API reference.
func (c *InterClient) QueryTransfer(ctx context.Context, idemKey string) (*TransferResult, error) {
	var resp struct {
		EndToEndID string `json:"endToEndId"`
		Status     string `json:"status"`
	}
	if err := c.do(ctx, http.MethodGet, pathBankingPix+"/"+url.PathEscape(idemKey), nil, &resp); err != nil {
		// Treat a lookup failure as not-found so reconciliation reverses rather than
		// leaving money in limbo; a transient error will simply retry next run.
		return &TransferResult{Status: TransferNotFound}, nil
	}
	if resp.Status == "" {
		resp.Status = TransferNotFound
	}
	return &TransferResult{E2EID: resp.EndToEndID, Status: resp.Status}, nil
}

func (c *InterClient) Refund(ctx context.Context, e2eID string, amount int64, idemKey string) (*TransferResult, error) {
	body := map[string]any{"valor": centavosToReais(amount)}
	var resp struct {
		Status string `json:"status"`
	}
	// The devolução id must be unique and idempotent per refund — reuse idemKey.
	path := fmt.Sprintf(pathDevolucao, url.PathEscape(e2eID), url.PathEscape(idemKey))
	if err := c.do(ctx, http.MethodPut, path, body, &resp); err != nil {
		return nil, err
	}
	return &TransferResult{E2EID: e2eID, Status: resp.Status}, nil
}

// Ping asks the token manager for a token. A cached token satisfies it without
// a network call; otherwise it exercises the mTLS channel and the credentials.
func (c *InterClient) Ping(ctx context.Context) error {
	_, err := c.tokens.get(ctx)
	return err
}

// --- HTTP plumbing ---

func (c *InterClient) do(ctx context.Context, method, path string, body, out any) error {
	return c.doIdem(ctx, method, path, body, out, "")
}

func (c *InterClient) doIdem(ctx context.Context, method, path string, body, out any, idemKey string) error {
	token, err := c.tokens.get(ctx)
	if err != nil {
		return err
	}
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if idemKey != "" {
		// Inter accepts a client idempotency key on payout requests.
		req.Header.Set("x-id-idempotente", idemKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &statusError{Method: method, Path: path, Code: resp.StatusCode, Body: string(raw)}
	}
	if out != nil && len(raw) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}

// statusError carries the bank's HTTP status so callers can tell a client error
// (e.g. an unknown PIX key → 404) from a bank/transport failure.
type statusError struct {
	Method string
	Path   string
	Code   int
	Body   string
}

func (e *statusError) Error() string {
	return fmt.Sprintf("inter %s %s: status %d: %s", e.Method, e.Path, e.Code, e.Body)
}

func isStatus(err error, code int) bool {
	var se *statusError
	return errors.As(err, &se) && se.Code == code
}

// tokenManager fetches and caches the OAuth2 client_credentials token.
type tokenManager struct {
	client       *http.Client
	tokenURL     string
	clientID     string
	clientSecret string
	scope        string

	mu     sync.Mutex
	token  string
	expiry time.Time
}

func (t *tokenManager) get(ctx context.Context) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.token != "" && time.Now().Before(t.expiry) {
		return t.token, nil
	}
	form := url.Values{
		"client_id":     {t.clientID},
		"client_secret": {t.clientSecret},
		"grant_type":    {"client_credentials"},
		"scope":         {t.scope},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := t.client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("inter token: status %d: %s", resp.StatusCode, string(raw))
	}
	var tr struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(raw, &tr); err != nil {
		return "", err
	}
	t.token = tr.AccessToken
	// Refresh 30s before expiry to avoid edge-of-expiry 401s.
	t.expiry = time.Now().Add(time.Duration(tr.ExpiresIn-30) * time.Second)
	return t.token, nil
}

// --- money conversion (Inter uses "R$" decimal strings; wallet uses centavos) ---

func centavosToReais(centavos int64) string {
	return fmt.Sprintf("%d.%02d", centavos/100, centavos%100)
}

func reaisToCentavos(reais string) int64 {
	reais = strings.TrimSpace(reais)
	if reais == "" {
		return 0
	}
	parts := strings.SplitN(reais, ".", 2)
	intPart, _ := strconv.ParseInt(parts[0], 10, 64)
	cents := intPart * 100
	if len(parts) == 2 {
		frac := (parts[1] + "00")[:2]
		f, _ := strconv.ParseInt(frac, 10, 64)
		if intPart < 0 {
			cents -= f
		} else {
			cents += f
		}
	}
	return cents
}

func onlyDigits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

var _ PixClient = (*InterClient)(nil)
