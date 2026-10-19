package middleware

import (
	"strings"

	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/api-commons/problem"
	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
	"gopkg.aoctech.app/wallet/api/internal/walletproblem"
)

// Scopes the wallet defines for user-delegated and internal callers. Public
// wallet:* scopes describe capabilities a user may delegate to another OAuth
// client. internal:wallet:* scopes remain service-to-service only.
const (
	ScopeWalletStateRead        = "wallet:state:read"
	ScopeWalletTermsWrite       = "wallet:terms:write"
	ScopeWalletBalancesRead     = "wallet:balances:read"
	ScopeWalletLedgerRead       = "wallet:ledger:read"
	ScopeWalletDepositsWrite    = "wallet:deposits:write"
	ScopeWalletWithdrawalsWrite = "wallet:withdrawals:write"

	ScopeWalletCredit    = "internal:wallet:credit"     // sandbox only
	ScopeWalletDebit     = "internal:wallet:debit"      // sandbox only
	ScopeWalletRealDebit = "internal:wallet:debit-real" // real wallet — deliberately separate from sandbox debit

	// ScopeWalletHold holds, releases and cashes out on `real` for a skill game (Poker). One scope
	// for the whole duty of that caller.
	ScopeWalletHold = "internal:wallet:hold"

	// ScopeWalletBalance reads a user's game+sandbox balance (real excluded) —
	// consumed by skill games to show the user how much they hold. Read-only,
	// deliberately separate from ScopeWalletGameStatus (eligibility, not balance).
	ScopeWalletBalance = "internal:wallet:balance"

	ScopeWalletErasureEligibility = "internal:wallet:erasure-eligibility"
)

const walletPublicScopePrefix = "wallet:"

var walletPublicScopes = []string{
	ScopeWalletStateRead,
	ScopeWalletTermsWrite,
	ScopeWalletBalancesRead,
	ScopeWalletLedgerRead,
	ScopeWalletDepositsWrite,
	ScopeWalletWithdrawalsWrite,
}

// WalletPublicScopes returns every public capability enforced by this API.
// Returning a copy keeps tests and callers from mutating the policy table.
func WalletPublicScopes() []string {
	return append([]string(nil), walletPublicScopes...)
}

// AllowsUserScope applies delegated-scope authorization while preserving the
// existing first-party Wallet SPA during migration. Its legacy tokens carry no
// wallet:* scopes and keep their current access; as soon as a token carries any
// wallet:* scope, it is constrained to the exact capability requested here.
func AllowsUserScope(cl *Claims, required string) bool {
	if cl == nil {
		return false
	}
	delegated := false
	for _, scope := range cl.Scopes() {
		if strings.HasPrefix(scope, walletPublicScopePrefix) {
			delegated = true
			if scope == required {
				return true
			}
		}
	}
	return !delegated
}

// RequireUserScope narrows user/session tokens that carry delegated wallet:*
// permissions. Register it after RequireUser so M2M tokens never reach it.
func RequireUserScope(required string) fiber.Handler {
	return func(c fiber.Ctx) error {
		if !AllowsUserScope(GetClaims(c), required) {
			return problem.Forbidden("scope insuficiente para esta operação da carteira").Send(c)
		}
		return c.Next()
	}
}

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
				return walletproblem.KYCNotVerified().Send(c)
			}
		case KYCBasic:
			if cl.KYCLevel == "" {
				return walletproblem.KYCNotVerified().Send(c)
			}
		}
		return c.Next()
	}
}
