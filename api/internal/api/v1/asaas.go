package v1

import (
	"encoding/json"
	"log/slog"
	"math"

	"github.com/gofiber/fiber/v3"
)

// Asaas webhook payload shapes are intentionally lenient (no
// DisallowUnknownFields, unlike bindJSON): this is a third-party payload we
// don't control the schema of, and a field Asaas adds later must never turn
// into a hard failure here. Field names/shape are best-effort against the
// design research in docs/plans/2026-07-30-asaas-baas-implementation-plan.md
// §2.3 — confirm against a live Asaas-sandbox payload before trusting this as
// ground truth (plan §3.4).
type asaasTransferAuthorizationPayload struct {
	Transfer struct {
		ID                string  `json:"id"`
		Value             float64 `json:"value"` // Asaas represents money as decimal reais, not centavos — converted below
		ExternalReference string  `json:"externalReference"`
		PixAddressKey     string  `json:"pixAddressKey"`
		WalletID          string  `json:"walletId"`
	} `json:"transfer"`
}

type asaasTransferAuthorizationResponse struct {
	Status       string `json:"status"`
	RefuseReason string `json:"refuseReason,omitempty"`
}

// centavosFromReais converts Asaas's decimal-reais amount representation into
// the integer centavos every other money value in this codebase uses.
// Unverified against a live payload — flagged the same way as every other
// "confirm before build" item in the implementation plan.
func centavosFromReais(v float64) int64 {
	return int64(math.Round(v * 100))
}

// asaasTransferAuthorization answers Asaas's synchronous "transfer
// authorization" webhook (plan §2.3): every POST /v3/transfers call — a
// withdrawal payout, a fee sweep, a settlement leg — triggers this callback
// roughly 5 seconds later, and Asaas auto-cancels the transfer after 3 failed
// or invalid responses. The handler does ONE DynamoDB lookup (via
// BaasService.AuthorizeTransfer), no outbound calls, to stay well inside that
// latency budget.
func (h *handlers) asaasTransferAuthorization(c fiber.Ctx) error {
	var body asaasTransferAuthorizationPayload
	if err := json.Unmarshal(c.Body(), &body); err != nil {
		slog.WarnContext(c.Context(), "asaas transfer-authorization: bad json", "err", err)
		return c.Status(fiber.StatusOK).JSON(asaasTransferAuthorizationResponse{Status: "REFUSED", RefuseReason: "bad_request"})
	}
	destination := body.Transfer.WalletID
	if destination == "" {
		destination = body.Transfer.PixAddressKey
	}
	amount := centavosFromReais(body.Transfer.Value)
	approved, reason := h.baas.AuthorizeTransfer(c.Context(), body.Transfer.ExternalReference, amount, destination)
	if !approved {
		return c.Status(fiber.StatusOK).JSON(asaasTransferAuthorizationResponse{Status: "REFUSED", RefuseReason: reason})
	}
	return c.Status(fiber.StatusOK).JSON(asaasTransferAuthorizationResponse{Status: "APPROVED"})
}
