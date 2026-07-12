// Package kycclient calls ctech-account's internal KYC API using the wallet's
// own M2M client_credentials token (scope internal:kyc). Used to promote a user
// to verified on first deposit and to read the verified CPF for payer/withdrawal
// matching.
package kycclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/artur-oliveira/ctech-wallet/api/internal/config"
)

const (
	pathToken        = "/v1.0/token"
	pathKYCConfirm   = "/v1.0/internal/kyc/confirm"
	pathKYCGet       = "/v1.0/internal/kyc/%s"
	scopeInternalKYC = "internal:kyc"
)

// ErrCPFMismatch is returned when account rejects a confirm because the CPF does
// not match the declared KYC record (HTTP 409 kyc-cpf-mismatch).
var ErrCPFMismatch = fmt.Errorf("kyc: cpf mismatch")

// KYC is the unmasked identity record account returns to internal callers.
type KYC struct {
	Level     string `json:"level"`
	CPF       string `json:"cpf"`
	LegalName string `json:"legal_name"`
	BirthDate string `json:"birth_date"`
}

// Client talks to ctech-account's internal KYC endpoints.
type Client struct {
	base   string
	http   *http.Client
	tokens *tokenManager
}

// New builds the KYC client. base is the account URL (CTECH_URL).
func New(cfg *config.Config) *Client {
	httpClient := &http.Client{Timeout: 10 * time.Second}
	base := strings.TrimRight(cfg.CtechURL, "/")
	return &Client{
		base: base,
		http: httpClient,
		tokens: &tokenManager{
			client:       httpClient,
			tokenURL:     base + pathToken,
			clientID:     cfg.WalletClientID,
			clientSecret: cfg.WalletClientSecret,
			scope:        scopeInternalKYC,
		},
	}
}

// Confirm promotes a user to verified after matching the declared CPF.
// Idempotent at account; a CPF mismatch returns ErrCPFMismatch.
func (c *Client) Confirm(ctx context.Context, userID, cpf string) error {
	body, _ := json.Marshal(map[string]string{"user_id": userID, "cpf": cpf})
	req, err := c.authedRequest(ctx, http.MethodPost, c.base+pathKYCConfirm, strings.NewReader(string(body)), true)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusConflict {
		raw, _ := io.ReadAll(resp.Body)
		if strings.Contains(string(raw), "cpf-mismatch") {
			return ErrCPFMismatch
		}
		return fmt.Errorf("kyc confirm conflict: %s", string(raw))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("kyc confirm: status %d: %s", resp.StatusCode, string(raw))
	}
	return nil
}

// Get reads the user's KYC record (unmasked CPF) for payer/withdrawal matching.
func (c *Client) Get(ctx context.Context, userID string) (*KYC, error) {
	req, err := c.authedRequest(ctx, http.MethodGet, fmt.Sprintf(c.base+pathKYCGet, url.PathEscape(userID)), nil, false)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("kyc get: status %d: %s", resp.StatusCode, string(raw))
	}
	var k KYC
	if err := json.NewDecoder(resp.Body).Decode(&k); err != nil {
		return nil, err
	}
	return &k, nil
}

func (c *Client) authedRequest(ctx context.Context, method, urlStr string, body io.Reader, jsonBody bool) (*http.Request, error) {
	token, err := c.tokens.get(ctx)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, urlStr, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if jsonBody {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

// tokenManager fetches and caches the wallet's M2M client_credentials token.
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
		"grant_type":    {"client_credentials"},
		"client_id":     {t.clientID},
		"client_secret": {t.clientSecret},
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
		return "", fmt.Errorf("account token: status %d: %s", resp.StatusCode, string(raw))
	}
	var tr struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(raw, &tr); err != nil {
		return "", err
	}
	t.token = tr.AccessToken
	t.expiry = time.Now().Add(time.Duration(tr.ExpiresIn-30) * time.Second)
	return t.token, nil
}
