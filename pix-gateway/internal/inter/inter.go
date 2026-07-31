package inter

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"gopkg.aoctech.app/wallet/pix-gateway/internal/config"
	"gopkg.aoctech.app/wallet/pix-gateway/internal/secrets"
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

	// OAuth client credentials for the GetToken op. pix-gateway is the only
	// place that talks to Inter's token endpoint; the bearer itself is passed
	// per call by api (see bearer.go) and never fetched here except via GetToken.
	clientID string
	scope    string
	tokenURL string

	// clientSecret is the Inter OAuth client secret — consumed ONLY by GetToken.
	// It is NOT loaded at cold start: if empty here, GetToken loads it lazily
	// from secrets (once per container) and caches it. Tests set it directly.
	clientSecret string
	secrets      *secrets.Store
	secretMu     sync.Mutex
}

// Inter API paths (centralized — no scattered literals).
const (
	pathToken      = "/oauth/v2/token"
	pathCob        = "/pix/v2/cob/%s"              // PUT create / GET query, by txid
	pathBankingPix = "/banking/v2/pix"             // POST payout
	pathDevolucao  = "/pix/v2/pix/%s/devolucao/%s" // PUT refund by e2eid + devolucao id

	tokenScope      = "cob.read cob.write pix.read pix.write pagamento-pix.read pagamento-pix.write"
	chargeExpirySec = 300 // 5 min, mirrors the pix_deposits TTL

	// Inter validates the client-generated devolução id as [a-zA-Z0-9]{1,35}.
	// Existing compliant IDs must be preserved because changing one after a
	// successful request would defeat provider-side idempotency on a retry.
	interRefundIDMaxLength = 35
)

// NewInterClient builds the client. The mTLS keypair is already in memory
// (loaded from SSM SecureString — see internal/secrets) and never touches the
// filesystem. The OAuth client secret is NOT read here: GetToken loads it
// lazily (and caches it) on first use, so a cold start that never calls
// GetToken never hits SSM for the secret.
func NewInterClient(cfg *config.Config, kp *secrets.MTLSKeypair, store *secrets.Store) (*InterClient, error) {
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
		base:         strings.TrimRight(cfg.InterBaseURL, "/"),
		pixKey:       cfg.InterPixKey,
		http:         httpClient,
		clientID:     cfg.InterClientID,
		clientSecret: cfg.InterClientSecret,
		scope:        tokenScope,
		tokenURL:     strings.TrimRight(cfg.InterBaseURL, "/") + pathToken,
		secrets:      store,
	}
	return c, nil
}

