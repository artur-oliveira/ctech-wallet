package v1

import (
	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/wallet/api/internal/middleware"
)

// purchaseSandboxDirect opens a direct PIX→sandbox-credits sale (plan
// §9.1/§9.3) — a product sale, not custody: no KYC gate.
//
// Route deliberately named "/sandbox/purchases" (plural), not the plan's own
// literal "/sandbox/purchase" (singular) — that path is already taken by the
// existing ring-fence PurchaseSandbox route below (game→sandbox), and Fiber
// matches routes by path+method regardless of body shape. Flagged here since
// it's a deviation from the plan's literal text, made to avoid a real
// collision with existing code.
func (h *handlers) purchaseSandboxDirect(c fiber.Ctx) error {
	var body SandboxPurchaseDirectRequest
	if p := bindJSON(c, &body); p != nil {
		return sendProblem(c, p)
	}
	idemKey, p := requireIdempotencyKey(c)
	if p != nil {
		return sendProblem(c, p)
	}
	userID := middleware.GetUserID(c)
	purchase, charge, err := h.svc.PurchaseSandboxDirect(c.Context(), userID, body.SKU, idemKey, "")
	if err != nil {
		return sendProblem(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"purchase_id":      purchase.PurchaseID,
		"sku":              purchase.SKU,
		"amount":           purchase.AmountExpected,
		"credits_granted":  purchase.CreditsGranted,
		"status":           purchase.Status,
		"pix_copia_e_cola": charge.QRCode,
		"qr_code_base64":   charge.QRCodeB64,
	})
}

// refundSandboxPurchase reverses an unused direct-purchase pack (plan §9.2).
func (h *handlers) refundSandboxPurchase(c fiber.Ctx) error {
	idemKey, p := requireIdempotencyKey(c)
	if p != nil {
		return sendProblem(c, p)
	}
	userID := middleware.GetUserID(c)
	purchase, err := h.svc.RefundSandboxPurchase(c.Context(), userID, c.Params("id"), idemKey, "")
	if err != nil {
		return sendProblem(c, err)
	}
	return c.JSON(purchase)
}

// confirmSandboxPurchase is called by pix-gateway's webhook Lambda after it
// has already re-queried Inter, dispatched here via the "sbxp#" txid prefix
// instead of confirm-deposit (plan §9.3). Never trusts its own caller either:
// ConfirmSandboxPurchase re-queries Inter itself (Invariant #11).
func (h *handlers) confirmSandboxPurchase(c fiber.Ctx) error {
	var body ConfirmSandboxPurchaseRequest
	if p := bindJSON(c, &body); p != nil {
		return sendProblem(c, p)
	}
	if err := h.svc.ConfirmSandboxPurchase(c.Context(), body.Txid, false); err != nil {
		return sendProblem(c, err)
	}
	return c.SendStatus(fiber.StatusOK)
}
