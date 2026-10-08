# Wallet Inter Rail and Daily Limits Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Bring back "CTech Wallet" and the Inter PIX deposit and withdrawal rail on the `real` wallet, with per-wallet daily limits (default R$1.000 deposited per day, 1 withdrawal per day of at most R$1.000).

**Architecture:** The removed pre-BaaS code is mostly recoverable from git. `ConfirmDeposit`, `reverse`, `ReconcileWithdrawals`, the deposit and withdrawal repositories and the problem types still exist. This plan re-adds `InitiateDeposit`, `Withdraw`, their handlers and routes, and the UI dialogs, plus a daily-counter engine that reuses the optimistic user-row counter pattern already used by the responsible-gambling limits (`BumpDepositCounters`, `WindowKeys`).

**Tech Stack:** Go, Fiber v3, DynamoDB (aws-sdk-go-v2), Valkey locks, Next.js 16 / TypeScript / ShadCN.

**Spec:** `docs/specs/2026-10-08-wallet-restoration-design.md` (section 1). Also read `docs/specs/2026-10-07-asaas-removal.md` and root `CLAUDE.md`.

## Global Constraints

- All amounts are integer centavos. Never floats.
- Every mutation is idempotent (`Idempotency-Key` header on user routes) and a conditional `TransactWriteItems`; debits carry `balance >= :amount` (already inside `repo.Debit`).
- Every error is an RFC 7807 `*problem.Problem` returned through `sendProblem`. Never `fiber.Map` or `fiber.NewError` for errors.
- No magic strings or numbers: every limit default, key prefix and breach kind is a named constant.
- `real`-wallet deposit and withdrawal operations hold the `real` wallet lock (`acquireWallet`). All counter writes happen under that lock.
- The daily deposit cap counts GROSS inflow at credit time. A later refund of a credited deposit never frees headroom (same spirit as invariant #8).
- Limits are admin-only fields edited directly in DynamoDB. No API write path.
- `TestSandboxPurchaseNeverDebitsRealWallet` must stay green.
- UI: `npx eslint src --ext .ts,.tsx` must pass with zero errors and zero warnings before commit.
- Commits: Conventional Commits, no emojis, NO attribution trailers (no `Co-Authored-By`, no Claude mention).
- Work on a branch (`feat/wallet-inter-rail`), never directly on `main`.

## Review Focus

- Two concurrent deposit confirmations of the same user race on the daily cap: only one may credit; the other must be refunded, never double-credited.
- A paid deposit that pushes the user over the daily cap is refunded to the payer (reuse `rejectMismatch`), never left pending and never credited.
- A withdrawal whose PIX payout fails and is reversed must give the daily withdrawal slot back, otherwise a failed payout burns the day.
- Day rollover at BRT midnight: counters from yesterday count as zero; a deposit at 23:59:59 and one at 00:00:01 land in different days.
- Replay of a `POST /wallet/withdrawals` with the same `Idempotency-Key` returns the same withdrawal and never sends a second PIX payout or consumes a second daily slot.
- `Withdraw` with amount below the minimum is rejected unless it empties the whole balance.

---

### Task 1: Rebrand to "CTech Wallet"

**Files:**
- Modify (via revert): `api/internal/oauthresource/scope-manifest.json`, `ui/src/app/dashboard/page.tsx`, `ui/src/app/en/page.tsx`, `ui/src/app/layout.tsx`, `ui/src/app/pt-BR/page.tsx`, `ui/src/components/home.tsx`, `ui/src/lib/localized-metadata.ts`, `ui/src/locales/en.json`, `ui/src/locales/pt-BR.json`
- Delete: `docs/specs/2026-09-08-display-name-rename-ledger.md` is kept as history; instead add a superseded note at its top.

**Interfaces:**
- Produces: all customer-facing strings say "CTech Wallet".

- [ ] **Step 1: Create the branch**

```bash
cd /home/artur-revgas/Documents/Projects/Ctech/ctech-wallet
git checkout main && git pull origin main
git checkout -b feat/wallet-inter-rail
```

- [ ] **Step 2: Revert the display-name rename without committing**

```bash
git revert --no-commit 75d518c
```

Expected: possible conflicts in `api/internal/oauthresource/scope-manifest.json` (the `custody:write` scope was removed after the rename) and in `ui/src/app/dashboard/page.tsx`. Resolve by keeping the CURRENT file and only replacing the text "CTech Ledger" with "CTech Wallet" in the conflicting hunks. Do not reintroduce `wallet:custody:write`.

- [ ] **Step 3: Verify no "Ledger" brand strings remain**

```bash
grep -rIn "CTech Ledger" --exclude-dir=node_modules --exclude-dir=.git --exclude-dir=.next . | grep -v "docs/specs/2026-09-08\|docs/specs/2026-10-08\|docs/plans/"
```

Expected: no output. Fix any remaining hit by hand.

- [ ] **Step 4: Mark the old rename spec superseded**

Add as the first line under the title of `docs/specs/2026-09-08-display-name-rename-ledger.md`:

```markdown
> **Superseded 2026-10-08:** the Asaas BaaS integration was refused; the customer-facing name is "CTech Wallet" again. See `2026-10-08-wallet-restoration-design.md`.
```

- [ ] **Step 5: Lint and commit**

```bash
cd ui && npx eslint src --ext .ts,.tsx && cd ..
git add -A
git commit -m "refactor: restore the CTech Wallet display name"
```

---

### Task 2: Daily limit domain model

**Files:**
- Create: `api/internal/domain/wallet/daily_limits.go`
- Create: `api/internal/domain/wallet/daily_limits_test.go`
- Modify: `api/internal/domain/wallet/model.go` (Wallet struct, near `MinWithdrawal`)
- Modify: `api/internal/domain/wallet/user.go:48` (add counters field after `GameDepositCounters`)

**Interfaces:**
- Produces:
  - `type DailyLimits struct{ DepositCap, WithdrawCap, WithdrawCount int64 }`
  - `func EffectiveDailyLimits(w *Wallet) DailyLimits`
  - `type RealDailyCounters struct{ DayKey string; DepositSum, WithdrawSum, WithdrawCount int64 }`
  - `func (c RealDailyCounters) ForDay(day string) RealDailyCounters`
  - `type DailyBreach struct{ Kind string; Limit, Used int64; ResetsAt time.Time }`
  - `func CheckDailyDeposit(lim DailyLimits, c RealDailyCounters, amount int64, now time.Time) *DailyBreach`
  - `func CheckDailyWithdraw(lim DailyLimits, c RealDailyCounters, amount int64, now time.Time) *DailyBreach`
  - constants `DailyBreachDepositCap`, `DailyBreachWithdrawCap`, `DailyBreachWithdrawCount`

- [ ] **Step 1: Write the failing test**

`api/internal/domain/wallet/daily_limits_test.go`:

```go
package wallet

import (
	"testing"
	"time"
)

var dailyNow = time.Date(2026, 10, 8, 15, 0, 0, 0, saoPaulo)

func TestEffectiveDailyLimitsDefaults(t *testing.T) {
	got := EffectiveDailyLimits(&Wallet{})
	want := DailyLimits{DepositCap: 100000, WithdrawCap: 100000, WithdrawCount: 1}
	if got != want {
		t.Fatalf("got %+v want %+v", got, want)
	}
	if EffectiveDailyLimits(nil) != want {
		t.Fatal("nil wallet must yield defaults")
	}
}

func TestEffectiveDailyLimitsOverride(t *testing.T) {
	got := EffectiveDailyLimits(&Wallet{DailyDepositCap: 50000, DailyWithdrawCap: 20000, DailyWithdrawCount: 3})
	want := DailyLimits{DepositCap: 50000, WithdrawCap: 20000, WithdrawCount: 3}
	if got != want {
		t.Fatalf("got %+v want %+v", got, want)
	}
}

func TestCheckDailyDeposit(t *testing.T) {
	lim := DailyLimits{DepositCap: 100000}
	day, _, _ := WindowKeys(dailyNow)
	cases := []struct {
		name   string
		c      RealDailyCounters
		amount int64
		breach bool
	}{
		{"fresh exactly cap", RealDailyCounters{}, 100000, false},
		{"fresh over cap", RealDailyCounters{}, 100001, true},
		{"accumulated within", RealDailyCounters{DayKey: day, DepositSum: 60000}, 40000, false},
		{"accumulated over", RealDailyCounters{DayKey: day, DepositSum: 60000}, 40001, true},
		{"yesterday does not count", RealDailyCounters{DayKey: "2026-10-07", DepositSum: 100000}, 100000, false},
		{"counter drifted above cap", RealDailyCounters{DayKey: day, DepositSum: 200000}, 1, true},
	}
	for _, tc := range cases {
		got := CheckDailyDeposit(lim, tc.c, tc.amount, dailyNow)
		if (got != nil) != tc.breach {
			t.Errorf("%s: breach=%v want %v", tc.name, got != nil, tc.breach)
		}
		if got != nil && got.Kind != DailyBreachDepositCap {
			t.Errorf("%s: kind %q", tc.name, got.Kind)
		}
	}
}

func TestCheckDailyWithdraw(t *testing.T) {
	lim := DailyLimits{WithdrawCap: 100000, WithdrawCount: 1}
	day, _, _ := WindowKeys(dailyNow)

	if b := CheckDailyWithdraw(lim, RealDailyCounters{}, 100000, dailyNow); b != nil {
		t.Fatalf("first full-cap withdrawal must pass, got %+v", b)
	}
	if b := CheckDailyWithdraw(lim, RealDailyCounters{}, 100001, dailyNow); b == nil || b.Kind != DailyBreachWithdrawCap {
		t.Fatalf("over cap: %+v", b)
	}
	if b := CheckDailyWithdraw(lim, RealDailyCounters{DayKey: day, WithdrawCount: 1, WithdrawSum: 100}, 100, dailyNow); b == nil || b.Kind != DailyBreachWithdrawCount {
		t.Fatalf("second withdrawal must breach count: %+v", b)
	}
	if b := CheckDailyWithdraw(lim, RealDailyCounters{DayKey: "2026-10-07", WithdrawCount: 1}, 100, dailyNow); b != nil {
		t.Fatalf("yesterday must not count: %+v", b)
	}
}

func TestDayRolloverBoundary(t *testing.T) {
	lim := DailyLimits{DepositCap: 100000}
	late := time.Date(2026, 10, 8, 23, 59, 59, 0, saoPaulo)
	early := time.Date(2026, 10, 9, 0, 0, 1, 0, saoPaulo)
	lateDay, _, _ := WindowKeys(late)
	c := RealDailyCounters{DayKey: lateDay, DepositSum: 100000}
	if CheckDailyDeposit(lim, c, 1, late) == nil {
		t.Fatal("same day must breach")
	}
	if CheckDailyDeposit(lim, c, 1, early) != nil {
		t.Fatal("next BRT day must not breach")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd api && go test ./internal/domain/wallet/ -run 'DailyLimits|CheckDaily|DayRollover' -v`
Expected: FAIL (`undefined: EffectiveDailyLimits` and the Wallet fields).

- [ ] **Step 3: Add the Wallet fields**

In `api/internal/domain/wallet/model.go`, inside `type Wallet struct`, after the `MinWithdrawal` field:

```go
	// Daily PIX limits (admin-only, same convention as MinDeposit/MaxDeposit).
	// Zero means "use the default" (EffectiveDailyLimits).
	DailyDepositCap    int64 `dynamodbav:"daily_deposit_cap,omitempty" json:"daily_deposit_cap,omitempty"`
	DailyWithdrawCap   int64 `dynamodbav:"daily_withdraw_cap,omitempty" json:"daily_withdraw_cap,omitempty"`
	DailyWithdrawCount int64 `dynamodbav:"daily_withdraw_count,omitempty" json:"daily_withdraw_count,omitempty"`
```

In `api/internal/domain/wallet/user.go`, after the `GameDepositCounters` field:

```go
	RealDailyCounters *RealDailyCounters `dynamodbav:"real_daily_counters,omitempty" json:"-"`
```

- [ ] **Step 4: Write the implementation**

`api/internal/domain/wallet/daily_limits.go`:

```go
package wallet

import "time"

// Default daily PIX limits for the real wallet, in centavos. A wallet may
// override any of them via admin-only DynamoDB fields; zero means "default".
const (
	DefaultDailyDepositCap    int64 = 100_000 // R$ 1.000,00 deposited per day
	DefaultDailyWithdrawCap   int64 = 100_000 // R$ 1.000,00 withdrawn per day
	DefaultDailyWithdrawCount int64 = 1       // withdrawals per day
)

// Breach kinds reported by CheckDaily*.
const (
	DailyBreachDepositCap    = "deposit_cap"
	DailyBreachWithdrawCap   = "withdraw_cap"
	DailyBreachWithdrawCount = "withdraw_count"
)

// DailyLimits is the effective per-wallet daily PIX limit set.
type DailyLimits struct {
	DepositCap    int64
	WithdrawCap   int64
	WithdrawCount int64
}

// EffectiveDailyLimits applies the per-wallet overrides over the defaults.
// Pass w == nil for the defaults.
func EffectiveDailyLimits(w *Wallet) DailyLimits {
	lim := DailyLimits{
		DepositCap:    DefaultDailyDepositCap,
		WithdrawCap:   DefaultDailyWithdrawCap,
		WithdrawCount: DefaultDailyWithdrawCount,
	}
	if w == nil {
		return lim
	}
	if w.DailyDepositCap > 0 {
		lim.DepositCap = w.DailyDepositCap
	}
	if w.DailyWithdrawCap > 0 {
		lim.WithdrawCap = w.DailyWithdrawCap
	}
	if w.DailyWithdrawCount > 0 {
		lim.WithdrawCount = w.DailyWithdrawCount
	}
	return lim
}

// RealDailyCounters accumulate the real wallet's PIX activity for one São Paulo
// calendar day. A DayKey mismatch means the day rolled and every sum is
// logically zero. Stored on the user row (like GameDepositCounters) and only
// ever written under the real wallet lock.
type RealDailyCounters struct {
	DayKey        string `dynamodbav:"day_key,omitempty" json:"day_key,omitempty"`
	DepositSum    int64  `dynamodbav:"deposit_sum,omitempty" json:"deposit_sum,omitempty"`
	WithdrawSum   int64  `dynamodbav:"withdraw_sum,omitempty" json:"withdraw_sum,omitempty"`
	WithdrawCount int64  `dynamodbav:"withdraw_count,omitempty" json:"withdraw_count,omitempty"`
}

// ForDay returns the counters as they stand for day: unchanged when the key
// matches, zeroed (with the new key) when the day rolled.
func (c RealDailyCounters) ForDay(day string) RealDailyCounters {
	if c.DayKey != day {
		return RealDailyCounters{DayKey: day}
	}
	return c
}

// DailyBreach describes which daily limit an operation would overflow.
type DailyBreach struct {
	Kind     string
	Limit    int64
	Used     int64
	ResetsAt time.Time
}

// CheckDailyDeposit reports whether crediting amount would overflow the daily
// deposit cap. Uses `amount > cap-used` rather than `used+amount > cap` so huge
// sums cannot wrap past math.MaxInt64 into a false negative (SEC-04).
func CheckDailyDeposit(lim DailyLimits, c RealDailyCounters, amount int64, now time.Time) *DailyBreach {
	day, _, _ := WindowKeys(now)
	used := c.ForDay(day).DepositSum
	if amount > lim.DepositCap-used {
		reset, _, _ := WindowResets(now)
		return &DailyBreach{Kind: DailyBreachDepositCap, Limit: lim.DepositCap, Used: used, ResetsAt: reset}
	}
	return nil
}

// CheckDailyWithdraw reports whether a withdrawal of amount would exceed the
// daily withdrawal count or amount cap. The count is checked first.
func CheckDailyWithdraw(lim DailyLimits, c RealDailyCounters, amount int64, now time.Time) *DailyBreach {
	day, _, _ := WindowKeys(now)
	cur := c.ForDay(day)
	reset, _, _ := WindowResets(now)
	if cur.WithdrawCount >= lim.WithdrawCount {
		return &DailyBreach{Kind: DailyBreachWithdrawCount, Limit: lim.WithdrawCount, Used: cur.WithdrawCount, ResetsAt: reset}
	}
	if amount > lim.WithdrawCap-cur.WithdrawSum {
		return &DailyBreach{Kind: DailyBreachWithdrawCap, Limit: lim.WithdrawCap, Used: cur.WithdrawSum, ResetsAt: reset}
	}
	return nil
}
```

- [ ] **Step 5: Run to verify it passes**

Run: `cd api && go test ./internal/domain/wallet/ -v`
Expected: PASS (all, including pre-existing tests).

- [ ] **Step 6: Commit**

```bash
git add api/internal/domain/wallet
git commit -m "feat(api): add per-wallet daily PIX limit model"
```

---

### Task 3: Problem types and counter persistence

**Files:**
- Modify: `api/internal/problem/problem.go` (constants near `TypeDepositLimitExceeded:50`, constructors after `DepositLimitExceeded:245`)
- Modify: `api/internal/repositories/user.go:70-93` (generalize `BumpDepositCounters`, add `BumpRealDailyCounters`)
- Modify: `api/internal/services/wallet.go` (`UserRepo` interface, 3 lines)
- Modify: `api/internal/services/user_test.go:131` (stub gets the new method)
- Test: `api/internal/repositories/user_responsible_test.go`, `api/internal/problem/problem_test.go`

**Interfaces:**
- Consumes: `wallet.RealDailyCounters`, `wallet.DailyBreach` (Task 2).
- Produces:
  - `problem.DailyDepositLimit(limit, used int64, resetsAt time.Time) *Problem` (409)
  - `problem.DailyWithdrawLimit(kind string, limit, used int64, resetsAt time.Time) *Problem` (409)
  - `(*UserRepository).BumpRealDailyCounters(userID string, prev *wallet.RealDailyCounters, next wallet.RealDailyCounters) (types.TransactWriteItem, error)`
  - `UserRepo.BumpRealDailyCounters` with the same signature.

- [ ] **Step 1: Write the failing tests**

Append to `api/internal/repositories/user_responsible_test.go` (copy the helper style of the existing `TestBumpDepositCounters...` tests in that file; they build `r` the same way):

```go
func TestBumpRealDailyCountersFreshRowConditionsOnAbsence(t *testing.T) {
	r := newUserRepoForTest(t)
	item, err := r.BumpRealDailyCounters("u1", nil, wallet.RealDailyCounters{DayKey: "2026-10-08", DepositSum: 100})
	if err != nil {
		t.Fatal(err)
	}
	if got := *item.Update.ConditionExpression; got != "attribute_not_exists(#c)" {
		t.Fatalf("condition = %q", got)
	}
	if item.Update.ExpressionAttributeNames["#c"] != "real_daily_counters" {
		t.Fatalf("attr = %v", item.Update.ExpressionAttributeNames)
	}
}

func TestBumpRealDailyCountersConditionsOnPreviousValue(t *testing.T) {
	r := newUserRepoForTest(t)
	prev := &wallet.RealDailyCounters{DayKey: "2026-10-08", DepositSum: 100}
	item, err := r.BumpRealDailyCounters("u1", prev, wallet.RealDailyCounters{DayKey: "2026-10-08", DepositSum: 150})
	if err != nil {
		t.Fatal(err)
	}
	if got := *item.Update.ConditionExpression; got != "#c = :prev" {
		t.Fatalf("condition = %q", got)
	}
}
```

If the existing tests construct the repo inline rather than through a helper, replace `newUserRepoForTest(t)` with that exact inline construction (read lines 1-40 of the file first).

Append to `api/internal/problem/problem_test.go`:

```go
func TestDailyLimitProblems(t *testing.T) {
	reset := time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)
	d := DailyDepositLimit(100000, 60000, reset)
	if d.Status != http.StatusConflict || d.Type != TypeDailyDepositLimit || d.MaxAmount != 100000 {
		t.Fatalf("deposit problem: %+v", d)
	}
	w := DailyWithdrawLimit("withdraw_count", 1, 1, reset)
	if w.Status != http.StatusConflict || w.Type != TypeDailyWithdrawLimit {
		t.Fatalf("withdraw problem: %+v", w)
	}
}
```

(Add `net/http` and `time` imports if the test file lacks them.)

- [ ] **Step 2: Run to verify they fail**

Run: `cd api && go test ./internal/repositories/ ./internal/problem/ -run 'RealDaily|DailyLimitProblems' -v`
Expected: FAIL (undefined symbols).

- [ ] **Step 3: Add the problem types**

In `api/internal/problem/problem.go`, next to `TypeDepositLimitExceeded`:

```go
	TypeDailyDepositLimit  = "/problems/daily-deposit-limit"
	TypeDailyWithdrawLimit = "/problems/daily-withdraw-limit"
```

After `DepositLimitExceeded`:

```go
// DailyDepositLimit: the deposit would overflow the wallet's daily PIX deposit
// cap. MaxAmount carries the cap; Detail says when the BRT day resets.
func DailyDepositLimit(limit, used int64, resetsAt time.Time) *Problem {
	p := New(http.StatusConflict, TypeDailyDepositLimit, "Daily Deposit Limit",
		fmt.Sprintf("limite diário de depósito atingido; renova em %s", resetsAt.Format(time.RFC3339)))
	p.MaxAmount = limit
	return p
}

// DailyWithdrawLimit: the withdrawal would exceed the wallet's daily count or
// amount cap. kind is wallet.DailyBreachWithdrawCount or DailyBreachWithdrawCap.
func DailyWithdrawLimit(kind string, limit, used int64, resetsAt time.Time) *Problem {
	what := "valor"
	if kind == "withdraw_count" {
		what = "quantidade de saques"
	}
	p := New(http.StatusConflict, TypeDailyWithdrawLimit, "Daily Withdraw Limit",
		fmt.Sprintf("limite diário de %s atingido; renova em %s", what, resetsAt.Format(time.RFC3339)))
	p.MaxAmount = limit
	return p
}
```

Do not import the `wallet` package into `problem` (it would create a cycle); the string literal `"withdraw_count"` must match `wallet.DailyBreachWithdrawCount`. Add a one-line comment saying so.

- [ ] **Step 4: Generalize the counter bump (DRY)**

Replace the body of `BumpDepositCounters` in `api/internal/repositories/user.go` so both counters share one writer:

```go
const (
	attrGameDepositCounters = "game_deposit_counters"
	attrRealDailyCounters   = "real_daily_counters"
)

// BumpDepositCounters returns a TransactWriteItem replacing the user's
// game_deposit_counters with next, conditioned on the row still holding prev
// (attribute absent when prev == nil). See bumpCounters.
func (r *UserRepository) BumpDepositCounters(userID string, prev *wallet.GameDepositCounters, next wallet.GameDepositCounters) (types.TransactWriteItem, error) {
	return r.bumpCounters(attrGameDepositCounters, userID, prev != nil, prev, next)
}

// BumpRealDailyCounters is BumpDepositCounters for the real wallet's daily PIX
// counters. Always called under the real wallet lock.
func (r *UserRepository) BumpRealDailyCounters(userID string, prev *wallet.RealDailyCounters, next wallet.RealDailyCounters) (types.TransactWriteItem, error) {
	return r.bumpCounters(attrRealDailyCounters, userID, prev != nil, prev, next)
}

// bumpCounters writes next into attr, conditioned on attr still holding prev
// (or being absent when hasPrev is false). Ran inside the same transaction as
// the money movement, the optimistic condition makes the loser of a race cancel.
func (r *UserRepository) bumpCounters(attr, userID string, hasPrev bool, prev, next any) (types.TransactWriteItem, error) {
	nextAV, err := attributevalue.Marshal(next)
	if err != nil {
		return types.TransactWriteItem{}, err
	}
	values := map[string]types.AttributeValue{":next": nextAV, ":now": &types.AttributeValueMemberS{Value: NowStr()}}
	cond := "attribute_not_exists(#c)"
	if hasPrev {
		prevAV, err := attributevalue.Marshal(prev)
		if err != nil {
			return types.TransactWriteItem{}, err
		}
		cond = "#c = :prev"
		values[":prev"] = prevAV
	}
	return r.users.BuildRawUpdateTxItem(userID, nil,
		"SET #c = :next, #u = :now", cond,
		map[string]string{"#c": attr, "#u": "updated_at"}, values), nil
}
```

Careful: `prev != nil` on a typed nil pointer passed as `any` is non-nil, which is why `hasPrev` is passed explicitly.

- [ ] **Step 5: Extend the interface and the stub**

In `UserRepo` (`api/internal/services/wallet.go`) add:

```go
	BumpRealDailyCounters(userID string, prev *wallet.RealDailyCounters, next wallet.RealDailyCounters) (types.TransactWriteItem, error)
```

In `api/internal/services/user_test.go` after the stub's `BumpDepositCounters` (line 131):

```go
func (r *stubUserRepo) BumpRealDailyCounters(_ string, _ *wallet.RealDailyCounters, _ wallet.RealDailyCounters) (types.TransactWriteItem, error) {
	return types.TransactWriteItem{}, nil
}
```

Run `go vet ./...` and add the same method to any other type that fails to satisfy `UserRepo`.

- [ ] **Step 6: Run to verify**

Run: `cd api && go build ./... && go test ./internal/repositories/ ./internal/problem/ ./internal/services/ -v 2>&1 | tail -30`
Expected: PASS, including the pre-existing `TestBumpDepositCounters*` tests (proves the refactor kept behavior).

- [ ] **Step 7: Commit**

```bash
git add api
git commit -m "feat(api): add daily-limit problems and real daily counter persistence"
```

---

### Task 4: Withdrawal transaction builder

**Files:**
- Modify: `api/internal/repositories/wallet.go` (after `PutWithdrawal`, ~line 585)
- Modify: `api/internal/services/wallet.go` (`WithdrawalStore` interface)
- Test: `api/internal/repositories/wallet_test.go` (create if absent)

**Interfaces:**
- Produces: `(*WalletRepository).WithdrawalPutTx(w *wallet.Withdrawal) (types.TransactWriteItem, error)` and the same method on `WithdrawalStore`. It builds a put-if-absent item so the withdrawal row commits in the SAME transaction as the debit (SEC-01: no committed debit without a processing row).

- [ ] **Step 1: Write the failing test**

```go
func TestWithdrawalPutTxIsConditionalPut(t *testing.T) {
	r := newWalletRepoForTest(t) // same construction the neighbouring tests in this package use
	item, err := r.WithdrawalPutTx(&wallet.Withdrawal{WithdrawalID: "withdraw#u1#k1", WalletID: "w1", UserID: "u1", Amount: 100})
	if err != nil {
		t.Fatal(err)
	}
	if item.Put == nil || item.Put.ConditionExpression == nil {
		t.Fatalf("want a conditional put, got %+v", item)
	}
}
```

If the package has no repo test helper, build the repository exactly as `repositories/base_test.go` does and name the helper accordingly.

- [ ] **Step 2: Run to verify it fails**

Run: `cd api && go test ./internal/repositories/ -run WithdrawalPutTx -v`
Expected: FAIL (undefined method).

- [ ] **Step 3: Implement**

```go
// WithdrawalPutTx builds the put-if-absent item for a processing withdrawal so
// it commits in the same TransactWriteItems as the debit that funds it
// (SEC-01 / Invariant 14): never a debit without its tracking row.
func (r *WalletRepository) WithdrawalPutTx(w *wallet.Withdrawal) (types.TransactWriteItem, error) {
	av, err := Encode(w)
	if err != nil {
		return types.TransactWriteItem{}, err
	}
	return r.withdrawal.BuildPutTxItemIfAbsent(av), nil
}
```

Add `WithdrawalPutTx(w *wallet.Withdrawal) (types.TransactWriteItem, error)` to `WithdrawalStore` in `services/wallet.go`. Add the method to every fake implementing `Repo`/`WithdrawalStore` that `go vet ./...` flags.

- [ ] **Step 4: Run and commit**

Run: `cd api && go build ./... && go vet ./... && go test ./internal/repositories/ -v 2>&1 | tail -20` → PASS.

```bash
git add api && git commit -m "feat(api): expose a transactional withdrawal put for atomic debit"
```

---

### Task 5: `InitiateDeposit` with the daily-cap pre-check

**Files:**
- Modify: `api/internal/services/wallet.go` (new constant near `depositTTLMinutes`, new method before `ConfirmDeposit`, `Repo`-side nothing new except `PutDeposit`/`ReserveDepositIdem`, see Step 3)
- Modify: `api/internal/repositories/wallet.go` (`PutDeposit` already exists; add idempotent reservation)
- Modify: `api/internal/services/wallet.go` `DepositStore` interface
- Test: `api/internal/services/wallet_test.go`

**Interfaces:**
- Consumes: `wallet.EffectiveDailyLimits`, `wallet.CheckDailyDeposit`, `problem.DailyDepositLimit`, `s.users.Get`.
- Produces: `(*WalletService).InitiateDeposit(ctx, userID, kycLevel string, amount int64, idemKey string) (*wallet.PixDeposit, *pix.Charge, error)`.

Design notes (read before coding):
- The pre-check is ADVISORY (reads the counter without writing it). The binding enforcement is at credit time (Task 6) because two charges opened in parallel can each pass the pre-check. A rejected pre-check never opens a charge at Inter.
- Idempotency: the pre-BaaS code reserved the key with a guard row BEFORE opening the charge (`ReserveDepositIdem`, then `CreateCharge`, then `PutDeposit`). That repository method was deleted with Asaas. Reuse the sandbox-purchase technique already in this codebase instead of resurrecting a second guard table: a deterministic txid derived from `(userID, idemKey)`, written with put-if-absent BEFORE `CreateCharge` (mirrors `PurchaseSandboxDirect`).

- [ ] **Step 1: Add the deterministic txid and put-if-absent**

In `api/internal/repositories/wallet.go` add (mirrors `ErrWithdrawalExists`):

```go
// ErrDepositExists means PutDepositIfAbsent lost a race or is a genuine replay:
// the same txid is already registered.
var ErrDepositExists = errors.New("repositories: deposit already exists")

// PutDepositIfAbsent registers a pending deposit BEFORE any Inter charge is
// opened (SEC-08): a retried request can never open a second charge.
func (r *WalletRepository) PutDepositIfAbsent(ctx context.Context, d *wallet.PixDeposit) error {
	av, err := Encode(d)
	if err != nil {
		return err
	}
	item := r.deposits.BuildPutTxItemIfAbsent(av)
	if err := r.deposits.TransactWrite(ctx, []types.TransactWriteItem{item}); err != nil {
		if IsConditionFailed(err) {
			return ErrDepositExists
		}
		return err
	}
	return nil
}
```

Add `PutDepositIfAbsent(ctx context.Context, d *wallet.PixDeposit) error` to `DepositStore` and to the fakes.

In `api/internal/services/wallet.go`:

```go
const (
	depositTxIDPrefix          = "dep"
	depositTxIDSeparator       = "\x00"
	depositTxIDDigestLength    = 30 // Inter txids are 26-35 alphanumeric chars
	depositTTLMinutes          = 60
)

// depositTxID derives the Inter-compatible txid from the idempotency key so a
// retried POST maps to the same charge. Length: len("dep")+30 = 33.
func depositTxID(userID, idemKey string) string {
	sum := sha256.Sum256([]byte(userID + depositTxIDSeparator + idemKey))
	return depositTxIDPrefix + hex.EncodeToString(sum[:])[:depositTxIDDigestLength]
}
```

(`sandboxPurchaseTxID` in `services/sandbox_purchase.go` is the model; if a shared helper is cleaner, extract `idemTxID(prefix, digestLen string, parts ...string)` and make both call it, rather than duplicating. Run `rg "sha256.Sum256" api/internal/services` first and unify.)

IMPORTANT: confirm the pix-gateway webhook routes the `dep` prefix to `confirm-deposit`. Run `rg "TxidPrefix|HasPrefix" pix-gateway/cmd/webhook/main.go`; sandbox purchases use `sbxp`, product purchases their own prefix, and anything else falls through to deposit. If `dep` collides with another prefix, pick a different constant. Add a webhook routing test if one is missing.

- [ ] **Step 2: Write the failing tests**

In `api/internal/services/wallet_test.go`, using the existing fakes (read the top of the file for the `newTestService` helper and fake repo; reuse them):

```go
func TestInitiateDepositRejectsOverDailyCapBeforeCharge(t *testing.T) {
	svc, fakes := newTestService(t) // existing helper
	fakes.users.setRealCounters("u1", wallet.RealDailyCounters{DayKey: todayKey(), DepositSum: 90000})
	_, _, err := svc.InitiateDeposit(ctx, "u1", wallet.KYCVerified, 20000, "k1")
	var p *problem.Problem
	if !errors.As(err, &p) || p.Type != problem.TypeDailyDepositLimit {
		t.Fatalf("err = %v", err)
	}
	if fakes.pix.ChargesCreated() != 0 {
		t.Fatal("a charge was opened for a rejected amount")
	}
}

func TestInitiateDepositReplayReturnsSameChargeNoSecondCharge(t *testing.T) {
	svc, fakes := newTestService(t)
	d1, _, err := svc.InitiateDeposit(ctx, "u1", wallet.KYCVerified, 5000, "k1")
	if err != nil {
		t.Fatal(err)
	}
	d2, _, err := svc.InitiateDeposit(ctx, "u1", wallet.KYCVerified, 5000, "k1")
	if err != nil {
		t.Fatal(err)
	}
	if d1.Txid != d2.Txid {
		t.Fatalf("txid changed on replay: %s vs %s", d1.Txid, d2.Txid)
	}
	if fakes.pix.ChargesCreated() != 1 {
		t.Fatalf("charges created = %d", fakes.pix.ChargesCreated())
	}
}

func TestInitiateDepositRequiresKYC(t *testing.T) {
	svc, _ := newTestService(t)
	if _, _, err := svc.InitiateDeposit(ctx, "u1", "", 5000, "k1"); err == nil {
		t.Fatal("empty KYC level must be rejected")
	}
}

func TestInitiateDepositAmountOutsideRange(t *testing.T) {
	svc, _ := newTestService(t)
	if _, _, err := svc.InitiateDeposit(ctx, "u1", wallet.KYCVerified, 50, "k1"); err == nil {
		t.Fatal("below the 100 centavo floor must be rejected")
	}
}
```

Where the fakes lack `ChargesCreated()`, `setRealCounters`, or `todayKey()`, add them to the existing fakes (`pix.FakePixClient` counts `CreateCharge` calls; the stub user repo returns a `wallet.User` with the counters). Adapt names to the helpers that already exist in this file; do not create a parallel test harness.

- [ ] **Step 3: Run to verify they fail**

Run: `cd api && go test ./internal/services/ -run InitiateDeposit -v`
Expected: FAIL (`InitiateDeposit` undefined).

- [ ] **Step 4: Implement `InitiateDeposit`**

Restore the pre-BaaS signature and gates (`git show 2f77694^:api/internal/services/wallet.go`, lines 201-278, is the reference). Replace its `ReserveDepositIdem` block with the deterministic-txid technique, and run the advisory daily pre-check only when the request is NOT a replay, so a replay of an already-accepted request is never rejected and a rejected pre-check never leaves a pending row behind:

```go
func (s *WalletService) InitiateDeposit(ctx context.Context, userID, kycLevel string, amount int64, idemKey string) (*wallet.PixDeposit, *pix.Charge, error) {
	if kycLevel == "" {
		return nil, nil, problem.KYCNotVerified()
	}
	realw, err := s.repo.EnsureRealWallet(ctx, userID)
	if err != nil {
		return nil, nil, err
	}
	if amount > wallet.MaxInboundAmount {
		return nil, nil, problem.AmountAboveLimit(wallet.MaxInboundAmount)
	}
	// Range first: never open a PIX charge for an amount we will reject.
	if err := wallet.ValidateDepositAmount(amount, realw); err != nil {
		minAmt, maxAmt := wallet.DepositLimits(realw)
		return nil, nil, problem.DepositOutOfRange(minAmt, maxAmt)
	}

	txid := depositTxID(userID, idemKey)

	// Replay: the same key must mean the same request.
	existing, err := s.repo.GetDeposit(ctx, txid)
	if err != nil {
		return nil, nil, err
	}
	if existing != nil {
		if existing.UserID != userID || existing.AmountExpected != amount {
			return nil, nil, problem.IdempotencyConflict()
		}
		charge, qerr := s.pix.QueryCharge(ctx, txid)
		if qerr != nil {
			// Crash between the durable reservation and CreateCharge: Inter's
			// txid is unique, so re-creating with the same txid cannot open a
			// second charge.
			if charge, qerr = s.pix.CreateCharge(ctx, txid, existing.AmountExpected, ""); qerr != nil {
				return nil, nil, qerr
			}
		}
		return existing, charge, nil
	}

	// Advisory daily-cap pre-check for a NEW request. The binding enforcement
	// is at credit time (Task 6); this only avoids opening a doomed charge.
	breach, err := s.dailyDepositBreach(ctx, userID, realw, amount)
	if err != nil {
		return nil, nil, err
	}
	if p := breachProblem(breach); p != nil {
		return nil, nil, p
	}

	dep := &wallet.PixDeposit{
		Txid:           txid,
		WalletID:       realw.WalletID,
		UserID:         userID,
		AmountExpected: amount,
		Status:         wallet.DepositPending,
		CreatedAt:      repositories.NowStr(),
		TTL:            time.Now().Add(depositTTLMinutes * time.Minute).Unix(),
	}
	// Reserve the txid BEFORE opening the charge (SEC-08). Losing the race to a
	// concurrent identical request means it owns the charge: return its record.
	if err := s.repo.PutDepositIfAbsent(ctx, dep); err != nil {
		if errors.Is(err, repositories.ErrDepositExists) {
			return s.InitiateDeposit(ctx, userID, kycLevel, amount, idemKey)
		}
		return nil, nil, err
	}
	charge, err := s.pix.CreateCharge(ctx, txid, amount, "")
	if err != nil {
		return nil, nil, problem.InternalServer("falha ao criar cobrança PIX: " + err.Error())
	}
	return dep, charge, nil
}
```

The recursive call on `ErrDepositExists` re-enters at the replay branch (the row now exists), so it terminates after one level.

Define the two small helpers in the same file:

```go
func (s *WalletService) dailyDepositBreach(ctx context.Context, userID string, realw *wallet.Wallet, amount int64) (*wallet.DailyBreach, error) {
	u, err := s.users.Get(ctx, userID)
	if err != nil {
		return nil, err
	}
	var c wallet.RealDailyCounters
	if u != nil && u.RealDailyCounters != nil {
		c = *u.RealDailyCounters
	}
	return wallet.CheckDailyDeposit(wallet.EffectiveDailyLimits(realw), c, amount, time.Now()), nil
}

func breachProblem(b *wallet.DailyBreach) *problem.Problem {
	if b == nil {
		return nil
	}
	if b.Kind == wallet.DailyBreachDepositCap {
		return problem.DailyDepositLimit(b.Limit, b.Used, b.ResetsAt)
	}
	return problem.DailyWithdrawLimit(b.Kind, b.Limit, b.Used, b.ResetsAt)
}
```

- [ ] **Step 5: Run to verify they pass**

Run: `cd api && go test ./internal/services/ -run 'InitiateDeposit' -v` → PASS.

- [ ] **Step 6: Commit**

```bash
git add api && git commit -m "feat(api): restore Inter PIX deposit initiation with daily cap pre-check"
```

---

### Task 6: Enforce the daily deposit cap at credit time

**Files:**
- Modify: `api/internal/services/wallet.go` (`ConfirmDeposit`, the credit block near line 479)
- Test: `api/internal/services/wallet_test.go`, `api/tests/integration/wallet_test.go`

**Interfaces:**
- Consumes: `UserRepo.BumpRealDailyCounters`, `wallet.CheckDailyDeposit`, `rejectMismatch` (existing: moves pending to refund_pending and refunds the payer via Inter).
- Produces: `ConfirmDeposit` credits at most `DailyDepositCap` per BRT day per wallet; any excess paid deposit is refunded to its payer.

- [ ] **Step 1: Write the failing tests**

```go
func TestConfirmDepositOverDailyCapIsRefundedNotCredited(t *testing.T) {
	svc, fakes := newTestService(t)
	seedPaidDeposit(t, fakes, "u1", "txid-over", 20000)           // existing helper pattern: stage deposit + paid charge with matching masked CPF
	fakes.users.setRealCounters("u1", wallet.RealDailyCounters{DayKey: todayKey(), DepositSum: 90000})

	if err := svc.ConfirmDeposit(ctx, "txid-over", fakes.maskedCPFOf("u1"), "Pagador", false); err != nil {
		t.Fatal(err)
	}
	if bal := fakes.repo.balance("u1-real"); bal != 0 {
		t.Fatalf("over-cap deposit was credited: balance %d", bal)
	}
	if !fakes.pix.Refunded("txid-over") {
		t.Fatal("payer was not refunded")
	}
}

func TestConfirmDepositWithinCapCreditsAndBumpsCounter(t *testing.T) {
	svc, fakes := newTestService(t)
	seedPaidDeposit(t, fakes, "u1", "txid-ok", 30000)
	if err := svc.ConfirmDeposit(ctx, "txid-ok", fakes.maskedCPFOf("u1"), "Pagador", false); err != nil {
		t.Fatal(err)
	}
	if got := fakes.users.bumpedRealDeposit("u1"); got != 30000 {
		t.Fatalf("counter bump = %d", got)
	}
}
```

Reuse the deposit-seeding helpers already present in `wallet_test.go` for the existing `ConfirmDeposit` tests; run `rg "func seed|StageCharge" api/internal/services/wallet_test.go` and adapt the names. Add the `bumpedRealDeposit` capture to the stub user repo (record `next.DepositSum` in `BumpRealDailyCounters`).


Also pin the existing fail-closed behavior so it can never regress (this one passes today; it guards the sweep path):

```go
func TestConfirmDepositPaidWithoutPayerCPFStaysPendingAndCreditsNothing(t *testing.T) {
	svc, fakes := newTestService(t)
	seedPaidDeposit(t, fakes, "u1", "txid-nocpf", 10000)
	if err := svc.ConfirmDeposit(ctx, "txid-nocpf", "", "", true); err == nil {
		t.Fatal("a paid deposit with no payer identity must be quarantined with an error")
	}
	if fakes.repo.balance("u1-real") != 0 {
		t.Fatal("credited without payer identity")
	}
	if fakes.repo.depositStatus("txid-nocpf") != wallet.DepositPending {
		t.Fatal("deposit left its pending state")
	}
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `cd api && go test ./internal/services/ -run 'ConfirmDepositOverDailyCap|ConfirmDepositWithinCap' -v` → FAIL.

- [ ] **Step 3: Implement**

In `ConfirmDeposit`, replace the block starting at `release, err := acquireWallet(ctx, s.lock, dep.WalletID)` with:

```go
	release, err := acquireWallet(ctx, s.lock, dep.WalletID)
	if err != nil {
		return err
	}
	defer release()

	// Daily cap, enforced HERE (not only at initiation): two charges opened in
	// parallel can each pass the advisory pre-check. Counters are read and
	// bumped under the real wallet lock, so this check cannot race itself.
	u, err := s.users.Get(ctx, dep.UserID)
	if err != nil {
		return err
	}
	realw, err := s.repo.GetWallet(ctx, dep.WalletID)
	if err != nil {
		return err
	}
	var prev *wallet.RealDailyCounters
	var cur wallet.RealDailyCounters
	if u != nil && u.RealDailyCounters != nil {
		prev = u.RealDailyCounters
		cur = *prev
	}
	now := time.Now()
	if breach := wallet.CheckDailyDeposit(wallet.EffectiveDailyLimits(realw), cur, charge.Amount, now); breach != nil {
		slog.Warn("deposit over daily cap; refunding payer", "txid", txid, "limit", breach.Limit, "used", breach.Used)
		return s.rejectMismatch(ctx, dep, charge)
	}
	day, _, _ := wallet.WindowKeys(now)
	next := cur.ForDay(day)
	next.DepositSum += charge.Amount
	counterTx, err := s.users.BumpRealDailyCounters(dep.UserID, prev, next)
	if err != nil {
		return err
	}

	m := repositories.Mutation{
		WalletID:       dep.WalletID,
		Amount:         charge.Amount,
		EntryType:      wallet.EntryDeposit,
		Ref:            txid,
		IdempotencyKey: "deposit#" + txid,
		ReqHash:        reqHash(txid, charge.Amount),
	}
	if _, _, err := s.repo.ConfirmDepositCredit(ctx, m, txid, charge.E2EID, counterTx); err != nil {
		return err
	}
	s.broadcastDepositConfirmed(ctx, dep.UserID, dep.WalletID, txid, charge.Amount)
	return nil
```

Extend `ConfirmDepositCredit` (repo and `LedgerStore` interface) to accept variadic extras:

```go
func (r *WalletRepository) ConfirmDepositCredit(ctx context.Context, m Mutation, txid, e2eID string, extra ...types.TransactWriteItem) (*wallet.LedgerEntry, bool, error) {
	return r.Credit(ctx, m, append([]types.TransactWriteItem{r.depositStatusTx(txid, wallet.DepositPending, wallet.DepositConfirmed, e2eID)}, extra...)...)
}
```

Behavior note for replay: `mutate` re-applies `extra` on replay and ignores `ConditionalCheckFailed`, so a replayed credit never bumps the counter twice (the optimistic `#c = :prev` condition fails once already bumped).

- [ ] **Step 4: Run to verify they pass**

Run: `cd api && go test ./internal/services/ -v 2>&1 | tail -30` → PASS (including all pre-existing `ConfirmDeposit` tests; if one now fails because its stub user repo lacks counters, give the stub a zero-value `User` response).

- [ ] **Step 5: Integration test (DynamoDB-local)**

Append to `api/tests/integration/wallet_test.go` (follow the structure of the neighbouring deposit test; start DynamoDB-local per `docker compose -f docker-compose.test.yml up -d`):

```go
func TestConfirmDepositConcurrentOverCapOnlyOneCredits(t *testing.T) {
	h := newHarness(t)                                  // existing harness in setup_test.go
	user := h.newKYCUser("u-cap")
	h.stagePaidDeposit(user, "tx-a", 60000)             // two paid charges that together exceed the R$1.000 cap
	h.stagePaidDeposit(user, "tx-b", 60000)

	var wg sync.WaitGroup
	for _, tx := range []string{"tx-a", "tx-b"} {
		wg.Add(1)
		go func() { defer wg.Done(); _ = h.svc.ConfirmDeposit(ctx, tx, h.maskedCPF(user), "P", false) }()
	}
	wg.Wait()
	// A busy loser returns wallet-busy; the retry (Inter re-sends the webhook) settles it.
	for _, tx := range []string{"tx-a", "tx-b"} {
		_ = h.svc.ConfirmDeposit(ctx, tx, h.maskedCPF(user), "P", false)
	}

	if got := h.realBalance(user); got != 60000 {
		t.Fatalf("balance = %d; exactly one 60000 deposit must credit", got)
	}
	if h.pix.RefundCount() != 1 {
		t.Fatalf("refunds = %d; the other payer must be refunded", h.pix.RefundCount())
	}
}
```

Adapt `newHarness`, `newKYCUser`, `stagePaidDeposit` to the real helper names in `setup_test.go` (read it first).

Run: `cd api && make test-integration` → PASS.

- [ ] **Step 6: Commit**

```bash
git add api && git commit -m "feat(api): enforce the daily deposit cap at credit time and refund the excess"
```

---

### Task 7: `Withdraw` with daily limits and slot release on reversal

**Files:**
- Modify: `api/internal/services/wallet.go` (new `Withdraw`, constant `withdrawalIDPrefix`)
- Modify: `api/internal/services/reconcile.go:109` (`reverse` releases the slot)
- Test: `api/internal/services/wallet_test.go`, `api/internal/services/reconcile_test.go`

**Interfaces:**
- Consumes: `repo.Debit(ctx, m, extra...)`, `repo.WithdrawalPutTx`, `users.BumpRealDailyCounters`, `wallet.CheckDailyWithdraw`, `interIdemKey`, `s.reverse`, `s.kyc.Get`, `s.pix.Transfer`.
- Produces: `(*WalletService).Withdraw(ctx, userID, kycLevel string, amount int64, idemKey string) (*wallet.Withdrawal, error)`.

Design notes:
- The destination is ALWAYS the CPF on the caller's KYC record (the client never supplies a key). That is the "destination CPF == KYC CPF" guarantee, by construction.
- The debit, the ledger entry, the idempotency guard, the processing withdrawal row and the counter bump commit in ONE `TransactWriteItems` (`repo.Debit` + extras).
- No fee: debit exactly `amount`.

- [ ] **Step 1: Write the failing tests**

```go
func TestWithdrawDebitsExactAmountNoFee(t *testing.T) {
	svc, fakes := newTestService(t)
	fakes.fund("u1", 50000)
	w, err := svc.Withdraw(ctx, "u1", wallet.KYCVerified, 30000, "k1")
	if err != nil {
		t.Fatal(err)
	}
	if w.Status != wallet.WithdrawCompleted || fakes.repo.balance("u1-real") != 20000 {
		t.Fatalf("status %s balance %d", w.Status, fakes.repo.balance("u1-real"))
	}
	if fakes.pix.TransferTo() != fakes.kycCPF("u1") {
		t.Fatal("payout did not go to the KYC CPF")
	}
}

func TestWithdrawSecondOfTheDayIsRejected(t *testing.T) {
	svc, fakes := newTestService(t)
	fakes.fund("u1", 50000)
	if _, err := svc.Withdraw(ctx, "u1", wallet.KYCVerified, 10000, "k1"); err != nil {
		t.Fatal(err)
	}
	_, err := svc.Withdraw(ctx, "u1", wallet.KYCVerified, 10000, "k2")
	var p *problem.Problem
	if !errors.As(err, &p) || p.Type != problem.TypeDailyWithdrawLimit {
		t.Fatalf("err = %v", err)
	}
	if fakes.pix.TransferCount() != 1 {
		t.Fatalf("transfers = %d", fakes.pix.TransferCount())
	}
}

func TestWithdrawOverDailyAmountCap(t *testing.T) {
	svc, fakes := newTestService(t)
	fakes.fund("u1", 500000)
	_, err := svc.Withdraw(ctx, "u1", wallet.KYCVerified, 100001, "k1")
	var p *problem.Problem
	if !errors.As(err, &p) || p.Type != problem.TypeDailyWithdrawLimit {
		t.Fatalf("err = %v", err)
	}
}

func TestWithdrawReplaySameKeyNoSecondPayout(t *testing.T) {
	svc, fakes := newTestService(t)
	fakes.fund("u1", 50000)
	a, _ := svc.Withdraw(ctx, "u1", wallet.KYCVerified, 10000, "k1")
	b, err := svc.Withdraw(ctx, "u1", wallet.KYCVerified, 10000, "k1")
	if err != nil || a.WithdrawalID != b.WithdrawalID {
		t.Fatalf("replay: %v %v", b, err)
	}
	if fakes.pix.TransferCount() != 1 {
		t.Fatalf("transfers = %d", fakes.pix.TransferCount())
	}
}

func TestWithdrawKeyNotFoundReverseGivesTheSlotBack(t *testing.T) {
	svc, fakes := newTestService(t)
	fakes.fund("u1", 50000)
	fakes.pix.FailTransferWith(pix.ErrKeyNotFound)
	if _, err := svc.Withdraw(ctx, "u1", wallet.KYCVerified, 10000, "k1"); err == nil {
		t.Fatal("want pix-key-not-found")
	}
	if fakes.repo.balance("u1-real") != 50000 {
		t.Fatal("not refunded")
	}
	fakes.pix.FailTransferWith(nil)
	if _, err := svc.Withdraw(ctx, "u1", wallet.KYCVerified, 10000, "k2"); err != nil {
		t.Fatalf("slot was not released after the reversal: %v", err)
	}
}

func TestWithdrawBelowMinimumUnlessFullBalance(t *testing.T) {
	svc, fakes := newTestService(t)
	fakes.fund("u1", 5000)
	if _, err := svc.Withdraw(ctx, "u1", wallet.KYCVerified, 50, "k1"); err == nil {
		t.Fatal("below minimum must be rejected")
	}
	fakes.fund("u2", 50)
	if _, err := svc.Withdraw(ctx, "u2", wallet.KYCVerified, 50, "k2"); err != nil {
		t.Fatalf("emptying the whole balance below the minimum must pass: %v", err)
	}
}

func TestWithdrawRequiresEnhancedKYC(t *testing.T) {
	svc, _ := newTestService(t)
	if _, err := svc.Withdraw(ctx, "u1", "basic", 10000, "k1"); err == nil {
		t.Fatal("basic KYC must be rejected")
	}
}
```

Fake helpers (`fund`, `kycCPF`, `TransferTo`, `TransferCount`, `FailTransferWith`) go into the existing fakes. `FakePixClient` already implements `Transfer`; add the counters if absent (`rg "func (f \*FakePixClient) Transfer" api/internal/pix/fake.go`).

- [ ] **Step 2: Run to verify they fail**

Run: `cd api && go test ./internal/services/ -run '^TestWithdraw' -v` → FAIL.

- [ ] **Step 3: Implement `Withdraw`**

```go
const withdrawalIDPrefix = "withdraw#"

// withdrawalDescription is the statement text of the debit. Display only.
const withdrawalDescription = "Saque via PIX"

func (s *WalletService) Withdraw(ctx context.Context, userID, kycLevel string, amount int64, idemKey string) (*wallet.Withdrawal, error) {
	if kycLevel != wallet.KYCVerified {
		return nil, problem.KYCNotVerified()
	}
	withdrawalID := withdrawalIDPrefix + userID + "#" + idemKey

	realw, err := s.repo.EnsureRealWallet(ctx, userID)
	if err != nil {
		return nil, err
	}
	release, err := acquireWallet(ctx, s.lock, realw.WalletID)
	if err != nil {
		return nil, err
	}
	defer release()

	// Replay under the lock, so two concurrent identical calls cannot both pass.
	if existing, err := s.repo.GetWithdrawal(ctx, withdrawalID); err != nil {
		return nil, err
	} else if existing != nil {
		return existing, nil
	}

	if err := wallet.ValidateWithdrawalAmount(amount, realw, amount == realw.Balance, false); err != nil {
		return nil, problem.BadRequest("valor abaixo do mínimo de saque")
	}

	u, err := s.users.Get(ctx, userID)
	if err != nil {
		return nil, err
	}
	var prev *wallet.RealDailyCounters
	var cur wallet.RealDailyCounters
	if u != nil && u.RealDailyCounters != nil {
		prev = u.RealDailyCounters
		cur = *prev
	}
	now := time.Now()
	if breach := wallet.CheckDailyWithdraw(wallet.EffectiveDailyLimits(realw), cur, amount, now); breach != nil {
		return nil, problem.DailyWithdrawLimit(breach.Kind, breach.Limit, breach.Used, breach.ResetsAt)
	}
	day, _, _ := wallet.WindowKeys(now)
	next := cur.ForDay(day)
	next.WithdrawCount++
	next.WithdrawSum += amount
	counterTx, err := s.users.BumpRealDailyCounters(userID, prev, next)
	if err != nil {
		return nil, err
	}

	kyc, err := s.kyc.Get(ctx, userID)
	if err != nil {
		return nil, err
	}
	pixKey := kyc.CPF // destination is ALWAYS the KYC owner's CPF; the client never supplies a key

	w := &wallet.Withdrawal{
		WithdrawalID:   withdrawalID,
		WalletID:       realw.WalletID,
		UserID:         userID,
		Amount:         amount,
		PixKey:         pixKey,
		Status:         wallet.WithdrawProcessing,
		IdempotencyKey: idemKey,
		CreatedAt:      repositories.NowStr(),
		UpdatedAt:      repositories.NowStr(),
	}
	putTx, err := s.repo.WithdrawalPutTx(w)
	if err != nil {
		return nil, err
	}
	_, replayed, err := s.repo.Debit(ctx, repositories.Mutation{
		WalletID:       realw.WalletID,
		Amount:         amount,
		EntryType:      wallet.EntryWithdraw,
		Ref:            withdrawalID,
		Description:    withdrawalDescription,
		IdempotencyKey: withdrawalID,
		ReqHash:        reqHash(pixKey, amount),
	}, putTx, counterTx)
	if err != nil {
		return nil, err
	}
	if replayed {
		return s.repo.GetWithdrawal(ctx, withdrawalID)
	}

	res, err := s.pix.Transfer(ctx, pixKey, amount, interIdemKey(withdrawalID))
	if err != nil {
		if errors.Is(err, pix.ErrKeyNotFound) {
			s.reverse(ctx, *w)
			return nil, problem.PixKeyNotFound()
		}
		slog.Warn("withdrawal transfer failed, left in processing", "withdrawal_id", withdrawalID, "err", err)
		return w, nil
	}
	w.Status, w.E2EID = wallet.WithdrawCompleted, res.E2EID
	if err := s.repo.UpdateWithdrawal(ctx, withdrawalID, map[string]any{"status": wallet.WithdrawCompleted, "e2e_id": res.E2EID}); err != nil {
		return nil, err
	}
	s.broadcastWithdrawal(ctx, userID, eventWithdrawalComplete, withdrawalID, amount)
	return w, nil
}
```

Notes: the `ref`-vs-description rule (spec 2026-08-29) holds: `Ref` is the machine key, `Description` the human text. `eventWithdrawalComplete` already exists (`reconcile.go` uses it); if the compile error says it is named differently use the existing constant. A transfer failure other than `ErrKeyNotFound` leaves the withdrawal `processing` for reconcile (Invariant 14), already wired.

- [ ] **Step 4: Release the daily slot on reversal**

In `reverse` (`api/internal/services/reconcile.go`), build a counter decrement and pass it as the `extra` of the reversal `Credit`. `reverse` is called both from `Withdraw` (lock held) and from `ReverseWithdrawal` (which takes the lock; verify with `rg "ReverseWithdrawal" -n api/internal/services/reconcile.go` and read lines 82-104). Replace the `s.repo.Credit(...)` call with:

```go
	extra, xerr := s.releaseWithdrawalSlot(ctx, w)
	if xerr != nil {
		slog.Warn("withdrawal slot release skipped", "withdrawal_id", w.WithdrawalID, "err", xerr)
	}
	_, _, err := s.repo.Credit(ctx, repositories.Mutation{
		WalletID:       w.WalletID,
		Amount:         total,
		EntryType:      wallet.EntryReversal,
		Ref:            "reverse:" + w.WithdrawalID,
		IdempotencyKey: "reverse#" + w.WithdrawalID,
		ReqHash:        reqHash("reverse:"+w.WithdrawalID, total),
	}, extra...)
```

and in the same file:

```go
// releaseWithdrawalSlot builds the counter decrement that returns a reversed
// withdrawal's daily slot. It only applies when the counters still belong to the
// day the withdrawal was created; a reversal on a later day changes nothing. A
// failure here must never block the money reversal, so callers log and continue.
func (s *WalletService) releaseWithdrawalSlot(ctx context.Context, w wallet.Withdrawal) ([]types.TransactWriteItem, error) {
	u, err := s.users.Get(ctx, w.UserID)
	if err != nil || u == nil || u.RealDailyCounters == nil {
		return nil, err
	}
	created, perr := time.Parse(time.RFC3339Nano, w.CreatedAt)
	if perr != nil {
		return nil, perr
	}
	day, _, _ := wallet.WindowKeys(created)
	prev := u.RealDailyCounters
	if prev.DayKey != day {
		return nil, nil
	}
	next := *prev
	next.WithdrawCount = max(0, next.WithdrawCount-1)
	next.WithdrawSum = max(0, next.WithdrawSum-w.Amount)
	tx, err := s.users.BumpRealDailyCounters(w.UserID, prev, next)
	if err != nil {
		return nil, err
	}
	return []types.TransactWriteItem{tx}, nil
}
```

Check that `repositories.NowStr()` formats as RFC3339 (`rg "func NowStr" -A3 api/internal/repositories`). If it uses another layout, parse with that layout constant.

- [ ] **Step 5: Reconcile test for the slot release**

In `reconcile_test.go`, mirroring the existing `ReconcileWithdrawals` reversal test: stage a processing withdrawal whose `Transfer` reports not-found/failed, run `ReconcileWithdrawals`, then assert that the stub user repo recorded a `BumpRealDailyCounters` with `WithdrawCount == 0`.

- [ ] **Step 6: Run to verify**

Run: `cd api && go build ./... && go test ./internal/services/ -v 2>&1 | tail -40` → PASS.

- [ ] **Step 7: Commit**

```bash
git add api && git commit -m "feat(api): restore Inter PIX withdrawal with daily limits and slot release on reversal"
```

---

### Task 8: HTTP routes, DTOs, scopes, step-up

**Files:**
- Modify: `api/internal/api/v1/dto.go` (two request types)
- Modify: `api/internal/api/v1/wallet.go` (two handlers)
- Modify: `api/internal/api/v1/router.go:44-46` (routes)
- Test: `api/internal/api/v1/router_test.go`, new `api/internal/api/v1/wallet_rail_test.go`

**Interfaces:**
- Consumes: `svc.InitiateDeposit`, `svc.Withdraw`, `middleware.RequireUserScope`, `middleware.RequireKYC(middleware.KYCVerified)`, `middleware.RequireRecentMFA(middleware.StepUpMaxAge)`, constants `middleware.ScopeWalletDepositsWrite`, `middleware.ScopeWalletWithdrawalsWrite`.
- Produces: `POST /v1.0/wallet/deposits` (201, body `{txid, amount, status, pix_copia_e_cola, qr_code_base64, expires_at}`), `POST /v1.0/wallet/withdrawals` (201 completed, 202 processing, body is the `Withdrawal`).

- [ ] **Step 1: Failing router test**

Add to `router_test.go` (follow the neighbouring route-registration tests in that file):

```go
func TestRailRoutesRegistered(t *testing.T) {
	app := newTestApp(t) // existing helper
	for _, route := range []struct{ method, path string }{
		{"POST", "/v1.0/wallet/deposits"},
		{"POST", "/v1.0/wallet/withdrawals"},
	} {
		if !hasRoute(app, route.method, route.path) {
			t.Errorf("%s %s not registered", route.method, route.path)
		}
	}
}

func TestWithdrawalRouteRequiresStepUp(t *testing.T) {
	status := postWithToken(t, "/v1.0/wallet/withdrawals", `{"amount":10000}`, tokenWithKYC("enhanced").withLastMFAAt(time.Now().Add(-time.Hour)))
	if status != http.StatusForbidden {
		t.Fatalf("stale MFA must be 403 step-up-required, got %d", status)
	}
}
```

Use the real helper names from `router_test.go` (read it first); keep the assertions.

- [ ] **Step 2: Run to verify they fail**

Run: `cd api && go test ./internal/api/v1/ -run 'RailRoutes|WithdrawalRouteRequiresStepUp' -v` → FAIL.

- [ ] **Step 3: DTOs**

Append to `dto.go`:

```go
// DepositRequest opens a PIX charge for the caller's real wallet.
type DepositRequest struct {
	Amount int64 `json:"amount" validate:"required,gt=0"`
}

// WithdrawRequest asks for a PIX payout. There is deliberately NO destination
// field: the payout always goes to the CPF on the caller's KYC record.
type WithdrawRequest struct {
	Amount int64 `json:"amount" validate:"required,gt=0"`
}
```

- [ ] **Step 4: Handlers**

Insert `createDeposit` and `createWithdrawal` into `wallet.go`, restored from `git show 2f77694^:api/internal/api/v1/wallet.go | sed -n 31,83p`, with ONE edit: the withdrawal comment changes from "debits amount+fee" to "debits amount", and the handler needs the `wallet` import for `wallet.WithdrawProcessing` (already imported in this file). The 201/202 split stays.

- [ ] **Step 5: Routes**

In `router.go` next to the other `w.Post` lines:

```go
	w.Post("/deposits", middleware.RequireUserScope(middleware.ScopeWalletDepositsWrite), middleware.RequireKYC(middleware.KYCVerified), h.createDeposit)
	w.Post("/withdrawals", middleware.RequireUserScope(middleware.ScopeWalletWithdrawalsWrite), middleware.RequireKYC(middleware.KYCVerified), middleware.RequireRecentMFA(middleware.StepUpMaxAge), h.createWithdrawal)
```

Confirm `ScopeWalletDepositsWrite`/`ScopeWalletWithdrawalsWrite` are in the scope manifest (`rg "wallet:deposits:write|wallet:withdrawals:write" api ui/src/lib/auth`). `ui/src/lib/auth/scopes.ts` must request both (Task 10).

- [ ] **Step 6: Run and commit**

Run: `cd api && go build ./... && go test ./internal/api/... -v 2>&1 | tail -30` → PASS.

```bash
git add api && git commit -m "feat(api): register deposit and withdrawal routes with KYC and step-up gates"
```

---

### Task 9: Integration tests for the rail

**Files:**
- Test: `api/tests/integration/wallet_test.go`, `api/tests/integration/rail_test.go` (create)

- [ ] **Step 1: Write the tests** (use the harness in `setup_test.go`; start DynamoDB-local first)

```go
func TestDepositFullLoopAndReplay(t *testing.T) {
	h := newHarness(t)
	u := h.newKYCUser("u-loop")
	dep, _, err := h.svc.InitiateDeposit(ctx, u.ID, wallet.KYCVerified, 25000, "idem-1")
	require.NoError(t, err)
	dep2, _, err := h.svc.InitiateDeposit(ctx, u.ID, wallet.KYCVerified, 25000, "idem-1")
	require.NoError(t, err)
	require.Equal(t, dep.Txid, dep2.Txid)
	require.Equal(t, 1, h.pix.ChargesCreated())

	h.pix.StageCharge(dep.Txid, 25000, pix.ChargeCompleted, h.maskedCPF(u), "e2e-1")
	require.NoError(t, h.svc.ConfirmDeposit(ctx, dep.Txid, h.maskedCPF(u), "Pagador", false))
	require.NoError(t, h.svc.ConfirmDeposit(ctx, dep.Txid, h.maskedCPF(u), "Pagador", false)) // webhook retry
	require.Equal(t, int64(25000), h.realBalance(u))
	require.Equal(t, 1, h.ledgerCount(u, wallet.EntryDeposit))
}

func TestWithdrawalConcurrentSameDayOnlyOneSucceeds(t *testing.T) {
	h := newHarness(t)
	u := h.newKYCUser("u-w")
	h.fundReal(u, 500000)
	var ok, limited atomic.Int32
	var wg sync.WaitGroup
	for i := range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := h.svc.Withdraw(ctx, u.ID, wallet.KYCVerified, 10000, fmt.Sprintf("k%d", i))
			var p *problem.Problem
			switch {
			case err == nil:
				ok.Add(1)
			case errors.As(err, &p) && (p.Type == problem.TypeDailyWithdrawLimit || p.Type == problem.TypeWalletBusy):
				limited.Add(1)
			default:
				t.Errorf("unexpected: %v", err)
			}
		}()
	}
	wg.Wait()
	require.Equal(t, int32(1), ok.Load())
	require.Equal(t, int64(490000), h.realBalance(u))
}

func TestWithdrawalProcessingResolvedByReconcile(t *testing.T) {
	h := newHarness(t)
	u := h.newKYCUser("u-p")
	h.fundReal(u, 50000)
	h.pix.FailTransferWith(errors.New("bank timeout"))
	w, err := h.svc.Withdraw(ctx, u.ID, wallet.KYCVerified, 10000, "k1")
	require.NoError(t, err)
	require.Equal(t, wallet.WithdrawProcessing, w.Status)
	h.pix.StageTransfer(interIdemFor(w.WithdrawalID), pix.TransferDone, "e2e-w")
	_, _, _, err = h.svc.ReconcileWithdrawals(ctx)
	require.NoError(t, err)
	got, _ := h.repo.GetWithdrawal(ctx, w.WithdrawalID)
	require.Equal(t, wallet.WithdrawCompleted, got.Status)
}
```

Adapt `newHarness`/`newKYCUser`/`fundReal`/`ledgerCount`/`interIdemFor` to what `setup_test.go` already exposes; add small helpers there when missing rather than a second harness. If `require` (testify) is not used by this module, convert to `t.Fatalf` form (`rg "testify" api/go.mod`).

- [ ] **Step 2: Run**

Run: `cd api && docker compose -f docker-compose.test.yml up -d && make test-integration` → PASS, and `go test ./... -run TestSandboxPurchaseNeverDebitsRealWallet -v` → PASS.

- [ ] **Step 3: Commit**

```bash
git add api && git commit -m "test(api): integration coverage for the Inter rail and daily limits"
```

---

### Task 10: UI, restore deposit and withdrawal flows

**Files:**
- Modify: `ui/src/lib/types/api.ts`, `ui/src/lib/api/client.ts`, `ui/src/lib/auth/scopes.ts`, `ui/src/lib/auth/oauth.ts`, `ui/src/lib/hooks/useWalletRealtime.ts`
- Modify: `ui/src/components/wallet/amount-dialog.tsx`, `confirm-money-dialog.tsx`, `balance-cards.tsx`, `ui/src/app/dashboard/page.tsx`
- Create (from git): `ui/src/components/wallet/pix-charge-dialog.tsx`
- Modify: `ui/src/locales/en.json`, `ui/src/locales/pt-BR.json`
- Modify: `ui/src/lib/mock.ts` (mock `createDeposit`/`createWithdrawal` only if the mock client interface requires it)

**Do NOT restore:** `asaas-badge.tsx`, `deposit-gate.tsx`, `onboarding-dialog.tsx`, `custody-fee-dialog.tsx`, `transaction-status-list.tsx`/`transaction-status.ts` (custody-era). Deposit and withdrawal confirmation is shown by the existing toast and ledger refresh. Keep text minimal: self-describing buttons, no explanatory paragraphs (design spec section 4), and no "—" characters in any string you write.

- [ ] **Step 1: Restore the generic pieces from git**

```bash
git show 23270a1^:ui/src/components/wallet/pix-charge-dialog.tsx > ui/src/components/wallet/pix-charge-dialog.tsx
```

Then remove the "Closing this window is safe" explanatory sentence by keeping `pix.description` short in the locale file (Step 6).

- [ ] **Step 2: Types and client**

`ui/src/lib/types/api.ts` (restore):

```ts
export interface DepositResult {
  txid: string
  amount: number
  status: string
  pix_copia_e_cola: string
  qr_code_base64?: string
  expires_at: number // unix seconds
}

export interface Withdrawal {
  withdrawal_id: string
  wallet_id: string
  user_id: string
  amount: number
  pix_key: string
  status: 'processing' | 'completed' | 'reversed' | 'refund_failed'
  e2e_id?: string
  created_at: string
  updated_at: string
}
```

`ui/src/lib/api/client.ts`: add `DepositResult`, `Withdrawal` to the type import and, after `getBalances`:

```ts
  async createDeposit(amount: number, idempotencyKey: string): Promise<DepositResult> {
    return (
      await this.http.post<DepositResult>('/v1.0/wallet/deposits', {amount}, idemConfig(idempotencyKey))
    ).data
  }

  async createWithdrawal(amount: number, idempotencyKey: string): Promise<Withdrawal> {
    return (
      await this.http.post<Withdrawal>('/v1.0/wallet/withdrawals', {amount}, idemConfig(idempotencyKey))
    ).data
  }
```

If `MockApiClient` in `lib/mock.ts` implements the same interface and the build complains, add matching mock methods that resolve a fixed `DepositResult`/`Withdrawal`.

- [ ] **Step 3: Scopes and step-up**

`ui/src/lib/auth/scopes.ts`: ensure `WALLET_SCOPES` contains `'wallet:deposits:write'` and `'wallet:withdrawals:write'` (verify with `rg`; add if missing). `ui/src/lib/auth/oauth.ts` restore:

```ts
/**
 * Step-up variant of startOAuthFlow: forces ctech-account to require a fresh
 * interactive login (max_age=0) instead of reusing the SSO session.
 */
export async function startStepUpFlow(returnTo = '/'): Promise<void> {
  await client.startOAuthFlow(returnTo, {maxAge: 0})
}
```

`AuthContext.reverify`: `git show 23270a1 -- ui/src/lib/context/AuthContext.tsx` shows the removed `reverify`; restore only that member (it calls `startStepUpFlow(window.location.pathname)`), not any custody members.

- [ ] **Step 4: Realtime toasts**

Keep `useWalletRealtime()` argument-free (the callback props were only for the removed status list). The existing handler already toasts `deposit_confirmed` and the `withdraw_*` events; confirm `WITHDRAW_TOAST_KEY` still maps them and that they invalidate `['balances']` and `['ledger']`.

- [ ] **Step 5: Dialogs and dashboard**

`amount-dialog.tsx`: restore flows `'deposit' | 'withdraw'` in `Flow` and `FLOW_KEY`, and `capMillion = flow === 'deposit' || flow === 'fund-game'`; for withdraw restore `overWithdrawable` message. Do NOT restore the `AsaasBadge` header or the `pixDestination` paragraph.

`confirm-money-dialog.tsx`: restore `'withdraw'` in `Flow`/`FLOW_KEY` and the `stepUp`/`onReverify` props, the focus effect and the stepUp branch exactly as in `git show 23270a1 -- ui/src/components/wallet/confirm-money-dialog.tsx` (reverse-apply: `git show 23270a1 -- ui/src/components/wallet/confirm-money-dialog.tsx | git apply -R`).

`balance-cards.tsx`: restore props `onDeposit`, `onWithdraw` and the two buttons on the Real card (Deposit as `variant="brand"`, Withdraw as the outlined button), WITHOUT `DepositGate`, `AsaasBadge`, custody props or the provider note.

`dashboard/page.tsx`: restore only:
- `Flow` gains `'deposit' | 'withdraw'`; `confirm.flow` gains `'withdraw'`.
- state `charge`/`chargeOpen`, `receipt` already exists, `stepUp`.
- the `deposit` and `withdraw` `useMutation`s, simplified: on deposit success `setFlow(null); setCharge(result); setChargeOpen(true)`; on withdraw success `setConfirm(null); setFlow(null); refresh()` then `toast.info(t('toast.withdrawProcessing'))` when `status === 'processing'`, else `setReceipt({title: t('toast.withdrawSent'), amountLabel: formatBRL(w.amount)})`; `onError` for `/problems/step-up-required` does `setStepUp(true)`.
- `PROBLEM_KEY` entries: `/problems/withdraw-cpf-mismatch`, `/problems/pix-key-not-found`, `/problems/step-up-required`, plus new `/problems/daily-deposit-limit` → `errors.dailyDepositLimit` and `/problems/daily-withdraw-limit` → `errors.dailyWithdrawLimit`; and the `deposit-out-of-range` branch in `problemMessage`.
- JSX: the two `AmountDialog`s, the `PixChargeDialog` (`{charge && chargeOpen && <PixChargeDialog deposit={charge} onClose={() => setChargeOpen(false)}/>}`), and the `withdraw` branches in `ConfirmMoneyDialog` (`stepUp`, `onReverify={reverify}`, `pending`, `onConfirm`).
- `const {profile, logout, reverify} = useAuth()`.
- Persist the open charge across reloads? NO (that was the custody-era `transaction-status` machinery; a closed dialog simply means the user opens a new deposit).

Reference diff: `git show 23270a1 -- ui/src/app/dashboard/page.tsx`; apply only the hunks listed above.

- [ ] **Step 6: Locales (pt-BR and en, same keys)**

Restore from `git show 23270a1 -- ui/src/locales/pt-BR.json ui/src/locales/en.json` ONLY these keys, and keep each string to one short sentence with no "—": `dialog.deposit.*`, `dialog.withdraw.*` (title, description), `dialog.error.overWithdrawable`, `confirm.withdraw.*`, `confirm.stepUp.*`, `pix.*`, `toast.pixCopied`, `toast.withdrawSent`, `toast.withdrawProcessing`, `toast.withdrawReversed`, `toast.withdrawRefundFailed`, `toast.depositConfirmed`, `toast.realtimeDeposit`, `balance.deposit`, `balance.withdraw`, `errors.withdrawCpfMismatch`, `errors.pixKeyNotFound`, `errors.stepUpRequired`, `errors.depositOutOfRange`. Add new:

```json
"errors.dailyDepositLimit": "Limite diário de depósito atingido",
"errors.dailyWithdrawLimit": "Limite diário de saque atingido"
```
(en: "Daily deposit limit reached", "Daily withdrawal limit reached"). Do not restore any `custody.*`, `onboarding.*`, `deposit.gate.*`, `deposit.providerNote` keys.

- [ ] **Step 7: Verify and commit**

```bash
cd ui && npx tsc --noEmit && npx eslint src --ext .ts,.tsx && node --test src/**/*.test.mjs
```
Expected: no errors, no warnings, tests pass (update `ui-hardening.test.mjs`/`ui-adaptation.test.mjs` only if they pin the removed buttons).

Manual check: `npm run dev`, log in with the mock (`USE_MOCK`), click Depositar (amount dialog then QR dialog) and Sacar (amount, confirm, step-up path).

```bash
git add ui && git commit -m "feat(ui): restore PIX deposit and withdrawal flows"
```

---

### Task 11: Docs and invariants

**Files:**
- Modify: `CLAUDE.md` (root), `api/CLAUDE.md`, `api/ENDPOINTS.md`, `OPERATIONS.md`, `docs/specs/2026-10-07-asaas-removal.md`, `docs/specs/2026-10-08-wallet-restoration-design.md`, `ui/CLAUDE.md` if it mentions "PIX off"

- [ ] **Step 1: Rewrite invariants in root `CLAUDE.md` and the summary in `api/CLAUDE.md`**

Replace invariant #12 with:

```markdown
12. **Custody of user money on CTech's Inter account is an explicit, accepted risk.** Deposits and withdrawals run
    on the Inter PIX rail for a closed group of KYC-approved users (KYC approved manually by the owner). Every
    deposit must be attributable to its user by payer-CPF match (masked CPF compared on the digits Inter
    reveals) or it is refunded to the payer; every payout goes only to the CPF on the user's KYC record.
    See `docs/specs/2026-10-08-wallet-restoration-design.md`.
```

Update the intro sentence "There is currently no deposit and no withdrawal rail", the Step-up paragraph (withdrawals use `RequireRecentMFA` again), the "PIX deposit range" paragraph (add the three daily limits: `daily_deposit_cap`, `daily_withdraw_cap`, `daily_withdraw_count`, defaults 100000/100000/1, admin-only), and the error-code list (`daily-deposit-limit`, `daily-withdraw-limit`). Add the daily limits to the testing table row "PIX flow".

- [ ] **Step 2: ENDPOINTS.md**

Add `POST /v1.0/wallet/deposits` and `POST /v1.0/wallet/withdrawals` with request, response, status codes (201/202, 409 `daily-*`, 403 `step-up-required`, 422 `pix-key-not-found`), scopes and the invariant map.

- [ ] **Step 3: Cross-reference the superseded spec**

Add at the top of `docs/specs/2026-10-07-asaas-removal.md`: `> **Atualizado 2026-10-08:** o rail Inter de depósito e saque foi restaurado; ver 2026-10-08-wallet-restoration-design.md.` (The restoration spec itself was already corrected: the reconcile sweep fails closed today, so no sweep change is part of this work. Add a regression test in Task 6 or 9 asserting that a paid deposit with no payer CPF stays pending and credits nothing.)

- [ ] **Step 4: Cross-project review note**

State in the PR description which components were reviewed: api, ui, cdk (no change; confirm `cdk/lib/iam-stack.ts` already grants `UpdateItem` on `wallet_users`, which the counters use), pix-gateway (no change; confirm webhook routes the `dep` txid prefix to confirm-deposit, Task 5), ctech-account (step-up `max_age` already supported).

- [ ] **Step 5: Final verification and commit**

```bash
cd api && go build ./... && make test && make test-integration
cd ../ui && npx eslint src --ext .ts,.tsx
cd .. && git add -A && git commit -m "docs: restore the Inter rail invariants and endpoint docs"
```

Suggested PR title: `feat: restore CTech Wallet Inter PIX rail with daily limits`.
