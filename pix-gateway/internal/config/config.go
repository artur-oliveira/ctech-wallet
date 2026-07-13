// Package config holds the 12-Factor environment configuration for pix-gateway.
package config

import (
	"fmt"

	"github.com/caarlos0/env/v11"
)

// Config configures both Lambda functions (outbound + webhook). Not every
// field is used by both — cmd/webhook additionally needs WalletAPIURL,
// CtechURL, PixGatewayClientID/Secret to call api's confirm-deposit endpoint;
// cmd/outbound only needs the Inter fields.
type Config struct {
	AWSRegion string `env:"AWS_REGION" envDefault:"us-east-1"`
	Env       string `env:"ENVIRONMENT" envDefault:"dev"`

	// PIX / Inter partner bank. Mirrors api/internal/config/config.go's fields —
	// this is the only place they now live.
	InterBaseURL      string `env:"INTER_BASE_URL" envDefault:"https://cdpj.partners.bancointer.com.br"`
	InterClientID     string `env:"INTER_CLIENT_ID"`
	InterClientSecret string `env:"INTER_CLIENT_SECRET"`
	InterPixKey       string `env:"INTER_PIX_KEY"`

	// ctech-account, for the webhook Lambda's own M2M token (client_credentials,
	// scope internal:pix:confirm-deposit) — a distinct client from api's own
	// WALLET_CLIENT_ID (see cross-project contract, root CLAUDE.md).
	CtechURL               string `env:"CTECH_URL"`
	PixGatewayClientID     string `env:"PIX_GATEWAY_CLIENT_ID"`
	PixGatewayClientSecret string `env:"PIX_GATEWAY_CLIENT_SECRET"`

	// WalletAPIURL is api's public base URL (e.g. https://wallet.aoctech.app) —
	// the webhook Lambda calls POST {WalletAPIURL}/v1.0/internal/pix/confirm-deposit.
	WalletAPIURL string `env:"WALLET_API_URL"`
}

// Load reads config from environment variables.
func Load() (*Config, error) {
	cfg := &Config{}
	if err := env.Parse(cfg); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	return cfg, nil
}
