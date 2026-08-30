package services

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"gopkg.aoctech.app/wallet/api/internal/asaas"
	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
	"gopkg.aoctech.app/wallet/api/internal/pix"
	"gopkg.aoctech.app/wallet/api/internal/problem"
	"gopkg.aoctech.app/wallet/api/internal/repositories"
)

// The one-off verification fee a user pays before their custody subaccount is
// opened (docs/specs/2026-08-30-asaas-only-deposits.md).
//
// It is a PRODUCT SALE, not a deposit: the user is buying the opening of their
// account, the money is CTech's, and nothing is credited to any wallet. So it
// reuses ProductPurchase wholesale — the same row, the same idempotent
// reservation before the charge, the same confirm-by-re-query. Exactly two
// things differ from every other product sale, and both follow from where the
// cost lands:
//
//   - The charge is opened on CTech's master account at the CUSTODY provider,
//     not at Inter. The provider debits its own subaccount fee from that
//     account's balance, so collecting it anywhere else means holding the money
//     somewhere it cannot pay the bill it exists to cover.
//   - It is never refunded. The provider consumes the fee when the subaccount
//     is created and does not return it when registration is refused — which is
//     why a refused registration re-submits documents on the same subaccount
//     instead of closing it and charging again.

const (
	// custodyFeeRefPrefix separates a verification-fee payment from a user
	// deposit in the provider's PAYMENT_RECEIVED webhook, which carries only an
	// external reference. Both arrive on the same route; the prefix is what
	// makes them impossible to confuse.
	custodyFeeRefPrefix = "vfee#"
	// custodyFeeSKU labels the sale in history and in the purchases table.
	custodyFeeSKU = "custody_account_verification"
	// custodyFeeQRExpirationSeconds is far longer than a deposit's window: the
	// user is paying an onboarding fee, possibly reading terms first, not
	// completing a transfer they already decided on.
	custodyFeeQRExpirationSeconds = 24 * 60 * 60
	// pendingDocumentsDelay is the provider's stated minimum wait between
	// creating a subaccount and asking which documents it wants. Asking sooner
	// answers nothing useful.
	pendingDocumentsDelay = 15 * time.Second
)

// CustodyFeeConfig is where the verification fee is collected and how much it
// is. All three come from configuration rather than a constant: the amount is
// the provider's price and changes without a deploy, and the account it lands
// in differs per environment.
type CustodyFeeConfig struct {
	// MasterAccountID identifies CTech's own account at the provider, so an
	// inbound payment webhook can be told apart from a user subaccount's.
	MasterAccountID string
	// MasterPixKey is that account's static EVP key — the fee QR is built on it.
	MasterPixKey string
	AmountCents  int64
}

// SetCustodyFee wires the verification-fee charge after construction — same
// setter pattern as SetWithdrawalReverser, so existing NewBaasService call
// sites keep compiling. Without it the onboarding request refuses rather than
// opening a subaccount whose fee was never collected.
func (b *BaasService) SetCustodyFee(cfg CustodyFeeConfig, purchases ProductPurchaseRepo) {
	b.feeConfig = cfg
	b.purchases = purchases
}

// custodyFeePurchaseID is deterministic in the user alone: a user opens at most
// one custody account, so a retried request must always land on the same
// purchase row rather than opening a second charge for the same fee.
func custodyFeePurchaseID(userID string) string {
	return productPurchaseTxID(userID, custodyFeeSKU, custodyFeeSKU)
}

// IsCustodyFeeReference reports whether an inbound payment reference belongs to
// a verification fee rather than a deposit.
func IsCustodyFeeReference(externalReference string) bool {
	return strings.HasPrefix(externalReference, custodyFeeRefPrefix)
}

