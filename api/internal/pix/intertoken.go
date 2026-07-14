// Package pix — intertoken.go owns the Inter OAuth token lifecycle on api's
// behalf. The token is fetched from pix-gateway's GetToken op (the only place
// that talks to Inter's token endpoint) and cached in memory. A Valkey-backed
// single-flight lock guards the refresh so Inter is hit at most a few times per
// hour, well under its 5/min throttle. api passes the current bearer to every
// PIX op on the wire; pix-gateway never reads SSM for it.
package pix

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/artur-oliveira/ctech-wallet/api/internal/config"
	"github.com/artur-oliveira/ctech-wallet/api/internal/lock"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
)

// tokenRefreshLockKey is the Valkey key serializing refreshes. It reuses the
// generic per-key Locker (key becomes "wallet:inter:token:refresh").
const tokenRefreshLockKey = "inter:token:refresh"

// tokenRefreshFloor is how far before expiry a cached token is treated as
// stale, so edge-of-expiry calls never hit Inter with an expired bearer.
const tokenRefreshFloor = 30 * time.Second

// proactiveRefreshInterval is how often the background loop force-refreshes.
// Token lifetime is ~1h; refresh 5 min early.
const proactiveRefreshInterval = 55 * time.Minute

// InterTokenManager owns the Inter OAuth bearer. It is an fx provider; its
// constructor registers startup prime + a background refresh loop.
type InterTokenManager struct {
	invoker lambdaInvoker
	locker  *lock.Locker

	mu        sync.Mutex
	cond      *sync.Cond // signals waiters when a refresh completes
	token     string
	expiry    time.Time
	refreshing bool   // an in-process refresh is in flight
	lastErr   error  // error from the most recent refresh (shared with waiters)
}

// NewInterTokenManager builds the manager. functionName is pix-gateway's
// outbound Lambda (config.PixGatewayFunctionName). The locker provides the
// cross-replica single-flight guard.
func NewInterTokenManager(client *lambda.Client, cfg *config.Config, locker *lock.Locker) *InterTokenManager {
	m := &InterTokenManager{
		invoker: &awsLambdaInvoker{client: client, functionName: cfg.PixGatewayFunctionName},
		locker:  locker,
	}
	m.cond = sync.NewCond(&m.mu)
	return m
}

// Get returns a usable bearer. A cached, unexpired token short-circuits (no
// I/O) regardless of force — this is what lets concurrent refreshes coalesce to
// a single fetch: the first caller refreshes, the rest wake up to a populated
// cache. force=true only matters on the initial decision to refresh when no
// usable token exists (e.g. after a 401). The Valkey lock extends the guard
// across replicas (best-effort; if it is unavailable, PIX traffic is never
// blocked on it — the in-process coalescing still holds).
func (m *InterTokenManager) Get(ctx context.Context, force bool) (string, error) {
	m.mu.Lock()
	if !force && m.token != "" && time.Now().Before(m.expiry) {
		tok := m.token
		m.mu.Unlock()
		return tok, nil
	}
	if m.refreshing {
		// Another goroutine is refreshing; wait for it, then take what it cached.
		m.cond.Wait()
		tok := m.token
		err := m.lastErr
		stillRefreshing := m.refreshing
		m.mu.Unlock()
		if tok != "" {
			return tok, nil
		}
		if !stillRefreshing {
			// Refresh finished but failed — surface the shared error (bounded retry).
			if err != nil {
				return "", err
			}
		}
		return m.Get(ctx, force)
	}
	// We become the refresher for this cycle.
	m.refreshing = true
	m.mu.Unlock()

	// Cross-replica guard: serialize the actual Inter call across processes.
	if m.locker != nil {
		if release, ok, lerr := m.locker.Acquire(ctx, tokenRefreshLockKey); lerr == nil && ok {
			defer release()
		}
	}

	tok, exp, ferr := m.fetch(ctx)

	m.mu.Lock()
	m.refreshing = false
	if ferr == nil {
		m.token = tok
		m.expiry = exp
		m.lastErr = nil
	} else {
		m.lastErr = ferr
	}
	m.cond.Broadcast()
	m.mu.Unlock()

	if ferr != nil {
		return "", ferr
	}
	return tok, nil
}

// fetch invokes pix-gateway's GetToken op and computes the local expiry from
// Inter's expires_in (clock-skew floor so a skewed value can't persist).
func (m *InterTokenManager) fetch(ctx context.Context) (string, time.Time, error) {
	reqJSON, err := json.Marshal(rpcRequest{Op: opGetToken})
	if err != nil {
		return "", time.Time{}, err
	}
	respJSON, err := m.invoker.invoke(ctx, reqJSON)
	if err != nil {
		return "", time.Time{}, err
	}
	var resp rpcResponse
	if err := json.Unmarshal(respJSON, &resp); err != nil {
		return "", time.Time{}, err
	}
	if resp.Error != "" {
		return "", time.Time{}, fmt.Errorf("inter token: %s", resp.Error)
	}
	var res rpcGetTokenResult
	if err := json.Unmarshal(resp.Payload, &res); err != nil {
		return "", time.Time{}, err
	}
	if res.Token == "" {
		return "", time.Time{}, errors.New("inter token: empty token")
	}
	exp := time.Now().Add(time.Duration(res.ExpiresIn)*time.Second - tokenRefreshFloor)
	return res.Token, exp, nil
}

// RefreshLoop proactively refreshes every proactiveRefreshInterval so the cache
// never goes stale in steady state. It runs until ctx is cancelled.
func (m *InterTokenManager) RefreshLoop(ctx context.Context) {
	ticker := time.NewTicker(proactiveRefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := m.Get(context.Background(), true); err != nil {
				slog.Warn("inter token proactive refresh failed", "err", err)
			}
		}
	}
}
