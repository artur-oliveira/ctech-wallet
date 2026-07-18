package middleware

import (
	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/api-commons/jwtverify"
)

// Fiber locals keys.
const (
	ClaimsKey = "claims"
	UserIDKey = "user_id"
)

// Claims holds the ctech-account access-token fields the wallet consumes.
// An empty SID marks an M2M client_credentials token (see ctech-account).
// Parsing lives in gopkg.aoctech.app/api-commons/jwtverify; this is a local
// alias so call sites don't need to import that package directly.
type Claims = jwtverify.Claims

// GetClaims returns the authenticated caller's claims from Fiber locals.
func GetClaims(c fiber.Ctx) *Claims {
	cl, _ := c.Locals(ClaimsKey).(*Claims)
	return cl
}

// GetUserID returns the authenticated caller's subject.
func GetUserID(c fiber.Ctx) string {
	if cl := GetClaims(c); cl != nil {
		return cl.Sub
	}
	return ""
}
