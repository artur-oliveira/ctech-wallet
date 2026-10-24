package v1

import (
	"net/http"
	"testing"
)

// Request bodies bind through go-common validation: unknown fields and trailing
// data are 400, a failed rule is 422, an oversize body is 413 (never parsed).
func TestMoneyBodiesBindStrictly(t *testing.T) {
	app := descriptionApp(&handlers{})
	base := `{"user_id":"u1","amount":100,"idempotency_key":"k1"}`
	cases := []struct {
		name, body string
		want       int
	}{
		{"unknown field", `{"user_id":"u1","amount":100,"idempotency_key":"k1","x":1}`, http.StatusBadRequest},
		{"trailing data", base + `{}`, http.StatusBadRequest},
		{"zero amount", `{"user_id":"u1","amount":0,"idempotency_key":"k1"}`, http.StatusUnprocessableEntity},
		{"missing key", `{"user_id":"u1","amount":100}`, http.StatusUnprocessableEntity},
		{"oversize", `{"user_id":"` + string(make([]byte, 1<<20+1)) + `"}`, http.StatusRequestEntityTooLarge},
	}
	for _, tc := range cases {
		if got := postRaw(t, app, "/sandbox/credit", tc.body); got != tc.want {
			t.Errorf("%s: status %d, want %d", tc.name, got, tc.want)
		}
	}
}
