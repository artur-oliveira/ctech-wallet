# Responsible Gambling (Self-Exclusion + Game-Deposit Limits) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement the approved spec `docs/specs/2026-07-19-responsible-gambling-design.md`: self-exclusion (30d/90d/indefinite) and mandatory daily/weekly/monthly game-deposit limits with increase cooldowns, plus the M2M status route ctech-poker Phase 5 consumes.

**Architecture:** All state lives on the existing `User` row (partial updates only — whole-row Puts are forbidden there). Enforcement hooks into the existing chokepoints: `ActivateGambling`, `FundGame`, `HoldGame`. Window counters are optimistically-conditioned items inside the same DynamoDB transaction as the deposit transfer.

**Tech Stack:** Go + Fiber v3 + DynamoDB (existing wallet stack), Next.js (existing wallet UI).

## Global Constraints

- Amounts are `int64` centavos everywhere.
- `User` row writers MUST update partially (`UpdateItem` with SET on specific attributes) — never Put the whole row (existing invariant, see `domain/wallet/user.go`).
- Calendar windows in `America/Sao_Paulo`; import `_ "time/tzdata"` in `cmd/api/main.go` and `cmd/reconcile/main.go` so containers without zoneinfo still resolve it.
- Every new problem type goes in `api/internal/problem/problem.go` following the existing constructor style.
- Existing invariants must survive: `game/withdraw`, `hold/release`, `cashout` are NEVER blocked by exclusion or limits; sandbox play is never blocked by exclusion.
- Error copy in English (problem details), UI copy in pt-BR (existing i18n pattern).

---

### Task 1: Domain — types, window keys, and pure gating logic

**Files:**
- Create: `api/internal/domain/wallet/responsible.go`
- Test: `api/internal/domain/wallet/responsible_test.go`
- Modify: `api/internal/domain/wallet/user.go` (add fields to `User`)

**Interfaces:**
- Produces (consumed by Tasks 2-6):
  - `type SelfExclusion struct { Period, RequestedAt, Until string }` (embedded pointer on `User` as `self_exclusion`)
  - `type GameLimits struct { Daily, Weekly, Monthly int64; Pending *PendingLimits }` (`game_limits`)
  - `type PendingLimits struct { Daily, Weekly, Monthly int64; AppliesAt string }`
  - `type GameDepositCounters struct { DayKey string; DaySum int64; WeekKey string; WeekSum int64; MonthKey string; MonthSum int64 }` (`game_deposit_counters`)
  - `func (u *User) SelfExcluded(now time.Time) bool`
  - `func (u *User) EffectiveGameLimits(now time.Time) (GameLimits, bool /*pendingApplied*/)`
  - `func (u *User) LimitsConfigured() bool`
  - `func WindowKeys(now time.Time) (day, week, month string)`
  - `func WindowResets(now time.Time) (day, week, month time.Time)`
  - `func (c GameDepositCounters) SumsFor(day, week, month string) (d, w, m int64)` — zero when key mismatches
  - `func CheckDeposit(limits GameLimits, c GameDepositCounters, amount int64, now time.Time) *LimitBreach` (`nil` = allowed); `type LimitBreach struct { Window string; Limit, Used int64; ResetsAt time.Time }`
  - `func ExclusionUntil(period string, now time.Time) (until string, err error)` — `"30d"`/`"90d"` → RFC3339, `"indefinite"` → `""`

- [ ] **Step 1: Write failing tests**

