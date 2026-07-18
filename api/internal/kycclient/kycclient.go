// Package kycclient calls ctech-account's internal KYC API using the wallet's
// own M2M client_credentials token (scope internal:wallet:confirm-deposit).
// KYC promotion is manual-review-only on account's side now; this client only
// reads the verified CPF and level for payer/withdrawal matching.
package kycclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"gopkg.aoctech.app/api-commons/oauth2client"
	"gopkg.aoctech.app/wallet/api/internal/config"
)

const (
	pathToken  = "/v1.0/token"
	pathKYCGet = "/v1.0/internal/kyc/%s"
	scopeKYC   = "internal:account:kyc"
)

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
	tokens *oauth2client.TokenManager
}

// New builds the KYC client. base is the account URL (CTECH_URL).
func New(cfg *config.Config) *Client {
	httpClient := &http.Client{Timeout: 10 * time.Second}
	base := strings.TrimRight(cfg.CtechURL, "/")
	return &Client{
		base:   base,
		http:   httpClient,
		tokens: oauth2client.New(httpClient, base+pathToken, cfg.WalletClientID, cfg.WalletClientSecret, scopeKYC),
	}
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
	token, err := c.tokens.Get(ctx)
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
