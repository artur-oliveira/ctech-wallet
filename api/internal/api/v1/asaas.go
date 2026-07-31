package v1

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v3"
)

// asaasCentavos decodes Asaas's decimal-reais JSON representation without
// floating point. Provider money is accepted only as a positive, ordinary
// decimal with at most two fractional digits; exponent notation, overflow and
// fractions below one centavo fail closed.
type asaasCentavos int64

const (
	centavosPerReal        int64 = 100
	maxMoneyFractionDigits       = 2
)

func (a *asaasCentavos) UnmarshalJSON(raw []byte) error {
	s := string(bytes.TrimSpace(raw))
	if s == "" || strings.ContainsAny(s, "eE+") {
		return errors.New("invalid money value")
	}
	parts := strings.Split(s, ".")
	if len(parts) > 2 || parts[0] == "" || strings.HasPrefix(parts[0], "-") {
		return errors.New("invalid money value")
	}
	whole, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || whole > (int64(^uint64(0)>>1)-(centavosPerReal-1))/centavosPerReal {
		return errors.New("money value out of range")
	}
	var fraction int64
	if len(parts) == 2 {
		if len(parts[1]) == 0 || len(parts[1]) > maxMoneyFractionDigits {
			return errors.New("money value must have at most two decimal places")
		}
		fraction, err = strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			return errors.New("invalid money value")
		}
		if len(parts[1]) == 1 {
			fraction *= centavosPerReal / 10
		}
	}
	value := whole*centavosPerReal + fraction
	if value <= 0 {
		return errors.New("money value must be positive")
	}
	*a = asaasCentavos(value)
	return nil
}

// Asaas webhook payload shapes are intentionally lenient (no
// DisallowUnknownFields, unlike bindJSON): this is a third-party payload we
// don't control the schema of, and a field Asaas adds later must never turn
// into a hard failure here. Field names/shape are best-effort against the
// design research in docs/plans/2026-07-30-asaas-baas-implementation-plan.md
// §2.3 — confirm against a live Asaas-sandbox payload before trusting this as
// ground truth (plan §3.4).
type asaasTransferAuthorizationPayload struct {
	Transfer struct {
		ID                string        `json:"id"`
		Value             asaasCentavos `json:"value"`
		ExternalReference string        `json:"externalReference"`
		PixAddressKey     string        `json:"pixAddressKey"`
		WalletID          string        `json:"walletId"`
	} `json:"transfer"`
}

type asaasTransferAuthorizationResponse struct {
	Status       string `json:"status"`
	RefuseReason string `json:"refuseReason,omitempty"`
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
	amount := int64(body.Transfer.Value)
	approved, reason := h.baas.AuthorizeTransfer(c.Context(), body.Transfer.ExternalReference, amount, destination)
	if !approved {
		return c.Status(fiber.StatusOK).JSON(asaasTransferAuthorizationResponse{Status: "REFUSED", RefuseReason: reason})
	}
	return c.Status(fiber.StatusOK).JSON(asaasTransferAuthorizationResponse{Status: "APPROVED"})
}
