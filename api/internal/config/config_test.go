package config

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	// Billing is the only rail: every Load needs its settings.
	for k, v := range map[string]string{"BILLING_URL": "https://billing-api.example", "BILLING_CLIENT_ID": "wallet",
		"BILLING_CLIENT_SECRET": "s", "BILLING_EVENT_SECRET": "e", "CTECH_URL": "https://account.example"} {
		_ = os.Setenv(k, v)
	}
	os.Exit(m.Run())
}

func TestLoadFailsClosedWithoutValkeyURLInProd(t *testing.T) {
	t.Setenv("ENVIRONMENT", "prod")
	t.Setenv("SERVICE_AUDIENCE", "https://wallet.aoctech.app")
	t.Setenv("CTECH_URL", "https://account.aoctech.app")
	t.Setenv("CTECH_ISSUER_URL", "https://account.aoctech.app")
	t.Setenv("CORS_ALLOWED_ORIGINS", "https://wallet.aoctech.app")
	t.Setenv("TABLE_PREFIX", "prod")
	t.Setenv("ERASURE_QUEUE_URL", "https://sqs.us-east-1.amazonaws.com/868899309401/prod-ctech-wallet-erasure")
	t.Setenv("VALKEY_REVOCATION_URL", "redis://valkey.internal:6379")
	t.Setenv("ERASURE_CLIENT_ID", "wallet-erasure")
	t.Setenv("ERASURE_CLIENT_SECRET", "s")
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
	t.Setenv("ERASURE_QUEUE_URL", "https://sqs.us-east-1.amazonaws.com/868899309401/prod-ctech-wallet-erasure")
	t.Setenv("VALKEY_REVOCATION_URL", "redis://valkey.internal:6379")
	t.Setenv("ERASURE_CLIENT_ID", "wallet-erasure")
	t.Setenv("ERASURE_CLIENT_SECRET", "s")
	t.Setenv("VALKEY_URL", "redis://valkey.internal:6379/0")

	if _, err := Load(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadSucceedsWithoutValkeyURLOutsideProd(t *testing.T) {
	t.Setenv("ENVIRONMENT", "dev")
	t.Setenv("TABLE_PREFIX", "dev")
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
	t.Setenv("CTECH_ISSUER_URL", "")
	t.Setenv("CORS_ALLOWED_ORIGINS", "")
	t.Setenv("VALKEY_URL", "")
	// Not an API-only guard: every settlement leg reconcile submits needs a
	// destination wallet, so this one applies to both processes in production.

	if _, err := LoadReconcile(); err != nil {
		t.Fatalf("unexpected reconcile config error: %v", err)
	}
}

func prodEnv(t *testing.T) {
	t.Helper()
	t.Setenv("ENVIRONMENT", "prod")
	t.Setenv("SERVICE_AUDIENCE", "https://wallet.aoctech.app")
	t.Setenv("CTECH_URL", "https://account.aoctech.app")
	t.Setenv("CTECH_ISSUER_URL", "https://account.aoctech.app")
	t.Setenv("CORS_ALLOWED_ORIGINS", "https://wallet.aoctech.app")
	t.Setenv("TABLE_PREFIX", "prod")
	t.Setenv("VALKEY_URL", "redis://valkey.internal:6379/2")
	t.Setenv("ERASURE_QUEUE_URL", "https://sqs.us-east-1.amazonaws.com/868899309401/prod-ctech-wallet-erasure")
	t.Setenv("VALKEY_REVOCATION_URL", "redis://valkey.internal:6379")
	t.Setenv("ERASURE_CLIENT_ID", "wallet-erasure")
	t.Setenv("ERASURE_CLIENT_SECRET", "s")
}

// D1/D3: in prod the wallet must neither skip the deletion lock nor read the
// revocation list from the wrong Valkey DB.
func TestLoadFailsClosedWithoutErasureConfigInProd(t *testing.T) {
	for _, unset := range []string{"ERASURE_QUEUE_URL", "VALKEY_REVOCATION_URL", "ERASURE_CLIENT_ID", "ERASURE_CLIENT_SECRET"} {
		prodEnv(t)
		t.Setenv(unset, "")
		if _, err := Load(); err == nil {
			t.Errorf("Load succeeded in prod with %s unset", unset)
		}
	}
}

func TestLoadReconcileNeedsNoErasureConfig(t *testing.T) {
	prodEnv(t)
	t.Setenv("ERASURE_QUEUE_URL", "")
	t.Setenv("VALKEY_REVOCATION_URL", "")
	if _, err := LoadReconcile(); err != nil {
		t.Fatalf("LoadReconcile: %v", err)
	}
}

func TestBillingSettingsAreRequired(t *testing.T) {
	t.Setenv("ENVIRONMENT", "dev")
	t.Setenv("TABLE_PREFIX", "t")
	t.Setenv("BILLING_URL", "")
	if _, err := LoadReconcile(); err == nil {
		t.Fatal("without BILLING_URL the reconcile process must not start")
	}
	t.Setenv("BILLING_URL", "https://billing-api.example")
	if _, err := LoadReconcile(); err != nil {
		t.Fatalf("the reconcile process needs no secret in its environment: %v", err)
	}

	t.Setenv("BILLING_CLIENT_SECRET", "")
	t.Setenv("BILLING_EVENT_SECRET", "")
	if _, err := Load(); err == nil {
		t.Fatal("the API must refuse to start without the client secret and the event secret")
	}
	t.Setenv("BILLING_CLIENT_SECRET", "s")
	if _, err := Load(); err == nil {
		t.Fatal("the event secret alone missing must fail too")
	}
	t.Setenv("BILLING_EVENT_SECRET", "e")
	if _, err := Load(); err != nil {
		t.Fatalf("complete settings: %v", err)
	}
}
