package v1

import (
	"time"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/api-commons/cache"
	"gopkg.aoctech.app/api-commons/ratelimit"
	"gopkg.aoctech.app/api-commons/ratelimit/fiberlimit"
	"gopkg.aoctech.app/wallet/api/internal/middleware"
)

// Per-user limits on the user routes (JWT sub). Conservative on purpose: they stop a runaway
// client or a scripted abuse, not a person using the app. Retired (410) routes and the internal
// M2M routes (Poker's hot path, keyed by a body field, already bounded by scopes and wallet locks)
// are not limited.
const (
	// ReadLimitPerMinute: balances, ledger, purchase lists, /auth/me and terms acceptance.
	// 2 req/s per user covers the UI's refreshes. Fail OPEN: availability beats abuse protection.
	ReadLimitPerMinute = 120
	// MoneyLimitPerMinute: deposits and withdrawals (they open a Billing invoice or payout).
	// Daily caps already bound the real volume; this only stops retry storms. Fail CLOSED:
	// a silent limiter on a money route is an unbounded fraud hole.
	MoneyLimitPerMinute = 10

	limitWindow = time.Minute

	prefixRead  = "wallet:read"
	prefixMoney = "wallet:money"
)

// userLimits are the two per-user policies as route middleware.
type userLimits struct {
	read, money fiber.Handler
}

// newUserLimits counts in the wallet's own Valkey (DB 2) when c is one, else in memory
// (dev and tests; not shared across replicas).
func newUserLimits(c cache.Backend) userLimits {
	var counter ratelimit.Counter = ratelimit.NewMemoryCounter()
	if rb, ok := c.(*cache.RedisBackend); ok {
		counter = ratelimit.NewValkeyCounter(rb.Client())
	}
	return userLimits{
		read:  userPolicy(counter, prefixRead, ReadLimitPerMinute, ratelimit.FailOpen),
		money: userPolicy(counter, prefixMoney, MoneyLimitPerMinute, ratelimit.FailClosed),
	}
}

func userPolicy(counter ratelimit.Counter, prefix string, max int64, mode ratelimit.FailMode) fiber.Handler {
	return fiberlimit.NewPolicy(fiberlimit.PolicyConfig{
		Limiter: &ratelimit.Limiter{Counter: counter, Prefix: prefix, Max: max, Window: limitWindow, FailMode: mode},
		Key:     func(c fiber.Ctx) string { return ratelimit.Key(middleware.GetUserID(c)) },
	})
}
