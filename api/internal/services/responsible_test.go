package services

import (
	"context"
	"testing"
	"time"

	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
	"gopkg.aoctech.app/wallet/api/internal/kycclient"
	"gopkg.aoctech.app/wallet/api/internal/problem"
)

func newRespSvc(users *stubUserRepo) (*WalletService, *stubRepo, *stubAudit) {
	repo := newStubRepo()
	audit := &stubAudit{}
	svc := NewWalletService(repo, users, audit, &stubLocker{}, nil, &stubKYC{rec: &kycclient.KYC{}})
	return svc, repo, audit
}

func excludedUser(period, until string) *stubUserRepo {
	return &stubUserRepo{user: &wallet.User{
		GamblingAddendumVersion: wallet.CurrentGamblingAddendumVersion,
		SelfExclusion:           &wallet.SelfExclusion{Period: period, RequestedAt: time.Now().Format(time.RFC3339), Until: until},
	}}
}

func TestSelfExcludeStoresAndAudits(t *testing.T) {
	users := &stubUserRepo{}
	svc, _, audit := newRespSvc(users)
	ex, err := svc.SelfExclude(context.Background(), "u1", "30d", "ip", "ua")
	if err != nil {
		t.Fatal(err)
	}
	if ex.Until == "" || users.user.SelfExclusion == nil {
		t.Fatalf("exclusion not stored: %+v", ex)
	}
	if len(audit.events) != 1 || audit.events[0].EventType != wallet.EventSelfExcluded {
		t.Fatalf("audit missing: %+v", audit.events)
	}
}

func TestSelfExcludeOnlyExtends(t *testing.T) {
	far := time.Now().Add(80 * 24 * time.Hour).Format(time.RFC3339)
	svc, _, _ := newRespSvc(excludedUser("90d", far))
	if _, err := svc.SelfExclude(context.Background(), "u1", "30d", "", ""); err == nil {
		t.Fatal("shortening must be rejected")
	}
	if _, err := svc.SelfExclude(context.Background(), "u1", "indefinite", "", ""); err != nil {
		t.Fatalf("upgrade to indefinite must succeed: %v", err)
	}
	svc2, _, _ := newRespSvc(excludedUser("indefinite", ""))
	if _, err := svc2.SelfExclude(context.Background(), "u1", "90d", "", ""); err == nil {
		t.Fatal("nothing extends indefinite")
	}
}

func TestRevokeSelfExclusionRules(t *testing.T) {
	// Fixed period: never revocable.
	svc, _, _ := newRespSvc(excludedUser("30d", time.Now().Add(24*time.Hour).Format(time.RFC3339)))
	if err := svc.RevokeSelfExclusion(context.Background(), "u1", "", ""); err == nil {
		t.Fatal("fixed period revoke must be rejected")
	}
	// Indefinite before 90 days: rejected.
	svc2, _, _ := newRespSvc(excludedUser("indefinite", ""))
	if err := svc2.RevokeSelfExclusion(context.Background(), "u1", "", ""); err == nil {
		t.Fatal("early indefinite revoke must be rejected")
	}
	// Indefinite after 90 days: allowed and audited.
	users := excludedUser("indefinite", "")
	users.user.SelfExclusion.RequestedAt = time.Now().Add(-91 * 24 * time.Hour).Format(time.RFC3339)
	svc3, _, audit := newRespSvc(users)
	if err := svc3.RevokeSelfExclusion(context.Background(), "u1", "", ""); err != nil {
		t.Fatal(err)
	}
	if users.user.SelfExclusion != nil {
		t.Fatal("exclusion not cleared")
	}
	if len(audit.events) != 1 || audit.events[0].EventType != wallet.EventSelfExclusionRevoked {
		t.Fatalf("audit missing: %+v", audit.events)
	}
}

func TestExclusionGates(t *testing.T) {
	users := excludedUser("indefinite", "")
	users.user.GameLimits = &wallet.GameLimits{Daily: 1000, Weekly: 1000, Monthly: 1000}
	svc, _, _ := newRespSvc(users)
	ctx := context.Background()

	if _, _, err := svc.FundGame(ctx, "u1", 100, "k1"); err == nil {
		t.Fatal("FundGame must be blocked for excluded user")
	} else {
		isProblem(t, err, problem.TypeSelfExcluded)
	}
	if _, err := svc.HoldGame(ctx, "u1", 100, "table-1", "k2"); err == nil {
		t.Fatal("HoldGame must be blocked for excluded user")
	}
	if _, _, err := svc.ActivateGambling(ctx, "u1", wallet.KYCVerified, "", ""); err == nil {
		t.Fatal("ActivateGambling must be blocked for excluded user")
	}

	// Reducing exposure and settling stay open.
	if _, _, err := svc.ReturnFromGame(ctx, "u1", 100, "k3"); err != nil {
		t.Fatalf("ReturnFromGame must stay open: %v", err)
	}
	if _, _, err := svc.PurchaseSandbox(ctx, "u1", 100, "k4"); err != nil {
		t.Fatalf("PurchaseSandbox must stay open (play-money): %v", err)
	}
}

func TestExpiredExclusionNoLongerGates(t *testing.T) {
	users := excludedUser("30d", time.Now().Add(-time.Hour).Format(time.RFC3339))
	users.user.GameLimits = &wallet.GameLimits{Daily: 1000, Weekly: 1000, Monthly: 1000}
	svc, _, _ := newRespSvc(users)
	if _, _, err := svc.FundGame(context.Background(), "u1", 100, "k1"); err != nil {
		t.Fatalf("expired exclusion must not block: %v", err)
	}
}
