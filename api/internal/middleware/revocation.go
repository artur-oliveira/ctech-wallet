package middleware

import (
	"errors"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/api-commons/cache"
	"gopkg.aoctech.app/api-commons/jwtverify"
	"gopkg.aoctech.app/api-commons/problem"
)

// RequireUnrevoked re-checks ctech-account's JWT revocation list for a route
// that changes a user's money or consent, and FAILS CLOSED (saga protocol §5,
// decision D3): an unreachable list is a 503, never a pass. The auth
// middleware already verified the token and ran the same check fail-open;
// this gives jwtverify.VerifyClaimsStrict's semantics without parsing the
// token twice. A nil backend disables it (route tests only: config.Load
// requires VALKEY_REVOCATION_URL in prod). Register after the auth middleware.
func RequireUnrevoked(revocation cache.Backend) fiber.Handler {
	return func(c fiber.Ctx) error {
		if revocation == nil {
			return c.Next()
		}
		cl := GetClaims(c)
		if cl == nil {
			return problem.Unauthorized("credenciais ausentes").Send(c)
		}
		err := jwtverify.CheckRevoked(c.Context(), revocation, cl.Sub, cl.IssuedAt)
		switch {
		case err == nil:
			return c.Next()
		case errors.Is(err, jwtverify.ErrTokenRevoked):
			return problem.Unauthorized("sessão encerrada").Send(c)
		default:
			return problem.ServiceUnavailable("não foi possível validar a sessão; tente novamente").WithCause(err).Send(c)
		}
	}
}
