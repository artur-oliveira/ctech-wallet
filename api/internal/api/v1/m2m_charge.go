package v1

import (
	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/wallet/api/internal/middleware"
	"gopkg.aoctech.app/wallet/api/internal/services"
)

// The caller-supplied-amount charge routes. Same handler shape as
// m2mPurchaseProduct and the same response body, deliberately: the consumer
// parses one thing whichever route opened the charge.

func (h *handlers) m2mOpenCharge(c fiber.Ctx) error {
	var body M2MOpenChargeRequest
	if p := bindJSON(c, &body); p != nil {
		return sendProblem(c, p)
	}
	purchase, charge, err := h.svc.OpenCharge(c.Context(), services.OpenChargeInput{
		UserID:         body.UserID,
		AmountCents:    body.AmountCents,
		Reference:      body.Reference,
		IdempotencyKey: body.IdempotencyKey,
		PayerHintCPF:   body.PayerTaxID,
		Description:    body.Description,
		// From the token, never the body. A client-supplied identity here would
		// let any holder of this scope open charges billed to — and refundable
		// by — somebody else.
		RequestingClient: middleware.GetClaims(c).AZP,
	})
	if err != nil {
		return sendProblem(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"purchase_id":      purchase.PurchaseID,
		"sku":              purchase.SKU,
		"user_id":          purchase.UserID,
		"amount_expected":  purchase.AmountExpected,
		"status":           purchase.Status,
		"pix_copia_e_cola": charge.QRCode,
		"qr_code_base64":   charge.QRCodeB64,
		"expires_at":       productExpiresAtRFC3339(purchase.TTL),
	})
}

// m2mGetCharge is the read-back every confirmation goes through. The consumer
// treats its own notify-back as a wake-up signal and asks here for the truth,
// which is why this route must never describe another client's charge.
func (h *handlers) m2mGetCharge(c fiber.Ctx) error {
	purchase, err := h.svc.GetCharge(c.Context(), c.Params("id"), middleware.GetClaims(c).AZP)
	if err != nil {
		return sendProblem(c, err)
	}
	return c.JSON(withProductExpiry(purchase))
}
