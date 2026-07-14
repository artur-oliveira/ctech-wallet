package pix

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/artur-oliveira/ctech-wallet/api/internal/cache"
	"github.com/artur-oliveira/ctech-wallet/api/internal/lock"
)

// countingInvoker returns a fixed token for GetToken and counts calls.
type countingInvoker struct {
	mu    sync.Mutex
	calls int
	token string
}

func (c *countingInvoker) invoke(_ context.Context, payload []byte) ([]byte, error) {
	var req rpcRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return nil, err
	}
	if req.Op != opGetToken {
		return json.Marshal(rpcResponse{Error: "unexpected op"})
	}
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
	return json.Marshal(rpcResponse{Payload: mustJSON(rpcGetTokenResult{Token: c.token, ExpiresIn: 3600})})
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