// RequestCustodyAccount is step one of onboarding: it reserves the user's
// custody record and opens the verification-fee charge. It deliberately does
// NOT create the subaccount — that happens once the fee clears, in
// ConfirmCustodyFee, because the provider bills the moment the subaccount
// exists.
//
// Idempotent in both directions: called again while the fee is outstanding it
// returns the same charge, and called again after onboarding has moved on it
// returns the account with no charge at all.
func (b *BaasService) RequestCustodyAccount(ctx context.Context, userID, kycLevel string, incomeValue int64) (*wallet.BaasAccount, *pix.Charge, error) {
	if kycLevel != wallet.KYCVerified {
		return nil, nil, problem.KYCNotVerified()
	}
	if b.purchases == nil || b.feeConfig.AmountCents <= 0 || b.feeConfig.MasterPixKey == "" {
		// Fail closed. Opening a subaccount without collecting the fee would
		// bill CTech for it silently, once per user, with nothing to notice it.
		slog.ErrorContext(ctx, "ALARM custody fee not configured; onboarding refused", "user_id", userID)
		return nil, nil, problem.InternalServer("cobrança da taxa de verificação indisponível")
	}

	existing, err := b.repo.GetBaasAccount(ctx, userID)
	if err != nil {
		return nil, nil, err
	}
	if existing != nil && existing.Status != wallet.BaasFeePending {
		// Already past this step — never re-charge, never re-open.
		return existing, nil, nil
	}

	if existing == nil {
		now := repositories.NowStr()
		reservation := &wallet.BaasAccount{
			UserID: userID, Status: wallet.BaasFeePending, IncomeValue: incomeValue,
			FeePurchaseID: custodyFeePurchaseID(userID), CreatedAt: now, UpdatedAt: now,
		}
		if err := b.repo.PutBaasAccount(ctx, reservation); err != nil {
			if !errors.Is(err, repositories.ErrBaasAccountExists) {
				return nil, nil, err
			}
			if existing, err = b.repo.GetBaasAccount(ctx, userID); err != nil {
				return nil, nil, err
			}
		} else {
			existing = reservation
		}
	}

	// A charge already opened is handed back from what was stored: opening a
	// second QR code at the provider for one fee would leave two payable codes
	// for a payment the user only owes once.
	if existing.FeeQRPayload != "" {
		return existing, b.storedFeeCharge(existing), nil
	}

	charge, err := b.openCustodyFeeCharge(ctx, userID)
	if err != nil {
		return nil, nil, err
	}
	if err := b.repo.UpdateBaasAccount(ctx, userID, map[string]any{
		"fee_qr_payload": charge.QRCode, "fee_qr_image": charge.QRCodeB64,
	}); err != nil {
		return nil, nil, err
	}
	return existing, charge, nil
}

// storedFeeCharge rebuilds the outstanding fee charge from the record, with no
// provider call.
func (b *BaasService) storedFeeCharge(acc *wallet.BaasAccount) *pix.Charge {
	if acc == nil || acc.FeeQRPayload == "" {
		return nil
	}
	return &pix.Charge{
		Txid: acc.FeePurchaseID, Amount: b.feeConfig.AmountCents, QRCode: acc.FeeQRPayload,
		QRCodeB64: acc.FeeQRImage, Status: pix.ChargeActive,
	}
}

// OutstandingFeeCharge returns the verification-fee charge a user still owes,
// or nil once it is settled. Read-only.
func (b *BaasService) OutstandingFeeCharge(acc *wallet.BaasAccount) *pix.Charge {
	if acc == nil || acc.Status != wallet.BaasFeePending {
		return nil
	}
	return b.storedFeeCharge(acc)
}

