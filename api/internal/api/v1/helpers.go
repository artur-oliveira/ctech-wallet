package v1

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/artur-oliveira/ctech-wallet/api/internal/problem"
	"github.com/artur-oliveira/ctech-wallet/api/internal/repositories"
	"github.com/artur-oliveira/ctech-wallet/api/internal/validation"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/gofiber/fiber/v3"
)

// HeaderIdempotencyKey is the client-supplied idempotency header on user mutations.
const HeaderIdempotencyKey = "Idempotency-Key"

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
	return problem.InternalServer(err.Error()).Send(c)
}

// bindJSON strictly decodes the JSON body into dst (rejecting unknown fields)
// then runs struct validation. Returns nil on success, or a *problem.Problem.
func bindJSON[T any](c fiber.Ctx, dst *T) *problem.Problem {
	dec := json.NewDecoder(bytes.NewReader(c.Body()))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		// The client-facing message stays generic — it must not echo the parse
		// error, which can reveal internal struct field names. But swallowing it
		// entirely left every bad-request rejection unauditable (e.g. an M2M
		// caller sending a field this deployed version doesn't know yet), so log
		// the real cause server-side only. Never log the raw body: request
		// payloads carry payer CPF and other PII that must not land in logs.
		slog.WarnContext(c.Context(), "bind json failed", "path", c.Path(), "err", err)
		return problem.BadRequest("corpo JSON inválido")
	}
	return validation.Struct(dst)
}

// requireIdempotencyKey reads the Idempotency-Key header, or returns a 400 problem.
func requireIdempotencyKey(c fiber.Ctx) (string, *problem.Problem) {
	k := c.Get(HeaderIdempotencyKey)
	if k == "" {
		return "", problem.BadRequest("cabeçalho Idempotency-Key obrigatório")
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
	items := make([]map[string]any, 0, len(result.Items))
	for _, it := range result.Items {
		var m map[string]any
		if err := attributevalue.UnmarshalMap(it, &m); err != nil {
			return sendProblem(c, err)
		}
		items = append(items, m)
	}
	return c.JSON(PaginatedResponse{
		Items:      items,
		NextCursor: buildNextCursor(result.LastEvaluatedKey),
		HasNext:    len(result.LastEvaluatedKey) > 0,
	})
}
