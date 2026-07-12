//go:build integration

package integration_test

import (
	"context"
	"testing"

	"github.com/artur-oliveira/ctech-wallet/api/internal/domain/id"
	"github.com/artur-oliveira/ctech-wallet/api/internal/domain/wallet"
)

// Two independent consent documents live on one row. Accepting either must never
// erase the other — a whole-row Put here would silently revoke consent the user
// actually gave.
func TestAcceptingOneAddendumDoesNotEraseTheOther(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())

	user := "u-" + id.New()
	if err := h.userRepo.AcceptTerms(ctx, user); err != nil {
		t.Fatalf("AcceptTerms: %v", err)
	}
	if err := h.userRepo.AcceptGamblingAddendum(ctx, user); err != nil {
		t.Fatalf("AcceptGamblingAddendum: %v", err)
	}
	u, err := h.userRepo.Get(ctx, user)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !u.TermsAccepted() {
		t.Error("accepting the gambling addendum erased the terms acceptance")
	}
	if !u.GamblingAccepted() {
		t.Error("gambling acceptance was not recorded")
	}

	// And in the other order.
	user2 := "u-" + id.New()
	if err := h.userRepo.AcceptGamblingAddendum(ctx, user2); err != nil {
		t.Fatalf("AcceptGamblingAddendum: %v", err)
	}
	if err := h.userRepo.AcceptTerms(ctx, user2); err != nil {
		t.Fatalf("AcceptTerms: %v", err)
	}
	u2, err := h.userRepo.Get(ctx, user2)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !u2.TermsAccepted() || !u2.GamblingAccepted() {
		t.Error("accepting the terms addendum erased the gambling acceptance")
	}
}

func TestAuditAppendIsAppendOnly(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := "u-" + id.New()

	first := &wallet.AuditEvent{
		UserID: user, EventType: wallet.EventGamblingActivated,
		Actor: user, IP: "203.0.113.7", UserAgent: "test-agent",
	}
	if err := h.audit.Append(ctx, first); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if first.EventID == "" || first.CreatedAt == "" {
		t.Fatal("Append must stamp EventID and CreatedAt")
	}

	second := &wallet.AuditEvent{
		UserID: user, EventType: wallet.EventGamblingAddendumAccepted, Actor: user,
	}
	if err := h.audit.Append(ctx, second); err != nil {
		t.Fatalf("Append second: %v", err)
	}

	events, err := h.audit.List(ctx, user, 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("len(events) = %d, want 2", len(events))
	}

	// Re-appending an event with the SAME identity must not overwrite the first —
	// the table is append-only, so the write is rejected outright.
	dup := &wallet.AuditEvent{
		UserID: user, EventType: "tampered", Actor: "attacker",
		EventID: first.EventID, CreatedAt: first.CreatedAt,
	}
	if err := h.audit.Append(ctx, dup); err == nil {
		t.Fatal("Append with an existing event identity must fail — the audit log is append-only")
	}

	events, err = h.audit.List(ctx, user, 10)
	if err != nil {
		t.Fatalf("List after dup: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("len(events) after rejected dup = %d, want 2", len(events))
	}
	for _, e := range events {
		if e.EventType == "tampered" || e.Actor == "attacker" {
			t.Fatal("an existing audit row was mutated")
		}
	}
}
