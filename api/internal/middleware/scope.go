package middleware

import (
	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
	"gopkg.aoctech.app/wallet/api/internal/problem"
)

// Scopes the wallet defines for its own internal callers (poker/dominó/billing,
// and pix-gateway's webhook Lambda).
const (
	ScopeWalletCredit      = "internal:wallet:credit"     // sandbox only
	ScopeWalletDebit       = "internal:wallet:debit"      // sandbox only
	ScopeWalletRealDebit   = "internal:wallet:debit-real" // real wallet — deliberately separate from sandbox debit
	ScopePixConfirmDeposit = "internal:wallet:confirm-deposit"

	// game wallet holds (skill-game integration, e.g. poker). Deliberately
	// separate scopes so a caller that only ever holds/releases never needs
	// cashout, and vice versa.

	// ScopeWalletGameHold hold game wallet value
	ScopeWalletGameHold = "internal:wallet:game-hold"
	// ScopeWalletGameCashout release game wallet value
	ScopeWalletGameCashout = "internal:wallet:game-cashout"
	// ScopeWalletGameStatus read a user's real-money eligibility (activation,
	// self-exclusion, limits) — consumed by skill games before buy-in
	ScopeWalletGameStatus = "internal:wallet:game-status"

	// ScopeWalletBalance reads a user's game+sandbox balance (real excluded) —
	// consumed by skill games to show the user how much they hold. Read-only,
	// deliberately separate from ScopeWalletGameStatus (eligibility, not balance).
	ScopeWalletBalance = "internal:wallet:balance"

	// ScopeWalletSandboxPurchase lets an M2M client (e.g. ctech-poker) open,
	// poll, and refund a direct PIX→sandbox-credits sale on a user's behalf —
	// same underlying flow as the user-facing /wallet/sandbox/purchases
	// routes, just caller-initiated. Deliberately its own scope, not reused
	// from ScopeWalletCredit: this creates a PIX charge and a purchase
	// record, not a bare ledger credit.
	ScopeWalletSandboxPurchase = "internal:wallet:sandbox-purchase"
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

// RequireUser rejects client_credentials tokens on user-facing routes. The
// account contract uses an empty sid for M2M tokens; accepting one here would
// blur the user/M2M trust boundary even when its sub cannot normally collide
// with a user ID.
func RequireUser(c fiber.Ctx) error {
	cl := GetClaims(c)
	if cl == nil {
		return problem.Unauthorized("credenciais ausentes").Send(c)
	}
	if cl.SID == "" {
		return problem.Forbidden("token de usuário obrigatório").Send(c)
	}
	return c.Next()
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
