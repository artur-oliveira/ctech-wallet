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
