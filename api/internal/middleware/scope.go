package middleware

import (
	"github.com/artur-oliveira/ctech-wallet/api/internal/domain/wallet"
	"github.com/artur-oliveira/ctech-wallet/api/internal/problem"
	"github.com/gofiber/fiber/v3"
)

// Scopes the wallet defines for its own internal callers (poker/dominó/billing,
// and pix-gateway's webhook Lambda).
const (
	ScopeWalletCredit      = "internal:wallet:credit"
	ScopeWalletDebit       = "internal:wallet:debit"
	ScopePixConfirmDeposit = "internal:wallet:confirm-deposit"
)

// KYC levels are defined once, in the domain — services gate on them too.
const (
	KYCBasic    = wallet.KYCBasic
	KYCVerified = wallet.KYCVerified
)

// RequireScope gates an /internal route on an M2M client_credentials token.
// A non-empty SID means a user/session token — never allowed on internal routes,
// even if it somehow carries the scope. Must be registered after the auth middleware.
func RequireScope(scope string) fiber.Handler {
	return func(c fiber.Ctx) error {
		cl := GetClaims(c)
		if cl == nil || cl.SID != "" || !cl.HasScope(scope) {
			return problem.Forbidden("scope insuficiente para rota interna").Send(c)
		}
		return c.Next()
	}
}

// RequireKYC gates a route on a minimum KYC level from the token's kyc_level claim.
// min is KYCBasic (any verification started) or KYCVerified (fully verified).
func RequireKYC(min string) fiber.Handler {
	return func(c fiber.Ctx) error {
		cl := GetClaims(c)
		if cl == nil {
			return problem.Unauthorized("credenciais ausentes").Send(c)
		}
		switch min {
		case KYCVerified:
			if cl.KYCLevel != KYCVerified {
				return problem.KYCNotVerified().Send(c)
			}
		case KYCBasic:
			if cl.KYCLevel == "" {
				return problem.KYCNotVerified().Send(c)
			}
		}
		return c.Next()
	}
}
