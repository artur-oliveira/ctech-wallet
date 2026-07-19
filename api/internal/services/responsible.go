// Responsible-gambling flows: self-exclusion and game-deposit limits
// (docs/specs/2026-07-19-responsible-gambling-design.md). Enforcement is
// wired into the existing chokepoints in wallet.go (ActivateGambling,
// FundGame, HoldGame); this file owns the state transitions.
package services

import (
	"context"
	"fmt"
	"time"

	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
	"gopkg.aoctech.app/wallet/api/internal/problem"
)

// requireNotExcluded loads the user row and fails with SelfExcluded while an
// exclusion is active. Callers that also need the row reuse the return value.
// A nil row (user never accepted anything) is trivially not excluded.
func (s *WalletService) requireNotExcluded(ctx context.Context, userID string) (*wallet.User, error) {
	u, err := s.users.Get(ctx, userID)
	if err != nil {
		return nil, err
	}
	if u.SelfExcluded(time.Now()) {
		until := ""
		if u.SelfExclusion != nil {
			until = u.SelfExclusion.Until
		}
		return nil, problem.SelfExcluded(until)
	}
	return u, nil
}

// SelfExclude records a self-exclusion. Extension-only: a new exclusion may
// never end earlier than the one it replaces, and nothing extends indefinite.
func (s *WalletService) SelfExclude(ctx context.Context, userID, period, ip, userAgent string) (*wallet.SelfExclusion, error) {
	now := time.Now()
	until, err := wallet.ExclusionUntil(period, now)
	if err != nil {
		return nil, problem.BadRequest(err.Error())
	}
	u, err := s.users.Get(ctx, userID)
	if err != nil {
		return nil, err
	}
	if cur := userExclusion(u); cur != nil && u.SelfExcluded(now) {
		if cur.Until == "" {
			return nil, problem.ExclusionChangeRejected("você já está autoexcluído por tempo indeterminado")
		}
		if until != "" {
			curUntil, err := time.Parse(time.RFC3339, cur.Until)
			newUntil, err2 := time.Parse(time.RFC3339, until)
			if err != nil || err2 != nil || !newUntil.After(curUntil) {
				return nil, problem.ExclusionChangeRejected("uma autoexclusão só pode ser estendida, nunca encurtada")
			}
		}
	}
	ex := &wallet.SelfExclusion{Period: period, RequestedAt: now.Format(time.RFC3339), Until: until}
	if err := s.users.SetSelfExclusion(ctx, userID, ex); err != nil {
		return nil, err
	}
	if err := s.audit.Append(ctx, &wallet.AuditEvent{
		UserID: userID, EventType: wallet.EventSelfExcluded, Actor: userID,
		After: period, IP: ip, UserAgent: userAgent,
	}); err != nil {
		return nil, err
	}
	return ex, nil
}

// RevokeSelfExclusion lifts an indefinite exclusion after its 90-day floor.
// Fixed periods are not revocable — they expire on their own.
func (s *WalletService) RevokeSelfExclusion(ctx context.Context, userID, ip, userAgent string) error {
	now := time.Now()
	u, err := s.users.Get(ctx, userID)
	if err != nil {
		return err
	}
	if userExclusion(u) == nil || !u.SelfExcluded(now) {
		return problem.ExclusionChangeRejected("não há autoexclusão ativa")
	}
	if u.SelfExclusion.Until != "" {
		return problem.ExclusionChangeRejected("autoexclusões por período fixo expiram sozinhas e não podem ser revogadas")
	}
	requested, err := time.Parse(time.RFC3339, u.SelfExclusion.RequestedAt)
	if err != nil || now.Sub(requested) < 90*24*time.Hour {
		return problem.ExclusionChangeRejected("autoexclusão por tempo indeterminado só pode ser revogada após 90 dias")
	}
	if err := s.users.SetSelfExclusion(ctx, userID, nil); err != nil {
		return err
	}
	return s.audit.Append(ctx, &wallet.AuditEvent{
		UserID: userID, EventType: wallet.EventSelfExclusionRevoked, Actor: userID,
		IP: ip, UserAgent: userAgent,
	})
}

