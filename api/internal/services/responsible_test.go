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
	if _, _, err := svc.ActivateGambling(ctx, "u1", wallet.KYCVerified, "", "", 100, 200, 300); err == nil {
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

func configuredUser(d, w, m int64) *stubUserRepo {
	return &stubUserRepo{user: &wallet.User{
		GamblingAddendumVersion: wallet.CurrentGamblingAddendumVersion,
		GameLimits:              &wallet.GameLimits{Daily: d, Weekly: w, Monthly: m},
	}}
}

func TestSetGameLimitsFirstSetIsImmediate(t *testing.T) {
	users := &stubUserRepo{}
	svc, _, _ := newRespSvc(users)
	lim, err := svc.SetGameLimits(context.Background(), "u1", 100, 200, 300, "", "")
	if err != nil || lim.Daily != 100 || lim.Pending != nil {
		t.Fatalf("first set must be immediate: %+v err=%v", lim, err)
	}
}

func TestSetGameLimitsDecreaseImmediateIncreasePended(t *testing.T) {
	svc, _, _ := newRespSvc(configuredUser(100, 200, 300))
	// Pure decrease: immediate.
	lim, err := svc.SetGameLimits(context.Background(), "u1", 50, 100, 300, "", "")
	if err != nil || lim.Daily != 50 || lim.Weekly != 100 || lim.Pending != nil {
		t.Fatalf("decrease must be immediate: %+v err=%v", lim, err)
	}
	// Mixed: daily down now, weekly increase pended at +7d.
	svc2, _, _ := newRespSvc(configuredUser(100, 200, 300))
	lim, err = svc2.SetGameLimits(context.Background(), "u1", 80, 250, 300, "", "")
	if err != nil || lim.Daily != 80 || lim.Weekly != 200 || lim.Pending == nil {
		t.Fatalf("mixed change wrong: %+v err=%v", lim, err)
	}
	at, _ := time.Parse(time.RFC3339, lim.Pending.AppliesAt)
	if d := time.Until(at); d < 6*24*time.Hour || d > 8*24*time.Hour {
		t.Fatalf("weekly increase cooldown must be ~7d, got %v", d)
	}
	// Monthly increase: whole pending set waits 14d.
	svc3, _, _ := newRespSvc(configuredUser(100, 200, 300))
	lim, err = svc3.SetGameLimits(context.Background(), "u1", 100, 250, 400, "", "")
	if err != nil || lim.Pending == nil {
		t.Fatalf("monthly increase must pend: %+v err=%v", lim, err)
	}
	at, _ = time.Parse(time.RFC3339, lim.Pending.AppliesAt)
	if d := time.Until(at); d < 13*24*time.Hour || d > 15*24*time.Hour {
		t.Fatalf("monthly increase cooldown must be ~14d, got %v", d)
	}
}

func TestSetGameLimitsRejectsIncoherentOrdering(t *testing.T) {
	svc, _, _ := newRespSvc(&stubUserRepo{})
	if _, err := svc.SetGameLimits(context.Background(), "u1", 100, 50, 300, "", ""); err == nil {
		t.Fatal("weekly < daily must be rejected")
	}
}

func TestFundGameRequiresConfiguredLimits(t *testing.T) {
	svc, _, _ := newRespSvc(&stubUserRepo{})
	_, _, err := svc.FundGame(context.Background(), "u1", 100, "k1")
	isProblem(t, err, problem.TypeLimitsNotConfigured)
}

func TestFundGameEnforcesAndAccumulates(t *testing.T) {
	users := configuredUser(1000, 2000, 3000)
	svc, repo, _ := newRespSvc(users)
	ctx := context.Background()

	if _, _, err := svc.FundGame(ctx, "u1", 600, "k1"); err != nil {
		t.Fatal(err)
	}
	c := users.user.GameDepositCounters
	if c == nil || c.DaySum != 600 || c.WeekSum != 600 || c.MonthSum != 600 {
		t.Fatalf("counters after first deposit: %+v", c)
	}
	if _, _, err := svc.FundGame(ctx, "u1", 400, "k2"); err != nil {
		t.Fatal(err)
	}
	if users.user.GameDepositCounters.DaySum != 1000 {
		t.Fatalf("counters must accumulate: %+v", users.user.GameDepositCounters)
	}
	// Daily window exhausted.
	repo.transferCalled = false
	_, _, err := svc.FundGame(ctx, "u1", 1, "k3")
	isProblem(t, err, problem.TypeDepositLimitExceeded)
	if repo.transferCalled {
		t.Fatal("a blocked deposit must not touch wallets")
	}
	// A rolled day window resets the day sum but the week still counts.
	users.user.GameDepositCounters.DayKey = "2000-01-01"
	if _, _, err := svc.FundGame(ctx, "u1", 900, "k4"); err != nil {
		t.Fatalf("fresh day must reset the daily sum: %v", err)
	}
	if got := users.user.GameDepositCounters; got.DaySum != 900 || got.WeekSum != 1900 {
		t.Fatalf("after day roll: %+v", got)
	}
	// Weekly window now nearly exhausted: 1900/2000 — 200 more must breach weekly.
	_, _, err = svc.FundGame(ctx, "u1", 200, "k5")
	isProblem(t, err, problem.TypeDepositLimitExceeded)
}

func TestFundGamePromotesMaturedPending(t *testing.T) {
	users := configuredUser(100, 200, 300)
	users.user.GameLimits.Pending = &wallet.PendingLimits{Daily: 1000, Weekly: 2000, Monthly: 3000,
		AppliesAt: time.Now().Add(-time.Minute).Format(time.RFC3339)}
	svc, _, _ := newRespSvc(users)
	if _, _, err := svc.FundGame(context.Background(), "u1", 500, "k1"); err != nil {
		t.Fatalf("matured pending must apply before metering: %v", err)
	}
	if users.user.GameLimits.Pending != nil || users.user.GameLimits.Daily != 1000 {
		t.Fatalf("pending not promoted: %+v", users.user.GameLimits)
	}
}

func TestActivateGamblingRequiresLimits(t *testing.T) {
	users := &stubUserRepo{user: &wallet.User{GamblingAddendumVersion: wallet.CurrentGamblingAddendumVersion}}
	svc, repo, _ := newRespSvc(users)
	repo.notActivated = true
	if _, _, err := svc.ActivateGambling(context.Background(), "u1", wallet.KYCVerified, "", "", 0, 0, 0); err == nil {
		t.Fatal("fresh activation without limits must fail")
	}
	if _, _, err := svc.ActivateGambling(context.Background(), "u1", wallet.KYCVerified, "", "", 100, 200, 300); err != nil {
		t.Fatalf("activation with limits: %v", err)
	}
	if users.user.GameLimits == nil || users.user.GameLimits.Daily != 100 {
		t.Fatalf("limits not stored at activation: %+v", users.user.GameLimits)
	}
}

func TestCancelPendingLimits(t *testing.T) {
	users := configuredUser(100, 200, 300)
	users.user.GameLimits.Pending = &wallet.PendingLimits{Daily: 1000, Weekly: 2000, Monthly: 3000,
		AppliesAt: time.Now().Add(time.Hour).Format(time.RFC3339)}
	svc, _, _ := newRespSvc(users)
	lim, err := svc.CancelPendingLimits(context.Background(), "u1")
	if err != nil || lim.Pending != nil || lim.Daily != 100 {
		t.Fatalf("cancel failed: %+v err=%v", lim, err)
	}
}

func TestGameLimitsStatusReportsUsage(t *testing.T) {
	users := configuredUser(1000, 2000, 3000)
	svc, _, _ := newRespSvc(users)
	if _, _, err := svc.FundGame(context.Background(), "u1", 250, "k1"); err != nil {
		t.Fatal(err)
	}
	st, err := svc.GameLimitsStatus(context.Background(), "u1")
	if err != nil || st.Limits == nil || st.Usage.Daily != 250 || st.Usage.Weekly != 250 {
		t.Fatalf("status: %+v err=%v", st, err)
	}
	if st.Usage.DayReset == "" || st.Usage.MonthReset == "" {
		t.Fatalf("resets missing: %+v", st.Usage)
	}
}

func TestGameEligibilityFor(t *testing.T) {
	users := configuredUser(100, 200, 300)
	svc, repo, _ := newRespSvc(users)
	el, err := svc.GameEligibilityFor(context.Background(), "u1")
	if err != nil || !el.Activated || el.SelfExcluded || !el.LimitsConfigured {
		t.Fatalf("eligible user: %+v err=%v", el, err)
	}
	repo.notActivated = true
	users.user.SelfExclusion = &wallet.SelfExclusion{Period: "indefinite", RequestedAt: time.Now().Format(time.RFC3339)}
	el, err = svc.GameEligibilityFor(context.Background(), "u1")
	if err != nil || el.Activated || !el.SelfExcluded {
		t.Fatalf("excluded non-activated user: %+v err=%v", el, err)
	}
}
