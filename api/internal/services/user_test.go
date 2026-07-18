package services

import (
	"context"
	"testing"

	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
)

type stubUserRepo struct {
	user             *wallet.User
	accepted         bool
	gamblingAccepted bool
}

func (r *stubUserRepo) Get(_ context.Context, _ string) (*wallet.User, error) { return r.user, nil }
func (r *stubUserRepo) AcceptTerms(_ context.Context, _ string) error {
	r.accepted = true
	if r.user == nil {
		r.user = &wallet.User{}
	}
	r.user.TermsAddendumVersion = wallet.CurrentTermsAddendumVersion
	return nil
}
func (r *stubUserRepo) AcceptGamblingAddendum(_ context.Context, _ string) error {
	r.gamblingAccepted = true
	if r.user == nil {
		r.user = &wallet.User{}
	}
	r.user.GamblingAddendumVersion = wallet.CurrentGamblingAddendumVersion
	return nil
}

// stubAudit records appended events so tests can assert consent was audited.
type stubAudit struct{ events []wallet.AuditEvent }

func (a *stubAudit) Append(_ context.Context, e *wallet.AuditEvent) error {
	a.events = append(a.events, *e)
	return nil
}

func TestMeGatesUserWithNoRow(t *testing.T) {
	// A user who never accepted has no row at all → must be gated.
	svc := NewUserService(&stubUserRepo{user: nil}, &stubAudit{})
	me, err := svc.Me(context.Background(), "u1")
	if err != nil {
		t.Fatalf("Me: %v", err)
	}
	if me.TermsAddendumAccepted {
		t.Error("expected a user with no row to be gated")
	}
}

func TestMeGatesStaleVersion(t *testing.T) {
	// Accepted an older version → must accept again (this is what a version bump does).
	svc := NewUserService(&stubUserRepo{user: &wallet.User{TermsAddendumVersion: "0.9"}}, &stubAudit{})
	me, _ := svc.Me(context.Background(), "u1")
	if me.TermsAddendumAccepted {
		t.Error("expected a stale accepted version to re-gate the user")
	}
}

func TestAcceptThenAccepted(t *testing.T) {
	repo := &stubUserRepo{}
	svc := NewUserService(repo, &stubAudit{})

	if err := svc.AcceptTermsAddendum(context.Background(), "u1"); err != nil {
		t.Fatalf("AcceptTermsAddendum: %v", err)
	}
	if !repo.accepted {
		t.Fatal("expected acceptance to be persisted")
	}
	me, _ := svc.Me(context.Background(), "u1")
	if !me.TermsAddendumAccepted {
		t.Error("expected user to be accepted after accepting the current version")
	}
	if me.TermsAddendumVersion != wallet.CurrentTermsAddendumVersion {
		t.Errorf("version = %q, want %q", me.TermsAddendumVersion, wallet.CurrentTermsAddendumVersion)
	}
}

func TestAcceptGamblingAddendumIsRecordedAndAudited(t *testing.T) {
	repo := &stubUserRepo{}
	audit := &stubAudit{}
	svc := NewUserService(repo, audit)

	if err := svc.AcceptGamblingAddendum(context.Background(), "u1", "203.0.113.7", "agent"); err != nil {
		t.Fatalf("AcceptGamblingAddendum: %v", err)
	}

	me, err := svc.Me(context.Background(), "u1")
	if err != nil {
		t.Fatalf("Me: %v", err)
	}
	if !me.GamblingAddendumAccepted {
		t.Error("gambling acceptance was not recorded")
	}
	// Accepting the gambling addendum must NOT grant the terms addendum.
	if me.TermsAddendumAccepted {
		t.Error("gambling acceptance must not imply terms acceptance")
	}

	// Consent must be provable after the fact.
	if len(audit.events) != 1 {
		t.Fatalf("len(audit events) = %d, want 1", len(audit.events))
	}
	e := audit.events[0]
	if e.EventType != wallet.EventGamblingAddendumAccepted || e.IP != "203.0.113.7" || e.UserAgent != "agent" {
		t.Errorf("audit event = %+v, want gambling_addendum_accepted with request context", e)
	}
}
