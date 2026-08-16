package middleware

import (
	"strings"

	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
	"gopkg.aoctech.app/wallet/api/internal/problem"
)

// Scopes the wallet defines for user-delegated and internal callers. Public
// wallet:* scopes describe capabilities a user may delegate to another OAuth
// client. internal:wallet:* scopes remain service-to-service only.
const (
	ScopeWalletStateRead             = "wallet:state:read"
	ScopeWalletTermsWrite            = "wallet:terms:write"
	ScopeWalletBalancesRead          = "wallet:balances:read"
	ScopeWalletLedgerRead            = "wallet:ledger:read"
	ScopeWalletDepositsWrite         = "wallet:deposits:write"
	ScopeWalletWithdrawalsWrite      = "wallet:withdrawals:write"
	ScopeWalletSandboxPurchasesRead  = "wallet:sandbox-purchases:read"
	ScopeWalletSandboxPurchasesWrite = "wallet:sandbox-purchases:write"
	ScopeWalletProductPurchasesRead  = "wallet:product-purchases:read"
	ScopeWalletGameWrite             = "wallet:game:write"
	ScopeWalletGamblingRead          = "wallet:gambling:read"
	ScopeWalletGamblingWrite         = "wallet:gambling:write"
	ScopeWalletCustodyWrite          = "wallet:custody:write"

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

	// ScopeWalletProductPurchase lets an M2M client sell a generic digital
	// product for real PIX money with no ledger effect — deliberately its
	// own scope, not reused from ScopeWalletSandboxPurchase: materially
	// different blast radius (docs/specs/2026-08-12-product-purchase-skus.md).
	ScopeWalletProductPurchase = "internal:wallet:product-purchase"

	// ScopeWalletChargeAmount lets an M2M client open a PIX charge for an amount
	// it supplies itself, instead of a catalogue price.
	//
	// It is a new scope and not a wider ScopeWalletProductPurchase, and that is
	// the whole security argument. The catalogue is a fraud defense: a client
	// holding the product scope can only ever sell a R$ 4,90 item, whatever it
	// asks for. Accepting an amount field under that same scope would hand every
	// existing holder — ctech-poker among them — the ability to name its own
	// price the day the field lands. Removing the catalogue for one caller must
	// not remove it for all of them.
	//
	// Granted to ctech-billing alone, whose amounts are arbitrary by construction
	// (proration and metered usage), and bounded by M2MClient.MaxChargeCents
	// rather than by a catalogue.
	ScopeWalletChargeAmount = "internal:wallet:charge-amount"
)

const walletPublicScopePrefix = "wallet:"

var walletPublicScopes = []string{
	ScopeWalletStateRead,
	ScopeWalletTermsWrite,
	ScopeWalletBalancesRead,
	ScopeWalletLedgerRead,
	ScopeWalletDepositsWrite,
	ScopeWalletWithdrawalsWrite,
	ScopeWalletSandboxPurchasesRead,
	ScopeWalletSandboxPurchasesWrite,
	ScopeWalletProductPurchasesRead,
	ScopeWalletGameWrite,
	ScopeWalletGamblingRead,
	ScopeWalletGamblingWrite,
	ScopeWalletCustodyWrite,
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
