package config

import (
	"fmt"
	"log/slog"

	"github.com/caarlos0/env/v11"
)

// Config holds the 12-Factor environment configuration for the wallet API.
// Unlike ctech-dfe, there is no multi-tenant/SEFAZ config; instead there is
// PIX/Inter and the wallet's own M2M client used to call ctech-account KYC.
type Config struct {
	// Server
	AppVersion string `env:"APP_VERSION" envDefault:"0.0.1"`
	Port       int    `env:"PORT" envDefault:"8000"`
	Env        string `env:"ENVIRONMENT" envDefault:"dev"`

	// GamblingEnabled gates the entire game-wallet surface: activation and both
	// real↔game transfer routes. It stays FALSE in production until the personal
	// limit engine ships — a user must never be able to activate a gambling wallet
	// with no limits configured, which is the one thing this design forbids.
	// With it off, those routes are not registered at all and 404.
	GamblingEnabled bool `env:"GAMBLING_ENABLED" envDefault:"false"`

	ReadTimeout        int64    `env:"READ_TIMEOUT" envDefault:"10"`
	IdleTimeout        int64    `env:"IDLE_TIMEOUT" envDefault:"60"`
	WriteTimeout       int64    `env:"WRITE_TIMEOUT" envDefault:"10"`
	TrustedProxies     []string `env:"TRUSTED_PROXIES"`
	CorsAllowedOrigins []string `env:"CORS_ALLOWED_ORIGINS"`

	// AWS
	AWSRegion        string `env:"AWS_REGION" envDefault:"us-east-1"`
	TablePrefix      string `env:"TABLE_PREFIX,required"`
	DynamoDBEndpoint string `env:"DYNAMODB_ENDPOINT"` // local override

	// Auth (ctech-account)
	CtechURL        string `env:"CTECH_URL"`
	CtechJWKSURL    string `env:"CTECH_JWKS_URL"`
	ServiceAudience string `env:"SERVICE_AUDIENCE" envDefault:"https://wallet-api.aoctech.app"` // expected aud claim; empty = no audience check (transition only)

	// Wallet's own M2M client (to call account internal:kyc)
	WalletClientID     string `env:"WALLET_CLIENT_ID"`
	WalletClientSecret string `env:"WALLET_CLIENT_SECRET"`

	// PixGatewayFunctionName is pix-gateway's outbound Lambda — api invokes it
	// synchronously for every PixClient call. api no longer talks to Inter
	// directly (see docs/specs/2026-07-13-pix-gateway-lambda-design.md).
	PixGatewayFunctionName string `env:"PIX_GATEWAY_FUNCTION_NAME,required"`

	// Cache / lock
	RedisURL string `env:"VALKEY_URL"` // Redis/Valkey URL — optional; falls back to in-memory
}

// Load reads config from environment variables.
func Load() (*Config, error) {
	cfg := &Config{}
	if err := env.Parse(cfg); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	if cfg.CtechJWKSURL == "" && cfg.CtechURL != "" {
		cfg.CtechJWKSURL = cfg.CtechURL + "/.well-known/jwks.json"
	}
	if cfg.ServiceAudience == "" && cfg.Env == "prod" {
		// Fail closed: without an audience check, any RS256 token the identity
		// provider signs for any client would be accepted here. Never safe in prod.
		return nil, fmt.Errorf("config: SERVICE_AUDIENCE must be set in production so the aud claim is verified")
	}
	if cfg.CtechURL == "" && cfg.Env == "prod" {
		slog.Warn("CTECH_URL is empty in production — the iss claim is not being checked")
	}
	return cfg, nil
}