```go
// api/internal/domain/wallet/responsible_test.go
package wallet

import (
	"testing"
	"time"
)

func spTime(s string) time.Time {
	loc, _ := time.LoadLocation("America/Sao_Paulo")
	t, _ := time.ParseInLocation("2006-01-02 15:04", s, loc)
	return t
}

func TestWindowKeysUseSaoPauloCalendar(t *testing.T) {
	// 2026-07-19 23:30 UTC is 2026-07-19 20:30 in São Paulo (UTC-3).
	utc := time.Date(2026, 7, 19, 23, 30, 0, 0, time.UTC)
	day, week, month := WindowKeys(utc)
	if day != "2026-07-19" || month != "2026-07" {
		t.Fatalf("got day=%s month=%s", day, month)
	}
	if week != "2026-W29" { // 2026-07-19 is a Sunday of ISO week 29
		t.Fatalf("got week=%s", week)
	}
	// 00:30 UTC next day is STILL 2026-07-19 in São Paulo.
	day2, _, _ := WindowKeys(time.Date(2026, 7, 20, 0, 30, 0, 0, time.UTC))
	if day2 != "2026-07-19" {
		t.Fatalf("UTC rollover leaked into SP day key: %s", day2)
	}
}

func TestSelfExcluded(t *testing.T) {
	now := spTime("2026-07-19 12:00")
	cases := []struct {
		name string
		ex   *SelfExclusion
		want bool
	}{
		{"nil", nil, false},
		{"active fixed", &SelfExclusion{Period: "30d", Until: now.Add(time.Hour).Format(time.RFC3339)}, true},
		{"expired fixed", &SelfExclusion{Period: "30d", Until: now.Add(-time.Hour).Format(time.RFC3339)}, false},
		{"indefinite", &SelfExclusion{Period: "indefinite"}, true},
	}
	for _, c := range cases {
		u := &User{SelfExclusion: c.ex}
		if got := u.SelfExcluded(now); got != c.want {
			t.Fatalf("%s: got %v", c.name, got)
		}
	}
}

func TestEffectiveGameLimitsAppliesMaturedPending(t *testing.T) {
	now := spTime("2026-07-19 12:00")
	u := &User{GameLimits: &GameLimits{Daily: 100, Weekly: 500, Monthly: 1000,
		Pending: &PendingLimits{Daily: 200, Weekly: 500, Monthly: 1000, AppliesAt: now.Add(-time.Minute).Format(time.RFC3339)}}}
	lim, applied := u.EffectiveGameLimits(now)
	if !applied || lim.Daily != 200 || lim.Pending != nil {
		t.Fatalf("matured pending not applied: %+v applied=%v", lim, applied)
	}
	u.GameLimits.Pending.AppliesAt = now.Add(time.Hour).Format(time.RFC3339)
	lim, applied = u.EffectiveGameLimits(now)
	if applied || lim.Daily != 100 {
		t.Fatalf("immature pending applied early: %+v", lim)
	}
}

func TestCheckDeposit(t *testing.T) {
	now := spTime("2026-07-19 12:00")
	day, week, month := WindowKeys(now)
	lim := GameLimits{Daily: 100, Weekly: 300, Monthly: 500}
	c := GameDepositCounters{DayKey: day, DaySum: 80, WeekKey: week, WeekSum: 80, MonthKey: month, MonthSum: 80}
	if b := CheckDeposit(lim, c, 20, now); b != nil {
		t.Fatalf("exactly-at-limit must pass: %+v", b)
	}
	if b := CheckDeposit(lim, c, 21, now); b == nil || b.Window != "daily" {
		t.Fatalf("expected daily breach, got %+v", b)
	}
	// Stale day key = fresh day, but week still counts.
	c.DayKey = "2026-07-18"
	c.WeekSum = 290
	if b := CheckDeposit(lim, c, 20, now); b == nil || b.Window != "weekly" {
		t.Fatalf("expected weekly breach, got %+v", b)
	}
}

func TestExclusionUntil(t *testing.T) {
	now := spTime("2026-07-19 12:00")
	until, err := ExclusionUntil("30d", now)
	if err != nil || until == "" {
		t.Fatal(err)
	}
	if until2, _ := ExclusionUntil("indefinite", now); until2 != "" {
		t.Fatal("indefinite must have empty until")
	}
	if _, err := ExclusionUntil("7d", now); err == nil {
		t.Fatal("unknown period must error")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd api && go test ./internal/domain/wallet/ -run 'TestWindowKeys|TestSelfExcluded|TestEffective|TestCheckDeposit|TestExclusionUntil' -v`
Expected: compile errors (types undefined).

- [ ] **Step 3: Implement**

