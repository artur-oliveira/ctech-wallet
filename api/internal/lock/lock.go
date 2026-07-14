// Package lock provides a per-wallet advisory lock so only one operation runs
// against a wallet at a time (design spec §B, invariant 4). It is backed by
// Valkey SETNX in production and an in-memory store when no Valkey is configured
// (single-replica/dev only — the in-memory store is NOT shared across replicas).
package lock

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/artur-oliveira/ctech-wallet/api/internal/cache"
	"github.com/redis/go-redis/v9"
)

// LockTTL bounds how long a lock is held before it auto-releases, so a crashed
// process can never wedge a wallet forever (fail safe).
const LockTTL = 10 * time.Second

const lockKeyFmt = "wallet:%s"

// store is the minimal primitive the locker needs: atomic acquire and
// owner-checked release.
type store interface {
	setNX(ctx context.Context, key, token string, ttl time.Duration) (bool, error)
	delIfMatch(ctx context.Context, key, token string) error
}

// Locker acquires per-wallet locks.
type Locker struct {
	store store
}

// NewLocker returns a Valkey-backed locker when the cache backend is Redis,
// otherwise an in-memory locker (dev/single-replica only).
func NewLocker(c cache.Backend) *Locker {
	if rb, ok := c.(*cache.RedisBackend); ok {
		return &Locker{store: &redisStore{client: rb.Client()}}
	}
	return &Locker{store: newMemStore()}
}

// Acquire takes the lock for one wallet. On success it returns a release func
// (safe to call once) and ok=true. On contention it returns ok=false and no error.
func (l *Locker) Acquire(ctx context.Context, walletID string) (release func(), ok bool, err error) {
	token, err := newToken()
	if err != nil {
		return nil, false, err
	}
	key := fmt.Sprintf(lockKeyFmt, walletID)
	got, err := l.store.setNX(ctx, key, token, LockTTL)
	if err != nil {
		return nil, false, err
	}
	if !got {
		return nil, false, nil
	}
	return func() { _ = l.store.delIfMatch(ctx, key, token) }, true, nil
}

// AcquireOrdered takes locks for multiple wallets in a total order — the wallet
// IDs are sorted lexicographically (see sort.Strings below) — so any caller
// locking the same set acquires them in the identical order. That deterministic
// total order is what prevents deadlock (design spec §B, invariant 5); it does
// not depend on wallet type. It is all-or-nothing: if any lock is contended, the
// already-held ones are released and ok=false is returned.
func (l *Locker) AcquireOrdered(ctx context.Context, walletIDs ...string) (release func(), ok bool, err error) {
	ids := append([]string(nil), walletIDs...)
	sort.Strings(ids)

	releases := make([]func(), 0, len(ids))
	releaseAll := func() {
		for i := len(releases) - 1; i >= 0; i-- {
			releases[i]()
		}
	}
	for _, id := range ids {
		rel, got, err := l.Acquire(ctx, id)
		if err != nil {
			releaseAll()
			return nil, false, err
		}
		if !got {
			releaseAll()
			return nil, false, nil
		}
		releases = append(releases, rel)
	}
	return releaseAll, true, nil
}

func newToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// --- redis-backed store ---

type redisStore struct {
	client *redis.Client
}

func (s *redisStore) setNX(ctx context.Context, key, token string, ttl time.Duration) (bool, error) {
	return s.client.SetNX(ctx, key, token, ttl).Result()
}

// casDel deletes the key only if its value still matches token, so a lock whose
// TTL already expired (and was re-acquired by someone else) is never released by
// the previous owner.
var casDel = redis.NewScript(`
if redis.call("get", KEYS[1]) == ARGV[1] then
	return redis.call("del", KEYS[1])
end
return 0
`)

func (s *redisStore) delIfMatch(ctx context.Context, key, token string) error {
	return casDel.Run(ctx, s.client, []string{key}, token).Err()
}

// --- in-memory store (single replica / tests) ---

type memEntry struct {
	token   string
	expires time.Time
}

type memStore struct {
	mu   sync.Mutex
	keys map[string]memEntry
}

func newMemStore() *memStore { return &memStore{keys: make(map[string]memEntry)} }

func (s *memStore) setNX(_ context.Context, key, token string, ttl time.Duration) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.keys[key]; ok && time.Now().Before(e.expires) {
		return false, nil
	}
	s.keys[key] = memEntry{token: token, expires: time.Now().Add(ttl)}
	return true, nil
}

func (s *memStore) delIfMatch(_ context.Context, key, token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.keys[key]; ok && e.token == token {
		delete(s.keys, key)
	}
	return nil
}