// openCustodyFeeCharge reserves the purchase row BEFORE opening the charge, so
// a retried request can never open a second QR for the same fee — the same
// ordering every other charge-opening path in this package uses.
func (b *BaasService) openCustodyFeeCharge(ctx context.Context, userID string) (*pix.Charge, error) {
	purchaseID := custodyFeePurchaseID(userID)
	amount := b.feeConfig.AmountCents
	now := repositories.NowStr()
	p := &wallet.ProductPurchase{
		PurchaseID:     purchaseID,
		UserID:         userID,
		SKU:            custodyFeeSKU,
		Kind:           wallet.ProductPurchaseKindCustodyFee,
		AmountExpected: amount,
		RequestHash:    reqHash(custodyFeeSKU+"#"+userID, amount),
		Status:         wallet.ProductPurchasePending,
		Description:    "Taxa de verificação da conta de pagamento",
		CreatedAt:      now,
		UpdatedAt:      now,
		// No TTL: unlike a product sale, an unpaid verification fee is the
		// user's open onboarding step and must still be payable tomorrow.
	}
	if err := b.purchases.PutIfAbsent(ctx, p); err != nil && !errors.Is(err, repositories.ErrProductPurchaseExists) {
		return nil, err
	}
	// The price may have moved since an older reservation was written. The row
	// is authoritative for what this user owes — charging the new price against
	// a row that records the old one would confirm to an amount mismatch.
	if stored, err := b.purchases.Get(ctx, purchaseID); err != nil {
		return nil, err
	} else if stored != nil {
		amount = stored.AmountExpected
	}

	qr, err := b.asaas.CreatePixQRCode(ctx, b.parentAPIKey, asaas.QRCodeRequest{
		AddressKey:        b.feeConfig.MasterPixKey,
		Value:             amount,
		Format:            "ALL",
		ExpirationSeconds: custodyFeeQRExpirationSeconds,
		ExternalReference: custodyFeeRefPrefix + purchaseID,
	})
	if err != nil {
		slog.ErrorContext(ctx, "custody fee charge creation failed", "user_id", userID, "err", err)
		return nil, problem.InternalServer("falha ao criar cobrança da taxa de verificação")
	}
	return &pix.Charge{
		Txid: purchaseID, Amount: amount, QRCode: qr.Payload,
		QRCodeB64: qr.EncodedImage, Status: pix.ChargeActive,
	}, nil
}

// ConfirmCustodyFee is the fee's half of the PAYMENT_RECEIVED webhook. The
// webhook supplies only the payment id; the amount and status come from
// re-querying the provider with the master credential (Invariant #11), and the
// subaccount is created only after that read agrees.
//
// No payer-CPF check, deliberately: this is a purchase, and anyone may pay for
// a user's onboarding. The CPF gate belongs to deposits, where the money
// becomes the payer's own balance.
func (b *BaasService) ConfirmCustodyFee(ctx context.Context, paymentID, externalReference string) error {
	purchaseID := strings.TrimPrefix(externalReference, custodyFeeRefPrefix)
	p, err := b.purchases.Get(ctx, purchaseID)
	if err != nil || p == nil {
		return err // unknown reference — idempotent no-op
	}
	if p.Kind != wallet.ProductPurchaseKindCustodyFee {
		return nil
	}
	if p.Status != wallet.ProductPurchasePending {
		return nil // already settled; a redelivered webhook must not re-run onboarding
	}

	payment, err := b.asaas.QueryPayment(ctx, b.parentAPIKey, paymentID)
	if err != nil {
		return err
	}
	if payment.Status != asaas.PaymentReceived {
		return nil // not paid yet — safe to be woken again
	}
	if payment.Value != p.AmountExpected {
		slog.ErrorContext(ctx, "ALARM custody fee amount mismatch",
			"purchase_id", purchaseID, "expected", p.AmountExpected, "paid", payment.Value)
		return problem.InternalServer("valor pago não corresponde à taxa de verificação")
	}

	moved, err := b.purchases.TransitionStatus(ctx, purchaseID, wallet.ProductPurchasePending, wallet.ProductPurchaseConfirmed, payment.ID)
	if err != nil {
		return err
	}
	if !moved {
		return nil // lost the race with a concurrent delivery
	}
	// Tell the screen the fee cleared BEFORE opening the subaccount: opening it
	// is a provider round trip that can fail, and the user's question ("did my
	// payment land?") is already answered.
	b.announce(ctx, p.UserID, wallet.BaasFeePaid)
	return b.openSubaccount(ctx, p.UserID)
}

