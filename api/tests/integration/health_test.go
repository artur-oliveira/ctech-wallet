//go:build integration

package integration_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	apiv1 "github.com/artur-oliveira/ctech-wallet/api/internal/api/v1"
	"github.com/artur-oliveira/ctech-wallet/api/internal/awsclient"
	"github.com/artur-oliveira/ctech-wallet/api/internal/cache"
	"github.com/artur-oliveira/ctech-wallet/api/internal/config"
	"github.com/artur-oliveira/ctech-wallet/api/internal/pix"

	"github.com/gofiber/fiber/v3"
)

const (
	testAppVersion  = "1.2.3"
	multiStatusCode = 207
)

type healthBody struct {
	Status    string `json:"status"`
	ReleaseID string `json:"releaseId"`
	ServiceID string `json:"serviceId"`
	Checks    map[string]struct {
		Status        string  `json:"status"`
		ObservedValue float64 `json:"observedValue"`
		ObservedUnit  string  `json:"observedUnit"`
	} `json:"checks"`
}

// healthApp wires the health routes against DynamoDB-local. The JWKS verifier is
// absent (no account stub here), so it reports warn — which is exactly the
// degraded-but-serving path we want to assert.
func healthApp(t *testing.T) *fiber.App {
	t.Helper()
	app := fiber.New()
	cfg := &config.Config{AppVersion: testAppVersion, TablePrefix: tablePrefix}
	apiv1.RegisterHealth(app.Group("/v1.0"), &awsclient.Clients{DynamoDB: db}, cache.NewMemoryBackend(16), pix.NewFake(), nil, cfg)
	return app
}

func doHealth(t *testing.T, app *fiber.App, path string) (*http.Response, healthBody) {
	t.Helper()
	resp, err := app.Test(httptest.NewRequest(http.MethodGet, path, nil))
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	var body healthBody
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode %s: %v (%s)", path, err, raw)
	}
	return resp, body
}

func TestHealthCheckReportsDependencies(t *testing.T) {
	resp, body := doHealth(t, healthApp(t), "/v1.0/health-check")

	// DynamoDB reachable + JWKS verifier absent → degraded, still serving.
	if resp.StatusCode != multiStatusCode {
		t.Fatalf("status = %d, want %d (warn)", resp.StatusCode, multiStatusCode)
	}
	if body.Status != "warn" {
		t.Fatalf("overall = %s, want warn", body.Status)
	}
	if body.ReleaseID != testAppVersion {
		t.Fatalf("releaseId = %q, want %q — APP_VERSION must surface in the health check", body.ReleaseID, testAppVersion)
	}

	// DynamoDB is the load-bearing check: it must pass against DynamoDB-local.
	dynamo, ok := body.Checks["dynamodb"]
	if !ok {
		t.Fatal("checks.dynamodb missing")
	}
	if dynamo.Status != "pass" {
		t.Fatalf("dynamodb = %s, want pass", dynamo.Status)
	}
	if dynamo.ObservedUnit != "ms" {
		t.Fatalf("dynamodb unit = %q, want ms", dynamo.ObservedUnit)
	}

	for name, want := range map[string]string{"cache": "pass", "pix": "pass", "jwks": "warn"} {
		got, ok := body.Checks[name]
		if !ok {
			t.Fatalf("checks.%s missing", name)
		}
		if got.Status != want {
			t.Fatalf("%s = %s, want %s", name, got.Status, want)
		}
	}

	for _, name := range []string{"uptime", "cpu", "memory"} {
		if _, ok := body.Checks[name]; !ok {
			t.Fatalf("checks.%s missing", name)
		}
	}
}

// The ALB probe must stay dependency-free: a degraded dependency must not cycle
// the instance out of the target group.
func TestLivenessIgnoresDependencies(t *testing.T) {
	resp, body := doHealth(t, healthApp(t), "/v1.0/health")

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if body.Status != "pass" || body.ReleaseID != testAppVersion {
		t.Fatalf("liveness = %+v, want status pass and releaseId %s", body, testAppVersion)
	}
	if len(body.Checks) != 0 {
		t.Fatalf("liveness must not run dependency checks, got %v", body.Checks)
	}
}
