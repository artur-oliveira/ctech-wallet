package v1

import (
	"log/slog"

	"gopkg.aoctech.app/wallet/api/internal/middleware"
	"gopkg.aoctech.app/wallet/api/internal/services"

	"github.com/gofiber/fiber/v3"
)

// meResponse is Me plus the pre-flight deposit gate. Composed here rather than
// on services.Me because the answer spans two services: consent state lives in
// UserService, custody/KYC state in WalletService.
type meResponse struct {
	*services.Me
	// Deposit is omitted when the readiness probe itself failed. Absent means
	// "unknown, behave as before" — never "blocked": a transient read must not
	// take the deposit button away from a user who is perfectly onboarded.
	Deposit *services.DepositReadiness `json:"deposit,omitempty"`
}

// getMe returns the caller's wallet-side state: terms-addendum acceptance (the
// UI gates the whole app on it) plus the pre-flight deposit gate, so the
// dashboard can render the user's real next step instead of a button that is
// going to 403.
func (h *handlers) getMe(c fiber.Ctx) error {
	userID := middleware.GetUserID(c)
	me, err := h.userSvc.Me(c.Context(), userID)
	if err != nil {
		return sendProblem(c, err)
	}
	deposit, err := h.svc.DepositReadiness(c.Context(), userID, middleware.GetClaims(c).KYCLevel)
	if err != nil {
		slog.ErrorContext(c.Context(), "deposit readiness probe failed", "user_id", userID, "err", err)
		deposit = nil
	}
	return c.JSON(meResponse{Me: me, Deposit: deposit})
}

// acceptTermsAddendum records acceptance of the current addendum version.
func (h *handlers) acceptTermsAddendum(c fiber.Ctx) error {
	if err := h.userSvc.AcceptTermsAddendum(c.Context(), middleware.GetUserID(c)); err != nil {
		return sendProblem(c, err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}
