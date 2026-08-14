package config

import "testing"

func TestLoadFailsClosedWithoutValkeyURLInProd(t *testing.T) {
	t.Setenv("ENVIRONMENT", "prod")
	t.Setenv("SERVICE_AUDIENCE", "https://wallet.aoctech.app")
	t.Setenv("CTECH_URL", "https://account.aoctech.app")
	t.Setenv("CTECH_ISSUER_URL", "https://account.aoctech.app")
	t.Setenv("CORS_ALLOWED_ORIGINS", "https://wallet.aoctech.app")
	t.Setenv("TABLE_PREFIX", "prod")
	t.Setenv("PIX_GATEWAY_FUNCTION_NAME", "prod-pix-gateway-outbound")
	t.Setenv("VALKEY_URL", "")

	if _, err := Load(); err == nil {
		t.Fatal("expected Load to fail closed with VALKEY_URL unset in prod")
	}
}

func TestLoadSucceedsWithValkeyURLInProd(t *testing.T) {
	t.Setenv("ENVIRONMENT", "prod")
	t.Setenv("SERVICE_AUDIENCE", "https://wallet.aoctech.app")
	t.Setenv("CTECH_URL", "https://account.aoctech.app")
	t.Setenv("CTECH_ISSUER_URL", "https://account.aoctech.app")
	t.Setenv("CORS_ALLOWED_ORIGINS", "https://wallet.aoctech.app")
	t.Setenv("TABLE_PREFIX", "prod")
	t.Setenv("PIX_GATEWAY_FUNCTION_NAME", "prod-pix-gateway-outbound")
	t.Setenv("VALKEY_URL", "redis://valkey.internal:6379/0")

	if _, err := Load(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadSucceedsWithoutValkeyURLOutsideProd(t *testing.T) {
	t.Setenv("ENVIRONMENT", "dev")
	t.Setenv("TABLE_PREFIX", "dev")
	t.Setenv("PIX_GATEWAY_FUNCTION_NAME", "dev-pix-gateway-outbound")
	t.Setenv("VALKEY_URL", "")

	if _, err := Load(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// The scheduled reconciler does not serve HTTP, verify JWTs, or use Valkey
// for wallet locking. API-only production guards must therefore not prevent it
// from starting, while Load keeps those same guards fail-closed for the server.
func TestLoadReconcileSucceedsWithoutAPIRuntimeConfigInProd(t *testing.T) {
	t.Setenv("ENVIRONMENT", "prod")
	t.Setenv("TABLE_PREFIX", "prod")
	t.Setenv("PIX_GATEWAY_FUNCTION_NAME", "prod-pix-gateway-outbound")
	t.Setenv("CTECH_ISSUER_URL", "")
	t.Setenv("CORS_ALLOWED_ORIGINS", "")
	t.Setenv("VALKEY_URL", "")

	if _, err := LoadReconcile(); err != nil {
		t.Fatalf("unexpected reconcile config error: %v", err)
	}
}
