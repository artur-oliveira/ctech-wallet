package v1

import (
	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/api-commons/dynamo"
	"gopkg.aoctech.app/api-commons/idempotency"
	"gopkg.aoctech.app/wallet/api/internal/awsclient"
	"gopkg.aoctech.app/wallet/api/internal/config"
	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
	"gopkg.aoctech.app/wallet/api/internal/middleware"
)

// newMoneyIdempotency is go-common idempotency on the user routes that create money
// movements (deposit, withdrawal): the first answer is replayed for a retried key,
// a concurrent duplicate is refused (409), and the same key with another body is a
// 409 conflict. It adds to, and does not replace, the in-transaction IDEM# guard of
// the ledger write (that one makes the effect exactly-once even if the answer is lost).
//
// Required (400 without the key) and fail CLOSED: when the store is unreachable the
// request is refused with a retryable 503, never run unprotected. The key lives in
// the caller's own namespace (JWT sub); register it after auth.
func newMoneyIdempotency(clients *awsclient.Clients, cfg *config.Config) fiber.Handler {
	var store idempotency.Store = &idempotency.MemoryStore{} // route tests only: Register without AWS clients
	if clients != nil && clients.DynamoDB != nil {
		store = idempotency.NewDynamoStore(dynamo.NewBase(clients.DynamoDB, cfg.TablePrefix, wallet.TableHTTPIdempotency))
	}
	return idempotency.New(idempotency.Config{
		Store:    store,
		Scope:    func(c fiber.Ctx) (string, bool) { sub := middleware.GetUserID(c); return sub, sub != "" },
		Required: true,
	})
}
