package v1

import (
	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/wallet/api/internal/middleware"
)

// m2mPurchaseSandbox is the M2M counterpart to purchaseSandboxDirect (scope
// internal:wallet:sandbox-purchase) — a caller like ctech-poker opens a
// direct PIX→sandbox-credits sale for one of its own users and renders the
// returned QR itself, rather than redirecting the user into the wallet's own
// UI. The caller's AZP becomes the purchase's RequestingClient, which is what
// gates m2mGetSandboxPurchase/m2mRefundSandboxPurchase below and what
// resolves the notify-back webhook URL on confirm/refund.
func (h *handlers) m2mPurchaseSandbox(c fiber.Ctx) error {
	var body M2MSandboxPurchaseRequest
	if p := bindJSON(c, &body); p != nil {
		return sendProblem(c, p)
	}
	client := middleware.GetClaims(c).AZP
	purchase, charge, err := h.svc.PurchaseSandboxDirect(c.Context(), body.UserID, body.SKU, body.IdempotencyKey, client)
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

// m2mGetSandboxPurchase is the status-poll route the caller is expected to
// hit before crediting anything on its own side — the webhook notify-back is
// only a wake-up signal, never trusted for money movement (mirrors Invariant
// #11's own posture for api's inbound PIX webhook).
func (h *handlers) m2mGetSandboxPurchase(c fiber.Ctx) error {
	client := middleware.GetClaims(c).AZP
	purchase, err := h.svc.GetSandboxPurchase(c.Context(), c.Params("id"), client)
	if err != nil {
		return sendProblem(c, err)
	}
	return c.JSON(purchase)
}

// m2mRefundSandboxPurchase is the M2M counterpart to refundSandboxPurchase.
func (h *handlers) m2mRefundSandboxPurchase(c fiber.Ctx) error {
	var body M2MRefundSandboxPurchaseRequest
	if p := bindJSON(c, &body); p != nil {
		return sendProblem(c, p)
	}
	client := middleware.GetClaims(c).AZP
	purchase, err := h.svc.RefundSandboxPurchase(c.Context(), body.UserID, c.Params("id"), body.IdempotencyKey, client)
	if err != nil {
		return sendProblem(c, err)
	}
	return c.JSON(purchase)
}
