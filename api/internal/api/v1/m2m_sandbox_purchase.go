package v1

import (
	"time"

	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
	"gopkg.aoctech.app/wallet/api/internal/middleware"
)

// expiresAtRFC3339 converts a SandboxPurchase's TTL (unix seconds, already
// the purchase's real expiry — see sandboxPurchaseTTLMinutes) into an
// RFC3339 string for the frontend countdown.
func expiresAtRFC3339(ttl int64) string {
	return time.Unix(ttl, 0).UTC().Format(time.RFC3339)
}

// sandboxPurchaseWithExpiry adds the computed expires_at to a SandboxPurchase's
// JSON output without adding a stored field to the domain model — TTL is
// already the real expiry, this just formats it (see expiresAtRFC3339).
type sandboxPurchaseWithExpiry struct {
	*wallet.SandboxPurchase
	ExpiresAt string `json:"expires_at"`
}

func withExpiry(p *wallet.SandboxPurchase) sandboxPurchaseWithExpiry {
	return sandboxPurchaseWithExpiry{SandboxPurchase: p, ExpiresAt: expiresAtRFC3339(p.TTL)}
}

// m2mListSandboxSKUs is the M2M counterpart of the internal ListSKUs() —
// callers like ctech-poker need the catalog to render purchase options
// before opening a purchase.
func (h *handlers) m2mListSandboxSKUs(c fiber.Ctx) error {
	skus := wallet.ListSKUs()
	out := make([]fiber.Map, len(skus))
	for i, s := range skus {
		out[i] = fiber.Map{
			"id": s.ID, "price_cents": s.PriceCents, "base_credits": s.BaseCredits,
			"bonus_percent": s.BonusPercent, "total_credits": s.TotalCredits(),
		}
	}
	return c.JSON(out)
}

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
		"expires_at":       expiresAtRFC3339(purchase.TTL),
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
	return c.JSON(withExpiry(purchase))
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
	return c.JSON(withExpiry(purchase))
}
