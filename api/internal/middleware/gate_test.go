package middleware

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
)

// gateApp mounts inject (sets claims) → gate → 200 handler, and returns the status.
func gateApp(t *testing.T, claims *Claims, gate fiber.Handler) int {
	t.Helper()
	app := fiber.New()
	app.Get("/x", func(c fiber.Ctx) error {
		c.Locals(ClaimsKey, claims)
		return c.Next()
	}, gate, func(c fiber.Ctx) error {
		return c.SendStatus(fiber.StatusOK)
	})
	resp, err := app.Test(httptest.NewRequest(fiber.MethodGet, "/x", nil))
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	return resp.StatusCode
}

func TestRequireScope(t *testing.T) {
	// M2M token (empty sid) with the scope → allowed.
	if got := gateApp(t, &Claims{SID: "", Scope: ScopeWalletCredit}, RequireScope(ScopeWalletCredit)); got != 200 {
		t.Errorf("M2M with scope: got %d want 200", got)
	}
	// User token (non-empty sid) even WITH the scope → forbidden (users never hit internal).
	if got := gateApp(t, &Claims{SID: "sess", Scope: ScopeWalletCredit}, RequireScope(ScopeWalletCredit)); got != 403 {
		t.Errorf("user token with scope: got %d want 403", got)
	}
	// M2M without the scope → forbidden.
	if got := gateApp(t, &Claims{SID: "", Scope: "other"}, RequireScope(ScopeWalletCredit)); got != 403 {
		t.Errorf("M2M without scope: got %d want 403", got)
	}
}

func TestRequireKYC(t *testing.T) {
	if got := gateApp(t, &Claims{KYCLevel: "verified"}, RequireKYC(KYCVerified)); got != 200 {
		t.Errorf("verified: got %d want 200", got)
	}
	if got := gateApp(t, &Claims{KYCLevel: "basic"}, RequireKYC(KYCVerified)); got != 403 {
		t.Errorf("basic vs verified: got %d want 403", got)
	}
	if got := gateApp(t, &Claims{KYCLevel: ""}, RequireKYC(KYCBasic)); got != 403 {
		t.Errorf("none vs basic: got %d want 403", got)
	}
	if got := gateApp(t, &Claims{KYCLevel: "basic"}, RequireKYC(KYCBasic)); got != 200 {
		t.Errorf("basic vs basic: got %d want 200", got)
	}
}

func TestRequireRecentMFA(t *testing.T) {
	now := time.Now().Unix()
	if got := gateApp(t, &Claims{LastMFAAt: now}, RequireRecentMFA(StepUpMaxAge)); got != 200 {
		t.Errorf("fresh mfa: got %d want 200", got)
	}
	if got := gateApp(t, &Claims{LastMFAAt: 0}, RequireRecentMFA(StepUpMaxAge)); got != 403 {
		t.Errorf("no mfa: got %d want 403", got)
	}
	stale := time.Now().Add(-10 * time.Minute).Unix()
	if got := gateApp(t, &Claims{LastMFAAt: stale}, RequireRecentMFA(StepUpMaxAge)); got != 403 {
		t.Errorf("stale mfa: got %d want 403", got)
	}
}
