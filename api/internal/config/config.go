package config

import (
	"fmt"

	"github.com/caarlos0/env/v11"
)

// Config holds the 12-Factor environment configuration for the wallet API.
// Unlike ctech-dfe, there is no multi-tenant/SEFAZ config; instead there is
// PIX/Inter and the wallet's own M2M client used to call ctech-account KYC.
type Config struct {
	// Server
	AppVersion string `env:"APP_VERSION" envDefault:"0.0.1"`
	Port       int    `env:"PORT" envDefault:"8002"`
	Env        string `env:"ENVIRONMENT" envDefault:"dev"`

	// GamblingEnabled gates the entire game-wallet surface: activation and both
	// real↔game transfer routes. It stays FALSE in production until the personal
	// limit engine ships — a user must never be able to activate a gambling wallet
	// with no limits configured, which is the one thing this design forbids.
	// With it off, those routes are not registered at all and 404.
	GamblingEnabled bool `env:"GAMBLING_ENABLED" envDefault:"false"`

	// AsaasParentWalletID is Asaas's walletId for CTech's own parent/master
	// account — the settlement destination for the game-funded
	// sandbox-purchase settlement leg (plan §5.2, §9.1a).
	AsaasParentWalletID string `env:"ASAAS_PARENT_WALLET_ID"`

	// AsaasFreeReceiptsPerMonth caps how many PIX receipts one subaccount may
	// take in a calendar month. Asaas gives every account 100 free static-QR
	// receipts per month and charges per receipt beyond that, so the default
	// sits just below the free ceiling — see
	// docs/specs/2026-08-30-asaas-only-deposits.md.
	AsaasFreeReceiptsPerMonth int64 `env:"ASAAS_FREE_RECEIPTS_PER_MONTH" envDefault:"95"`

	// AsaasMasterAccountID is CTech's own account id at the provider. It exists
	// so an inbound payment webhook for the verification fee is distinguishable
	// from a user subaccount's deposit.
	AsaasMasterAccountID string `env:"ASAAS_MASTER_ACCOUNT_ID"`

	// AsaasMasterPixKey is that account's static EVP key — the verification-fee
	// QR is built on it, so the fee lands where the provider debits its own
	// subaccount charge from.
	AsaasMasterPixKey string `env:"ASAAS_MASTER_PIX_KEY"`

	// AsaasVerificationFeeCents is the one-off fee a user pays to have their
	// custody subaccount opened. Configured, not a constant: it tracks the
	// provider's own price and must change without a deploy.
	AsaasVerificationFeeCents int64 `env:"ASAAS_VERIFICATION_FEE_CENTS" envDefault:"1290"`

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
	CtechIssuerURL  string `env:"CTECH_ISSUER_URL"`
	CtechJWKSURL    string `env:"CTECH_JWKS_URL"`
	ServiceAudience string `env:"SERVICE_AUDIENCE" envDefault:"https://wallet.aoctech.app"` // expected aud claim; empty = no audience check (transition only)

	// Wallet's own M2M client (to call account's internal:account:kyc KYC status endpoint)
	WalletClientID     string `env:"WALLET_CLIENT_ID"`
	WalletClientSecret string `env:"WALLET_CLIENT_SECRET"`

	// PixGatewayFunctionName is pix-gateway's outbound Lambda — api invokes it
	// synchronously for every PixClient call. api no longer talks to Inter
	// directly (see docs/specs/2026-07-13-pix-gateway-lambda-design.md).
	PixGatewayFunctionName string `env:"PIX_GATEWAY_FUNCTION_NAME,required"`

	// Cache / lock
	RedisURL string `env:"VALKEY_URL"` // Redis/Valkey URL — optional; falls back to in-memory
}

// load parses the shared process configuration and enforces requirements that
// apply to both the HTTP server and the scheduled reconciler.
func load() (*Config, error) {
	cfg := &Config{}
	if err := env.Parse(cfg); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	if cfg.CtechJWKSURL == "" && cfg.CtechURL != "" {
		cfg.CtechJWKSURL = cfg.CtechURL + "/.well-known/jwks.json"
	}
	if cfg.AsaasParentWalletID == "" && cfg.Env == "prod" {
		// Fail closed: every settlement/fee-sweep leg needs a destination — an
		// empty parent wallet ID would either panic deep in a money-out path or
		// (worse) submit a transfer with an empty WalletID.
		return nil, fmt.Errorf("config: ASAAS_PARENT_WALLET_ID must be set in production")
	}
	if cfg.AsaasFreeReceiptsPerMonth <= 0 {
		return nil, fmt.Errorf("config: ASAAS_FREE_RECEIPTS_PER_MONTH must be positive")
	}
	if cfg.AsaasVerificationFeeCents <= 0 {
		return nil, fmt.Errorf("config: ASAAS_VERIFICATION_FEE_CENTS must be positive")
	}
	return cfg, nil
}

// Load reads and validates the HTTP API's configuration. Auth, CORS, and
// fleet-wide Valkey locking are server concerns and remain fail-closed in
// production.
func Load() (*Config, error) {
	cfg, err := load()
	if err != nil {
		return nil, err
	}
	if cfg.ServiceAudience == "" && cfg.Env == "prod" {
		// Fail closed: without an audience check, any RS256 token the identity
		// provider signs for any client would be accepted here. Never safe in prod.
		return nil, fmt.Errorf("config: SERVICE_AUDIENCE must be set in production so the aud claim is verified")
	}
	if cfg.CtechIssuerURL == "" && cfg.Env == "prod" {
		// Fail closed: without an issuer check, any RS256 token the identity
		// provider signs (for any audience) would be accepted here if its aud
		// happens to match. Never safe in prod — mirror the ServiceAudience guard.
		return nil, fmt.Errorf("config: CTECH_ISSUER_URL must be set in production so the iss claim is verified")
	}
	if len(cfg.CorsAllowedOrigins) == 0 && cfg.Env == "prod" {
		return nil, fmt.Errorf("config: CORS_ALLOWED_ORIGINS must be set in production")
	}
	if cfg.AsaasMasterPixKey == "" && cfg.Env == "prod" {
		// Fail closed: without it the verification fee cannot be collected, and
		// opening subaccounts anyway bills CTech once per user with nothing to
		// notice it. An API-only guard — the reconciler never opens a
		// subaccount, so LoadReconcile does not carry it.
		return nil, fmt.Errorf("config: ASAAS_MASTER_PIX_KEY must be set in production so the verification fee can be collected")
	}
	if cfg.RedisURL == "" && cfg.Env == "prod" {
		// Fail closed: an empty VALKEY_URL in prod means per-wallet locking
		// silently degrades to an in-memory store that is NOT shared across
		// the ASG's other instances — Invariant #4 ("one operation per wallet
		// at a time") stops holding fleet-wide with no signal. Never boot into
		// that state; mirror the SERVICE_AUDIENCE/CTECH_URL guards above.
		return nil, fmt.Errorf("config: VALKEY_URL must be set in production so wallet locking is fleet-shared")
	}
	return cfg, nil
}

// LoadReconcile reads configuration for the scheduled reconciliation process.
// It deliberately skips HTTP-only issuer/audience/CORS validation and the
// API's fleet-wide Valkey requirement: this process verifies no JWTs, serves
// no browser requests, and constructs its financial-operation locker in
// memory. If VALKEY_URL is present it is used only for best-effort WebSocket
// broadcasts.
func LoadReconcile() (*Config, error) {
	return load()
}
