package v1

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/wallet/api/internal/repositories"
)

const exportPageSize = 100

// ResponsibleExporter pages the responsible-gambling state out of the users table.
type ResponsibleExporter interface {
	ExportPage(ctx context.Context, start map[string]types.AttributeValue, limit int) ([]repositories.ExportedUser, []repositories.ExportedCarriedExclusion, map[string]types.AttributeValue, error)
}

// WithResponsibleExport mounts the one-off export Poker imports when it takes the
// gambling rules over. An option of Register for the same reason as
// WithBillingEvents: it belongs inside the /internal group's auth.
func WithResponsibleExport(e ResponsibleExporter) Option {
	return func(o *registerOptions) { o.responsibleExport = e }
}

// responsibleExport serves one page. M2M, scope internal:wallet:responsible-export.
func (h *handlers) responsibleExport(c fiber.Ctx) error {
	users, carried, next, err := h.exporter.ExportPage(c.Context(), decodeCursor(c.Query(queryParamCursor)), exportPageSize)
	if err != nil {
		return sendProblem(c, err)
	}
	if users == nil {
		users = []repositories.ExportedUser{}
	}
	if carried == nil {
		carried = []repositories.ExportedCarriedExclusion{}
	}
	return c.JSON(fiber.Map{"users": users, "carried_exclusions": carried, "next_cursor": buildNextCursor(next)})
}