```go
// api/internal/domain/wallet/responsible.go
package wallet

import (
	"fmt"
	"time"
)

// saoPaulo is the calendar for every responsible-gambling window: users reason
// in their own local day/week/month, not UTC.
var saoPaulo = mustLoadSP()

func mustLoadSP() *time.Location {
	loc, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		panic("America/Sao_Paulo tz missing — import _ \"time/tzdata\" in main: " + err.Error())
	}
	return loc
}

// SelfExclusion records a user's self-exclusion. Fixed periods carry Until;
// indefinite has Until == "" and never expires on its own.
type SelfExclusion struct {
	Period      string `dynamodbav:"period" json:"period"` // "30d" | "90d" | "indefinite"
	RequestedAt string `dynamodbav:"requested_at" json:"requested_at"`
	Until       string `dynamodbav:"until,omitempty" json:"until,omitempty"`
}

// GameLimits are the user-set deposit caps in centavos. Zero = not configured.
type GameLimits struct {
	Daily   int64          `dynamodbav:"daily" json:"daily"`
	Weekly  int64          `dynamodbav:"weekly" json:"weekly"`
	Monthly int64          `dynamodbav:"monthly" json:"monthly"`
	Pending *PendingLimits `dynamodbav:"pending,omitempty" json:"pending,omitempty"`
}

// PendingLimits is a scheduled increase waiting out its cooldown.
type PendingLimits struct {
	Daily     int64  `dynamodbav:"daily" json:"daily"`
	Weekly    int64  `dynamodbav:"weekly" json:"weekly"`
	Monthly   int64  `dynamodbav:"monthly" json:"monthly"`
	AppliesAt string `dynamodbav:"applies_at" json:"applies_at"`
}

// GameDepositCounters accumulate real→game deposits per calendar window. A key
// mismatch means the window rolled and the sum is logically zero.
type GameDepositCounters struct {
	DayKey   string `dynamodbav:"day_key,omitempty" json:"day_key,omitempty"`
	DaySum   int64  `dynamodbav:"day_sum,omitempty" json:"day_sum,omitempty"`
	WeekKey  string `dynamodbav:"week_key,omitempty" json:"week_key,omitempty"`
	WeekSum  int64  `dynamodbav:"week_sum,omitempty" json:"week_sum,omitempty"`
	MonthKey string `dynamodbav:"month_key,omitempty" json:"month_key,omitempty"`
	MonthSum int64  `dynamodbav:"month_sum,omitempty" json:"month_sum,omitempty"`
}

// LimitBreach describes which window a deposit would overflow.
type LimitBreach struct {
	Window   string // "daily" | "weekly" | "monthly"
	Limit    int64
	Used     int64
	ResetsAt time.Time
}

// WindowKeys returns the São Paulo calendar keys for now.
func WindowKeys(now time.Time) (day, week, month string) {
	t := now.In(saoPaulo)
	y, w := t.ISOWeek()
	return t.Format("2006-01-02"), fmt.Sprintf("%04d-W%02d", y, w), t.Format("2006-01")
}

// WindowResets returns when each window rolls over (São Paulo calendar).
func WindowResets(now time.Time) (day, week, month time.Time) {
	t := now.In(saoPaulo)
	day = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, saoPaulo).AddDate(0, 0, 1)
	daysToMonday := (8 - int(t.Weekday())) % 7
	if daysToMonday == 0 {
		daysToMonday = 7
	}
	week = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, saoPaulo).AddDate(0, 0, daysToMonday)
	month = time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, saoPaulo).AddDate(0, 1, 0)
	return day, week, month
}

// SumsFor returns the counters' sums for the given keys, zeroing any window
// whose key rolled.
func (c GameDepositCounters) SumsFor(day, week, month string) (d, w, m int64) {
	if c.DayKey == day {
		d = c.DaySum
	}
	if c.WeekKey == week {
		w = c.WeekSum
	}
	if c.MonthKey == month {
		m = c.MonthSum
	}
	return d, w, m
}

// CheckDeposit reports the first window `amount` would overflow, or nil.
func CheckDeposit(limits GameLimits, c GameDepositCounters, amount int64, now time.Time) *LimitBreach {
	day, week, month := WindowKeys(now)
	d, w, m := c.SumsFor(day, week, month)
	dr, wr, mr := WindowResets(now)
	switch {
	case d+amount > limits.Daily:
		return &LimitBreach{Window: "daily", Limit: limits.Daily, Used: d, ResetsAt: dr}
	case w+amount > limits.Weekly:
		return &LimitBreach{Window: "weekly", Limit: limits.Weekly, Used: w, ResetsAt: wr}
	case m+amount > limits.Monthly:
		return &LimitBreach{Window: "monthly", Limit: limits.Monthly, Used: m, ResetsAt: mr}
	}
	return nil
}

// SelfExcluded reports whether the user is currently excluded. Fixed periods
// expire lazily; indefinite never does.
func (u *User) SelfExcluded(now time.Time) bool {
	if u == nil || u.SelfExclusion == nil {
		return false
	}
	if u.SelfExclusion.Until == "" {
		return true // indefinite
	}
	until, err := time.Parse(time.RFC3339, u.SelfExclusion.Until)
	return err != nil || now.Before(until) // unparseable fails closed (excluded)
}

// LimitsConfigured reports whether all three limits are set.
func (u *User) LimitsConfigured() bool {
	return u != nil && u.GameLimits != nil &&
		u.GameLimits.Daily > 0 && u.GameLimits.Weekly > 0 && u.GameLimits.Monthly > 0
}

// EffectiveGameLimits returns the limits with a matured pending increase
// applied. `applied` tells the caller to persist the promotion (lazy apply).
func (u *User) EffectiveGameLimits(now time.Time) (GameLimits, bool) {
	if u == nil || u.GameLimits == nil {
		return GameLimits{}, false
	}
	lim := *u.GameLimits
	if p := lim.Pending; p != nil {
		at, err := time.Parse(time.RFC3339, p.AppliesAt)
		if err == nil && !now.Before(at) {
			return GameLimits{Daily: p.Daily, Weekly: p.Weekly, Monthly: p.Monthly}, true
		}
	}
	return lim, false
}

// ValidateLimits enforces 0 < daily ≤ weekly ≤ monthly.
func ValidateLimits(daily, weekly, monthly int64) error {
	if daily <= 0 || weekly < daily || monthly < weekly {
		return fmt.Errorf("limits must satisfy 0 < daily <= weekly <= monthly")
	}
	return nil
}

// ExclusionUntil resolves a period string to its expiry (empty = indefinite).
func ExclusionUntil(period string, now time.Time) (string, error) {
	switch period {
	case "30d":
		return now.AddDate(0, 0, 30).Format(time.RFC3339), nil
	case "90d":
		return now.AddDate(0, 0, 90).Format(time.RFC3339), nil
	case "indefinite":
		return "", nil
	default:
		return "", fmt.Errorf("unknown exclusion period %q", period)
	}
}
```

Add to `User` struct in `user.go` (after `GamblingActivatedAt`):

```go
	SelfExclusion       *SelfExclusion       `dynamodbav:"self_exclusion,omitempty" json:"-"`
	GameLimits          *GameLimits          `dynamodbav:"game_limits,omitempty" json:"-"`
	GameDepositCounters *GameDepositCounters `dynamodbav:"game_deposit_counters,omitempty" json:"-"`
```

- [ ] **Step 4: Run to verify pass** — same command as Step 2, expected PASS. Also `go build ./...`.

- [ ] **Step 5: Commit** — `git add api/internal/domain/wallet && git commit -m "feat(domain): self-exclusion, game-deposit limits and window counters"`

---

### Task 2: Problem types

**Files:**
- Modify: `api/internal/problem/problem.go`
- Test: extend `api/internal/problem/` if a test file exists; otherwise covered by service tests (Task 4-5).

