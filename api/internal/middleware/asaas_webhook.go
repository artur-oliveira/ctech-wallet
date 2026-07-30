package middleware

import (
	"crypto/subtle"

	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/wallet/api/internal/problem"
)

// HeaderAsaasAccessToken is the header Asaas echoes back on every inbound
// webhook call, carrying the static token configured at webhook creation
// (plan §2.3) — not a body signature, same shape as Inter's ?hmac= query
// param.
const HeaderAsaasAccessToken = "asaas-access-token"

// RequireAsaasWebhookToken gates the Asaas webhook routes on the configured
// static token. An empty configured token always refuses — this must never
// silently become "any token accepted" if the SSM fetch was skipped (e.g.
// AsaasCustodyEnabled=false in an environment with no parameter provisioned).
func RequireAsaasWebhookToken(token string) fiber.Handler {
	return func(c fiber.Ctx) error {
		got := c.Get(HeaderAsaasAccessToken)
		if token == "" || got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
			return problem.Unauthorized("token de webhook inválido").Send(c)
		}
		return c.Next()
	}
}
