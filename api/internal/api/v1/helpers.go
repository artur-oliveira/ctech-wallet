package v1

import (
	"context"
	"errors"

	"gopkg.aoctech.app/api-commons/dynamo"
	"gopkg.aoctech.app/api-commons/problem"
	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
	"gopkg.aoctech.app/wallet/api/internal/repositories"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/gofiber/fiber/v3"
)

// HeaderIdempotencyKey is the client-supplied idempotency header on user mutations.
const HeaderIdempotencyKey = "Idempotency-Key"

const queryParamCursor, queryParamLimit = "cursor", "limit"

// sendProblem writes an RFC 7807 Problem response. Detects *problem.Problem for
// the correct status; all other errors become 500.
func sendProblem(c fiber.Ctx, err error) error {
	if p, ok := errors.AsType[*problem.Problem](err); ok {
		return p.Send(c)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return problem.GatewayTimeout("a requisição demorou demais; tente novamente").WithCause(err).Send(c)
	}
	return problem.InternalServer("erro interno").WithCause(err).Send(c)
}

// requireIdempotencyKey reads the Idempotency-Key header, or returns a 400 problem.
func requireIdempotencyKey(c fiber.Ctx) (string, *problem.Problem) {
	k := c.Get(HeaderIdempotencyKey)
	if k == "" {
		return "", problem.BadRequest("cabeçalho Idempotency-Key obrigatório")
	}
	if len(k) > MaxIdempotencyKeyLength {
		return "", problem.BadRequest("Idempotency-Key excede o tamanho máximo")
	}
	return k, nil
}

// pageStart reads the list query: the page size (go-common policy: default 25, max 100) and the
// ExclusiveStartKey from the opaque cursor, which must have been issued for owner. A malformed or
// foreign cursor is a 400 (never a silent restart from page 1).
func pageStart(c fiber.Ctx, owner string) (int, map[string]types.AttributeValue, error) {
	startKey, err := dynamo.DecodeCursor(c.Query(queryParamCursor), owner)
	return dynamo.ParseLimit(c.Query(queryParamLimit)), startKey, err
}

// sendStatement writes a ledger page as the shared {data, next_cursor} envelope; owner is the wallet.
func sendStatement(c fiber.Ctx, result *repositories.QueryResult, owner string) error {
	entries, err := repositories.DecodeItems[wallet.LedgerEntry](result.Items)
	if err != nil {
		return sendProblem(c, err)
	}
	items := make([]ledgerEntryView, 0, len(entries))
	for _, e := range entries {
		items = append(items, newLedgerEntryView(e))
	}
	return c.JSON(dynamo.NewPage(items, result.LastEvaluatedKey, owner))
}
