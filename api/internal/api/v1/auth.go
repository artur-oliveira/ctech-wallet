package v1

import (
	"github.com/artur-oliveira/ctech-wallet/api/internal/middleware"

	"github.com/gofiber/fiber/v3"
)

// getMe returns the caller's wallet-side state — currently just whether they
// have accepted the current terms addendum. The UI gates the whole app on this.
func (h *handlers) getMe(c fiber.Ctx) error {
	me, err := h.userSvc.Me(c.Context(), middleware.GetUserID(c))
	if err != nil {
		return sendProblem(c, err)
	}
	return c.JSON(me)
}

// acceptTermsAddendum records acceptance of the current addendum version.
func (h *handlers) acceptTermsAddendum(c fiber.Ctx) error {
	if err := h.userSvc.AcceptTermsAddendum(c.Context(), middleware.GetUserID(c)); err != nil {
		return sendProblem(c, err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}
