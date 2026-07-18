package pix

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"gopkg.aoctech.app/api-commons/cache"
	"gopkg.aoctech.app/wallet/api/internal/lock"
	rpccontract "gopkg.aoctech.app/wallet/rpc-contract"
)

// countingInvoker returns a fixed token for GetToken and counts calls.
type countingInvoker struct {
	mu    sync.Mutex
	calls int
	token string
}

func (c *countingInvoker) invoke(_ context.Context, payload []byte) ([]byte, error) {
	var req rpccontract.Request
	if err := json.Unmarshal(payload, &req); err != nil {
		return nil, err
	}
	if req.Op != rpccontract.OpGetToken {
		return json.Marshal(rpccontract.Response{Error: "unexpected op"})
	}
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
	return json.Marshal(rpccontract.Response{Payload: mustJSON(rpccontract.GetTokenResult{Token: c.token, ExpiresIn: 3600})})
}

func (c *countingInvoker) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func newTestManager(inv *countingInvoker) *InterTokenManager {
	// in-memory locker (no Redis) — single-process single-flight still holds via
	// the in-process cond; the Valkey guard is skipped because locker is non-nil
	// but in-memory.
	m := &InterTokenManager{invoker: inv, locker: lock.NewLocker(cache.NewMemoryBackend(16))}
	m.cond = sync.NewCond(&m.mu)
	return m
}

// newTestTokenMgr builds a manager over an arbitrary invoker with cond
// initialized (the production constructor does this too).
func newTestTokenMgr(inv lambdaInvoker) *InterTokenManager {
	m := &InterTokenManager{invoker: inv}
	m.cond = sync.NewCond(&m.mu)
	return m
}

func TestInterTokenManagerFreshCacheShortCircuits(t *testing.T) {
	inv := &countingInvoker{token: "T1"}
	m := newTestManager(inv)
	ctx := context.Background()

	if tok, err := m.Get(ctx, false); err != nil || tok != "T1" {
		t.Fatalf("first Get: tok=%q err=%v", tok, err)
	}
	if tok, err := m.Get(ctx, false); err != nil || tok != "T1" {
		t.Fatalf("second Get should be cached: tok=%q err=%v", tok, err)
	}
	if inv.count() != 1 {
		t.Fatalf("expected 1 fetch (cache hit on 2nd), got %d", inv.count())
	}
}

func TestInterTokenManagerForceRefetch(t *testing.T) {
	inv := &countingInvoker{token: "T1"}
	m := newTestManager(inv)
	ctx := context.Background()

	if _, err := m.Get(ctx, false); err != nil {
		t.Fatalf("prime: %v", err)
	}
	// Force a refresh (as on a 401); expect a second fetch.
	if tok, err := m.Get(ctx, true); err != nil || tok != "T1" {
		t.Fatalf("forced Get: tok=%q err=%v", tok, err)
	}
	if inv.count() != 2 {
		t.Fatalf("expected 2 fetches (1 prime + 1 forced), got %d", inv.count())
	}
}

// TestInterTokenSharedAcrossReplicas proves the token lives in the shared
// cache, not in any one replica: one manager fetches, the other reads it
// without calling Inter. In prod the backend is Valkey (shared); here a
// MemoryBackend shared by both managers simulates that.
func TestInterTokenSharedAcrossReplicas(t *testing.T) {
	shared := cache.NewMemoryBackend(16)
	invA := &countingInvoker{token: "A"}
	invB := &countingInvoker{token: "B"} // must never be used
	mA := &InterTokenManager{invoker: invA, locker: lock.NewLocker(shared), cache: shared}
	mA.cond = sync.NewCond(&mA.mu)
	mB := &InterTokenManager{invoker: invB, locker: lock.NewLocker(shared), cache: shared}
	mB.cond = sync.NewCond(&mB.mu)
	ctx := context.Background()

	if tok, err := mA.Get(ctx, false); err != nil || tok != "A" {
		t.Fatalf("A.Get: tok=%q err=%v", tok, err)
	}
	// B must read A's token from the shared cache, not fetch its own.
	if tok, err := mB.Get(ctx, false); err != nil || tok != "A" {
		t.Fatalf("B.Get: tok=%q err=%v (expected A from shared cache)", tok, err)
	}
	if invA.count() != 1 {
		t.Fatalf("A should have fetched exactly once, got %d", invA.count())
	}
	if invB.count() != 0 {
		t.Fatalf("B must not fetch when A's token is shared, got %d", invB.count())
	}
}

func TestInterTokenManagerConcurrentSingleFlight(t *testing.T) {
	inv := &countingInvoker{token: "T"}
	m := newTestManager(inv)
	ctx := context.Background()

	const n = 25
	var wg sync.WaitGroup
	results := make([]string, n)
	errs := make([]error, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			// Token starts empty, so all goroutines want a refresh; they must coalesce
			// to a single fetch (a cold-start stampede), not one fetch each.
			results[i], errs[i] = m.Get(ctx, false)
		}(i)
	}
	wg.Wait()

	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("goroutine %d error: %v", i, errs[i])
		}
		if results[i] != "T" {
			t.Fatalf("goroutine %d got %q, want T", i, results[i])
		}
	}
	if inv.count() != 1 {
		t.Fatalf("concurrent refresh must coalesce to exactly 1 fetch, got %d", inv.count())
	}
}
