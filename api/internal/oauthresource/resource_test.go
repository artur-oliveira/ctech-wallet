package oauthresource

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func TestProtectedResourceMetadataAdvertisesOnlyPublicScopes(t *testing.T) {
	app := fiber.New()
	Register(app, "https://wallet.example.test", "https://accounts.example.test")
	resp, err := app.Test(httptest.NewRequest("GET", "/.well-known/oauth-protected-resource", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var body struct {
		Resource string   `json:"resource"`
		Scopes   []string `json:"scopes_supported"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Resource != "https://wallet.example.test" {
		t.Fatalf("unexpected metadata: %#v", body)
	}
	if len(body.Scopes) != 13 {
		t.Fatalf("scopes_supported = %v, want 13 public scopes", body.Scopes)
	}
	for _, scope := range body.Scopes {
		if len(scope) >= len("internal:") && scope[:len("internal:")] == "internal:" {
			t.Fatalf("internal scope advertised: %q", scope)
		}
	}
}
