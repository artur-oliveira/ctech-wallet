// Package pix — intertoken.go owns the Inter OAuth token lifecycle on api's
// behalf. The token is fetched from pix-gateway's GetToken op (the only place
// that talks to Inter's token endpoint) and cached in Valkey so every api
// replica reads the SAME bearer. Only the lock winner ever calls Inter; the
// rest wait for the shared token to appear. A per-key Valkey lock serializes
// refreshes across replicas so Inter is hit at most a few times per hour, well
// under its 5/min throttle. api passes the current bearer to every PIX op on
// the wire; pix-gateway never reads SSM for it.
package pix

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"gopkg.aoctech.app/api-commons/cache"
	"gopkg.aoctech.app/wallet/api/internal/config"
	"gopkg.aoctech.app/wallet/api/internal/lock"
	rpccontract "gopkg.aoctech.app/wallet/rpc-contract"
)

// tokenCacheKey is the Valkey key holding the shared Inter bearer. All replicas
// read this; only the refresh-lock winner writes it.
const tokenCacheKey = "inter:token"

// tokenRefreshLockKey is the Valkey key serializing refreshes. It reuses the
// generic per-key Locker (key becomes "wallet:inter:token:refresh").
const tokenRefreshLockKey = "inter:token:refresh"

// tokenRefreshFloor is how far before expiry a cached token is treated as
// stale, so edge-of-expiry calls never hit Inter with an expired bearer.
const tokenRefreshFloor = 30 * time.Second

// proactiveRefreshInterval is how often the background loop nudges a refresh.
// The shared token's Valkey TTL drives the real refresh; the loop just keeps a
// replica warm so the first post-expiry call never blocks on a fetch.
const proactiveRefreshInterval = 55 * time.Minute

// cachedToken is the shared-token wire shape stored in Valkey.
type cachedToken struct {
	Token  string `json:"token"`
	Expiry int64  `json:"expiry"` // unix seconds, already floor-trimmed
}

// InterTokenManager owns the Inter OAuth bearer. It is an fx provider; its
// constructor registers startup prime + a background refresh loop.
type InterTokenManager struct {
	invoker lambdaInvoker
	locker  *lock.Locker
	cache   cache.Backend // shared token store (Valkey in prod, in-memory in dev)

	mu         sync.Mutex
	cond       *sync.Cond // signals waiters when a refresh completes
	token      string
	expiry     time.Time
	refreshing bool  // an in-process refresh is in flight
	lastErr    error // error from the most recent refresh (shared with waiters)
}

// NewInterTokenManager builds the manager. functionName is pix-gateway's
// outbound Lambda (config.PixGatewayFunctionName). The locker provides the
// cross-replica single-flight guard; cache is the shared token store.
func NewInterTokenManager(client *lambda.Client, cfg *config.Config, locker *lock.Locker, c cache.Backend) *InterTokenManager {
	m := &InterTokenManager{
		invoker: &awsLambdaInvoker{client: client, functionName: cfg.PixGatewayFunctionName},
		locker:  locker,
		cache:   c,
	}
	m.cond = sync.NewCond(&m.mu)
	return m
}

// Get returns a usable bearer. A cached, unexpired token short-circuits (no
// I/O): first the in-process hot cache, then the shared Valkey cache. Only when
// both miss does it refresh. force=true (used on a 401) bypasses both caches
// and refreshes a genuinely fresh token.
func (m *InterTokenManager) Get(ctx context.Context, force bool) (string, error) {
	if !force {
		if tok, ok := m.localValid(); ok {
			return tok, nil
		}
		if tok, exp, ok := m.sharedValid(ctx); ok {
			m.setLocal(tok, exp)
			return tok, nil
		}
	}
	return m.refresh(ctx, force)
}

