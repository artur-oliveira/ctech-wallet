// Package middleware provides Fiber middleware for JWT RS256 auth (JWKS from
// ctech-account), M2M scope gating, KYC gating, and step-up MFA.
package middleware

import (
	"strings"

	"gopkg.aoctech.app/api-commons/cache"
	"gopkg.aoctech.app/api-commons/jwtverify"
	"gopkg.aoctech.app/wallet/api/internal/problem"

	"github.com/gofiber/fiber/v3"
)

// Verifier validates RS256 access tokens issued by ctech-account against its
// JWKS. The JWKS-fetch and claims-parsing mechanics live in the shared
// gopkg.aoctech.app/api-commons/jwtverify package; this wrapper only adds the
// Fiber-facing bits (locals wiring, RFC 7807 error responses) specific to the wallet.
type Verifier struct {
	*jwtverify.Verifier
}

func NewVerifier(jwksURL, audience, issuer string, cacheBackend cache.Backend) *Verifier {
	return &Verifier{jwtverify.NewVerifier(jwksURL, audience, issuer, cacheBackend)}
}

// Middleware validates the Bearer token and stores the typed claims in locals.
func (v *Verifier) Middleware() fiber.Handler {
	return func(c fiber.Ctx) error {
		authHeader := c.Get("Authorization")
		if !strings.HasPrefix(authHeader, "Bearer ") {
			return problem.Unauthorized("missing bearer token").Send(c)
		}
		tokenStr := strings.TrimPrefix(authHeader, "Bearer ")

		claims, err := v.VerifyClaims(c.Context(), tokenStr)
		if err != nil || claims.Sub == "" {
			return problem.Unauthorized("invalid credentials").Send(c)
		}

		c.Locals(ClaimsKey, claims)
		c.Locals(UserIDKey, claims.Sub)
		return c.Next()
	}
}