// SetGameLimits stores new deposit limits. First configuration and decreases
// apply immediately; any increase waits out its cooldown as a pending set
// (7 days; 14 when the monthly limit grows). One pending set at a time — a new
// call replaces it wholesale and re-derives the cooldown.
func (s *WalletService) SetGameLimits(ctx context.Context, userID string, daily, weekly, monthly int64, ip, userAgent string) (*wallet.GameLimits, error) {
	if err := wallet.ValidateLimits(daily, weekly, monthly); err != nil {
		return nil, problem.BadRequest(err.Error())
	}
	u, err := s.users.Get(ctx, userID)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	cur, matured := u.EffectiveGameLimits(now)
	if matured { // promote before diffing so cooldowns compare against reality
		promoted := cur
		if err := s.users.SetGameLimits(ctx, userID, &promoted); err != nil {
			return nil, err
		}
	}
	next := wallet.GameLimits{Daily: daily, Weekly: weekly, Monthly: monthly}
	if u.LimitsConfigured() || matured {
		applied := wallet.GameLimits{
			Daily:   min(daily, cur.Daily),
			Weekly:  min(weekly, cur.Weekly),
			Monthly: min(monthly, cur.Monthly),
		}
		if daily > cur.Daily || weekly > cur.Weekly || monthly > cur.Monthly {
			cooldown := 7 * 24 * time.Hour
			if monthly > cur.Monthly {
				cooldown = 14 * 24 * time.Hour
			}
			applied.Pending = &wallet.PendingLimits{Daily: daily, Weekly: weekly, Monthly: monthly,
				AppliesAt: now.Add(cooldown).Format(time.RFC3339)}
		}
		next = applied
	}
	if err := s.users.SetGameLimits(ctx, userID, &next); err != nil {
		return nil, err
	}
	if err := s.audit.Append(ctx, &wallet.AuditEvent{UserID: userID,
		EventType: wallet.EventGameLimitsChanged, Actor: userID,
		After: fmt.Sprintf("d=%d w=%d m=%d", daily, weekly, monthly),
		IP:    ip, UserAgent: userAgent}); err != nil {
		return nil, err
	}
	return &next, nil
}

// CancelPendingLimits drops a scheduled increase. Always allowed — cancelling
// an increase keeps the stricter current limits.
func (s *WalletService) CancelPendingLimits(ctx context.Context, userID string) (*wallet.GameLimits, error) {
	u, err := s.users.Get(ctx, userID)
	if err != nil {
		return nil, err
	}
	if u == nil || u.GameLimits == nil {
		return nil, problem.LimitsNotConfigured()
	}
	lim, matured := u.EffectiveGameLimits(time.Now())
	if matured { // it already applied — cancelling now would be a silent decrease bypass… of the user's own stricter value, which is fine, but keep semantics honest: promote it
		if err := s.users.SetGameLimits(ctx, userID, &lim); err != nil {
			return nil, err
		}
		return &lim, nil
	}
	cleared := *u.GameLimits
	cleared.Pending = nil
	if err := s.users.SetGameLimits(ctx, userID, &cleared); err != nil {
		return nil, err
	}
	return &cleared, nil
}

// LimitsStatus is the UI-facing snapshot of the limit engine for one user.
type LimitsStatus struct {
	Limits   *wallet.GameLimits    `json:"limits"` // nil when unconfigured
	Usage    UsageWindows          `json:"usage"`
	Excluded *wallet.SelfExclusion `json:"excluded,omitempty"`
}

// UsageWindows carries the current-window sums and their reset times.
type UsageWindows struct {
	Daily      int64  `json:"daily"`
	Weekly     int64  `json:"weekly"`
	Monthly    int64  `json:"monthly"`
	DayReset   string `json:"day_resets_at"`
	WeekReset  string `json:"week_resets_at"`
	MonthReset string `json:"month_resets_at"`
}

// GameLimitsStatus returns limits (with matured pending promoted), current
// window usage and exclusion state for the responsible-gambling page.
func (s *WalletService) GameLimitsStatus(ctx context.Context, userID string) (*LimitsStatus, error) {
	u, err := s.users.Get(ctx, userID)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	st := &LimitsStatus{Excluded: userExclusion(u)}
	if u != nil && u.GameLimits != nil {
		lim, matured := u.EffectiveGameLimits(now)
		if matured {
			if err := s.users.SetGameLimits(ctx, userID, &lim); err != nil {
				return nil, err
			}
		}
		st.Limits = &lim
	}
	day, week, month := wallet.WindowKeys(now)
	var c wallet.GameDepositCounters
	if u != nil && u.GameDepositCounters != nil {
		c = *u.GameDepositCounters
	}
	d, w, m := c.SumsFor(day, week, month)
	dr, wr, mr := wallet.WindowResets(now)
	st.Usage = UsageWindows{Daily: d, Weekly: w, Monthly: m,
		DayReset: dr.Format(time.RFC3339), WeekReset: wr.Format(time.RFC3339), MonthReset: mr.Format(time.RFC3339)}
	return st, nil
}

// userExclusion nil-safely extracts the exclusion record.
func userExclusion(u *wallet.User) *wallet.SelfExclusion {
	if u == nil {
		return nil
	}
	return u.SelfExclusion
}
