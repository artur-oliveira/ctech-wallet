package v1

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gopkg.aoctech.app/api-commons/cache"
	"gopkg.aoctech.app/wallet/api/internal/awsclient"
	"gopkg.aoctech.app/wallet/api/internal/config"

	"github.com/gofiber/fiber/v3"
)

// testCacheSize is the in-memory cache size the route tests use.
const testCacheSize = 8

func healthTestApp() *fiber.App {
	app := fiber.New()
	// No DynamoDB client: the primary check must fail closed (503) while liveness stays 200.
	RegisterHealth(app.Group("/v1.0"), &awsclient.Clients{}, cache.NewMemoryBackend(testCacheSize), nil, &config.Config{AppVersion: "test-rel"})
	return app
}

func getHealth(t *testing.T, path string) (int, string, string) {
	t.Helper()
	resp, err := healthTestApp().Test(httptest.NewRequest(http.MethodGet, path, nil))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header.Get("Content-Type"), string(b)
}

// Liveness is the probe nginx/deploy and the UI hit: dependency-free, 200, and
// it carries releaseId and serviceId.
func TestLivenessIsDependencyFree(t *testing.T) {
	code, ct, body := getHealth(t, "/v1.0/health")
	if code != http.StatusOK {
		t.Fatalf("liveness = %d, want 200", code)
	}
	if !strings.HasPrefix(ct, "application/health+json") {
		t.Fatalf("content-type = %q", ct)
	}
	for _, want := range []string{`"status":"pass"`, `"releaseId":"test-rel"`, `"serviceId":"CTech Wallet"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("liveness body %s missing %s", body, want)
		}
	}
}

// DynamoDB is the only primary check: unreachable means 503; the missing JWKS
// verifier is only a warning.
func TestReportFailsClosedWithoutDynamo(t *testing.T) {
	code, _, body := getHealth(t, "/v1.0/health-check")
	if code != http.StatusServiceUnavailable {
		t.Fatalf("report = %d, want 503 (%s)", code, body)
	}
	if !strings.Contains(body, `"jwks"`) || !strings.Contains(body, `"dynamodb"`) {
		t.Fatalf("report missing components: %s", body)
	}
}
