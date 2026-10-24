package v1

import (
	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/api-commons/dynamo"
	"gopkg.aoctech.app/wallet/api/internal/middleware"
	"gopkg.aoctech.app/wallet/api/internal/repositories"
)

// The sale of Sandbox packs and goods left the wallet (Poker sells through Billing), but the
// purchases already made stay readable: these two routes list the caller's own old rows, read-only.

// listSandboxPurchases returns the caller's old Sandbox-pack purchase records, not ledger entries.
func (h *handlers) listSandboxPurchases(c fiber.Ctx) error {
	userID := middleware.GetUserID(c)
	limit, startKey, err := pageStart(c, userID)
	if err != nil {
		return sendProblem(c, err)
	}
	page, err := h.svc.SandboxPurchaseHistory(c.Context(), userID, limit, startKey)
	if err != nil {
		return sendProblem(c, err)
	}
	return sendPage(c, page, userID)
}

// listProductPurchases returns the caller's old product purchases. It exposes no client secret or
// webhook-delivery metadata.
func (h *handlers) listProductPurchases(c fiber.Ctx) error {
	userID := middleware.GetUserID(c)
	limit, startKey, err := pageStart(c, userID)
	if err != nil {
		return sendProblem(c, err)
	}
	page, err := h.svc.ProductPurchaseHistory(c.Context(), userID, limit, startKey)
	if err != nil {
		return sendProblem(c, err)
	}
	return sendPage(c, page, userID)
}

func sendPage[T any](c fiber.Ctx, page *repositories.Page[T], owner string) error {
	return c.JSON(dynamo.NewPage(page.Items, page.LastEvaluatedKey, owner))
}
