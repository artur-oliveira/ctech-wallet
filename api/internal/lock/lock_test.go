package lock

import (
	"context"
	"testing"
)

func newTestLocker() *Locker { return &Locker{store: newMemStore()} }

func TestAcquireAndRelease(t *testing.T) {
	l := newTestLocker()
	ctx := context.Background()

	rel, ok, err := l.Acquire(ctx, "w1")
	if err != nil || !ok {
		t.Fatalf("first acquire: ok=%v err=%v", ok, err)
	}

	// Second acquire on the same wallet is contended.
	_, ok2, err := l.Acquire(ctx, "w1")
	if err != nil {
		t.Fatalf("second acquire err: %v", err)
	}
	if ok2 {
		t.Fatal("expected contention on held wallet")
	}

	// Different wallet is independent.
	rel3, ok3, _ := l.Acquire(ctx, "w2")
	if !ok3 {
		t.Fatal("expected independent wallet to acquire")
	}
	rel3()

	// After release, the wallet is acquirable again.
	rel()
	_, ok4, _ := l.Acquire(ctx, "w1")
	if !ok4 {
		t.Fatal("expected reacquire after release")
	}
}

func TestAcquireOrderedAllOrNothing(t *testing.T) {
	l := newTestLocker()
	ctx := context.Background()

	// Pre-hold the sandbox wallet so the ordered acquire must fail cleanly.
	rel, ok, _ := l.Acquire(ctx, "sandbox")
	if !ok {
		t.Fatal("setup acquire failed")
	}

	_, ok2, err := l.AcquireOrdered(ctx, "real", "sandbox")
	if err != nil {
		t.Fatalf("ordered acquire err: %v", err)
	}
	if ok2 {
		t.Fatal("expected ordered acquire to fail when one lock is held")
	}

	// The "real" lock must have been released back (all-or-nothing).
	relReal, okReal, _ := l.Acquire(ctx, "real")
	if !okReal {
		t.Fatal("expected 'real' to be free after failed ordered acquire")
	}
	relReal()
	rel()

	// Now both free → ordered acquire succeeds.
	relAll, ok3, _ := l.AcquireOrdered(ctx, "real", "sandbox")
	if !ok3 {
		t.Fatal("expected ordered acquire to succeed when both free")
	}
	relAll()
}
