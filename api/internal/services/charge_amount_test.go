package services

import (
	"context"
	"testing"

	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
	"gopkg.aoctech.app/wallet/api/internal/pix"
	"gopkg.aoctech.app/wallet/api/internal/problem"
)

// The acceptance list from ctech-billing's spec
// (docs/specs/2026-08-15-wallet-invoice-charge.md § 5), as tests.
//
// The route removes wallet's oldest fraud defense for one caller — the fixed
// catalogue — so the tests that matter are the ones about what replaced it: the
// ceiling, and an idempotency key bound to the amount from the request.

func chargeInput(client, user, ref, key string, amount int64) OpenChargeInput {
	return OpenChargeInput{
		UserID: user, AmountCents: amount, Reference: ref,
		IdempotencyKey: key, RequestingClient: client,
	}
}

func TestOpenChargeTakesAnArbitraryAmount(t *testing.T) {
	svc, _, _ := newTestWalletServiceForProduct()
	ctx := context.Background()

	// A number no catalogue would ever contain, which is the point: it is what a
	// month of proration plus metered usage actually produces.
	p, charge, err := svc.OpenCharge(ctx, chargeInput("billing", "user-1", "in_abc", "in_abc:1", 4137))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if p.AmountExpected != 4137 {
		t.Fatalf("amount_expected = %d, want 4137", p.AmountExpected)
	}
	if p.Kind != wallet.ProductPurchaseKindCharge {
		t.Fatalf("kind = %q, want charge", p.Kind)
	}
	if p.SKU != "in_abc" {
		t.Fatalf("sku = %q, want the caller's reference", p.SKU)
	}
	if charge.QRCode == "" {
		t.Fatal("no PIX code returned")
	}
}

func TestOpenChargeReplaysInsteadOfChargingTwice(t *testing.T) {
	svc, repo, _ := newTestWalletServiceForProduct()
	ctx := context.Background()

	first, c1, err := svc.OpenCharge(ctx, chargeInput("billing", "user-1", "in_abc", "in_abc:1", 4137))
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, c2, err := svc.OpenCharge(ctx, chargeInput("billing", "user-1", "in_abc", "in_abc:1", 4137))
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if second.PurchaseID != first.PurchaseID || c1.QRCode != c2.QRCode {
		t.Fatalf("replay opened a second charge: %s vs %s", second.PurchaseID, first.PurchaseID)
	}
	if len(repo.purchases) != 1 {
		t.Fatalf("%d reservations written, want 1", len(repo.purchases))
	}
}

// The hole the catalogue used to close. Without binding the hash to the amount
// from the request, replaying one key with a bigger number returns the original
// charge and reads as success.
func TestOpenChargeRefusesTheSameKeyWithADifferentAmount(t *testing.T) {
	svc, repo, _ := newTestWalletServiceForProduct()
	ctx := context.Background()

	first, _, err := svc.OpenCharge(ctx, chargeInput("billing", "user-1", "in_abc", "in_abc:1", 4137))
	if err != nil {
		t.Fatalf("first: %v", err)
	}

	if _, _, err := svc.OpenCharge(ctx, chargeInput("billing", "user-1", "in_abc", "in_abc:1", 99999)); err == nil {
		t.Fatal("the same key with a different amount must conflict")
	}

	// And the original is untouched — a rejected replay must not edit what it
	// collided with.
	stored, _ := repo.Get(ctx, first.PurchaseID)
	if stored.AmountExpected != 4137 {
		t.Fatalf("original amount is now %d", stored.AmountExpected)
	}
}

// The same key from a different client is a different charge entirely, because
// the requesting client is part of the txid. Two consumers cannot collide on
// each other's keys.
func TestOpenChargeIsolatesClients(t *testing.T) {
	svc, _, _ := newTestWalletServiceForProduct()
	ctx := context.Background()

	a, _, err := svc.OpenCharge(ctx, chargeInput("billing", "user-1", "in_abc", "shared-key", 4137))
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	b, _, err := svc.OpenCharge(ctx, chargeInput("other", "user-1", "in_abc", "shared-key", 4137))
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if a.PurchaseID == b.PurchaseID {
		t.Fatal("two clients shared one charge")
	}
}

// The ceiling is what replaced the catalogue, and it refuses rather than
// truncates: a silently reduced charge is a paid invoice that is still short.
func TestOpenChargeRefusesAboveTheCeiling(t *testing.T) {
	svc, repo, _ := newTestWalletServiceForProduct()
	ctx := context.Background()

	_, _, err := svc.OpenCharge(ctx, chargeInput("billing", "user-1", "in_abc", "in_abc:1", DefaultMaxChargeCents+1))
	if err == nil {
		t.Fatal("one centavo over the ceiling must be refused")
	}
	if p, ok := err.(*problem.Problem); !ok || p.Status != 422 {
		t.Fatalf("error = %v, want a 422", err)
	}
	// No reservation, which is what keeps the caller's key reusable after it
	// corrects the amount.
	if len(repo.purchases) != 0 {
		t.Fatalf("a refused charge wrote %d rows", len(repo.purchases))
	}
}

