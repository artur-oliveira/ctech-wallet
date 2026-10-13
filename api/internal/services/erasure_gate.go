package services

import (
	"context"
	"fmt"
	"time"

	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
	"gopkg.aoctech.app/wallet/api/internal/problem"
)

// ErasureGate reports whether the account-deletion saga has locked or erased
// a user (erasure.Store satisfies it). Decision D1: from the start of the
// grace period nothing new may happen to that user's money, whatever valid
// token the caller holds.
type ErasureGate interface {
	Blocked(ctx context.Context, sub string) (bool, error)
}

// CarriedExclusionStore reads a self-exclusion kept from an erased account of
// the same CPF (D13).
type CarriedExclusionStore interface {
	GetCarriedSelfExclusion(ctx context.Context, cpfHMAC string, now time.Time) (*wallet.SelfExclusion, error)
}

// checkErasure refuses a state-changing operation for a locked or erased
// user. A nil gate (cmd/reconcile, unit tests, local dev without
// ERASURE_QUEUE_URL) allows everything. A store error fails closed.
//
// Only operations that open NEW exposure call it. Settlement of what already
// exists (confirm a paid charge, release a hold, cash out) never does: that
// money cannot be refused, so it is accepted and reported as a blocker.
func checkErasure(ctx context.Context, gate ErasureGate, userID string) error {
	if gate == nil {
		return nil
	}
	blocked, err := gate.Blocked(ctx, userID)
	if err != nil {
		return fmt.Errorf("erasure gate: %w", err)
	}
	if blocked {
		return problem.AccountErasurePending()
	}
	return nil
}

// SetErasure wires the deletion lock and the carried self-exclusion lookup.
// Same setter pattern as SetBroadcaster: unset in cmd/reconcile and tests.
func (s *WalletService) SetErasure(gate ErasureGate, carried CarriedExclusionStore) {
	s.erasureGate = gate
	s.carried = carried
}

// SetErasureGate wires the deletion lock into the consent writes.
func (s *UserService) SetErasureGate(gate ErasureGate) {
	s.erasureGate = gate
}