**Interfaces:**
- Produces: `problem.SelfExcluded(until string)`, `problem.LimitsNotConfigured()`, `problem.DepositLimitExceeded(window string, limit, used int64, resetsAt time.Time)`, `problem.ExclusionChangeRejected(detail string)` — consumed by Tasks 4-6.

- [ ] **Step 1: Implement** (follow the existing constructor style, e.g. `GamblingNotActivated`):

```go
// SelfExcluded: the user self-excluded from real-money gambling. until is
// RFC3339, or "" for indefinite.
func SelfExcluded(until string) *Problem {
	p := New(fiber.StatusConflict, "https://ctech.app/problems/self-excluded",
		"Self-excluded", "user has self-excluded from real-money gambling")
	p.Extensions = map[string]any{"until": until}
	return p
}

// LimitsNotConfigured: gambling is activated but the mandatory deposit limits
// were never set (pre-limits activation) — the user must configure them first.
func LimitsNotConfigured() *Problem {
	return New(fiber.StatusConflict, "https://ctech.app/problems/limits-not-configured",
		"Deposit limits not configured", "set daily/weekly/monthly game deposit limits first")
}

// DepositLimitExceeded: the deposit would overflow a responsible-gambling window.
func DepositLimitExceeded(window string, limit, used int64, resetsAt time.Time) *Problem {
	p := New(fiber.StatusConflict, "https://ctech.app/problems/deposit-limit-exceeded",
		"Deposit limit exceeded", fmt.Sprintf("%s game-deposit limit reached", window))
	p.Extensions = map[string]any{"window": window, "limit": limit, "used": used,
		"resets_at": resetsAt.UTC().Format(time.RFC3339)}
	return p
}

// ExclusionChangeRejected: revoke too early, or a shortening re-exclusion.
func ExclusionChangeRejected(detail string) *Problem {
	return New(fiber.StatusConflict, "https://ctech.app/problems/exclusion-change-rejected",
		"Exclusion change rejected", detail)
}
```

If `Problem` has no `Extensions` field, check `api-commons/problem`'s shape first and use whatever extension mechanism exists (e.g. `With(key, value)`); do not invent a second one.

- [ ] **Step 2: Build** — `go build ./...` PASS.
- [ ] **Step 3: Commit** — `git commit -am "feat(problem): responsible-gambling problem types"`

---

### Task 3: UserRepository — partial-update writers

**Files:**
- Modify: `api/internal/repositories/user.go`
- Test: `api/internal/repositories/user_responsible_test.go` (integration-style, same harness as existing repo tests — check `base_test.go` for the local-DynamoDB setup pattern and skip condition)

**Interfaces:**
- Consumes: Task 1 types.
- Produces (consumed by Tasks 4-5):
  - `func (r *UserRepository) SetSelfExclusion(ctx, userID string, ex *wallet.SelfExclusion) error` — SET `self_exclusion` (pass `nil` to REMOVE, used by revoke)
  - `func (r *UserRepository) SetGameLimits(ctx, userID string, lim *wallet.GameLimits) error` — SET `game_limits` (whole nested object; the object is small and owned by one writer path)
  - `func (r *UserRepository) BumpDepositCounters(ctx, userID string, prev *wallet.GameDepositCounters, next wallet.GameDepositCounters) (types.TransactWriteItem, error)` — returns a TransactWriteItem updating `game_deposit_counters` with `ConditionExpression` asserting the row still holds `prev` (or the attribute is absent when `prev == nil`). The optimistic condition makes two concurrent deposits serialize: the loser's transaction cancels and the service retries.

All writers use `UpdateItem`/`Update` expressions on the single attribute — never a whole-row Put (existing `User` invariant).

- [ ] **Step 1: Write failing test** (mirror the existing repo test harness; key assertions):

```go
func TestSelfExclusionAndLimitsRoundTrip(t *testing.T) {
	repo, ctx := newTestUserRepo(t) // same helper style as existing repo tests
	uid := "user-resp-1"
	ex := &wallet.SelfExclusion{Period: "30d", RequestedAt: "2026-07-19T12:00:00Z", Until: "2026-08-18T12:00:00Z"}
	if err := repo.SetSelfExclusion(ctx, uid, ex); err != nil {
		t.Fatal(err)
	}
	u, err := repo.Get(ctx, uid)
	if err != nil || u.SelfExclusion == nil || u.SelfExclusion.Period != "30d" {
		t.Fatalf("round trip failed: %+v err=%v", u, err)
	}
	if err := repo.SetSelfExclusion(ctx, uid, nil); err != nil { // revoke = REMOVE
		t.Fatal(err)
	}
	if u, _ = repo.Get(ctx, uid); u.SelfExclusion != nil {
		t.Fatal("revoke did not clear exclusion")
	}
	// Partial update must not clobber sibling consent fields.
	if err := repo.AcceptTerms(ctx, uid); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetGameLimits(ctx, uid, &wallet.GameLimits{Daily: 100, Weekly: 200, Monthly: 300}); err != nil {
		t.Fatal(err)
	}
	u, _ = repo.Get(ctx, uid)
	if !u.TermsAccepted() || u.GameLimits == nil {
		t.Fatal("SetGameLimits clobbered terms acceptance or dropped limits")
	}
}
```

