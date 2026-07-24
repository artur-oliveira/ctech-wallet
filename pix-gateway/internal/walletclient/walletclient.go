// Package walletclient calls api's internal confirm-deposit endpoint using
// pix-gateway's own M2M client_credentials token (scope
// internal:pix:confirm-deposit). This is the only way money moves as a result
// of the webhook: pix-gateway itself never touches the ledger.
package walletclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"gopkg.aoctech.app/api-commons/oauth2client"
	"gopkg.aoctech.app/wallet/pix-gateway/internal/config"
)

const (
	pathToken           = "/v1.0/token"
	pathConfirmDeposit  = "/v1.0/internal/pix/confirm-deposit"
	scopeConfirmDeposit = "internal:wallet:confirm-deposit"
)

// Client calls api's confirm-deposit endpoint.
type Client struct {
	base   string
	http   *http.Client
	tokens *oauth2client.TokenManager
}

// New builds the client. clientSecret is passed explicitly (loaded from SSM at
// cold start by cmd/webhook, not stored in cfg) rather than trusting
// cfg.PixGatewayClientSecret's env-var value, mirroring how cmd/outbound
// resolves the Inter client secret.
func New(cfg *config.Config, clientSecret string) *Client {
	httpClient := &http.Client{Timeout: 10 * time.Second}
	tokenURL := strings.TrimRight(cfg.CtechURL, "/") + pathToken
	return &Client{
		base:   strings.TrimRight(cfg.WalletAPIURL, "/"),
		http:   httpClient,
		tokens: oauth2client.New(httpClient, tokenURL, cfg.PixGatewayClientID, clientSecret, scopeConfirmDeposit),
	}
}

// ConfirmDeposit calls api's confirm-deposit endpoint for txid. api re-derives
// amount/status/devolução from its own re-query of Inter — this call never
// carries those (Financial Safety Invariant 11: the webhook is a wake-up
// signal, never the source of truth, and neither is this call). payerCPF/
// payerName are the one exception: Inter's charge re-query no longer returns
// the payer, so the webhook body (this call's only source) forwards them for
// api to persist and use in its CPF-match check, never for crediting.
func (c *Client) ConfirmDeposit(ctx context.Context, txid, payerCPF, payerName string) error {
	body, err := json.Marshal(map[string]string{"txid": txid, "payer_cpf": payerCPF, "payer_name": payerName})
	if err != nil {
		return err
	}
	token, err := c.tokens.Get(ctx)
	if err != nil {
		return fmt.Errorf("walletclient: get token: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+pathConfirmDeposit, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("walletclient: confirm-deposit status %d: %s", resp.StatusCode, string(raw))
	}
	return nil
}
