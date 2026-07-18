package middleware

import (
	"time"

	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/wallet/api/internal/problem"
)

// StepUpMaxAge is the freshness window for step-up-protected routes (withdrawals),
// mirroring ctech-account's RequireRecentMFA(5m).
const StepUpMaxAge = 5 * time.Minute

// RequireRecentMFA rejects requests whose token lacks an MFA proof newer than
// maxAge. Stateless: it reads only the last_mfa_at JWT claim, so after a
// successful step-up challenge the client must silent-refresh to update claims.
// Must be registered after the auth middleware.
func RequireRecentMFA(maxAge time.Duration) fiber.Handler {
	return func(c fiber.Ctx) error {
		cl := GetClaims(c)
		if cl == nil || cl.LastMFAAt == 0 || time.Since(time.Unix(cl.LastMFAAt, 0)) > maxAge {
			return problem.StepUpRequired(int(maxAge.Seconds())).Send(c)
		}
		return c.Next()
	}
}