Plus a `BumpDepositCounters` test: build the item for `prev=nil, next={DayKey:..., DaySum:50,...}`, run it via `TransactWriteItems`, re-run with the SAME stale `prev` and assert the transaction cancels with a conditional check failure.

- [ ] **Step 2: Run to verify failure** — `go test ./internal/repositories/ -run Responsible -v` (or whatever run filter matches), FAIL undefined methods.

- [ ] **Step 3: Implement.** Follow the exact `expression.Builder`/`UpdateItem` idiom already in `user.go` (`AcceptTerms` is the template). For `BumpDepositCounters`, condition when `prev != nil`:
`game_deposit_counters.day_key = :pdk AND game_deposit_counters.day_sum = :pds AND game_deposit_counters.week_key = :pwk AND game_deposit_counters.week_sum = :pws AND game_deposit_counters.month_key = :pmk AND game_deposit_counters.month_sum = :pms`; when `prev == nil`: `attribute_not_exists(game_deposit_counters)`. Update: `SET game_deposit_counters = :next, updated_at = :now`.

- [ ] **Step 4: Run to verify pass.**
- [ ] **Step 5: Commit** — `git commit -am "feat(repo): user writers for self-exclusion, limits and deposit counters"`

---

### Task 4: Service — self-exclusion + exclusion gates

**Files:**
- Modify: `api/internal/services/wallet.go` (gates in `ActivateGambling`, `FundGame`, `HoldGame`)
- Create: `api/internal/services/responsible.go` (new service methods; `wallet.go` is already >700 lines)
- Test: `api/internal/services/responsible_test.go` (same fake/mocking style as `wallet_test.go` — read it first and reuse its fixtures)

**Interfaces:**
- Consumes: Tasks 1-3.
- Produces (consumed by Task 6 handlers):
  - `func (s *WalletService) SelfExclude(ctx, userID, period, ip, userAgent string) (*wallet.SelfExclusion, error)`
  - `func (s *WalletService) RevokeSelfExclusion(ctx, userID, ip, userAgent string) error`
  - `func (s *WalletService) requireNotExcluded(ctx, userID string) (*wallet.User, error)` (internal)

- [ ] **Step 1: Write failing tests** — cases:
  1. `SelfExclude` with `"30d"` stores period+until, appends audit event `EventSelfExcluded`.
  2. Re-exclude extending (30d → 90d, or fixed → indefinite) succeeds; shortening (90d active → 30d whose new until is earlier, or indefinite → 30d) returns `ExclusionChangeRejected`.
  3. `RevokeSelfExclusion` on an active fixed period → `ExclusionChangeRejected` ("fixed periods expire on their own").
  4. `RevokeSelfExclusion` on indefinite before 90 days from `RequestedAt` → rejected; after 90 days → clears and audits `EventSelfExclusionRevoked`.
  5. `FundGame` for an excluded user → `problem.SelfExcluded`.
  6. `HoldGame` for an excluded user → `problem.SelfExcluded`.
  7. `ActivateGambling` for an excluded user → `problem.SelfExcluded`.
  8. `ReturnFromGame`, `ReleaseHold`, `CashoutGame`, `PurchaseSandbox` for an excluded user still succeed.

- [ ] **Step 2: Run to verify failure.**

- [ ] **Step 3: Implement.**

```go
// api/internal/services/responsible.go
package services

import (
	"context"
	"time"

	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
	"gopkg.aoctech.app/wallet/api/internal/problem"
)

// (check the real module path in go.mod before writing imports)

// requireNotExcluded loads the user row and fails with SelfExcluded while an
// exclusion is active. Callers that also need the row reuse the return value.
func (s *WalletService) requireNotExcluded(ctx context.Context, userID string) (*wallet.User, error) {
	u, err := s.users.Get(ctx, userID)
	if err != nil {
		return nil, err
	}
	if u.SelfExcluded(time.Now()) {
		until := ""
		if u.SelfExclusion != nil {
			until = u.SelfExclusion.Until
		}
		return nil, problem.SelfExcluded(until)
	}
	return u, nil
}

// SelfExclude records a self-exclusion. Extensions only: a new exclusion may
// never end earlier than the one it replaces.
func (s *WalletService) SelfExclude(ctx context.Context, userID, period, ip, userAgent string) (*wallet.SelfExclusion, error) {
	now := time.Now()
	until, err := wallet.ExclusionUntil(period, now)
	if err != nil {
		return nil, problem.BadRequest(err.Error())
	}
	u, err := s.users.Get(ctx, userID)
	if err != nil {
		return nil, err
	}
	if cur := u.SelfExclusion; cur != nil && u.SelfExcluded(now) {
		if cur.Until == "" { // already indefinite — nothing extends it
			return nil, problem.ExclusionChangeRejected("already indefinitely excluded")
		}
		if until != "" && until <= cur.Until { // RFC3339 compares lexically
			return nil, problem.ExclusionChangeRejected("an exclusion may only be extended")
		}
	}
	ex := &wallet.SelfExclusion{Period: period, RequestedAt: now.Format(time.RFC3339), Until: until}
	if err := s.users.SetSelfExclusion(ctx, userID, ex); err != nil {
		return nil, err
	}
	if err := s.audit.Append(ctx, &wallet.AuditEvent{
		UserID: userID, EventType: wallet.EventSelfExcluded, Actor: userID,
		After: period, IP: ip, UserAgent: userAgent,
	}); err != nil {
		return nil, err
	}
	return ex, nil
}

// RevokeSelfExclusion lifts an indefinite exclusion after its 90-day floor.
// Fixed periods are not revocable — they expire on their own.
func (s *WalletService) RevokeSelfExclusion(ctx context.Context, userID, ip, userAgent string) error {
	now := time.Now()
	u, err := s.users.Get(ctx, userID)
	if err != nil {
		return err
	}
	if u.SelfExclusion == nil || !u.SelfExcluded(now) {
		return problem.ExclusionChangeRejected("no active exclusion")
	}
	if u.SelfExclusion.Until != "" {
		return problem.ExclusionChangeRejected("fixed-period exclusions expire on their own")
	}
	requested, err := time.Parse(time.RFC3339, u.SelfExclusion.RequestedAt)
	if err != nil || now.Sub(requested) < 90*24*time.Hour {
		return problem.ExclusionChangeRejected("indefinite exclusion may be revoked only after 90 days")
	}
	if err := s.users.SetSelfExclusion(ctx, userID, nil); err != nil {
		return err
	}
	return s.audit.Append(ctx, &wallet.AuditEvent{
		UserID: userID, EventType: wallet.EventSelfExclusionRevoked, Actor: userID,
		IP: ip, UserAgent: userAgent,
	})
}
```

