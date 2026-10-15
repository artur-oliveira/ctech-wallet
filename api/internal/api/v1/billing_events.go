package v1

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/wallet/api/internal/billingclient"
)

// PathBillingEvents is where Billing delivers its signed webhooks.
const PathBillingEvents = "/internal/billing/events"

const (
	eventInvoicePaid = "invoice.paid"
	// eventSkew is how far a delivery's timestamp may be from now. Billing retries
	// with its original timestamp, so a retry after the window is refused and the
	// sweep covers it; the window is the replay protection.
	eventSkew = 5 * time.Minute
)

// InvoiceLookup reads the invoice an event names.
type InvoiceLookup interface {
	InvoiceByID(ctx context.Context, id string) (*billingclient.Invoice, error)
}

// DepositConfirmer is the one thing an event may cause: the wallet re-reads the
// deposit and decides for itself.
type DepositConfirmer interface {
	ConfirmDeposit(ctx context.Context, txid, payerCPF, payerName string, sweep bool) error
}

// BillingEvents receives Billing's invoice.paid. The event is a wake-up and
// nothing more: it names an invoice, the wallet reads that invoice from Billing,
// finds the deposit it belongs to, and ConfirmDeposit re-reads and judges it as
// it always does (amount, payer, both checks). Nothing in the body moves money.
type BillingEvents struct {
	Secret   string
	Invoices InvoiceLookup
	Confirm  DepositConfirmer
	Now      func() time.Time
}

type eventEnvelope struct {
	Type string `json:"type"`
	Data struct {
		Object string `json:"object"`
		ID     string `json:"id"`
	} `json:"data"`
}

// RegisterBillingEvents mounts the route. It is unauthenticated by JWT on
// purpose: the HMAC over timestamp and body is the credential.
func RegisterBillingEvents(v1 fiber.Router, e *BillingEvents) {
	if e.Now == nil {
		e.Now = time.Now
	}
	v1.Post(PathBillingEvents, e.handle)
}

func (e *BillingEvents) handle(c fiber.Ctx) error {
	body := c.Body()
	if !billingclient.VerifySignature(e.Secret, c.Get(billingclient.HeaderSignature), c.Get(billingclient.HeaderTimestamp), body, e.Now(), eventSkew) {
		return c.SendStatus(fiber.StatusUnauthorized)
	}
	var ev eventEnvelope
	if err := json.Unmarshal(body, &ev); err != nil {
		return c.SendStatus(fiber.StatusBadRequest)
	}
	// Anything but a paid invoice, or an invoice that is not named, is not ours to
	// act on; 200 so Billing does not retry it.
	if ev.Type != eventInvoicePaid || ev.Data.Object != "invoice" || ev.Data.ID == "" {
		return c.SendStatus(fiber.StatusOK)
	}
	inv, err := e.Invoices.InvoiceByID(c.Context(), ev.Data.ID)
	switch {
	case errors.Is(err, billingclient.ErrNotFound):
		return c.SendStatus(fiber.StatusOK)
	case err != nil:
		slog.Warn("billing event: invoice unreadable, Billing will retry", "invoice_id", ev.Data.ID, "err", err)
		return c.SendStatus(fiber.StatusBadGateway)
	}
	if inv.Txid == "" {
		return c.SendStatus(fiber.StatusOK) // an invoice of another service, or not a deposit
	}
	// An unknown deposit is a no-op inside ConfirmDeposit. An error (the payer is
	// not recorded yet, a lock, the table) is answered 5xx so Billing retries in
	// half a minute; the pending-deposit sweep covers a retry that never comes.
	if err := e.Confirm.ConfirmDeposit(c.Context(), inv.Txid, "", "", false); err != nil {
		slog.Info("billing event: deposit not settled yet, Billing will retry", "txid", inv.Txid, "err", err)
		return c.SendStatus(fiber.StatusServiceUnavailable)
	}
	return c.SendStatus(fiber.StatusOK)
}
