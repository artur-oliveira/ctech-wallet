// Package lock provides a per-wallet advisory lock so only one operation runs
// against a wallet at a time (design spec §B, invariant 4). It is backed by
// Valkey SETNX in production and an in-memory store when no Valkey is configured
// (single-replica/dev only — the in-memory store is NOT shared across replicas).
//
// The CAS acquire/release mechanics are shared with ctech-poker's per-table
// lease in gopkg.aoctech.app/api-commons/lock; this package only adds
// wallet's own TTL and the "wallet:" key namespace, so a wallet ID (or any
// other key wallet code locks, e.g. the pix inter-token refresh key) can
// never collide with an unrelated key sharing the same Valkey instance.
package lock

import (
	"context"
	"time"

	"gopkg.aoctech.app/api-commons/cache"
	sharedlock "gopkg.aoctech.app/api-commons/lock"
)

// LockTTL bounds how long a lock is held before it auto-releases, so a crashed
// process can never wedge a wallet forever (fail safe).
const LockTTL = 10 * time.Second

// lockKeyPrefix namespaces every key this package locks.
const lockKeyPrefix = "wallet:"

// Locker acquires per-wallet locks. The CAS acquire/release mechanics live in
// the shared gopkg.aoctech.app/api-commons/lock package; this type only adds
// the "wallet:" key namespace.
type Locker struct {
	*sharedlock.Locker
}

// NewLocker returns a Valkey-backed locker when the cache backend is Redis,
// otherwise an in-memory locker (dev/single-replica only).
func NewLocker(c cache.Backend) *Locker {
	return &Locker{sharedlock.New(c, LockTTL)}
}

// Acquire takes the lock for one wallet. On success it returns a release func
// (safe to call once) and ok=true. On contention it returns ok=false and no error.
func (l *Locker) Acquire(ctx context.Context, walletID string) (release func(), ok bool, err error) {
	return l.Locker.Acquire(ctx, lockKeyPrefix+walletID)
}

// AcquireOrdered takes locks for multiple wallets in a total order — the
// shared Locker sorts the keys lexicographically — so any caller locking the
// same set acquires them in the identical order. That deterministic total
// order is what prevents deadlock (design spec §B, invariant 5); it does not
// depend on wallet type. It is all-or-nothing: if any lock is contended, the
// already-held ones are released and ok=false is returned.
func (l *Locker) AcquireOrdered(ctx context.Context, walletIDs ...string) (release func(), ok bool, err error) {
	keys := make([]string, len(walletIDs))
	for i, id := range walletIDs {
		keys[i] = lockKeyPrefix + id
	}
	return l.Locker.AcquireOrdered(ctx, keys...)
}

// Renew and StartHeartbeat are not used by wallet today (its locks are
// short-lived, per-operation — see LockTTL), but they're promoted from the
// embedded sharedlock.Locker and must apply the same "wallet:" prefix as
// Acquire/AcquireOrdered so a future caller can't accidentally operate on an
// unprefixed key that could collide with an unrelated cache entry.

func (l *Locker) Renew(ctx context.Context, walletID string) error {
	return l.Locker.Renew(ctx, lockKeyPrefix+walletID)
}

func (l *Locker) StartHeartbeat(ctx context.Context, walletID string, interval time.Duration, onLost func()) (stop func()) {
	return l.Locker.StartHeartbeat(ctx, lockKeyPrefix+walletID, interval, onLost)
}