Audit constants (add in `domain/wallet/audit.go`):

```go
	EventSelfExcluded          = "self_excluded"
	EventSelfExclusionRevoked  = "self_exclusion_revoked"
	EventGameLimitsChanged     = "game_limits_changed"
```

Gates — first lines of the existing methods:
- `ActivateGambling`: after the KYC check, replace the plain `s.users.Get` with `u, err := s.requireNotExcluded(ctx, userID)` (keeping the `GamblingAccepted` check on `u`).
- `FundGame`: after `requireActivated`, add `if _, err := s.requireNotExcluded(ctx, userID); err != nil { return nil, nil, err }` (Task 5 will fold this load into the limit check — one read, not two).
- `HoldGame`: same one-liner after its `requireActivated`.

- [ ] **Step 4: Run to verify pass** — `go test ./internal/services/ -run 'Exclu|SelfExclude' -v` then full `go test ./...`.
- [ ] **Step 5: Commit** — `git commit -am "feat(services): self-exclusion with extension-only and 90-day revoke floor"`

---

### Task 5: Service — limit engine on `FundGame` + limits management

**Files:**
- Modify: `api/internal/services/responsible.go`, `api/internal/services/wallet.go` (`FundGame`, `ActivateGambling` signature), `api/internal/repositories/wallet.go` (transfer variant carrying extra transact items)
- Test: extend `api/internal/services/responsible_test.go`

**Interfaces:**
- Consumes: Tasks 1-4.
- Produces (consumed by Task 6):
  - `func (s *WalletService) SetGameLimits(ctx, userID string, daily, weekly, monthly int64, ip, userAgent string) (*wallet.GameLimits, error)` — first set immediate; decreases immediate; increases pended (+7d daily/weekly, +14d monthly)
  - `func (s *WalletService) CancelPendingLimits(ctx, userID string) (*wallet.GameLimits, error)`
  - `func (s *WalletService) GameLimitsStatus(ctx, userID string) (*LimitsStatus, error)` where

```go
type LimitsStatus struct {
	Limits    *wallet.GameLimits          `json:"limits"`     // nil when unconfigured
	Usage     wallet.GameDepositCounters  `json:"usage"`      // sums already normalized to current windows
	ResetsAt  map[string]string           `json:"resets_at"`  // window → RFC3339
	Excluded  *wallet.SelfExclusion       `json:"excluded,omitempty"`
}
```
  - `ActivateGambling` gains `daily, weekly, monthly int64` params (0,0,0 from an already-activated idempotent replay is tolerated only when limits are already configured).

- [ ] **Step 1: Write failing tests** — cases:
  1. `SetGameLimits` first configuration applies immediately (no pending).
  2. Decrease applies immediately; increase creates `Pending` with `AppliesAt` ≈ now+7d; monthly increase ⇒ now+14d; mixed (daily down, monthly up) applies daily now and pends monthly at +14d.
  3. Invalid ordering (weekly < daily) ⇒ `BadRequest`.
  4. `FundGame` with no limits configured ⇒ `LimitsNotConfigured`.
  5. `FundGame` overflowing the daily window ⇒ `DepositLimitExceeded` with `window="daily"`; counters unchanged; wallet balances unchanged.
  6. Successful `FundGame` increments all three sums; a second deposit in the same windows accumulates; a deposit after the day key rolls (inject clock or precompute keys for "yesterday") resets the day sum but keeps week/month.
  7. Matured pending is promoted on the next `FundGame` or `GameLimitsStatus` read (lazy apply persists via `SetGameLimits`-style write).
  8. `ActivateGambling` without valid limits for a fresh user ⇒ `BadRequest`; with limits ⇒ activates and stores them.

- [ ] **Step 2: Run to verify failure.**

- [ ] **Step 3: Implement.**