// openSubaccount creates the provider subaccount for a user whose fee has
// cleared. Separated from RequestCustodyAccount because this is the call that
// spends the money: it must never run on an unpaid onboarding, and it must run
// exactly once.
func (b *BaasService) openSubaccount(ctx context.Context, userID string) error {
	acc, err := b.repo.GetBaasAccount(ctx, userID)
	if err != nil {
		return err
	}
	if acc == nil {
		return errors.New("custody: fee confirmed for a user with no onboarding record")
	}
	if acc.ProviderAccountID != "" {
		return nil // already created — a redelivered webhook must not create a second one
	}
	if err := b.repo.UpdateBaasAccount(ctx, userID, map[string]any{"status": wallet.BaasFeePaid}); err != nil {
		return err
	}

	kyc, err := b.kyc.Get(ctx, userID)
	if err != nil {
		return err
	}
	created, err := b.asaas.CreateAccount(ctx, b.parentAPIKey, asaas.CreateAccountRequest{
		Name: kyc.LegalName, CPF: kyc.CPF, Email: kyc.Email, MobilePhone: kyc.Phone,
		BirthDate: kyc.BirthDate, Address: kyc.Address.Street, AddressNumber: kyc.Address.Number,
		Complement: kyc.Address.Complement, Province: kyc.Address.District, City: kyc.Address.City,
		State: kyc.Address.State, PostalCode: kyc.Address.ZipCode, IncomeValue: acc.IncomeValue,
	})
	if err != nil {
		// The fee is already paid and stays paid: the account is left in
		// fee_paid so a retry resumes here instead of charging again.
		slog.ErrorContext(ctx, "asaas account creation failed", "user_id", userID, "err", err)
		return err
	}

	ciphertext, nonce, err := asaas.EncryptAPIKey(b.masterKey, created.APIKey)
	if err != nil {
		return err
	}
	updates := map[string]any{
		"status":              wallet.BaasOnboarding,
		"provider_account_id": created.ID,
		"provider_wallet_id":  created.WalletID,
		"api_key_ciphertext":  ciphertext,
		"api_key_nonce":       nonce,
	}
	if created.OnboardingURL != "" {
		updates["onboarding_url"] = created.OnboardingURL
	}
	if err := b.repo.UpdateBaasAccount(ctx, userID, updates); err != nil {
		return err
	}
	b.announce(ctx, userID, wallet.BaasOnboarding)
	if b.audit == nil {
		return nil
	}
	return b.audit.Append(ctx, &wallet.AuditEvent{
		UserID: userID, EventType: wallet.EventBaasSubaccountCreated, Actor: userID, After: created.ID,
	})
}

// OnboardingState is the read the onboarding UI polls. For an account that is
// still in progress it refreshes the provider-side view first, so the user is
// told which document to send rather than left on a generic "under review"
// until some webhook happens to fire.
//
// The refresh is best-effort: a provider read that fails returns the stored
// record instead of an error, because a stale status is a worse answer than no
// answer only if it is wrong, and the stored one is the last thing known true.
func (b *BaasService) OnboardingState(ctx context.Context, userID string) (*wallet.BaasAccount, error) {
	acc, err := b.repo.GetBaasAccount(ctx, userID)
	if err != nil || acc == nil {
		return acc, err
	}
	switch acc.Status {
	case wallet.BaasOnboarding, wallet.BaasPendingDocuments, wallet.BaasPendingApproval:
	default:
		return acc, nil
	}
	if acc.ProviderAccountID == "" || !createdLongerAgoThan(acc.CreatedAt, pendingDocumentsDelay) {
		// The provider explicitly refuses to answer about a subaccount younger
		// than pendingDocumentsDelay.
		return acc, nil
	}
	if err := b.ProcessAccountStatusWebhook(ctx, acc.ProviderAccountID); err != nil {
		slog.WarnContext(ctx, "onboarding status refresh failed", "user_id", userID, "err", err)
		return acc, nil
	}
	return b.repo.GetBaasAccount(ctx, userID)
}

// createdLongerAgoThan reports whether a stored RFC3339 timestamp is older than
// d. An unparseable timestamp answers true: the delay only exists to avoid a
// call the provider will refuse, so failing it closed would strand onboarding.
func createdLongerAgoThan(createdAt string, d time.Duration) bool {
	t, err := time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return true
	}
	return time.Since(t) >= d
}
