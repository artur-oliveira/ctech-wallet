package rail

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	qrcode "github.com/skip2/go-qrcode"

	"gopkg.aoctech.app/wallet/api/internal/billingclient"
	"gopkg.aoctech.app/wallet/api/internal/pix"
)

// BillingAPI is what BillingRail needs of Billing. *billingclient.Client is it.
type BillingAPI interface {
	EnsureCustomer(ctx context.Context, userID, taxID, name, email string) (string, error)
	CreateInvoice(ctx context.Context, customerID, reference, description string, amountCents int64) (*billingclient.Invoice, error)
	InvoiceByReference(ctx context.Context, reference string) (*billingclient.Invoice, error)
	OpenPix(ctx context.Context, inv *billingclient.Invoice) error
	RefundInvoice(ctx context.Context, invoiceID string, amountCents int64, reason, idemKey string) error
	CreatePayout(ctx context.Context, cpf string, amountCents int64, reference, message string) (*billingclient.Payout, error)
	PayoutByReference(ctx context.Context, reference string) (*billingclient.Payout, error)
}

// MismatchPayer is the payer document BillingRail reports when Billing says the
// payer is not the customer. It is no CPF and no masked CPF, so the wallet's own
// payer check cannot accept it: the deposit goes to the refund saga.
const MismatchPayer = "BILLING-MISMATCH"

const (
	depositDescription = "Depósito na carteira"
	payoutMessage      = "Saque da carteira"
	refundReason       = "Devolução de depósito da carteira"
)

// BillingRail moves the wallet's money through Billing, which owns the Inter
// connection. It shapes what Billing says into what the deposit and withdrawal
// flows already consume, so nothing past the seam knows it is there.
type BillingRail struct{ b BillingAPI }

func NewBilling(b BillingAPI) *BillingRail { return &BillingRail{b: b} }

// PayoutGrace: Billing's payout may be created a moment after the call that asked
// for it timed out, so "unknown" is not believed for the first minutes.
func (r *BillingRail) PayoutGrace() time.Duration { return 10 * time.Minute }

// NeedsIdentity: Billing needs the user's customer (CPF, name, e-mail).
func (r *BillingRail) NeedsIdentity() bool { return true }

func (r *BillingRail) OpenDeposit(ctx context.Context, in DepositRequest) (*pix.Charge, error) {
	if in.TaxID == "" {
		return nil, errors.New("billing rail: the user has no CPF on record")
	}
	customer, err := r.b.EnsureCustomer(ctx, in.UserID, in.TaxID, in.Name, in.Email)
	if err != nil {
		return nil, fmt.Errorf("billing customer: %w", err)
	}
	inv, err := r.b.CreateInvoice(ctx, customer, in.Txid, depositDescription, in.AmountCents)
	if err != nil {
		return nil, fmt.Errorf("billing invoice: %w", err)
	}
	return r.charge(ctx, inv)
}

func (r *BillingRail) QueryDeposit(ctx context.Context, txid string) (*pix.Charge, error) {
	inv, err := r.b.InvoiceByReference(ctx, txid)
	if err != nil {
		return nil, err
	}
	return r.charge(ctx, inv)
}