// A client configured with a lower ceiling gets the lower one; one with no
// entry at all gets the default rather than no limit.
func TestOpenChargeUsesThePerClientCeiling(t *testing.T) {
	svc, _, _ := newTestWalletServiceForProduct()
	svc.SetM2MClients(map[string]M2MClient{
		"billing": {MaxChargeCents: 5000},
	})
	ctx := context.Background()

	if _, _, err := svc.OpenCharge(ctx, chargeInput("billing", "user-1", "in_abc", "k1", 5001)); err == nil {
		t.Fatal("above the client's own ceiling must be refused")
	}
	if _, _, err := svc.OpenCharge(ctx, chargeInput("billing", "user-1", "in_abc", "k2", 5000)); err != nil {
		t.Fatalf("at the ceiling must be allowed: %v", err)
	}
	// Not in the blob at all: the default applies, not "no limit".
	if _, _, err := svc.OpenCharge(ctx, chargeInput("unlisted", "user-1", "in_abc", "k3", DefaultMaxChargeCents+1)); err == nil {
		t.Fatal("an unlisted client must still be bounded")
	}
}

// Confirmation goes through the same re-query as every other sale: a caller
// claiming success for a charge the provider reports pending changes nothing.
func TestChargeConfirmationRequiresTheProvider(t *testing.T) {
	svc, repo, fakePix := newTestWalletServiceForProduct()
	ctx := context.Background()

	p, _, err := svc.OpenCharge(ctx, chargeInput("billing", "user-1", "in_abc", "in_abc:1", 4137))
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	// Provider still says pending.
	fakePix.StageCharge(p.PurchaseID, p.AmountExpected, pix.ChargeActive, "", "")
	if err := svc.ConfirmProductPurchase(ctx, p.PurchaseID, false); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if stored, _ := repo.Get(ctx, p.PurchaseID); stored.Status != wallet.ProductPurchasePending {
		t.Fatalf("an unpaid charge was confirmed: %s", stored.Status)
	}

	fakePix.StageCharge(p.PurchaseID, p.AmountExpected, pix.ChargeCompleted, "", "e2e-"+p.PurchaseID)
	if err := svc.ConfirmProductPurchase(ctx, p.PurchaseID, false); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if stored, _ := repo.Get(ctx, p.PurchaseID); stored.Status != wallet.ProductPurchaseConfirmed {
		t.Fatalf("a paid charge is %s", stored.Status)
	}
}

// A short payment is an alarm, never a confirmation. Same rule as the catalogue
// rail, and it matters more here: the amount came from the caller, so this is
// the only place the two numbers are ever compared.
func TestChargeConfirmationRefusesAShortPayment(t *testing.T) {
	svc, repo, fakePix := newTestWalletServiceForProduct()
	ctx := context.Background()

	p, _, err := svc.OpenCharge(ctx, chargeInput("billing", "user-1", "in_abc", "in_abc:1", 4137))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	fakePix.StageCharge(p.PurchaseID, p.AmountExpected-1, pix.ChargeCompleted, "", "e2e-"+p.PurchaseID)

	if err := svc.ConfirmProductPurchase(ctx, p.PurchaseID, false); err == nil {
		t.Fatal("a short payment was accepted")
	}
	if stored, _ := repo.Get(ctx, p.PurchaseID); stored.Status != wallet.ProductPurchasePending {
		t.Fatalf("a short payment moved the charge to %s", stored.Status)
	}
}

// The read-back is ownership-scoped, and it does not describe another client's
// charge — it denies knowing it. This is the call a consumer makes before moving
// money on its own side.
func TestGetChargeIsScopedToItsClient(t *testing.T) {
	svc, _, _ := newTestWalletServiceForProduct()
	ctx := context.Background()

	p, _, err := svc.OpenCharge(ctx, chargeInput("billing", "user-1", "in_abc", "in_abc:1", 4137))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := svc.GetCharge(ctx, p.PurchaseID, "billing"); err != nil {
		t.Fatalf("owner cannot read its own charge: %v", err)
	}
	if _, err := svc.GetCharge(ctx, p.PurchaseID, "poker"); err == nil {
		t.Fatal("another client read a charge that is not its own")
	}
}

// A catalogue sale is not reachable through the charge read-back, and vice
// versa. The two rails share a table; they must not share an identity.
func TestGetChargeDoesNotReturnACatalogueSale(t *testing.T) {
	svc, _, _ := newTestWalletServiceForProduct()
	ctx := context.Background()

	p, _, err := svc.PurchaseProductDirect(ctx, "user-1", "poker_reaction_cold", "idem-1", "poker", "")
	if err != nil {
		t.Fatalf("purchase: %v", err)
	}
	if _, err := svc.GetCharge(ctx, p.PurchaseID, "poker"); err == nil {
		t.Fatal("a catalogue purchase was returned as a charge")
	}
}
