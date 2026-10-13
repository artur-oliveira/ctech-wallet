package services

import (
	"context"
	"testing"
	"time"

	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
	"gopkg.aoctech.app/wallet/api/internal/kycclient"
	"gopkg.aoctech.app/wallet/api/internal/problem"
)

type stubCarried struct {
	ex      *wallet.SelfExclusion
	gotHMAC string
}

func (c *stubCarried) GetCarriedSelfExclusion(_ context.Context, h string, _ time.Time) (*wallet.SelfExclusion, error) {
	c.gotHMAC = h
	return c.ex, nil
}

func carrySvc(kyc *kycclient.KYC, carried *stubCarried) (*WalletService, *stubUserRepo, *stubAudit) {
	users := &stubUserRepo{user: &wallet.User{GamblingAddendumVersion: wallet.CurrentGamblingAddendumVersion}}
	audit := &stubAudit{}
	svc := NewWalletService(newStubRepo(), users, audit, &stubLocker{}, nil, &stubKYC{rec: kyc})
	svc.SetErasure(nil, carried)
	return svc, users, audit
}

// D13: deleting an account must not be a way out of one's own self-exclusion.
func TestActivateGamblingReappliesCarriedSelfExclusion(t *testing.T) {
	until := time.Now().Add(20 * 24 * time.Hour).Format(time.RFC3339)
	carried := &stubCarried{ex: &wallet.SelfExclusion{Period: "30d", RequestedAt: time.Now().Add(-10 * 24 * time.Hour).Format(time.RFC3339), Until: until}}
	svc, users, audit := carrySvc(&kycclient.KYC{Level: "basic", CPF: "52998224725", CPFHMAC: "h1"}, carried)

	_, _, err := svc.ActivateGambling(context.Background(), "u-new", "basic", "", "", 100, 200, 300)
	isProblem(t, err, problem.TypeSelfExcluded)
	if carried.gotHMAC != "h1" {
		t.Fatalf("looked up %q, want h1", carried.gotHMAC)
	}
	if users.user.SelfExclusion == nil || users.user.SelfExclusion.Until != until {
		t.Fatalf("exclusion not re-applied: %+v", users.user.SelfExclusion)
	}
	last := audit.events[len(audit.events)-1]
	if last.EventType != wallet.EventSelfExclusionCarried || last.Actor != wallet.ActorErasureCarryOver {
		t.Fatalf("audit = %+v, want a carried-exclusion event", last)
	}
}

func TestActivateGamblingWithoutCarriedExclusionProceeds(t *testing.T) {
	carried := &stubCarried{}
	svc, _, _ := carrySvc(&kycclient.KYC{Level: "basic", CPF: "52998224725", CPFHMAC: "h1"}, carried)
	if _, _, err := svc.ActivateGambling(context.Background(), "u-new", "basic", "", "", 100, 200, 300); err != nil {
		t.Fatalf("ActivateGambling: %v", err)
	}
}

func TestActivateGamblingSkipsLookupWithoutHMAC(t *testing.T) {
	carried := &stubCarried{ex: &wallet.SelfExclusion{Period: "indefinite"}}
	svc, _, _ := carrySvc(&kycclient.KYC{Level: "basic", CPF: "52998224725"}, carried)
	if _, _, err := svc.ActivateGambling(context.Background(), "u-new", "basic", "", "", 100, 200, 300); err != nil {
		t.Fatalf("ActivateGambling: %v", err)
	}
	if carried.gotHMAC != "" {
		t.Fatal("no cpf_hmac served: there is nothing to look up")
	}
}