`FundGame` core (replacing today's body after the `MaxInboundAmount` check):

```go
	u, err := s.requireNotExcluded(ctx, userID)
	if err != nil {
		return nil, nil, err
	}
	now := time.Now()
	lim, matured := u.EffectiveGameLimits(now)
	if matured { // lazy-apply the pending increase before metering against it
		applied := lim
		if err := s.users.SetGameLimits(ctx, userID, &applied); err != nil {
			return nil, nil, err
		}
	}
	if !u.LimitsConfigured() && !matured {
		return nil, nil, problem.LimitsNotConfigured()
	}
	var prev *wallet.GameDepositCounters
	cur := wallet.GameDepositCounters{}
	if u.GameDepositCounters != nil {
		prev = u.GameDepositCounters
		cur = *prev
	}
	if breach := wallet.CheckDeposit(lim, cur, amount, now); breach != nil {
		return nil, nil, problem.DepositLimitExceeded(breach.Window, breach.Limit, breach.Used, breach.ResetsAt)
	}
	day, week, month := wallet.WindowKeys(now)
	d, w, m := cur.SumsFor(day, week, month)
	next := wallet.GameDepositCounters{
		DayKey: day, DaySum: d + amount,
		WeekKey: week, WeekSum: w + amount,
		MonthKey: month, MonthSum: m + amount,
	}
	counterItem, err := s.users.BumpDepositCounters(ctx, userID, prev, next)
	if err != nil {
		return nil, nil, err
	}
	rl, game, _, err := s.requireActivated(ctx, userID)
	if err != nil {
		return nil, nil, err
	}
	return s.ringTransferWithExtra(ctx, rl, game, amount,
		wallet.EntryGameFundDebit, wallet.EntryGameFundCredit, "game_fund", idemKey, counterItem)
```

`ringTransferWithExtra` mirrors `ringTransfer` but passes the extra `types.TransactWriteItem` down to a `Transfer` variant in the repo that appends it to the existing `TransactWriteItems` slice. On `TransactionCanceledException` where the cancellation reason is the counter item's conditional check, map to `problem.WalletBusy()` (client retries) — read the repo's existing cancellation-reason handling in `mutate`/`Transfer` first and reuse it. Do NOT auto-retry inside the service: an idempotency key is in play and the existing `WalletBusy` contract already covers optimistic conflicts.

`SetGameLimits` (in `responsible.go`):

```go
func (s *WalletService) SetGameLimits(ctx context.Context, userID string, daily, weekly, monthly int64, ip, userAgent string) (*wallet.GameLimits, error) {
	if err := wallet.ValidateLimits(daily, weekly, monthly); err != nil {
		return nil, problem.BadRequest(err.Error())
	}
	u, err := s.users.Get(ctx, userID)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	cur, matured := u.EffectiveGameLimits(now)
	if matured { // promote before diffing so cooldowns compare against reality
		if err := s.users.SetGameLimits(ctx, userID, &cur); err != nil {
			return nil, err
		}
	}
	next := wallet.GameLimits{Daily: daily, Weekly: weekly, Monthly: monthly}
	if u.LimitsConfigured() {
		// Decreases apply now; increases wait out the cooldown.
		applied := wallet.GameLimits{Daily: min64(daily, cur.Daily), Weekly: min64(weekly, cur.Weekly), Monthly: min64(monthly, cur.Monthly)}
		if daily > cur.Daily || weekly > cur.Weekly || monthly > cur.Monthly {
			cooldown := 7 * 24 * time.Hour
			if monthly > cur.Monthly {
				cooldown = 14 * 24 * time.Hour
			}
			applied.Pending = &wallet.PendingLimits{Daily: daily, Weekly: weekly, Monthly: monthly,
				AppliesAt: now.Add(cooldown).Format(time.RFC3339)}
		}
		next = applied
	}
	if err := s.users.SetGameLimits(ctx, userID, &next); err != nil {
		return nil, err
	}
	if err := s.audit.Append(ctx, &wallet.AuditEvent{UserID: userID,
		EventType: wallet.EventGameLimitsChanged, Actor: userID,
		After: fmt.Sprintf("d=%d w=%d m=%d", daily, weekly, monthly), IP: ip, UserAgent: userAgent}); err != nil {
		return nil, err
	}
	return &next, nil
}
```

Cooldown subtlety the tests must pin: when BOTH a weekly and a monthly field increase, the whole pending set takes the LONGER (14d) cooldown — one pending set, one `AppliesAt`.

`ActivateGambling`: add `daily, weekly, monthly int64` params. Fresh activation ⇒ `ValidateLimits` then `SetGameLimits` (first-set path, immediate) after wallets are created. Already-activated replay ⇒ accept and ignore zeros when `u.LimitsConfigured()`, otherwise require valid limits (this is the re-gate for pre-limits users).

- [ ] **Step 4: Run to verify pass** — full `go test ./...`.
- [ ] **Step 5: Commit** — `git commit -am "feat(services): game-deposit limit engine with cooldown-gated increases"`

---

### Task 6: HTTP routes + M2M status

**Files:**
- Modify: `api/internal/api/v1/router.go`, `api/internal/api/v1/wallet.go`, `api/internal/api/v1/dto.go`, `api/internal/api/v1/internal.go`, `api/internal/middleware/scope.go`
- Test: `api/internal/api/v1/responsible_test.go` (same style as `router_test.go`/`internal_test.go`)

**Interfaces:**
- Consumes: Tasks 4-5 service methods.
- Produces (consumed by wallet UI Task 7 and ctech-poker Phase 5):
  - `POST /v1.0/wallet/gambling/self-exclude` `{"period":"30d"|"90d"|"indefinite"}` → 201 `SelfExclusion`
  - `POST /v1.0/wallet/gambling/self-exclude/revoke` → 204
  - `GET /v1.0/wallet/gambling/limits` → 200 `LimitsStatus`
  - `PUT /v1.0/wallet/gambling/limits` `{"daily_limit","weekly_limit","monthly_limit"}` → 200 `GameLimits`
  - `DELETE /v1.0/wallet/gambling/limits/pending` → 200 `GameLimits`
  - `ActivateGamblingRequest` gains the three limit fields (BREAKING for the UI — Task 7 updates the activate call)
  - `GET /v1.0/internal/wallet/game/status/:user_id` (scope `internal:wallet:game-status`) → `{"activated":bool,"self_excluded":bool,"limits_configured":bool}`

Routing notes:
- Self-exclude and `GET/PUT limits` register UNCONDITIONALLY (not behind `cfg.GamblingEnabled`): self-excluding and lowering limits reduce exposure — same principle as `game/withdraw`'s comment. Revoke and `DELETE pending` also register unconditionally (they only restore an already-consented state; the deposit door itself stays flag-gated).
- The status route registers unconditionally too — poker must be able to see "not eligible" even while the flag is off.
- Scope constant: `ScopeWalletGameStatus = "internal:wallet:game-status"` in `scope.go`.

- [ ] **Step 1: Write failing route tests** — self-exclude 201 + revoke-too-early 409; PUT limits 200 with pending in response; M2M status 200 shape and 403 without scope (mirror `internal_test.go`'s scope-rejection test).
- [ ] **Step 2: Run to verify failure.**
- [ ] **Step 3: Implement handlers** (thin: bind → service → JSON, use `sendProblem` like every sibling) and register routes per the notes above.
- [ ] **Step 4: Run to verify pass** — full `go test ./...`.
- [ ] **Step 5: Commit** — `git commit -am "feat(api): responsible-gambling routes and M2M game status"`

---

### Task 7: Wallet UI — "Jogo responsável" page

**Files:**
- Create: `ui/src/app/gambling/responsible/page.tsx` (+ colocated components if the page grows)
- Modify: `ui/src/lib/api/` (client for the new routes — follow the existing api-client module pattern), the activation flow under `ui/src/app/gambling/` (send the three limits), and the gambling dashboard (link + excluded-state banner)
- Test: whatever component-test harness exists; otherwise `npm run build` + lint is the gate (match the repo's current practice)

**Interfaces:**
- Consumes: Task 6 routes.

Content (pt-BR copy):
- Limits form: three currency inputs (centavos↔R$ mask, reuse the existing currency input if one exists), current usage bars "R$ 120 / R$ 500 esta semana" from `GET limits` (`usage` + `resets_at`), inline warning when a field increases: "Aumentos entram em vigor em 7 dias (mensal: 14 dias)". Pending banner with `applies_at` + "Cancelar aumento".
- Self-exclusion: period radio (30 dias / 90 dias / por tempo indeterminado), confirmation dialog that requires typing "EXCLUIR" and spells out irreversibility; while excluded, the gambling dashboard replaces deposit/play surfaces with the exclusion banner (até `until` or "por tempo indeterminado") — `game/withdraw` remains visible.
- Activation flow: add the three limit fields as a required step.

- [ ] **Step 1: Build the API client functions + page skeleton; `npm run build` PASS.**
- [ ] **Step 2: Wire forms/banners; manual pass with the API running locally (or the repo's mock layer, see `ui/src/lib/mock.ts`).**
- [ ] **Step 3: Commit** — `git commit -am "feat(ui): responsible-gambling page — limits, usage and self-exclusion"`

---

### Task 8: Cross-repo follow-ups (recorded, not done here)

- [ ] `ctech-account`: seed poker's M2M client with `internal:wallet:game-status` (same process as the game-hold scopes; plan task lives with the account/grant seeding).
- [ ] `ctech-poker` Phase 5 Task 1: point `IsGamblingActivated` at `GET /v1.0/internal/wallet/game/status/:user_id`, eligibility = `activated && !self_excluded && limits_configured` (already reflected in the poker plan's Global Constraints).
- [ ] After deploy + verification: `GAMBLING_ENABLED` may flip per the router comment — business decision, record it.

---

## Self-Review Notes

- Spec coverage: exclusion (Task 4), limits + cooldown + windows + counters (Tasks 1, 3, 5), activation gate (Tasks 5-6), M2M status (Task 6), UI (Task 7), scope grant (Task 8). Non-goals untouched.
- The `FundGame` sketch reads the user row once and reuses it for exclusion, limits and counters — no double read.
- Counter increment is transactional with the transfer and optimistically conditioned; concurrent deposits at the edge serialize via `WalletBusy`.
- `ExclusionUntil`/lexical RFC3339 comparison is valid because both strings come from `Format(time.RFC3339)` in UTC-less local offsets — if `Format` emits offsets, compare parsed `time.Time` instead (implementer: prefer parsing, it is strictly safer; the test in Task 4 pins the behavior).
