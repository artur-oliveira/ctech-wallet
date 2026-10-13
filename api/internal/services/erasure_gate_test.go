package services

import (
	"context"
	"errors"
	"testing"

	"gopkg.aoctech.app/wallet/api/internal/kycclient"
	"gopkg.aoctech.app/wallet/api/internal/pix"
	"gopkg.aoctech.app/wallet/api/internal/problem"
)

type stubGate struct {
	blocked bool
	err     error
	calls   []string
}

func (g *stubGate) Blocked(_ context.Context, sub string) (bool, error) {
	g.calls = append(g.calls, sub)
	return g.blocked, g.err
}

func gatedServices(gate ErasureGate) (*WalletService, *UserService) {
	users := &stubUserRepo{}
	svc := NewWalletService(newStubRepo(), users, &stubAudit{}, &stubLocker{}, pix.NewFake(), &stubKYC{rec: &kycclient.KYC{}})
	svc.SetSandboxPurchases(newStubSandboxPurchaseRepo())
	svc.SetProductPurchases(newStubProductPurchaseRepo())
	svc.SetErasure(gate, nil)
	userSvc := NewUserService(users, &stubAudit{})
	userSvc.SetErasureGate(gate)
	return svc, userSvc
}

// D1: from the start of grace, nothing new may happen to the user's money,
// whatever token the caller (user or M2M on the user's behalf) holds.
func TestErasureGateRefusesEveryNewExposure(t *testing.T) {
	ctx := context.Background()
	svc, userSvc := gatedServices(&stubGate{blocked: true})
	ops := map[string]func() error{
		"ActivateGambling": func() error { _, _, err := svc.ActivateGambling(ctx, "u1", "basic", "", "", 100, 200, 300); return err },
		"FundGame":         func() error { _, _, err := svc.FundGame(ctx, "u1", 100, "k"); return err },
		"ReturnFromGame":   func() error { _, _, err := svc.ReturnFromGame(ctx, "u1", 100, "k"); return err },
		"PurchaseSandbox":  func() error { _, _, err := svc.PurchaseSandbox(ctx, "u1", 100, "k"); return err },
		"CreditSandbox":    func() error { _, err := svc.CreditSandbox(ctx, "u1", 100, "k", "r", ""); return err },
		"DebitSandbox":     func() error { _, err := svc.DebitSandbox(ctx, "u1", 100, "k", "r", ""); return err },
		"DebitReal":        func() error { _, err := svc.DebitReal(ctx, "u1", 100, "k", "r", ""); return err },
		"HoldGame":         func() error { _, err := svc.HoldGame(ctx, "u1", 100, "t1", "k"); return err },
		"PurchaseSandboxDirect": func() error {
			_, _, err := svc.PurchaseSandboxDirect(ctx, "u1", "sku", "k", "", "")
			return err
		},
		"RefundSandboxPurchase": func() error { _, err := svc.RefundSandboxPurchase(ctx, "u1", "p1", "k", ""); return err },
		"PurchaseProductDirect": func() error {
			_, _, err := svc.PurchaseProductDirect(ctx, "u1", "sku", "k", "", "")
			return err
		},
		"RefundProductPurchase": func() error { _, err := svc.RefundProductPurchase(ctx, "u1", "p1", "k", ""); return err },
		"OpenCharge": func() error {
			_, _, err := svc.OpenCharge(ctx, OpenChargeInput{UserID: "u1", AmountCents: 100, Reference: "inv", IdempotencyKey: "k", RequestingClient: "billing"})
			return err
		},
		"SelfExclude":            func() error { _, err := svc.SelfExclude(ctx, "u1", "30d", "", ""); return err },
		"RevokeSelfExclusion":    func() error { return svc.RevokeSelfExclusion(ctx, "u1", "", "") },
		"SetGameLimits":          func() error { _, err := svc.SetGameLimits(ctx, "u1", 1, 2, 3, "", ""); return err },
		"CancelPendingLimits":    func() error { _, err := svc.CancelPendingLimits(ctx, "u1"); return err },
		"AcceptTermsAddendum":    func() error { return userSvc.AcceptTermsAddendum(ctx, "u1") },
		"AcceptGamblingAddendum": func() error { return userSvc.AcceptGamblingAddendum(ctx, "u1", "", "") },
	}
	for name, op := range ops {
		var p *problem.Problem
		if err := op(); !errors.As(err, &p) || p.Type != problem.TypeAccountErasurePending {
			t.Errorf("%s: err = %v, want %s", name, err, problem.TypeAccountErasurePending)
		}
	}
}

// Saga protocol §4.2: inbound money that cannot be refused is accepted and
// becomes a blocker. Refusing a cash-out would strand real money (Invariant #14).
func TestErasureGateLeavesSettlementOpen(t *testing.T) {
	ctx := context.Background()
	gate := &stubGate{blocked: true}
	svc, _ := gatedServices(gate)
	_, _ = svc.ReleaseHold(ctx, "u1", "h-missing", "k")
	_, _ = svc.CashoutGame(ctx, "u1", 100, "t1", []string{"h-missing"}, "k", "")
	_ = svc.ConfirmDeposit(ctx, "tx-missing", "", "", false)
	_ = svc.ConfirmSandboxPurchase(ctx, "sbxp-missing", false)
	_ = svc.ConfirmProductPurchase(ctx, "prdp-missing", false)
	if len(gate.calls) != 0 {
		t.Fatalf("settlement paths consulted the erasure gate %v; they must never be refused", gate.calls)
	}
}

func TestErasureGateFailsClosedOnStoreError(t *testing.T) {
	gate := &stubGate{err: errors.New("dynamodb down")}
	svc, _ := gatedServices(gate)
	if _, _, err := svc.FundGame(context.Background(), "u1", 100, "k"); err == nil {
		t.Fatal("a gate read error must refuse the operation")
	}
}

func TestErasureGateOpenUserProceeds(t *testing.T) {
	gate := &stubGate{}
	svc, _ := gatedServices(gate)
	_, err := svc.CreditSandbox(context.Background(), "u1", 100, "k", "r", "")
	var p *problem.Problem
	if errors.As(err, &p) && p.Type == problem.TypeAccountErasurePending {
		t.Fatal("an active user must not be refused")
	}
	if len(gate.calls) != 1 || gate.calls[0] != "u1" {
		t.Fatalf("gate calls = %v, want [u1]", gate.calls)
	}
}
