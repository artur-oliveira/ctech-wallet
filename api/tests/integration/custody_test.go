//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	"gopkg.aoctech.app/wallet/api/internal/asaas"
	"gopkg.aoctech.app/wallet/api/internal/domain/id"
	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
	"gopkg.aoctech.app/wallet/api/internal/problem"
)

// The whole onboarding sequence against real persistence: fee charged, fee
// paid, subaccount created, provider approves, deposits open. Every step is a
// separate durable state because each one can be interrupted, and the money
// consequences of resuming at the wrong one are asymmetric — charging a second
// non-refundable fee is worse than any amount of waiting.
func TestCustodyOnboardingSequence(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := "u-" + id.New()

	// 1. Requesting onboarding charges the fee and creates nothing at the
	//    provider: the provider bills the moment a subaccount exists.
	acc, charge, err := h.baas.RequestCustodyAccount(ctx, user, wallet.KYCVerified, 500000)
	if err != nil {
		t.Fatalf("RequestCustodyAccount: %v", err)
	}
	if acc.Status != wallet.BaasFeePending {
		t.Fatalf("status = %q, want %q", acc.Status, wallet.BaasFeePending)
	}
	if charge.Amount != custodyFeeCents {
		t.Fatalf("fee = %d, want %d", charge.Amount, custodyFeeCents)
	}
	if len(h.asaas.CreatedAccounts) != 0 {
		t.Fatalf("a subaccount was opened before the fee was paid")
	}

	// 2. While the fee is outstanding the user cannot deposit, and the reason
	//    given is the step they can actually act on.
	if _, _, err := h.svc.InitiateDeposit(ctx, user, wallet.KYCVerified, 5000, id.New()); err == nil {
		t.Fatal("deposit allowed before the subaccount exists")
	}
	readiness, err := h.svc.DepositReadiness(ctx, user, wallet.KYCVerified)
	if err != nil {
		t.Fatalf("DepositReadiness: %v", err)
	}
	if readiness.Allowed || readiness.BlockedBy != "custody_fee_pending" {
		t.Fatalf("readiness = %+v, want blocked on the fee", readiness)
	}

	// 3. The fee clears and the subaccount is created — once.
	ref := "vfee#" + charge.Txid
	h.asaas.StagePayment("pay_seq", "", custodyFeeCents, asaas.PaymentReceived, ref)
	if err := h.baas.ConfirmCustodyFee(ctx, "pay_seq", ref); err != nil {
		t.Fatalf("ConfirmCustodyFee: %v", err)
	}
	if err := h.baas.ConfirmCustodyFee(ctx, "pay_seq", ref); err != nil {
		t.Fatalf("redelivered fee webhook: %v", err)
	}
	if len(h.asaas.CreatedAccounts) != 1 {
		t.Fatalf("subaccounts created = %d, want 1", len(h.asaas.CreatedAccounts))
	}

	stored, err := h.baasRepo.GetBaasAccount(ctx, user)
	if err != nil || stored == nil {
		t.Fatalf("GetBaasAccount: %v (%+v)", err, stored)
	}
	if stored.Status != wallet.BaasOnboarding || stored.ProviderAccountID == "" {
		t.Fatalf("after fee payment: %+v", stored)
	}

	// 4. Still no deposit: created is not approved.
	if _, _, err := h.svc.InitiateDeposit(ctx, user, wallet.KYCVerified, 5000, id.New()); err == nil {
		t.Fatal("deposit allowed against an unapproved subaccount")
	}

	// 5. Provider approval creates the static PIX key every deposit is built on.
	apiKey, err := h.baas.DecryptAPIKey(stored)
	if err != nil {
		t.Fatalf("DecryptAPIKey: %v", err)
	}
	h.asaas.StageAccountStatus(apiKey, &asaas.AccountStatus{General: asaas.RegistrationApproved})
	if err := h.baas.ProcessAccountStatusWebhook(ctx, stored.ProviderAccountID); err != nil {
		t.Fatalf("ProcessAccountStatusWebhook: %v", err)
	}
	approved, err := h.baasRepo.GetBaasAccount(ctx, user)
	if err != nil {
		t.Fatalf("GetBaasAccount: %v", err)
	}
	if approved.Status != wallet.BaasApproved || approved.EVPPixKey == "" {
		t.Fatalf("after approval: %+v", approved)
	}

	// 6. Now the deposit opens — at the provider, never at Inter.
	dep, depCharge, err := h.svc.InitiateDeposit(ctx, user, wallet.KYCVerified, 5000, id.New())
	if err != nil {
		t.Fatalf("InitiateDeposit: %v", err)
	}
	if dep.Provider != wallet.ProviderAsaas || depCharge.QRCode == "" {
		t.Fatalf("deposit = %+v charge = %+v", dep, depCharge)
	}
	if len(h.pix.CreatedCharges) != 0 {
		t.Fatalf("a deposit opened %d Inter charge(s)", len(h.pix.CreatedCharges))
	}
}

// The provider bills per PIX receipt past a monthly free allowance, so the
// counter has to survive a real round-trip through the database, not just live
// in a service field.
func TestReceiptAllowanceIsMeteredAcrossConfirmations(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := "u-" + id.New()
	h.onboardCustody(t, user)

	dep, _, err := h.svc.InitiateDeposit(ctx, user, wallet.KYCVerified, 5000, id.New())
	if err != nil {
		t.Fatalf("InitiateDeposit: %v", err)
	}
	h.stagePaidDeposit(dep, 5000, cpf)
	if err := h.svc.ConfirmAsaasDeposit(ctx, dep.ProviderQRCodeID, dep.Txid); err != nil {
		t.Fatalf("ConfirmAsaasDeposit: %v", err)
	}

	_, _, month := wallet.WindowKeys(time.Now())
	acc, err := h.baasRepo.GetBaasAccount(ctx, user)
	if err != nil {
		t.Fatalf("GetBaasAccount: %v", err)
	}
	if got := acc.ReceiptsUsed(month); got != 1 {
		t.Fatalf("receipts used = %d, want 1", got)
	}

	// Spend the rest of the allowance and the next deposit is refused before a
	// charge is opened — a refused charge that had already been created would
	// have cost the very fee this gate exists to avoid.
	if err := h.baasRepo.UpdateBaasAccount(ctx, user, map[string]any{
		"receipts_month_key": month, "receipts_count": int64(wallet.DefaultReceiptsPerMonth),
	}); err != nil {
		t.Fatalf("UpdateBaasAccount: %v", err)
	}
	_, _, err = h.svc.InitiateDeposit(ctx, user, wallet.KYCVerified, 5000, id.New())
	wantProblem(t, err, problem.TypeDepositReceiptsExhausted)
}
