package v1

import (
	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/api-commons/erasure"
)

// erasureEligibility answers ctech-account's deletion pre-check (saga
// protocol §4.1). An unknown sub is eligible; an error is a 500 problem,
// which account treats as "not eligible, retry".
func (h *handlers) erasureEligibility(c fiber.Ctx) error {
	blockers, err := h.erasureSvc.Blockers(c.Context(), c.Params("sub"))
	if err != nil {
		return sendProblem(c, err)
	}
	return c.JSON(erasure.NewEligibility(blockers...))
}
