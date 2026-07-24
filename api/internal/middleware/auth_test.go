package middleware

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"gopkg.aoctech.app/api-commons/cache"
)

const testKID = "test-key-1"

// newJWKSServer returns an RSA key and an httptest server serving its public JWKS.
// JWKS-fetch/rotation/kid-rejection mechanics are covered by
// gopkg.aoctech.app/api-commons/jwtverify's own tests; this file only checks
// that the wallet's Verifier wrapper wires the shared verifier correctly.
func newJWKSServer(t *testing.T) (*rsa.PrivateKey, *httptest.Server) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	n := base64.RawURLEncoding.EncodeToString(key.N.Bytes())
	e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())
	body, _ := json.Marshal(map[string]any{"keys": []map[string]any{{"kid": testKID, "kty": "RSA", "n": n, "e": e}}})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return key, srv
}

func signToken(t *testing.T, key *rsa.PrivateKey, claims jwt.MapClaims) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = testKID
	s, err := tok.SignedString(key)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return s
}

func TestVerifier_VerifyClaimsExtractsAllFields(t *testing.T) {
	key, srv := newJWKSServer(t)
	v := NewVerifier(srv.URL, "https://wallet-api.aoctech.app", "https://accounts.aoctech.app", cache.NewMemoryBackend(10))

	now := time.Now().Unix()
	token := signToken(t, key, jwt.MapClaims{
		"sub":         "user_1",
		"scope":       "openid internal:wallet:credit",
		"sid":         "sess_1",
		"azp":         "poker",
		"kyc_level":   "verified",
		"last_mfa_at": now,
		"aud":         "https://wallet-api.aoctech.app",
		"iss":         "https://accounts.aoctech.app",
		"exp":         now + 900,
	})

	cl, err := v.VerifyClaims(context.Background(), token)
	if err != nil {
		t.Fatalf("VerifyClaims: %v", err)
	}
	if cl.Sub != "user_1" || cl.KYCLevel != "verified" || cl.LastMFAAt != now {
		t.Fatalf("bad claims: %+v", cl)
	}
	if !cl.HasScope("internal:wallet:credit") || cl.HasScope("internal:wallet:debit") {
		t.Fatalf("scope parsing wrong: %q", cl.Scope)
	}
}

func TestVerifier_RejectsInvalidToken(t *testing.T) {
	_, srv := newJWKSServer(t)
	v := NewVerifier(srv.URL, "", "", cache.NewMemoryBackend(10))

	if _, err := v.VerifyClaims(context.Background(), "not-a-jwt"); err == nil {
		t.Fatal("expected malformed token to be rejected")
	}
}
