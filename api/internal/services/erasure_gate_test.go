package services

import (
	"context"
	"errors"
	"testing"

	"gopkg.aoctech.app/wallet/api/internal/kycclient"
	"gopkg.aoctech.app/wallet/api/internal/problem"
	"gopkg.aoctech.app/wallet/api/internal/rail"
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
	svc := NewWalletService(newStubRepo(), users, &stubAudit{}, &stubLocker{}, rail.NewFake(), &stubKYC{rec: &kycclient.KYC{}})
	svc.SetSandboxPurchases(newStubSandboxPurchaseRepo())
	svc.SetProductPurchases(newStubProductPurchaseRepo())
	svc.SetErasure(gate, nil)
	userSvc := NewUserService(users, &stubAudit{})
	userSvc.SetErasureGate(gate)
	return svc, userSvc
}

func TestErasureGateFailsClosedOnStoreError(t *testing.T) {
	gate := &stubGate{err: errors.New("dynamodb down")}
	svc, _ := gatedServices(gate)
	if _, err := svc.HoldGame(context.Background(), "u1", 100, "t", "k"); err == nil {
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

type stubErased map[string]bool

func (e stubErased) Erased(_ context.Context, sub string) (bool, error) { return e[sub], nil }

// R18: the payer of a deposit for an erased user is refunded; nothing credits.
func TestConfirmDepositForAnErasedUserRefundsThePayer(t *testing.T) {
	repo, fake := paidDepositFixture(5000)
	svc := newRailSvc(repo, &stubUserRepo{}, fake)
	svc.SetErased(stubErased{"u1": true})

	if err := svc.ConfirmDeposit(context.Background(), "tx1", "***456789**", "Pagador", false); err != nil {
		t.Fatalf("ConfirmDeposit: %v", err)
	}
	if len(repo.creditCalls) != 0 {
		t.Fatalf("credits = %+v; an erased user must never be credited", repo.creditCalls)
	}
	if len(fake.Refunds) != 1 {
		t.Fatalf("refunds = %d, want the payer refunded once", len(fake.Refunds))
	}
}

// A LOCKED (not erased) user's deposit still credits.
func TestConfirmDepositForALockedUserStillCredits(t *testing.T) {
	repo, fake := paidDepositFixture(5000)
	svc := newRailSvc(repo, &stubUserRepo{}, fake)
	svc.SetErased(stubErased{}) // locked is not erased

	if err := svc.ConfirmDeposit(context.Background(), "tx1", "***456789**", "Pagador", false); err != nil {
		t.Fatal(err)
	}
	if len(repo.creditCalls) != 1 {
		t.Fatalf("credits = %d, want 1", len(repo.creditCalls))
	}
}

// Every operation that opens new exposure for a user refuses a user whose account is being deleted.
// A new service method of that kind joins this list.
func TestErasureGateRefusesEveryNewExposure(t *testing.T) {
	ctx := context.Background()
	svc, userSvc := gatedServices(&stubGate{blocked: true})
	ops := map[string]func() error{
		"InitiateDeposit":     func() error { _, _, err := svc.InitiateDeposit(ctx, "u1", "enhanced", 100, "k"); return err },
		"CreditSandbox":       func() error { _, err := svc.CreditSandbox(ctx, "u1", 100, "k", "r", ""); return err },
		"DebitSandbox":        func() error { _, err := svc.DebitSandbox(ctx, "u1", 100, "k", "r", ""); return err },
		"DebitReal":           func() error { _, err := svc.DebitReal(ctx, "u1", 100, "k", "r", ""); return err },
		"HoldGame":            func() error { _, err := svc.HoldGame(ctx, "u1", 100, "t1", "k"); return err },
		"AcceptTermsAddendum": func() error { return userSvc.AcceptTermsAddendum(ctx, "u1") },
	}
	for name, op := range ops {
		var p *problem.Problem
		if err := op(); !errors.As(err, &p) || p.Type != problem.TypeAccountErasurePending {
			t.Errorf("%s: err = %v, want %s", name, err, problem.TypeAccountErasurePending)
		}
	}
}

// Settlement of what already exists is never refused: that money cannot be turned away.
func TestErasureGateLeavesSettlementOpen(t *testing.T) {
	ctx := context.Background()
	gate := &stubGate{blocked: true}
	svc, _ := gatedServices(gate)
	_, _ = svc.ReleaseHold(ctx, "u1", "h-missing", "k")
	_, _ = svc.CashoutGame(ctx, "u1", 100, "t1", []string{"h-missing"}, "k", "")
	_ = svc.ConfirmDeposit(ctx, "tx-missing", "", "", false)
	if len(gate.calls) != 0 {
		t.Fatalf("settlement paths consulted the erasure gate %v; they must never be refused", gate.calls)
	}
}
