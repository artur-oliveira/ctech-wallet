package services

import (
	"context"

	"gopkg.aoctech.app/api/internal/domain/wallet"
)

// UserRepo is the persistence surface for wallet-side user state: the consent
// documents the user has accepted. Shared by UserService (which writes them) and
// WalletService (which gates gambling activation on them).
type UserRepo interface {
	Get(ctx context.Context, userID string) (*wallet.User, error)
	AcceptTerms(ctx context.Context, userID string) error
	AcceptGamblingAddendum(ctx context.Context, userID string) error
}

// UserService owns the consent-acceptance state (terms + gambling addenda).
type UserService struct {
	repo  UserRepo
	audit Auditor
}

func NewUserService(repo UserRepo, audit Auditor) *UserService {
	return &UserService{repo: repo, audit: audit}
}

// Me describes the caller to the frontend. Both *Accepted flags are COMPUTED
// against the current version constants — never stored — so bumping a version
// re-gates every user immediately.
type Me struct {
	UserID                   string `json:"user_id"`
	TermsAddendumAccepted    bool   `json:"terms_addendum_accepted"`
	TermsAddendumVersion     string `json:"terms_addendum_version"`
	GamblingAddendumAccepted bool   `json:"gambling_addendum_accepted"`
	GamblingAddendumVersion  string `json:"gambling_addendum_version"`
}

func (s *UserService) Me(ctx context.Context, userID string) (*Me, error) {
	u, err := s.repo.Get(ctx, userID)
	if err != nil {
		return nil, err
	}
	return &Me{
		UserID:                   userID,
		TermsAddendumAccepted:    u.TermsAccepted(),
		TermsAddendumVersion:     wallet.CurrentTermsAddendumVersion,
		GamblingAddendumAccepted: u.GamblingAccepted(),
		GamblingAddendumVersion:  wallet.CurrentGamblingAddendumVersion,
	}, nil
}

// AcceptTermsAddendum records the caller's acceptance of the current version.
func (s *UserService) AcceptTermsAddendum(ctx context.Context, userID string) error {
	return s.repo.AcceptTerms(ctx, userID)
}

// AcceptGamblingAddendum records the caller's acceptance of the current
// responsible-gambling addendum — the consent ActivateGambling gates on. The
// acceptance is audited: consent must be provable after the fact.
func (s *UserService) AcceptGamblingAddendum(ctx context.Context, userID, ip, userAgent string) error {
	if err := s.repo.AcceptGamblingAddendum(ctx, userID); err != nil {
		return err
	}
	return s.audit.Append(ctx, &wallet.AuditEvent{
		UserID:    userID,
		EventType: wallet.EventGamblingAddendumAccepted,
		Actor:     userID,
		After:     wallet.CurrentGamblingAddendumVersion,
		IP:        ip,
		UserAgent: userAgent,
	})
}
