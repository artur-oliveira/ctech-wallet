package wallet

import (
	"testing"
	"time"
)

func TestCarriedSelfExclusion(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)

	if _, _, ok := CarriedSelfExclusion(nil, now); ok {
		t.Fatal("no exclusion must carry nothing")
	}

	timed := &SelfExclusion{Period: "30d", RequestedAt: now.Add(-24 * time.Hour).Format(time.RFC3339),
		Until: now.Add(29 * 24 * time.Hour).Format(time.RFC3339)}
	ex, exp, ok := CarriedSelfExclusion(timed, now)
	if !ok || ex != *timed || !exp.Equal(now.Add(29*24*time.Hour)) {
		t.Fatalf("timed: got %+v %v %v, want the same exclusion until its end", ex, exp, ok)
	}

	expired := &SelfExclusion{Period: "30d", Until: now.Add(-time.Second).Format(time.RFC3339)}
	if _, _, ok := CarriedSelfExclusion(expired, now); ok {
		t.Fatal("an expired exclusion must not be carried")
	}

	indefinite := &SelfExclusion{Period: "indefinite", RequestedAt: now.Format(time.RFC3339)}
	ex, exp, ok = CarriedSelfExclusion(indefinite, now)
	if !ok || ex.Until != "" || !exp.Equal(now.AddDate(ErasureRetentionYears, 0, 0)) {
		t.Fatalf("indefinite: got %+v %v %v, want indefinite kept %d years", ex, exp, ok, ErasureRetentionYears)
	}

	// Same fail-closed rule as User.SelfExcluded: an unparseable end date
	// keeps the exclusion, for the longest horizon.
	garbage := &SelfExclusion{Period: "30d", Until: "not-a-date"}
	if _, exp, ok := CarriedSelfExclusion(garbage, now); !ok || !exp.Equal(now.AddDate(ErasureRetentionYears, 0, 0)) {
		t.Fatalf("unparseable until: got %v %v, want carried for %d years", exp, ok, ErasureRetentionYears)
	}
}
