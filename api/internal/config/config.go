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

	// RequireDescription makes the `description` field mandatory (3-255 chars
	// after trimming) on every M2M money route. Keep it OFF until ctech-poker and
	// ctech-billing send real descriptions, then turn it on per environment.
	RequireDescription bool `env:"REQUIRE_DESCRIPTION" envDefault:"false"`

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

	// Cache / lock
	RedisURL string `env:"VALKEY_URL"` // Redis/Valkey URL — optional; falls back to in-memory

	// ValkeyRevocationURL is the SHARED Valkey base URL (DB 0), where
	// ctech-account writes the JWT revocation list. VALKEY_URL is the wallet's
	// own DB 2 and cannot see those keys (saga protocol §5).
	ValkeyRevocationURL string `env:"VALKEY_REVOCATION_URL"`

	// ErasureQueueURL is the wallet's account-deletion SQS queue. Set = the
	// deletion lock, the purge consumer and the self-exclusion carry-over
	// are ON; empty = OFF (local dev only).
	ErasureQueueURL string `env:"ERASURE_QUEUE_URL"`

	// ErasureClientID/Secret is the DEDICATED confidential client that acks
	// purges to ctech-account (scope internal:account:erasure-ack only). It is
	// deliberately not WALLET_CLIENT_ID, which holds internal:account:kyc.
	ErasureClientID     string `env:"ERASURE_CLIENT_ID"`
	ErasureClientSecret string `env:"ERASURE_CLIENT_SECRET"`

	// GameWalletMerged switches the game-facing operations (holds, cash-outs,
	// balance reads) from the `game` wallet to `real` and retires the ring-fence
	// routes. Turn it on only after `merge-game` has emptied every game wallet.
	GameWalletMerged bool `env:"GAME_WALLET_MERGED" envDefault:"false"`

	// BillingURL is Billing's API base (https://billing-api.aoctech.app).
	BillingURL string `env:"BILLING_URL"`
	// BillingClientID/Secret is the wallet's OAuth client for Billing (scopes
	// billing:customers:read|write, billing:invoices:read|write and
	// internal:billing:payouts-read|write).
	BillingClientID     string `env:"BILLING_CLIENT_ID"`
	BillingClientSecret string `env:"BILLING_CLIENT_SECRET"`
	// BillingEventSecret signs the invoice.paid wake-ups Billing sends.
	BillingEventSecret string `env:"BILLING_EVENT_SECRET"`
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
	// The client secret is not required here: the reconcile Lambda reads it from SSM itself
	// (a SecureString never goes into a function's environment).
	if cfg.BillingURL == "" || cfg.BillingClientID == "" || cfg.CtechURL == "" {
		return nil, fmt.Errorf("config: BILLING_URL, BILLING_CLIENT_ID and CTECH_URL are required")
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
	if cfg.BillingClientSecret == "" || cfg.BillingEventSecret == "" {
		// The API verifies Billing's events and calls it: both secrets or nothing.
		return nil, fmt.Errorf("config: BILLING_CLIENT_SECRET and BILLING_EVENT_SECRET are required")
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
	if cfg.RedisURL == "" && cfg.Env == "prod" {
		// Fail closed: an empty VALKEY_URL in prod means per-wallet locking
		// silently degrades to an in-memory store that is NOT shared across
		// the ASG's other instances — Invariant #4 ("one operation per wallet
		// at a time") stops holding fleet-wide with no signal. Never boot into
		// that state; mirror the SERVICE_AUDIENCE/CTECH_URL guards above.
		return nil, fmt.Errorf("config: VALKEY_URL must be set in production so wallet locking is fleet-shared")
	}
	if cfg.ErasureQueueURL == "" && cfg.Env == "prod" {
		// Fail closed: without it a user in deletion grace keeps moving money
		// through M2M routes (decision D1), and no purge ever runs.
		return nil, fmt.Errorf("config: ERASURE_QUEUE_URL must be set in production (account-deletion lock)")
	}
	if cfg.ValkeyRevocationURL == "" && cfg.Env == "prod" {
		return nil, fmt.Errorf("config: VALKEY_REVOCATION_URL must be set in production (JWT revocation list, Valkey DB 0)")
	}
	if cfg.ErasureQueueURL != "" && (cfg.ErasureClientID == "" || cfg.ErasureClientSecret == "") {
		// Without the ack client every purge would run and never be acked.
		return nil, fmt.Errorf("config: ERASURE_CLIENT_ID and ERASURE_CLIENT_SECRET must be set when ERASURE_QUEUE_URL is")
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
