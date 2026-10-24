package v1

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"

	"gopkg.aoctech.app/api-commons/problem"
	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
	"gopkg.aoctech.app/wallet/api/internal/repositories"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/gofiber/fiber/v3"
)

// HeaderIdempotencyKey is the client-supplied idempotency header on user mutations.
const HeaderIdempotencyKey = "Idempotency-Key"

const (
	queryParamCursor    = "cursor"
	queryParamLimit     = "limit"
	defaultHistoryLimit = 50
	maxHistoryLimit     = 200
)

// PaginatedResponse is the standard envelope for list endpoints.
type PaginatedResponse struct {
	Items      any     `json:"items"`
	NextCursor *string `json:"next_cursor"`
	HasNext    bool    `json:"has_next"`
}

type cursorPayload struct {
	Key map[string]any `json:"k"`
}

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

func intQuery(c fiber.Ctx, key string, def int) int {
	s := c.Query(key)
	if s == "" {
		return def
	}
	var v int
	if _, err := fmt.Sscanf(s, "%d", &v); err != nil {
		return def
	}
	return v
}

func historyLimit(c fiber.Ctx) int {
	limit := intQuery(c, queryParamLimit, defaultHistoryLimit)
	if limit < 1 {
		return defaultHistoryLimit
	}
	if limit > maxHistoryLimit {
		return maxHistoryLimit
	}
	return limit
}

// buildNextCursor encodes a DynamoDB LastEvaluatedKey as an opaque next-page cursor.
func buildNextCursor(key map[string]types.AttributeValue) *string {
	if len(key) == 0 {
		return nil
	}
	var plainKey map[string]any
	if err := attributevalue.UnmarshalMap(key, &plainKey); err != nil {
		return nil
	}
	raw, err := json.Marshal(cursorPayload{Key: plainKey})
	if err != nil {
		return nil
	}
	s := base64.StdEncoding.EncodeToString(raw)
	return &s
}

// decodeCursor extracts the DynamoDB ExclusiveStartKey from an opaque cursor.
func decodeCursor(cursor string) map[string]types.AttributeValue {
	if cursor == "" {
		return nil
	}
	raw, err := base64.StdEncoding.DecodeString(cursor)
	if err != nil {
		return nil
	}
	var payload cursorPayload
	if err := json.Unmarshal(raw, &payload); err != nil || len(payload.Key) == 0 {
		return nil
	}
	avKey, err := attributevalue.MarshalMap(payload.Key)
	if err != nil {
		return nil
	}
	return avKey
}

// sendStatement writes a ledger page as a paginated JSON response.
func sendStatement(c fiber.Ctx, result *repositories.QueryResult) error {
	entries, err := repositories.DecodeItems[wallet.LedgerEntry](result.Items)
	if err != nil {
		return sendProblem(c, err)
	}
	items := make([]ledgerEntryView, 0, len(entries))
	for _, e := range entries {
		items = append(items, newLedgerEntryView(e))
	}
	return c.JSON(PaginatedResponse{
		Items:      items,
		NextCursor: buildNextCursor(result.LastEvaluatedKey),
		HasNext:    len(result.LastEvaluatedKey) > 0,
	})
}