// Invalidate drops the cached bearer (local + shared) so the next Get
// force-refreshes a genuinely new token. Used on a 401 from Inter.
func (m *InterTokenManager) Invalidate(ctx context.Context) {
	m.mu.Lock()
	m.token = ""
	m.expiry = time.Time{}
	m.mu.Unlock()
	if m.cache != nil {
		_ = m.cache.Delete(ctx, tokenCacheKey)
	}
}

// refresh coalesces in-process callers via the cond and serializes the actual
// Inter call across replicas via the Valkey lock. The lock winner fetches and
// writes the shared token; every other replica waits for it instead of calling
// Inter itself.
func (m *InterTokenManager) refresh(ctx context.Context, force bool) (string, error) {
	m.mu.Lock()
	// Re-check after taking the lock: another goroutine may have refreshed
	// while we contended for mu. (Inline the checks — we already hold m.mu, so
	// the locking helpers would deadlock.)
	if !force {
		if m.token != "" && time.Now().Before(m.expiry) {
			tok := m.token
			m.mu.Unlock()
			return tok, nil
		}
		if tok, exp, ok := m.sharedValid(ctx); ok { // sharedValid does not take mu
			m.token = tok
			m.expiry = exp
			m.mu.Unlock()
			return tok, nil
		}
	}
	if m.refreshing {
		// Another goroutine (in this process) is the refresher; wait for it.
		m.cond.Wait()
		tok := m.token
		err := m.lastErr
		stillRefreshing := m.refreshing
		m.mu.Unlock()
		if tok != "" {
			return tok, nil
		}
		// The refresher may have published only to the shared cache (another
		// replica could be the actual Inter caller); prefer that over a retry.
		if stok, exp, ok := m.sharedValid(ctx); ok {
			m.setLocal(stok, exp)
			return stok, nil
		}
		if !stillRefreshing && err != nil {
			return "", err
		}
		return m.Get(ctx, force)
	}
	m.refreshing = true
	m.mu.Unlock()

	// Force path (401 recovery): always fetch a fresh token and never reuse the
	// (rejected) shared one. Best-effort lock avoids an Inter storm.
	if force {
		if m.locker != nil {
			if release, ok, lerr := m.locker.Acquire(ctx, tokenRefreshLockKey); lerr == nil && ok {
				defer release()
			}
		}
		tok, exp, ferr := m.fetch(ctx)
		if ferr == nil {
			m.storeShared(ctx, tok, exp)
		}
		m.mu.Lock()
		m.refreshing = false
		if ferr == nil {
			m.token, m.expiry, m.lastErr = tok, exp, nil
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

	// Normal path: cross-replica single-flight. Only the lock winner calls
	// Inter; everyone else waits for the shared token to appear.
	if m.locker != nil {
		release, ok, lerr := m.locker.Acquire(ctx, tokenRefreshLockKey)
		if lerr == nil && ok {
			defer release()
		} else if lerr == nil && !ok {
			// Another replica holds the refresh lock. Wait for it to publish
			// the shared token rather than hitting Inter ourselves.
			if tok, exp, ok2 := m.waitShared(ctx); ok2 {
				m.setLocal(tok, exp)
				m.mu.Lock()
				m.refreshing = false
				m.cond.Broadcast()
				m.mu.Unlock()
				return tok, nil
			}
			// Timed out waiting; fall through and refresh ourselves (a redundant
			// Inter call beats a hard failure).
		}
	}

	// Double-check the shared cache now that we hold (or contended) the lock: a
	// racing replica may have published a fresh token in the meantime.
	if tok, exp, ok := m.sharedValid(ctx); ok {
		m.setLocal(tok, exp)
		m.mu.Lock()
		m.refreshing = false
		m.cond.Broadcast()
		m.mu.Unlock()
		return tok, nil
	}

	tok, exp, ferr := m.fetch(ctx)
	if ferr == nil {
		m.storeShared(ctx, tok, exp)
	}
	m.mu.Lock()
	m.refreshing = false
	if ferr == nil {
		m.token, m.expiry, m.lastErr = tok, exp, nil
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
	reqJSON, err := json.Marshal(rpccontract.Request{Op: rpccontract.OpGetToken})
	if err != nil {
		return "", time.Time{}, err
	}
	respJSON, err := m.invoker.invoke(ctx, reqJSON)
	if err != nil {
		return "", time.Time{}, err
	}
	var resp rpccontract.Response
	if err := json.Unmarshal(respJSON, &resp); err != nil {
		return "", time.Time{}, err
	}
	if resp.Error != "" {
		return "", time.Time{}, fmt.Errorf("inter token: %s", resp.Error)
	}
	var res rpccontract.GetTokenResult
	if err := json.Unmarshal(resp.Payload, &res); err != nil {
		return "", time.Time{}, err
	}
	if res.Token == "" {
		return "", time.Time{}, errors.New("inter token: empty token")
	}
	exp := time.Now().Add(time.Duration(res.ExpiresIn)*time.Second - tokenRefreshFloor)
	return res.Token, exp, nil
}

// localValid returns the in-process hot-cache token if still usable.
func (m *InterTokenManager) localValid() (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.token != "" && time.Now().Before(m.expiry) {
		return m.token, true
	}
	return "", false
}

// setLocal writes the in-process hot cache.
func (m *InterTokenManager) setLocal(token string, exp time.Time) {
	m.mu.Lock()
	m.token = token
	m.expiry = exp
	m.mu.Unlock()
}

// sharedValid returns the shared Valkey token if present and not stale.
func (m *InterTokenManager) sharedValid(ctx context.Context) (string, time.Time, bool) {
	if m.cache == nil {
		return "", time.Time{}, false
	}
	raw, ok, err := m.cache.Get(ctx, tokenCacheKey)
	if err != nil || !ok || len(raw) == 0 {
		return "", time.Time{}, false
	}
	var ct cachedToken
	if err := json.Unmarshal(raw, &ct); err != nil {
		return "", time.Time{}, false
	}
	if ct.Token == "" || time.Now().Unix() >= ct.Expiry {
		return "", time.Time{}, false
	}
	return ct.Token, time.Unix(ct.Expiry, 0), true
}

// storeShared writes the token to the shared Valkey cache. The TTL keeps the
// key alive a little past the token's floor-trimmed expiry so a refresher in
// the stale window can overwrite it; Redis auto-expires it shortly after.
func (m *InterTokenManager) storeShared(ctx context.Context, token string, exp time.Time) {
	if m.cache == nil {
		return
	}
	ct := cachedToken{Token: token, Expiry: exp.Unix()}
	raw, err := json.Marshal(ct)
	if err != nil {
		return
	}
	ttl := int(time.Until(exp).Seconds()) + 60
	if ttl < 1 {
		ttl = 1
	}
	_ = m.cache.Set(ctx, tokenCacheKey, raw, ttl)
}

// waitShared polls the shared cache for a freshly published token (written by
// the replica that won the refresh lock). Bounded so a stuck replica can't
// block us forever — we then fall back to refreshing ourselves.
func (m *InterTokenManager) waitShared(ctx context.Context) (string, time.Time, bool) {
	deadline := time.Now().Add(5 * time.Second)
	for {
		if tok, exp, ok := m.sharedValid(ctx); ok {
			return tok, exp, true
		}
		select {
		case <-ctx.Done():
			return "", time.Time{}, false
		case <-time.After(100 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			return "", time.Time{}, false
		}
	}
}

// RefreshLoop proactively nudges a refresh every proactiveRefreshInterval so a
// replica stays warm. It runs until ctx is cancelled. force=false lets it reuse
// the shared token (it only refreshes when the shared cache is actually stale).
func (m *InterTokenManager) RefreshLoop(ctx context.Context) {
	ticker := time.NewTicker(proactiveRefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := m.Get(context.Background(), false); err != nil {
				slog.Warn("inter token proactive refresh failed", "err", err)
			}
		}
	}
}