// GetToken fetches a fresh OAuth bearer using pix-gateway's own client
// credentials. It is the only place in pix-gateway that talks to Inter's token
// endpoint. It does NOT write to SSM — api owns the value.
func (c *InterClient) GetToken(ctx context.Context) (TokenResult, error) {
	secret, err := c.secret(ctx)
	if err != nil {
		return TokenResult{}, fmt.Errorf("inter: load client secret: %w", err)
	}
	form := url.Values{
		"client_id":     {c.clientID},
		"client_secret": {secret},
		"grant_type":    {"client_credentials"},
		"scope":         {c.scope},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return TokenResult{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.http.Do(req)
	if err != nil {
		return TokenResult{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return TokenResult{}, &statusError{Method: http.MethodPost, Path: c.tokenURL, Code: resp.StatusCode, Body: string(raw)}
	}
	var tr struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(raw, &tr); err != nil {
		return TokenResult{}, err
	}
	return TokenResult{Token: tr.AccessToken, ExpiresIn: tr.ExpiresIn}, nil
}

// secret returns the Inter OAuth client secret, loading it from SSM on first
// use and caching it for the container's lifetime. It is only ever consumed by
// GetToken, so this is never hit at cold start — only when a bearer is actually
// requested. Tests set c.clientSecret directly and leave c.secrets nil, taking
// the fast path with no SSM dependency.
func (c *InterClient) secret(ctx context.Context) (string, error) {
	if c.secrets == nil {
		return c.clientSecret, nil
	}
	c.secretMu.Lock()
	defer c.secretMu.Unlock()
	if c.clientSecret != "" {
		return c.clientSecret, nil
	}
	s, err := c.secrets.LoadInterClientSecret(ctx)
	if err != nil {
		return "", err
	}
	c.clientSecret = s
	return s, nil
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
	ch := &Charge{Txid: txid, Amount: amount, QRCode: resp.PixCopiaECola, Status: resp.Status}
	// Inter returns only the EMV string, never the QR image. Generate the PNG so
	// the frontend's <img> has something to render; a render miss is logged and
	// left empty — the EMV text still reaches the client.
	if ch.QRCode != "" {
		if b64, err := qrPNG(ch.QRCode); err != nil {
			slog.WarnContext(ctx, "inter: qr png generation failed", "err", err)
		} else {
			ch.QRCodeB64 = b64
		}
	}
	return ch, nil
}

func (c *InterClient) QueryCharge(ctx context.Context, txid string) (*Charge, error) {
	var resp struct {
		Status string `json:"status"`
		Valor  struct {
			Original string `json:"original"`
		} `json:"valor"`
		Pix []struct {
			EndToEndID string `json:"endToEndId"`
			Valor      string `json:"valor"`
			Pagador    struct {
				CPF string `json:"cpf"`
			} `json:"pagador"`
			Devolucoes []struct {
				RtrID  string `json:"rtrId"`
				Valor  string `json:"valor"`
				Status string `json:"status"`
			} `json:"devolucoes"`
		} `json:"pix"`
	}
	if err := c.do(ctx, http.MethodGet, fmt.Sprintf(pathCob, txid), nil, &resp); err != nil {
		return nil, err
	}
	ch := &Charge{
		Txid:   txid,
		Status: resp.Status,
		Amount: reaisToCentavos(resp.Valor.Original),
	}
	for _, p := range resp.Pix {
		payment := Payment{
			E2EID: p.EndToEndID, Amount: reaisToCentavos(p.Valor), PayerCPF: onlyDigits(p.Pagador.CPF),
		}
		for _, d := range p.Devolucoes {
			payment.Refunds = append(payment.Refunds, Refund{
				RtrID: d.RtrID, Amount: reaisToCentavos(d.Valor), Status: d.Status,
			})
		}
		ch.Payments = append(ch.Payments, payment)
	}
	if len(ch.Payments) > 0 {
		ch.PayerCPF = ch.Payments[0].PayerCPF
		ch.E2EID = ch.Payments[0].E2EID
		ch.Refunds = ch.Payments[0].Refunds
	}
	return ch, nil
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
		if isStatus(err, http.StatusNotFound) {
			return nil, fmt.Errorf("inter: pix key not registered: %w", ErrKeyNotFound)
		}
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
	// The devolução id must be unique and idempotent per refund. Service-level
	// keys commonly contain separators such as "sandbox_refund#...", which
	// Inter rejects. Preserve compliant historical IDs; map every other key to a
	// stable SHA-256-derived alphanumeric ID accepted by the provider.
	path := fmt.Sprintf(pathDevolucao, url.PathEscape(e2eID), interRefundID(idemKey))
	if err := c.do(ctx, http.MethodPut, path, body, &resp); err != nil {
		return nil, err
	}
	return &TransferResult{E2EID: e2eID, Status: resp.Status}, nil
}

// interRefundID returns an Inter-compatible, deterministic devolução id.
// Invalid service-level keys are represented by 140 bits of SHA-256 (35 hex
// characters), keeping collision risk negligible without exceeding Inter's
// path constraint.
func interRefundID(idemKey string) string {
	if len(idemKey) >= 1 && len(idemKey) <= interRefundIDMaxLength {
		valid := true
		for i := 0; i < len(idemKey); i++ {
			if !isASCIIAlphanumeric(idemKey[i]) {
				valid = false
				break
			}
		}
		if valid {
			return idemKey
		}
	}
	digest := sha256.Sum256([]byte(idemKey))
	return hex.EncodeToString(digest[:])[:interRefundIDMaxLength]
}

func isASCIIAlphanumeric(ch byte) bool {
	return ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9'
}

// Ping validates that a bearer was supplied for this call AND that the partner
// bank host is reachable. api owns the token lifecycle; pix-gateway only
// forwards what it receives, but a reachability probe catches a dead/blocked
// Inter endpoint (DNS, TLS, network ACL) that the bearer check alone would
// miss.
func (c *InterClient) Ping(ctx context.Context) error {
	if bearerFromContext(ctx) == "" {
		return fmt.Errorf("inter: ping requires an OAuth bearer (none supplied)")
	}
	u, err := url.Parse(c.base)
	if err != nil || u.Host == "" {
		return fmt.Errorf("inter: ping: bad base url %q: %w", c.base, err)
	}
	host := u.Host
	if u.Port() == "" {
		host = net.JoinHostPort(u.Hostname(), "443")
	}
	conn, err := net.DialTimeout("tcp", host, 5*time.Second)
	if err != nil {
		return fmt.Errorf("inter: ping: cannot reach %s: %w", host, err)
	}
	_ = conn.Close()
	return nil
}

// --- HTTP plumbing ---

func (c *InterClient) do(ctx context.Context, method, path string, body, out any) error {
	return c.doIdem(ctx, method, path, body, out, "")
}

func (c *InterClient) doIdem(ctx context.Context, method, path string, body, out any, idemKey string) error {
	token := bearerFromContext(ctx)
	if token == "" {
		return fmt.Errorf("inter: missing OAuth bearer on %s %s", method, path)
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
	// Never log the raw bank body: charge responses can contain payer PII and
	// error responses can echo sensitive request/provider details.
	slog.InfoContext(ctx, "inter response",
		"method", method,
		"path", path,
		"status", resp.StatusCode,
	)
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
