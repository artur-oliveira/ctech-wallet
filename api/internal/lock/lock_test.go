package lock

import (
	"context"
	"testing"

	"gopkg.aoctech.app/api-commons/cache"
)

// The CAS acquire/release/ordered-lock semantics are covered by the shared
// gopkg.aoctech.app/api-commons/lock package's own tests. This file only
// confirms the wrapper wires up correctly: NewLocker returns a working
// *Locker using wallet's own TTL and "wallet:" key namespace.

func TestNewLockerAcquireAndRelease(t *testing.T) {
	l := NewLocker(cache.NewMemoryBackend(16))
	ctx := context.Background()

	rel, ok, err := l.Acquire(ctx, "w1")
	if err != nil || !ok {
		t.Fatalf("first acquire: ok=%v err=%v", ok, err)
	}

	// Second acquire on the same wallet is contended — confirms the wrapper
	// actually delegates to the shared CAS locker rather than no-oping.
	_, ok2, err := l.Acquire(ctx, "w1")
	if err != nil {
		t.Fatalf("second acquire err: %v", err)
	}
	if ok2 {
		t.Fatal("expected contention on held wallet")
	}

	rel()
	_, ok3, _ := l.Acquire(ctx, "w1")
	if !ok3 {
		t.Fatal("expected reacquire after release")
	}
}

func TestNewLockerAcquireOrderedWiresThrough(t *testing.T) {
	l := NewLocker(cache.NewMemoryBackend(16))
	ctx := context.Background()

	rel, ok, err := l.AcquireOrdered(ctx, "game", "real", "sandbox")
	if err != nil || !ok {
		t.Fatalf("ordered acquire: ok=%v err=%v", ok, err)
	}
	rel()
}

// TestNewLockerAppliesWalletKeyPrefix proves the "wallet:" prefix is actually
// applied to the underlying store, not just self-consistent within the
// wrapper. It bypasses the wrapper's own Acquire by calling the embedded
// sharedlock.Locker directly (l.Locker) — the exact same store instance the
// wrapper's Acquire wrote to — so a raw Acquire on the literal prefixed
// string must find it contended, and a raw Acquire on the bare id must find
// it free (proving the wrapper didn't lock the unprefixed key instead).
//
// A second, independent NewLocker/sharedlock.New instance can't be used for
// this: the in-memory backend's CAS state lives in a per-Locker memStore,
// not in the cache.Backend argument itself (true of both the merged package
// and both of its pre-migration originals) — two separate instances never
// share lock state even when constructed against "the same" MemoryBackend.
func TestNewLockerAppliesWalletKeyPrefix(t *testing.T) {
	l := NewLocker(cache.NewMemoryBackend(16))
	ctx := context.Background()

	rel, ok, err := l.Acquire(ctx, "w1")
	if err != nil || !ok {
		t.Fatalf("acquire: ok=%v err=%v", ok, err)
	}
	defer rel()

	if _, okPrefixed, err := l.Locker.Acquire(ctx, "wallet:w1"); err != nil {
		t.Fatalf("raw acquire on prefixed key: %v", err)
	} else if okPrefixed {
		t.Fatal(`expected "wallet:w1" to already be held — the wrapper's Acquire must apply exactly this prefix`)
	}

	if _, okBare, err := l.Locker.Acquire(ctx, "w1"); err != nil {
		t.Fatalf("raw acquire on bare key: %v", err)
	} else if !okBare {
		t.Fatal(`expected the bare key "w1" to remain free — the wrapper must not lock the unprefixed key`)
	}
}
