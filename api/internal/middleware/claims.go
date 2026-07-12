package middleware

import (
	"strings"

	"github.com/gofiber/fiber/v3"
)

// Fiber locals keys and claim constants.
const (
	ClaimsKey = "claims"
	UserIDKey = "user_id"
)

// Claims holds the ctech-account access-token fields the wallet consumes.
// An empty SID marks an M2M client_credentials token (see ctech-account).
type Claims struct {
	Sub       string // user_id (or client_id for M2M)
	Scope     string // space-joined scope string
	SID       string // session id; empty for M2M tokens
	AZP       string // OAuth client_id
	KYCLevel  string // "" | "basic" | "verified"
	LastMFAAt int64  // unix seconds of the last MFA proof; 0 if absent
}

// HasScope reports whether the space-separated scope string contains want.
func (cl *Claims) HasScope(want string) bool {
	for _, s := range strings.Fields(cl.Scope) {
		if s == want {
			return true
		}
	}
	return false
}

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