// charge renders an invoice as the charge the deposit flow reads.
//
//   - Open: an active charge with its copia-e-cola.
//   - Paid, payer recorded and Billing says match (the statement's complete CPF)
//     or partial (the webhook's masked CPF, every shown digit equal): completed,
//     the payer's document and name in the fields the wallet's own CPF and name
//     checks read. Billing's verdict and the wallet's checks both have to pass.
//   - Paid, Billing says mismatch: completed with a payer no check accepts, so the
//     deposit is refunded to the payer.
//   - Paid, payer not recorded yet (or Billing cannot tell): completed with no
//     payer, which the deposit flow leaves pending and alarms (never credited,
//     never refunded) until the payer is known.
//   - Anything else (void, uncollectible, draft): not completed.
//
// The amount of a paid charge is what was actually paid, so an underpayment
// meets the existing amount-mismatch refund. Billing does not expose the bank's
// end-to-end id, so the invoice id stands for the payment.
func (r *BillingRail) charge(ctx context.Context, inv *billingclient.Invoice) (*pix.Charge, error) {
	c := &pix.Charge{Txid: inv.Txid, Amount: inv.AmountCents, Status: pix.ChargeActive}
	switch inv.Status {
	case billingclient.StatusOpen:
		if err := r.b.OpenPix(ctx, inv); err != nil {
			return nil, fmt.Errorf("billing pix: %w", err)
		}
		c.QRCode = inv.PixCopiaECola
		c.QRCodeB64 = qrImage(inv.PixCopiaECola)
	case billingclient.StatusPaid:
		payer, name := "", ""
		switch {
		case (inv.PayerMatch == billingclient.MatchYes || inv.PayerMatch == billingclient.MatchPartial) && inv.Payer != nil:
			payer, name = inv.Payer.Document, inv.Payer.Name
		case inv.PayerMatch == "mismatch":
			payer = MismatchPayer
		}
		c.Status, c.Amount, c.E2EID, c.PayerCPF, c.PayerName = pix.ChargeCompleted, inv.PaidCents, inv.ID, payer, name
		c.Payments = []pix.Payment{{E2EID: inv.ID, Amount: inv.PaidCents, PayerCPF: payer, PayerName: name}}
	default:
		c.Status = pix.ChargeRemoved
	}
	return c, nil
}

func (r *BillingRail) RefundDeposit(ctx context.Context, txid, _ string, amountCents int64, idemKey string) (*pix.TransferResult, error) {
	inv, err := r.b.InvoiceByReference(ctx, txid)
	if err != nil {
		return nil, err
	}
	if err := r.b.RefundInvoice(ctx, inv.ID, amountCents, refundReason, idemKey); err != nil {
		return nil, err
	}
	return &pix.TransferResult{E2EID: inv.ID, Status: pix.TransferDone}, nil
}

// Payout pays the CPF through Billing. The wallet's idempotency key is the
// reference. A transport error comes back as an error (the withdrawal stays
// processing and reconcile asks); only Billing's own word that the bank has no
// key for the CPF is pix.ErrKeyNotFound.
func (r *BillingRail) Payout(ctx context.Context, cpf string, amountCents int64, idemKey string) (*pix.TransferResult, error) {
	p, err := r.b.CreatePayout(ctx, cpf, amountCents, idemKey, payoutMessage)
	if err != nil {
		return nil, err
	}
	return payoutResult(p)
}

func (r *BillingRail) QueryPayout(ctx context.Context, idemKey string) (*pix.TransferResult, error) {
	p, err := r.b.PayoutByReference(ctx, idemKey)
	if errors.Is(err, billingclient.ErrNotFound) {
		return &pix.TransferResult{Status: pix.TransferNotFound}, nil
	}
	if err != nil {
		return nil, err
	}
	return payoutResult(p)
}

func payoutResult(p *billingclient.Payout) (*pix.TransferResult, error) {
	switch p.Status {
	case billingclient.PayoutDone:
		return &pix.TransferResult{E2EID: p.PaymentID, Status: pix.TransferDone}, nil
	case billingclient.PayoutFailed:
		if strings.EqualFold(p.FailureReason, billingclient.ReasonKeyNotFound) {
			return nil, pix.ErrKeyNotFound
		}
		// Refused by the bank: no money moved, and the reconcile job reverses it.
		return &pix.TransferResult{Status: pix.TransferNotFound}, nil
	default:
		return &pix.TransferResult{Status: pix.TransferProcessing}, nil
	}
}

// qrImage renders the copia-e-cola as a PNG, base64, for the screens that show
// the code as an image. Billing returns only the string. A failure leaves the
// image empty (the string still works, the screen shows it to copy), never fails
// the deposit.
func qrImage(payload string) string {
	png, err := qrcode.Encode(payload, qrcode.Medium, 320)
	if err != nil {
		slog.Warn("billing rail: could not render the Pix QR code", "err", err)
		return ""
	}
	return base64.StdEncoding.EncodeToString(png)
}
