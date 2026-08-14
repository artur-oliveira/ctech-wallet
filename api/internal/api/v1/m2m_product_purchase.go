package v1

import (
	"time"

	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
	"gopkg.aoctech.app/wallet/api/internal/middleware"
)

func productExpiresAtRFC3339(ttl int64) string {
	return time.Unix(ttl, 0).UTC().Format(time.RFC3339)
}

type productPurchaseWithExpiry struct {
	*wallet.ProductPurchase
	ExpiresAt string `json:"expires_at"`
}

func withProductExpiry(p *wallet.ProductPurchase) productPurchaseWithExpiry {
	return productPurchaseWithExpiry{ProductPurchase: p, ExpiresAt: productExpiresAtRFC3339(p.TTL)}
}

func (h *handlers) m2mListProductSKUs(c fiber.Ctx) error {
	skus := wallet.ListProductSKUs()
	out := make([]fiber.Map, len(skus))
	for i, s := range skus {
		out[i] = fiber.Map{"id": s.ID, "price_cents": s.PriceCents}
	}
	return c.JSON(out)
}

func (h *handlers) m2mPurchaseProduct(c fiber.Ctx) error {
	var body M2MSandboxPurchaseRequest // same {user_id, sku, idempotency_key} shape — reused, not duplicated
	if p := bindJSON(c, &body); p != nil {
		return sendProblem(c, p)
	}
	client := middleware.GetClaims(c).AZP
	purchase, charge, err := h.svc.PurchaseProductDirect(c.Context(), body.UserID, body.SKU, body.IdempotencyKey, client)
	if err != nil {
		return sendProblem(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"purchase_id":      purchase.PurchaseID,
		"sku":              purchase.SKU,
		"amount":           purchase.AmountExpected,
		"status":           purchase.Status,
		"pix_copia_e_cola": charge.QRCode,
		"qr_code_base64":   charge.QRCodeB64,
		"expires_at":       productExpiresAtRFC3339(purchase.TTL),
	})
}

func (h *handlers) m2mGetProductPurchase(c fiber.Ctx) error {
	client := middleware.GetClaims(c).AZP
	purchase, err := h.svc.GetProductPurchase(c.Context(), c.Params("id"), client)
	if err != nil {
		return sendProblem(c, err)
	}
	return c.JSON(withProductExpiry(purchase))
}

func (h *handlers) m2mRefundProductPurchase(c fiber.Ctx) error {
	var body M2MRefundSandboxPurchaseRequest // same {user_id, idempotency_key} shape — reused
	if p := bindJSON(c, &body); p != nil {
		return sendProblem(c, p)
	}
	client := middleware.GetClaims(c).AZP
	purchase, err := h.svc.RefundProductPurchase(c.Context(), body.UserID, c.Params("id"), body.IdempotencyKey, client)
	if err != nil {
		return sendProblem(c, err)
	}
	return c.JSON(withProductExpiry(purchase))
}

// confirmProductPurchase is the "prdp" webhook wake-up path. The caller's
// body is never payment authority: ConfirmProductPurchase re-queries Inter,
// validates completed status and the catalog-owned amount, then transitions
// the purchase and notifies its M2M owner.
func (h *handlers) confirmProductPurchase(c fiber.Ctx) error {
	var body ConfirmPurchaseRequest
	if p := bindJSON(c, &body); p != nil {
		return sendProblem(c, p)
	}
	if err := h.svc.ConfirmProductPurchase(c.Context(), body.Txid, false); err != nil {
		return sendProblem(c, err)
	}
	return c.SendStatus(fiber.StatusOK)
}
