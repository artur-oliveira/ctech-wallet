package services

import (
	"context"
	"errors"
	"testing"

	"gopkg.aoctech.app/wallet/api/internal/problem"
)

type recordingLocker struct {
	release      func()
	acquired     bool
	err          error
	singleCalls  int
	orderedCalls int
	orderedIDs   []string
}

func (l *recordingLocker) Acquire(context.Context, string) (func(), bool, error) {
	l.singleCalls++
	return l.release, l.acquired, l.err
}

func (l *recordingLocker) AcquireOrdered(_ context.Context, walletIDs ...string) (func(), bool, error) {
	l.orderedCalls++
	l.orderedIDs = append([]string(nil), walletIDs...)
	return l.release, l.acquired, l.err
}

func TestAcquireWallet(t *testing.T) {
	t.Run("returns release on success", func(t *testing.T) {
		released := false
		locker := &recordingLocker{acquired: true, release: func() { released = true }}

		release, err := acquireWallet(context.Background(), locker, "wallet-1")
		if err != nil {
			t.Fatalf("acquire wallet: %v", err)
		}
		release()
		if !released || locker.singleCalls != 1 || locker.orderedCalls != 0 {
			t.Fatalf("unexpected lock routing: released=%v single=%d ordered=%d", released, locker.singleCalls, locker.orderedCalls)
		}
	})

	t.Run("maps contention to wallet busy", func(t *testing.T) {
		_, err := acquireWallet(context.Background(), &recordingLocker{}, "wallet-1")
		var p *problem.Problem
		if !errors.As(err, &p) || p.Type != problem.TypeWalletBusy {
			t.Fatalf("expected wallet-busy problem, got %v", err)
		}
	})

	t.Run("preserves backend error", func(t *testing.T) {
		backendErr := errors.New("lock backend unavailable")
		_, err := acquireWallet(context.Background(), &recordingLocker{err: backendErr}, "wallet-1")
		if !errors.Is(err, backendErr) {
			t.Fatalf("expected backend error, got %v", err)
		}
	})
}

func TestAcquireWalletsUsesOrderedLock(t *testing.T) {
	locker := &recordingLocker{acquired: true, release: func() {}}
	release, err := acquireWallets(context.Background(), locker, "real-wallet", "game-wallet")
	if err != nil {
		t.Fatalf("acquire wallets: %v", err)
	}
	release()

	if locker.singleCalls != 0 || locker.orderedCalls != 1 {
		t.Fatalf("unexpected lock routing: single=%d ordered=%d", locker.singleCalls, locker.orderedCalls)
	}
	if len(locker.orderedIDs) != 2 || locker.orderedIDs[0] != "real-wallet" || locker.orderedIDs[1] != "game-wallet" {
		t.Fatalf("unexpected wallet IDs: %v", locker.orderedIDs)
	}
}
