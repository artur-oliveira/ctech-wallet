# Three-Wallet Topology + Gambling Activation — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Split the gambling ring-fence out of the `real` wallet into a new `game` wallet holding real money, gated behind explicit opt-in activation, so that personal gambling limits have exactly one edge to enforce on.

**Architecture:** Three wallet types (`real`, `game`, `sandbox`). Real money reaches gambling only via `real → game`; `sandbox` is bought from `game`, never from `real`. `game`/`sandbox` do not exist until activation (KYC `verified` + gambling addendum). Non-money events (activation, addendum acceptance, later limit changes) go to a new append-only `wallet_audit` table. The whole activation surface ships behind `GAMBLING_ENABLED`, off in production until the limit engine lands.

**Tech Stack:** Go 1.x, Fiber v3, DynamoDB (aws-sdk-go-v2), Valkey (locks), fx DI, Next.js 16 + TypeScript + ShadCN, AWS CDK.

**Spec:** `docs/specs/2026-07-12-three-wallet-topology-design.md` — read it before starting.

## Global Constraints

- All amounts are **integer centavos**. Never float.
- Errors: RFC 7807 only, via `problem.*` helpers and `sendProblem(c, err)`. Never `fiber.Map` errors, never `fiber.NewError`, never raw errors.
- Every string key, attribute name, scope, entry type, and numeric code is a **named constant** — no magic values.
- Layer separation: Repository = DynamoDB only. Service = business logic, locks, PIX/KYC. Route = parse → one service call → respond.
- Every balance mutation is a conditional `TransactWriteItems`; debits carry `balance >= :amount`. Ledger is append-only.
- Every mutation is idempotent via an `IDEM#{key}` guard item co-written in the same transaction.
- **Invariant #6 stays absolute:** `sandbox` is a sink. Nothing converts sandbox back into `game` or `real`.
- **New invariant #9:** `game` balance is real money — withdrawable via `real`, never expired or written off.
- Frontend gate: `npx eslint src --ext .ts,.tsx` must pass with **zero errors and zero warnings**.
- Integration tests run with `DYNAMODB_ENDPOINT=http://localhost:8123` (start with `docker compose -f docker-compose.test.yml up -d`). Omitting the env var makes the suite silently no-op — always confirm your new test names appear in the `-v` output.
- No `Co-Authored-By` trailer on commits. Conventional Commits, no emojis.

## File Structure

| File | Responsibility |
|---|---|
| `api/internal/domain/wallet/model.go` | Add `TypeGame`, new ledger entry types, `TableAudit` |
| `api/internal/domain/wallet/user.go` | Add `CurrentGamblingAddendumVersion`, gambling fields on `User`, `GamblingAccepted()` |
| `api/internal/domain/wallet/audit.go` | **New.** `AuditEvent` model + event-type constants |
| `api/internal/repositories/wallet.go` | Generalize marker loading to N types; `EnsureRealWallet`, `EnsureGamblingWallets` |
| `api/internal/repositories/audit.go` | **New.** Append-only audit writer |
| `api/internal/repositories/user.go` | Add `AcceptGamblingAddendum` |
| `api/internal/services/wallet.go` | `GetBalances` 3-way; `ActivateGambling`, `FundGame`, `ReturnFromGame`; `PurchaseSandbox` source change |
| `api/internal/api/v1/dto.go` | `GameTransferRequest`, `ActivateGamblingRequest` |
| `api/internal/api/v1/wallet.go` | New handlers |
| `api/internal/api/v1/router.go` | New routes, flag-gated |
| `api/internal/config/config.go` | `GAMBLING_ENABLED` |
| `api/internal/problem/problem.go` | `gambling-not-activated`, `gambling-terms-required` |
| `cdk/lib/*` | `wallet_audit` table |
| `ui/src/lib/types/api.ts` | `WalletType` gains `game`; `Balances` game/sandbox optional |
| `ui/src/app/dashboard/page.tsx` | Real-only until activated |

---

### Task 1: Domain constants — wallet type, entry types, gambling addendum

**Files:**
- Modify: `api/internal/domain/wallet/model.go`
- Modify: `api/internal/domain/wallet/user.go`
- Test: `api/internal/domain/wallet/user_test.go` (create)

**Interfaces:**
- Produces: `wallet.TypeGame`, `wallet.EntryGameFundDebit`, `wallet.EntryGameFundCredit`, `wallet.EntryGameReturnDebit`, `wallet.EntryGameReturnCredit`, `wallet.TableAudit`, `wallet.CurrentGamblingAddendumVersion`, `User.GamblingAddendumVersion`, `User.GamblingActivatedAt`, `(*User).GamblingAccepted() bool`

- [ ] **Step 1: Write the failing test**

Create `api/internal/domain/wallet/user_test.go`:

```go
package wallet

import "testing"

func TestGamblingAcceptedRequiresCurrentVersion(t *testing.T) {
	cases := []struct {
		name string
		u    *User
		want bool
	}{
		{"nil user", nil, false},
		{"never accepted", &User{}, false},
		{"stale version", &User{GamblingAddendumVersion: "0.9"}, false},
		{"current version", &User{GamblingAddendumVersion: CurrentGamblingAddendumVersion}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.u.GamblingAccepted(); got != tc.want {
				t.Errorf("GamblingAccepted() = %v, want %v", got, tc.want)
			}
		})
	}
}

// The gambling addendum is a document distinct from the wallet terms addendum;
// accepting one must never imply the other.
func TestGamblingAddendumIsIndependentOfTermsAddendum(t *testing.T) {
	u := &User{TermsAddendumVersion: CurrentTermsAddendumVersion}
	if u.GamblingAccepted() {
		t.Error("accepting the terms addendum must not grant gambling acceptance")
	}
	g := &User{GamblingAddendumVersion: CurrentGamblingAddendumVersion}
	if g.TermsAccepted() {
		t.Error("accepting the gambling addendum must not grant terms acceptance")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd api && go test ./internal/domain/wallet/ -run Gambling -v`
Expected: FAIL — build error, `CurrentGamblingAddendumVersion` and `GamblingAccepted` undefined.

- [ ] **Step 3: Add the constants**

In `api/internal/domain/wallet/model.go`, change the wallet-type block:

```go
// Wallet balance types. `game` holds REAL money earmarked for games: it is
// withdrawable (via `real`) and counts toward the user's real holdings. It
// exists so personal gambling limits have exactly one edge to meter —
// `real → game`. `sandbox` is virtual and remains a sink (Invariant #6).
const (
	TypeReal    = "real"
	TypeGame    = "game"
	TypeSandbox = "sandbox"
)
```

Add to the ledger entry type block (keep the existing entries unchanged):

```go
	// Ring-fence transfers between `real` and `game`. Funding is metered by the
	// personal limit engine; returning is always free and never limited.
	EntryGameFundDebit    = "game_fund_debit"    // debit real
	EntryGameFundCredit   = "game_fund_credit"   // credit game
	EntryGameReturnDebit  = "game_return_debit"  // debit game
	EntryGameReturnCredit = "game_return_credit" // credit real
```

Add to the table-name block:

```go
	TableAudit = "wallet_audit"
```

- [ ] **Step 4: Add the gambling addendum to the User model**

In `api/internal/domain/wallet/user.go`, add below `CurrentTermsAddendumVersion`:

```go
// CurrentGamblingAddendumVersion is the responsible-gambling addendum version
// (see docs/legal/wallet-gambling-addendum.md). It is a SEPARATE document from
// the wallet terms addendum: a user who accepted one has not accepted the other.
//
// Bumping it re-gates gambling for every user on their next call. A re-gated
// user keeps their game/sandbox balances and may still RETURN money to `real` —
// only funding and play are blocked. Money is never trapped by a terms change.
const CurrentGamblingAddendumVersion = "1.0"
```

Add the fields to `User`:

```go
type User struct {
	UserID                  string `dynamodbav:"pk" json:"user_id"`
	TermsAddendumVersion    string `dynamodbav:"terms_addendum_version,omitempty" json:"-"`
	TermsAcceptedAt         string `dynamodbav:"terms_accepted_at,omitempty" json:"-"`
	GamblingAddendumVersion string `dynamodbav:"gambling_addendum_version,omitempty" json:"-"`
	GamblingActivatedAt     string `dynamodbav:"gambling_activated_at,omitempty" json:"-"`
	CreatedAt               string `dynamodbav:"created_at,omitempty" json:"-"`
	UpdatedAt               string `dynamodbav:"updated_at,omitempty" json:"-"`
}
```

Add the accessor:

```go
// GamblingAccepted reports whether the user accepted the CURRENT gambling
// addendum version. Like TermsAccepted, this is a computed equality — never a
// stored boolean — so bumping the constant re-gates everyone at once.
func (u *User) GamblingAccepted() bool {
	return u != nil && u.GamblingAddendumVersion == CurrentGamblingAddendumVersion
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd api && go test ./internal/domain/wallet/ -v`
Expected: PASS — all tests, including the existing fee and deposit-limit tests.

- [ ] **Step 6: Commit**

```bash
git add api/internal/domain/wallet/model.go api/internal/domain/wallet/user.go api/internal/domain/wallet/user_test.go
git commit -m "feat(api): add game wallet type, ring-fence entry types, gambling addendum"
```

---

### Task 2: Audit model + append-only repository

**Files:**
- Create: `api/internal/domain/wallet/audit.go`
- Create: `api/internal/repositories/audit.go`
- Test: `api/tests/integration/audit_test.go` (create)

**Interfaces:**
- Consumes: `wallet.TableAudit` (Task 1)
- Produces: `wallet.AuditEvent`, `wallet.EventGamblingActivated`, `wallet.EventGamblingAddendumAccepted`, `repositories.NewAuditRepository(db, cfg) *AuditRepository`, `(*AuditRepository).Append(ctx, e *wallet.AuditEvent) error`, `(*AuditRepository).List(ctx, userID string, limit int) ([]wallet.AuditEvent, error)`

This table is what makes "every action is auditable" true for actions that move no money — the ledger deliberately only carries money. The limit engine (next plan) appends every limit change here.

- [ ] **Step 1: Write the failing integration test**

Create `api/tests/integration/audit_test.go`:

```go
//go:build integration

package integration

import (
	"context"
	"testing"

	"gopkg.aoctech.app/wallet/api/internal/domain/id"
	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
)

func TestAuditAppendIsAppendOnly(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := "u-" + id.New()

	first := &wallet.AuditEvent{
		UserID: user, EventType: wallet.EventGamblingActivated,
		Actor: user, IP: "203.0.113.7", UserAgent: "test-agent",
	}
	if err := h.audit.Append(ctx, first); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if first.EventID == "" || first.CreatedAt == "" {
		t.Fatal("Append must stamp EventID and CreatedAt")
	}

	second := &wallet.AuditEvent{
		UserID: user, EventType: wallet.EventGamblingAddendumAccepted, Actor: user,
	}
	if err := h.audit.Append(ctx, second); err != nil {
		t.Fatalf("Append second: %v", err)
	}

	events, err := h.audit.List(ctx, user, 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("len(events) = %d, want 2", len(events))
	}

	// Re-appending an event with the SAME EventID must not overwrite the first —
	// the table is append-only, so the write is rejected outright.
	dup := &wallet.AuditEvent{
		UserID: user, EventType: "tampered", Actor: "attacker",
		EventID: first.EventID, CreatedAt: first.CreatedAt,
	}
	if err := h.audit.Append(ctx, dup); err == nil {
		t.Fatal("Append with an existing EventID must fail — the audit log is append-only")
	}

	events, err = h.audit.List(ctx, user, 10)
	if err != nil {
		t.Fatalf("List after dup: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("len(events) after rejected dup = %d, want 2", len(events))
	}
	for _, e := range events {
		if e.EventType == "tampered" || e.Actor == "attacker" {
			t.Fatal("an existing audit row was mutated")
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd api && DYNAMODB_ENDPOINT=http://localhost:8123 go test -tags integration -count=1 ./tests/integration/ -run TestAudit -v`
Expected: FAIL — build error, `wallet.AuditEvent` and `h.audit` undefined.

- [ ] **Step 3: Create the audit domain model**

Create `api/internal/domain/wallet/audit.go`:

```go
package wallet

// Audit event types. The ledger records money; this records everything else that
// must be provable after the fact — consent, activation, and (next plan) every
// change to a personal gambling limit.
const (
	EventGamblingActivated        = "gambling_activated"
	EventGamblingAddendumAccepted = "gambling_addendum_accepted"
	EventTermsAddendumAccepted    = "terms_addendum_accepted"
)

// AuditEvent is an immutable record of a non-money action. Like LedgerEntry it is
// append-only: never updated, never deleted. Before/After carry the change for
// events that mutate settings (empty for events that do not).
type AuditEvent struct {
	UserID    string `dynamodbav:"pk" json:"user_id"`
	SK        string `dynamodbav:"sk" json:"-"`
	EventID   string `dynamodbav:"event_id" json:"event_id"`
	EventType string `dynamodbav:"event_type" json:"event_type"`
	Actor     string `dynamodbav:"actor" json:"actor"`
	Before    string `dynamodbav:"before,omitempty" json:"before,omitempty"`
	After     string `dynamodbav:"after,omitempty" json:"after,omitempty"`
	IP        string `dynamodbav:"ip,omitempty" json:"ip,omitempty"`
	UserAgent string `dynamodbav:"user_agent,omitempty" json:"user_agent,omitempty"`
	CreatedAt string `dynamodbav:"created_at" json:"created_at"`
}
```

- [ ] **Step 4: Create the audit repository**

Read `api/internal/repositories/wallet.go` first and mirror its table-helper construction and `Encode`/`Decode` usage exactly — do not invent a new persistence style.

Create `api/internal/repositories/audit.go`:

```go
package repositories

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"

	"gopkg.aoctech.app/wallet/api/internal/config"
	"gopkg.aoctech.app/wallet/api/internal/domain/id"
	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
)

// AuditRepository is the append-only store for non-money events. It exposes no
// update and no delete — that absence is the guarantee.
type AuditRepository struct {
	audit *Table
}

func NewAuditRepository(db *dynamodb.Client, cfg *config.Config) *AuditRepository {
	return &AuditRepository{audit: NewTable(db, cfg, wallet.TableAudit)}
}

// Append writes a new audit row. It stamps EventID/CreatedAt when unset and uses
// an attribute_not_exists condition so an existing row can never be overwritten.
func (r *AuditRepository) Append(ctx context.Context, e *wallet.AuditEvent) error {
	if e.EventID == "" {
		e.EventID = id.New()
	}
	if e.CreatedAt == "" {
		e.CreatedAt = NowStr()
	}
	e.SK = e.CreatedAt + "#" + e.EventID
	av, err := Encode(*e)
	if err != nil {
		return err
	}
	return r.audit.PutItemIfAbsent(ctx, av)
}

// List returns a user's audit trail, newest first.
func (r *AuditRepository) List(ctx context.Context, userID string, limit int) ([]wallet.AuditEvent, error) {
	res, err := r.audit.QueryByPK(ctx, userID, limit, nil)
	if err != nil {
		return nil, err
	}
	out := make([]wallet.AuditEvent, 0, len(res.Items))
	for _, item := range res.Items {
		e, err := Decode[wallet.AuditEvent](item)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, nil
}
```

**Note for the implementer:** `Table`, `NewTable`, `PutItemIfAbsent`, and `QueryByPK` are the names used above — the existing helper in `repositories/` may name these differently (e.g. `BuildPutTxItemIfAbsent`, `Statement`'s query path). Open `api/internal/repositories/wallet.go` and `api/internal/repositories/table.go`, use whatever the real helper names are, and add a plain `PutItemIfAbsent` + `QueryByPK` to the shared table helper only if no equivalent exists. Do not duplicate an existing helper.

- [ ] **Step 5: Wire the audit repo into the integration harness and fx**

In `api/tests/integration/` find `newHarness` (likely `main_test.go` or `harness_test.go`), add an `audit *repositories.AuditRepository` field, and construct it with `repositories.NewAuditRepository(db, cfg)` alongside the existing repo. Add the `wallet_audit` table to whatever the harness uses to create tables in DynamoDB-local (PK `pk` string, SK `sk` string).

In `api/internal/app/app.go`, add `repositories.NewAuditRepository` to the `fx.Provide` list next to `repositories.NewWalletRepository`.

- [ ] **Step 6: Run the test to verify it passes**

Run: `cd api && DYNAMODB_ENDPOINT=http://localhost:8123 go test -tags integration -count=1 ./tests/integration/ -run TestAudit -v`
Expected: PASS — and `=== RUN TestAuditAppendIsAppendOnly` must appear in the output. If no RUN line appears, the env var is missing and the suite no-op'd.

- [ ] **Step 7: Commit**

```bash
git add api/internal/domain/wallet/audit.go api/internal/repositories/audit.go api/internal/app/app.go api/tests/integration/
git commit -m "feat(api): add append-only wallet_audit table for non-money events"
```

---

### Task 3: Repository — real-only creation + atomic gambling-wallet creation

**Files:**
- Modify: `api/internal/repositories/wallet.go:104-167` (`EnsureWallets`, `loadByMarkers`)
- Modify: `api/internal/services/wallet.go` (the `Repo` interface + every `EnsureWallets` caller)
- Test: `api/tests/integration/wallet_test.go`

**Interfaces:**
- Consumes: `wallet.TypeGame` (Task 1)
- Produces:
  - `(*WalletRepository).EnsureRealWallet(ctx, userID string) (*wallet.Wallet, error)`
  - `(*WalletRepository).EnsureGamblingWallets(ctx, userID string) (game, sandbox *wallet.Wallet, err error)`
  - `(*WalletRepository).LoadWallets(ctx, userID string) (real, game, sandbox *wallet.Wallet, err error)` — `game`/`sandbox` are `nil` when not activated

This is the breaking change at the centre of the plan: `EnsureWallets` currently creates `real` **and** `sandbox` together. After this task, first access creates `real` only.

- [ ] **Step 1: Write the failing integration test**

Add to `api/tests/integration/wallet_test.go`:

```go
func TestEnsureRealWalletDoesNotCreateGamblingWallets(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := "u-" + id.New()

	real, err := h.repo.EnsureRealWallet(ctx, user)
	if err != nil {
		t.Fatalf("EnsureRealWallet: %v", err)
	}
	if real == nil || real.Type != wallet.TypeReal {
		t.Fatalf("real wallet = %+v, want type %q", real, wallet.TypeReal)
	}

	// A user who has not activated has NO game and NO sandbox wallet.
	gotReal, game, sandbox, err := h.repo.LoadWallets(ctx, user)
	if err != nil {
		t.Fatalf("LoadWallets: %v", err)
	}
	if gotReal == nil || gotReal.WalletID != real.WalletID {
		t.Fatalf("LoadWallets real = %+v, want %s", gotReal, real.WalletID)
	}
	if game != nil || sandbox != nil {
		t.Fatalf("before activation game=%v sandbox=%v, want both nil", game, sandbox)
	}
}

func TestEnsureGamblingWalletsIsAtomicAndIdempotent(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := "u-" + id.New()

	if _, err := h.repo.EnsureRealWallet(ctx, user); err != nil {
		t.Fatalf("EnsureRealWallet: %v", err)
	}

	game, sandbox, err := h.repo.EnsureGamblingWallets(ctx, user)
	if err != nil {
		t.Fatalf("EnsureGamblingWallets: %v", err)
	}
	if game.Type != wallet.TypeGame || sandbox.Type != wallet.TypeSandbox {
		t.Fatalf("types = %q/%q, want game/sandbox", game.Type, sandbox.Type)
	}
	if game.Balance != 0 || sandbox.Balance != 0 {
		t.Fatalf("new wallets must start at zero, got %d/%d", game.Balance, sandbox.Balance)
	}

	// Idempotent: a second call converges on the SAME wallets, never a second pair.
	game2, sandbox2, err := h.repo.EnsureGamblingWallets(ctx, user)
	if err != nil {
		t.Fatalf("EnsureGamblingWallets replay: %v", err)
	}
	if game2.WalletID != game.WalletID || sandbox2.WalletID != sandbox.WalletID {
		t.Fatalf("replay created new wallets: %s/%s vs %s/%s",
			game2.WalletID, sandbox2.WalletID, game.WalletID, sandbox.WalletID)
	}

	_, loadedGame, loadedSandbox, err := h.repo.LoadWallets(ctx, user)
	if err != nil {
		t.Fatalf("LoadWallets: %v", err)
	}
	if loadedGame == nil || loadedSandbox == nil {
		t.Fatal("after activation both game and sandbox must load")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd api && DYNAMODB_ENDPOINT=http://localhost:8123 go test -tags integration -count=1 ./tests/integration/ -run 'TestEnsure' -v`
Expected: FAIL — build error, `EnsureRealWallet` / `EnsureGamblingWallets` / `LoadWallets` undefined.

- [ ] **Step 3: Replace `EnsureWallets` and `loadByMarkers`**

In `api/internal/repositories/wallet.go`, delete `EnsureWallets` and `loadByMarkers` (lines 104–167) and replace with:

```go
// LoadWallets returns the user's wallets by type. game and sandbox are nil until
// the user activates gambling — their absence IS the "not activated" signal.
func (r *WalletRepository) LoadWallets(ctx context.Context, userID string) (real, game, sandbox *wallet.Wallet, err error) {
	byType, err := r.loadByMarkers(ctx, userID, wallet.TypeReal, wallet.TypeGame, wallet.TypeSandbox)
	if err != nil {
		return nil, nil, nil, err
	}
	return byType[wallet.TypeReal], byType[wallet.TypeGame], byType[wallet.TypeSandbox], nil
}

// EnsureRealWallet returns the user's real wallet, creating it on first access.
// It does NOT create game or sandbox: those exist only after explicit activation
// (see the three-wallet topology spec). Concurrent callers converge via the
// (user_id, type) marker guard.
func (r *WalletRepository) EnsureRealWallet(ctx context.Context, userID string) (*wallet.Wallet, error) {
	byType, err := r.loadByMarkers(ctx, userID, wallet.TypeReal)
	if err != nil {
		return nil, err
	}
	if w := byType[wallet.TypeReal]; w != nil {
		return w, nil
	}
	created, err := r.createWallets(ctx, userID, wallet.TypeReal)
	if err != nil {
		return nil, err
	}
	return created[wallet.TypeReal], nil
}

// EnsureGamblingWallets creates the game and sandbox wallets together, atomically,
// and is idempotent: a second call converges on the wallets the first created.
// Callers MUST gate on KYC and gambling-addendum acceptance before calling this —
// the repository enforces persistence, not policy.
func (r *WalletRepository) EnsureGamblingWallets(ctx context.Context, userID string) (game, sandbox *wallet.Wallet, err error) {
	byType, err := r.loadByMarkers(ctx, userID, wallet.TypeGame, wallet.TypeSandbox)
	if err != nil {
		return nil, nil, err
	}
	if byType[wallet.TypeGame] != nil && byType[wallet.TypeSandbox] != nil {
		return byType[wallet.TypeGame], byType[wallet.TypeSandbox], nil
	}
	created, err := r.createWallets(ctx, userID, wallet.TypeGame, wallet.TypeSandbox)
	if err != nil {
		return nil, nil, err
	}
	return created[wallet.TypeGame], created[wallet.TypeSandbox], nil
}

// createWallets writes the given wallet types (plus their markers) in ONE
// transaction, so a partial set can never exist. On a lost race it re-reads what
// the winner created.
func (r *WalletRepository) createWallets(ctx context.Context, userID string, types ...string) (map[string]*wallet.Wallet, error) {
	now := NowStr()
	out := make(map[string]*wallet.Wallet, len(types))
	items := make([]types_.TransactWriteItem, 0, len(types)*2)
	for _, typ := range types {
		w := wallet.Wallet{
			WalletID: id.New(), UserID: userID, Type: typ,
			Balance: 0, Version: 0, CreatedAt: now, UpdatedAt: now,
		}
		wav, err := Encode(w)
		if err != nil {
			return nil, err
		}
		mav, err := Encode(walletMarker{PK: markerPK(userID, typ), WalletID: w.WalletID, Type: typ, CreatedAt: now})
		if err != nil {
			return nil, err
		}
		items = append(items, r.wallets.BuildPutTxItemIfAbsent(wav), r.wallets.BuildPutTxItemIfAbsent(mav))
		out[typ] = &w
	}

	if err := r.wallets.TransactWrite(ctx, items); err != nil {
		if IsConditionFailed(err) {
			// A concurrent caller won the race — read what it created.
			return r.loadByMarkers(ctx, userID, types...)
		}
		return nil, err
	}
	return out, nil
}

// loadByMarkers resolves the requested wallet types via their (user_id, type)
// markers. A type with no marker is simply absent from the map.
func (r *WalletRepository) loadByMarkers(ctx context.Context, userID string, walletTypes ...string) (map[string]*wallet.Wallet, error) {
	out := make(map[string]*wallet.Wallet, len(walletTypes))
	for _, typ := range walletTypes {
		mItem, err := r.wallets.GetItem(ctx, markerPK(userID, typ))
		if err != nil {
			return nil, err
		}
		if mItem == nil {
			continue
		}
		m, err := Decode[walletMarker](mItem)
		if err != nil {
			return nil, err
		}
		w, err := r.GetWallet(ctx, m.WalletID)
		if err != nil {
			return nil, err
		}
		out[typ] = w
	}
	return out, nil
}
```

**Import note:** the file already imports the DynamoDB `types` package. The `types_` alias above is a placeholder to avoid shadowing the `types ...string` parameter — rename the *parameter* to `walletTypes` instead and keep the existing `types` import name. Fix this while implementing; do not leave `types_` in the code.

- [ ] **Step 4: Update the `Repo` interface and every caller**

In `api/internal/services/wallet.go`, replace the `EnsureWallets` line in the `Repo` interface:

```go
	EnsureRealWallet(ctx context.Context, userID string) (*wallet.Wallet, error)
	EnsureGamblingWallets(ctx context.Context, userID string) (game, sandbox *wallet.Wallet, err error)
	LoadWallets(ctx context.Context, userID string) (real, game, sandbox *wallet.Wallet, err error)
```

Update callers as follows (later tasks refine them further — the goal here is only to compile and keep behaviour):

- `GetBalances` → Task 4 rewrites it. For now: `real, game, sandbox, err := s.repo.LoadWallets(ctx, userID)` after `s.repo.EnsureRealWallet(ctx, userID)`.
- `InitiateDeposit` → `realw, err := s.repo.EnsureRealWallet(ctx, userID)`.
- `Withdraw` → `realw, err := s.repo.EnsureRealWallet(ctx, userID)`.
- `PurchaseSandbox` and `sandboxOp` → Task 6 rewrites them. For now, make them call `LoadWallets` and return `problem.GamblingNotActivated()` (added in Task 4) when `game`/`sandbox` is nil.

Run `cd api && rg -n "EnsureWallets" --no-heading` and fix every hit, including `api/tests/integration/` helpers (the `fund` helper calls it).

- [ ] **Step 5: Run the full suite**

Run: `cd api && go build ./... && go test ./... && DYNAMODB_ENDPOINT=http://localhost:8123 go test -tags integration -count=1 ./tests/integration/ -v 2>&1 | grep -E "^(--- |ok|FAIL)"`
Expected: build OK; all unit tests PASS; all integration tests PASS, including `TestEnsureRealWalletDoesNotCreateGamblingWallets` and `TestEnsureGamblingWalletsIsAtomicAndIdempotent`.

- [ ] **Step 6: Commit**

```bash
git add api/internal/repositories/wallet.go api/internal/services/wallet.go api/tests/integration/
git commit -m "feat(api): create real wallet on first access, gambling wallets on activation"
```

---

### Task 4: Service — activation, problems, and 3-way balances

**Files:**
- Modify: `api/internal/problem/problem.go`
- Modify: `api/internal/repositories/user.go`
- Modify: `api/internal/services/wallet.go`
- Test: `api/tests/integration/gambling_test.go` (create)

**Interfaces:**
- Consumes: `EnsureGamblingWallets`, `LoadWallets` (Task 3); `AuditRepository.Append` (Task 2); `User.GamblingAccepted()` (Task 1)
- Produces:
  - `problem.GamblingNotActivated() *Problem` (409, `/problems/gambling-not-activated`)
  - `problem.GamblingTermsRequired() *Problem` (403, `/problems/gambling-terms-required`)
  - `(*WalletService).ActivateGambling(ctx, userID, kycLevel, ip, userAgent string) (game, sandbox *wallet.Wallet, err error)`
  - `(*WalletService).GetBalances(ctx, userID string) (real, game, sandbox *wallet.Wallet, err error)`
  - `(*UserRepository).AcceptGamblingAddendum(ctx, userID string) error`

- [ ] **Step 1: Write the failing integration test**

Create `api/tests/integration/gambling_test.go`:

```go
//go:build integration

package integration

import (
	"context"
	"errors"
	"testing"

	"gopkg.aoctech.app/wallet/api/internal/domain/id"
	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
	"gopkg.aoctech.app/wallet/api/internal/kycclient"
	"gopkg.aoctech.app/wallet/api/internal/problem"
)

// Activation requires KYC `verified` — real money is about to enter a gambling
// ring-fence, so `basic` is not enough.
func TestActivateGamblingRequiresVerifiedKYC(t *testing.T) {
	ctx := context.Background()
	h := newHarness(&kycclient.KYC{Level: "basic", CPF: cpf})
	user := "u-" + id.New()
	acceptGambling(t, h, user)

	_, _, err := h.svc.ActivateGambling(ctx, user, "basic", "", "")
	var p *problem.Problem
	if !errors.As(err, &p) || p.Type != problem.TypeKYCNotVerified {
		t.Fatalf("ActivateGambling with basic KYC = %v, want kyc-not-verified", err)
	}

	_, game, sandbox, err := h.repo.LoadWallets(ctx, user)
	if err != nil {
		t.Fatalf("LoadWallets: %v", err)
	}
	if game != nil || sandbox != nil {
		t.Fatal("a rejected activation must not create wallets")
	}
}

// Activation requires the gambling addendum — a separate document from the
// wallet terms addendum.
func TestActivateGamblingRequiresAddendum(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := "u-" + id.New()

	_, _, err := h.svc.ActivateGambling(ctx, user, "verified", "", "")
	var p *problem.Problem
	if !errors.As(err, &p) || p.Type != problem.TypeGamblingTermsRequired {
		t.Fatalf("ActivateGambling without addendum = %v, want gambling-terms-required", err)
	}
}

func TestActivateGamblingHappyPathIsIdempotentAndAudited(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := "u-" + id.New()
	acceptGambling(t, h, user)

	game, sandbox, err := h.svc.ActivateGambling(ctx, user, "verified", "203.0.113.7", "test-agent")
	if err != nil {
		t.Fatalf("ActivateGambling: %v", err)
	}
	if game.Type != wallet.TypeGame || sandbox.Type != wallet.TypeSandbox {
		t.Fatalf("types = %q/%q", game.Type, sandbox.Type)
	}

	// Idempotent: activating twice converges on the same wallets.
	game2, _, err := h.svc.ActivateGambling(ctx, user, "verified", "", "")
	if err != nil {
		t.Fatalf("ActivateGambling replay: %v", err)
	}
	if game2.WalletID != game.WalletID {
		t.Fatalf("replay created a new game wallet: %s vs %s", game2.WalletID, game.WalletID)
	}

	events, err := h.audit.List(ctx, user, 10)
	if err != nil {
		t.Fatalf("audit List: %v", err)
	}
	var found bool
	for _, e := range events {
		if e.EventType == wallet.EventGamblingActivated {
			found = true
			if e.IP != "203.0.113.7" || e.UserAgent != "test-agent" {
				t.Errorf("audit row lost request context: ip=%q ua=%q", e.IP, e.UserAgent)
			}
		}
	}
	if !found {
		t.Fatal("activation must write a gambling_activated audit event")
	}
}

// Before activation the balances response carries the real wallet only.
func TestGetBalancesHidesGamblingWalletsUntilActivated(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := "u-" + id.New()

	real, game, sandbox, err := h.svc.GetBalances(ctx, user)
	if err != nil {
		t.Fatalf("GetBalances: %v", err)
	}
	if real == nil {
		t.Fatal("real wallet must always exist")
	}
	if game != nil || sandbox != nil {
		t.Fatal("game/sandbox must be nil before activation")
	}

	acceptGambling(t, h, user)
	if _, _, err := h.svc.ActivateGambling(ctx, user, "verified", "", ""); err != nil {
		t.Fatalf("ActivateGambling: %v", err)
	}

	real, game, sandbox, err = h.svc.GetBalances(ctx, user)
	if err != nil {
		t.Fatalf("GetBalances after activation: %v", err)
	}
	if real == nil || game == nil || sandbox == nil {
		t.Fatal("after activation all three wallets must be present")
	}
}
```

Add this helper to `api/tests/integration/wallet_test.go` next to `verified()`:

```go
// acceptGambling records the user's acceptance of the current gambling addendum,
// which activation requires.
func acceptGambling(t *testing.T, h *harness, userID string) {
	t.Helper()
	if err := h.userRepo.AcceptGamblingAddendum(context.Background(), userID); err != nil {
		t.Fatalf("AcceptGamblingAddendum: %v", err)
	}
}
```

Add a `userRepo *repositories.UserRepository` field to the harness if it does not already have one.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd api && DYNAMODB_ENDPOINT=http://localhost:8123 go test -tags integration -count=1 ./tests/integration/ -run TestActivate -v`
Expected: FAIL — `ActivateGambling` and `problem.TypeGamblingTermsRequired` undefined.

- [ ] **Step 3: Add the problem types**

In `api/internal/problem/problem.go`, add to the wallet-specific type block:

```go
	TypeGamblingNotActivated = "/problems/gambling-not-activated"
	TypeGamblingTermsRequired = "/problems/gambling-terms-required"
```

Add the constructors next to the other wallet-specific ones:

```go
// GamblingNotActivated: the caller has no game/sandbox wallet because they never
// opted in. Returned by every route inside the gambling ring-fence.
func GamblingNotActivated() *Problem {
	return New(http.StatusConflict, TypeGamblingNotActivated, "Gambling Not Activated",
		"ative a carteira de jogos antes de usar esta operação")
}

// GamblingTermsRequired: the caller has not accepted the CURRENT gambling
// addendum. A re-gated user may still return money to the real wallet.
func GamblingTermsRequired() *Problem {
	return New(http.StatusForbidden, TypeGamblingTermsRequired, "Gambling Terms Required",
		"aceite o termo de jogo responsável para continuar")
}
```

- [ ] **Step 4: Add `AcceptGamblingAddendum` — and fix the clobber hazard in `AcceptTerms`**

⚠️ **Read this before writing code.** The existing `AcceptTerms` (`api/internal/repositories/user.go:34`) uses **`PutItem`**, which overwrites the whole row. That was harmless when `terms_addendum_version` was the only meaningful field. It is **not** harmless now: with gambling fields on the same row, a `PutItem`-style accept would silently wipe the other document's acceptance. Accepting the gambling addendum would erase the terms acceptance, or vice versa.

So: write `AcceptGamblingAddendum` as a **partial update**, and convert `AcceptTerms` to one as well. `Base.UpdateItem(ctx, pk, sk, updates)` (`base.go:130`) does a partial `SET` and upserts the row if absent — exactly what both need.

Add a test for this first, in `api/tests/integration/gambling_test.go`:

```go
// Two independent consent documents live on one row. Accepting either must never
// erase the other — a PutItem-style write here would silently revoke consent.
func TestAcceptingOneAddendumDoesNotEraseTheOther(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := "u-" + id.New()

	if err := h.userRepo.AcceptTerms(ctx, user); err != nil {
		t.Fatalf("AcceptTerms: %v", err)
	}
	if err := h.userRepo.AcceptGamblingAddendum(ctx, user); err != nil {
		t.Fatalf("AcceptGamblingAddendum: %v", err)
	}

	u, err := h.userRepo.Get(ctx, user)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !u.TermsAccepted() {
		t.Error("accepting the gambling addendum erased the terms acceptance")
	}
	if !u.GamblingAccepted() {
		t.Error("gambling acceptance was not recorded")
	}

	// And in the other order.
	user2 := "u-" + id.New()
	if err := h.userRepo.AcceptGamblingAddendum(ctx, user2); err != nil {
		t.Fatalf("AcceptGamblingAddendum: %v", err)
	}
	if err := h.userRepo.AcceptTerms(ctx, user2); err != nil {
		t.Fatalf("AcceptTerms: %v", err)
	}
	u2, err := h.userRepo.Get(ctx, user2)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !u2.TermsAccepted() || !u2.GamblingAccepted() {
		t.Error("accepting the terms addendum erased the gambling acceptance")
	}
}
```

Run it — it fails on the first ordering, because `AcceptTerms`'s `PutItem` wipes the gambling fields.

Then rewrite both in `api/internal/repositories/user.go`:

```go
// AcceptTerms stamps the current terms addendum version and the acceptance
// timestamp. A PARTIAL update, not a Put: the row also carries the gambling
// addendum acceptance, and overwriting it wholesale would silently revoke that.
func (r *UserRepository) AcceptTerms(ctx context.Context, userID string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := r.users.UpdateItem(ctx, userID, nil, map[string]any{
		"terms_addendum_version": wallet.CurrentTermsAddendumVersion,
		"terms_accepted_at":      now,
		"updated_at":             now,
	})
	return err
}

// AcceptGamblingAddendum stamps the current gambling addendum version and the
// acceptance timestamp. A separate document from the terms addendum: accepting
// one never implies the other. Partial update, for the same reason as above.
func (r *UserRepository) AcceptGamblingAddendum(ctx context.Context, userID string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := r.users.UpdateItem(ctx, userID, nil, map[string]any{
		"gambling_addendum_version": wallet.CurrentGamblingAddendumVersion,
		"gambling_activated_at":     now,
		"updated_at":                now,
	})
	return err
}
```

Check `Base.UpdateItem`'s exact signature and upsert behaviour at `base.go:130` before relying on it — if it does not create the row when absent, add the `created_at` attribute to the update map and confirm with the test above. Re-run the existing `api/internal/services/user_test.go` to confirm the terms flow still passes.

- [ ] **Step 5: Implement `ActivateGambling` and rewrite `GetBalances`**

Add an `audit` dependency to `WalletService`. Update the struct, `NewWalletService`, and the fx wiring in `app.go`:

```go
// Auditor is the append-only audit surface (dependency inversion, as with Repo).
type Auditor interface {
	Append(ctx context.Context, e *wallet.AuditEvent) error
}

// UserRepo is the gambling-consent surface the service reads before activation.
type UserRepo interface {
	Get(ctx context.Context, userID string) (*wallet.User, error)
}

type WalletService struct {
	repo  Repo
	users UserRepo
	audit Auditor
	lock  Locker
	pix   pix.PixClient
	kyc   KYCClient
}

func NewWalletService(repo Repo, users UserRepo, audit Auditor, lock Locker, pixClient pix.PixClient, kyc KYCClient) *WalletService {
	return &WalletService{repo: repo, users: users, audit: audit, lock: lock, pix: pixClient, kyc: kyc}
}
```

(If `UserRepository` has no `Get`, use whatever method it exposes to read the row — check `api/internal/repositories/user.go` and `api/internal/services/user.go` first and reuse it rather than adding a second reader.)

Then:

```go
// GetBalances returns the caller's wallets. The real wallet is created on first
// access; game and sandbox are nil until the user activates gambling, and their
// absence is what the frontend reads to decide whether to show a gambling surface.
func (s *WalletService) GetBalances(ctx context.Context, userID string) (real, game, sandbox *wallet.Wallet, err error) {
	if _, err := s.repo.EnsureRealWallet(ctx, userID); err != nil {
		return nil, nil, nil, err
	}
	return s.repo.LoadWallets(ctx, userID)
}

// ActivateGambling opens the game + sandbox wallets after checking consent. Gates:
// KYC `verified` (real money is entering a gambling ring-fence) and acceptance of
// the CURRENT gambling addendum. Idempotent — activating twice returns the same
// wallets. Writes an audit event; activation must be provable after the fact.
func (s *WalletService) ActivateGambling(ctx context.Context, userID, kycLevel, ip, userAgent string) (game, sandbox *wallet.Wallet, err error) {
	if kycLevel != middleware.KYCVerified {
		return nil, nil, problem.KYCNotVerified()
	}
	u, err := s.users.Get(ctx, userID)
	if err != nil {
		return nil, nil, err
	}
	if !u.GamblingAccepted() {
		return nil, nil, problem.GamblingTermsRequired()
	}
	if _, err := s.repo.EnsureRealWallet(ctx, userID); err != nil {
		return nil, nil, err
	}

	game, sandbox, err = s.repo.EnsureGamblingWallets(ctx, userID)
	if err != nil {
		return nil, nil, err
	}
	if err := s.audit.Append(ctx, &wallet.AuditEvent{
		UserID:    userID,
		EventType: wallet.EventGamblingActivated,
		Actor:     userID,
		After:     wallet.CurrentGamblingAddendumVersion,
		IP:        ip,
		UserAgent: userAgent,
	}); err != nil {
		return nil, nil, err
	}
	return game, sandbox, nil
}
```

**Import note:** `middleware.KYCVerified` may create an import cycle (`services` → `middleware`). If it does, move the KYC-level constants into `domain/wallet` and have `middleware` consume them from there — do not duplicate the string.

- [ ] **Step 6: Run the tests**

Run: `cd api && go build ./... && DYNAMODB_ENDPOINT=http://localhost:8123 go test -tags integration -count=1 ./tests/integration/ -run 'TestActivate|TestGetBalances' -v`
Expected: PASS — all four new tests appear as `--- PASS` lines.

- [ ] **Step 7: Commit**

```bash
git add api/internal/problem/problem.go api/internal/repositories/user.go api/internal/services/wallet.go api/internal/app/app.go api/tests/integration/
git commit -m "feat(api): gambling activation gated on verified KYC and addendum"
```

---

### Task 5: Service — the `real → game` edge and the return path

**Files:**
- Modify: `api/internal/services/wallet.go`
- Test: `api/tests/integration/gambling_test.go`

**Interfaces:**
- Consumes: `LoadWallets` (Task 3), `problem.GamblingNotActivated` (Task 4), `Repo.Transfer`, `Locker.AcquireOrdered`
- Produces:
  - `(*WalletService).FundGame(ctx, userID string, amount int64, idemKey string) (debit, credit *wallet.LedgerEntry, err error)`
  - `(*WalletService).ReturnFromGame(ctx, userID string, amount int64, idemKey string) (debit, credit *wallet.LedgerEntry, err error)`

`AcquireOrdered` already sorts wallet IDs, so lock ordering is deadlock-free for any number of wallets — no lock changes are needed. Pass both IDs and let it sort.

**The limit check is NOT in this task.** `FundGame` is the edge the limit engine will meter; that plan adds the check. This task builds the edge and its tests.

- [ ] **Step 1: Write the failing integration test**

Add to `api/tests/integration/gambling_test.go`:

```go
// activated creates a user with real+game+sandbox and the given real balance.
func activated(t *testing.T, h *harness, amount int64) string {
	t.Helper()
	ctx := context.Background()
	user := "u-" + id.New()
	fund(t, h, user, amount)
	acceptGambling(t, h, user)
	if _, _, err := h.svc.ActivateGambling(ctx, user, "verified", "", ""); err != nil {
		t.Fatalf("ActivateGambling: %v", err)
	}
	return user
}

func TestFundGameMovesRealMoneyIntoTheRingFence(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := activated(t, h, 10000)

	if _, _, err := h.svc.FundGame(ctx, user, 3000, "idem-1"); err != nil {
		t.Fatalf("FundGame: %v", err)
	}

	real, game, _, err := h.repo.LoadWallets(ctx, user)
	if err != nil {
		t.Fatalf("LoadWallets: %v", err)
	}
	if real.Balance != 7000 {
		t.Errorf("real = %d, want 7000", real.Balance)
	}
	if game.Balance != 3000 {
		t.Errorf("game = %d, want 3000", game.Balance)
	}

	// Idempotent replay: the same key must not move money twice.
	if _, _, err := h.svc.FundGame(ctx, user, 3000, "idem-1"); err != nil {
		t.Fatalf("FundGame replay: %v", err)
	}
	real, game, _, _ = h.repo.LoadWallets(ctx, user)
	if real.Balance != 7000 || game.Balance != 3000 {
		t.Fatalf("replay moved money again: real=%d game=%d", real.Balance, game.Balance)
	}
}

func TestFundGameCannotOverdrawReal(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := activated(t, h, 5000)

	_, _, err := h.svc.FundGame(ctx, user, 5001, "idem-over")
	var p *problem.Problem
	if !errors.As(err, &p) || p.Type != problem.TypeInsufficientBalance {
		t.Fatalf("FundGame over balance = %v, want insufficient-balance", err)
	}
	real, game, _, _ := h.repo.LoadWallets(ctx, user)
	if real.Balance != 5000 || game.Balance != 0 {
		t.Fatalf("failed fund must not move money: real=%d game=%d", real.Balance, game.Balance)
	}
}

func TestFundGameRequiresActivation(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := "u-" + id.New()
	fund(t, h, user, 10000)

	_, _, err := h.svc.FundGame(ctx, user, 1000, "idem-x")
	var p *problem.Problem
	if !errors.As(err, &p) || p.Type != problem.TypeGamblingNotActivated {
		t.Fatalf("FundGame without activation = %v, want gambling-not-activated", err)
	}
}

func TestReturnFromGameMovesMoneyBackAndIsNeverLimited(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := activated(t, h, 10000)

	if _, _, err := h.svc.FundGame(ctx, user, 4000, "idem-f"); err != nil {
		t.Fatalf("FundGame: %v", err)
	}
	if _, _, err := h.svc.ReturnFromGame(ctx, user, 4000, "idem-r"); err != nil {
		t.Fatalf("ReturnFromGame: %v", err)
	}

	real, game, _, err := h.repo.LoadWallets(ctx, user)
	if err != nil {
		t.Fatalf("LoadWallets: %v", err)
	}
	if real.Balance != 10000 || game.Balance != 0 {
		t.Fatalf("after round trip real=%d game=%d, want 10000/0", real.Balance, game.Balance)
	}
}

func TestReturnFromGameCannotOverdrawGame(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := activated(t, h, 10000)
	if _, _, err := h.svc.FundGame(ctx, user, 2000, "idem-f2"); err != nil {
		t.Fatalf("FundGame: %v", err)
	}

	_, _, err := h.svc.ReturnFromGame(ctx, user, 2001, "idem-r2")
	var p *problem.Problem
	if !errors.As(err, &p) || p.Type != problem.TypeInsufficientBalance {
		t.Fatalf("ReturnFromGame over balance = %v, want insufficient-balance", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd api && DYNAMODB_ENDPOINT=http://localhost:8123 go test -tags integration -count=1 ./tests/integration/ -run 'TestFundGame|TestReturnFromGame' -v`
Expected: FAIL — `FundGame` / `ReturnFromGame` undefined.

- [ ] **Step 3: Implement both edges**

Add to `api/internal/services/wallet.go`. Note both share a helper — do not write the transfer twice.

```go
// requireActivated loads the caller's wallets and fails if gambling was never
// activated. Every route inside the ring-fence goes through this.
func (s *WalletService) requireActivated(ctx context.Context, userID string) (real, game, sandbox *wallet.Wallet, err error) {
	real, game, sandbox, err = s.repo.LoadWallets(ctx, userID)
	if err != nil {
		return nil, nil, nil, err
	}
	if real == nil || game == nil || sandbox == nil {
		return nil, nil, nil, problem.GamblingNotActivated()
	}
	return real, game, sandbox, nil
}

// ringTransfer moves money between two of the caller's wallets atomically,
// locking both (AcquireOrdered sorts the IDs, so the order is total and
// deadlock-free). The ledger pair and the idempotency guard are co-written in one
// transaction by repo.Transfer.
func (s *WalletService) ringTransfer(ctx context.Context, from, to *wallet.Wallet, amount int64, debitType, creditType, ns, idemKey string) (debit, credit *wallet.LedgerEntry, err error) {
	release, ok, err := s.lock.AcquireOrdered(ctx, from.WalletID, to.WalletID)
	if err != nil {
		return nil, nil, err
	}
	if !ok {
		return nil, nil, problem.WalletBusy()
	}
	defer release()

	key := ns + "#" + from.UserID + "#" + idemKey
	d, c, _, err := s.repo.Transfer(ctx, from.WalletID, to.WalletID, amount,
		debitType, creditType, key, key, reqHash(ns, amount))
	if err != nil {
		return nil, nil, err
	}
	return d, c, nil
}

// FundGame moves real money into the gambling ring-fence (real → game).
//
// This is the ONE edge by which real money reaches a game or sandbox, and the
// edge the personal limit engine meters. The limit is GROSS INFLOW: a later
// ReturnFromGame does NOT refund limit headroom, or the cap could be churned
// around indefinitely (fund → return → fund). The limit check itself is added by
// the limit-engine plan; it belongs here, before the transfer.
func (s *WalletService) FundGame(ctx context.Context, userID string, amount int64, idemKey string) (debit, credit *wallet.LedgerEntry, err error) {
	real, game, _, err := s.requireActivated(ctx, userID)
	if err != nil {
		return nil, nil, err
	}
	return s.ringTransfer(ctx, real, game, amount,
		wallet.EntryGameFundDebit, wallet.EntryGameFundCredit, "game_fund", idemKey)
}

// ReturnFromGame moves money back out of the ring-fence (game → real).
//
// This is NEVER limited and never charged a fee: moving money out of the
// ring-fence reduces the user's exposure, which is the behaviour the limits exist
// to encourage. It is not a PIX payout — to reach a bank account the user then
// withdraws from `real` as usual.
func (s *WalletService) ReturnFromGame(ctx context.Context, userID string, amount int64, idemKey string) (debit, credit *wallet.LedgerEntry, err error) {
	real, game, _, err := s.requireActivated(ctx, userID)
	if err != nil {
		return nil, nil, err
	}
	return s.ringTransfer(ctx, game, real, amount,
		wallet.EntryGameReturnDebit, wallet.EntryGameReturnCredit, "game_return", idemKey)
}
```

- [ ] **Step 4: Run the tests**

Run: `cd api && DYNAMODB_ENDPOINT=http://localhost:8123 go test -tags integration -count=1 ./tests/integration/ -run 'TestFundGame|TestReturnFromGame' -v`
Expected: PASS — all five tests as `--- PASS`.

- [ ] **Step 5: Commit**

```bash
git add api/internal/services/wallet.go api/tests/integration/gambling_test.go
git commit -m "feat(api): add real->game funding edge and unlimited return path"
```

---

### Task 6: Repoint sandbox purchase to `game` + the bypass regression test

**Files:**
- Modify: `api/internal/services/wallet.go` (`PurchaseSandbox`, `sandboxOp`)
- Test: `api/tests/integration/gambling_test.go`

**Interfaces:**
- Consumes: `requireActivated`, `ringTransfer` (Task 5)
- Produces: `PurchaseSandbox` now debits **`game`**, not `real`

**This task closes the bypass.** While `real → sandbox` exists, personal limits are decorative: a user at their cap simply buys sandbox from `real` instead. The regression test below is the executable form of the load-bearing invariant and is the most important test in this plan.

- [ ] **Step 1: Write the failing regression test**

Add to `api/tests/integration/gambling_test.go`:

```go
// THE BYPASS REGRESSION. Real money must never reach sandbox except across the
// real → game edge. If this test ever fails, personal gambling limits are
// unenforceable — a user at their cap could buy sandbox straight from `real`.
func TestSandboxPurchaseNeverDebitsRealWallet(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := activated(t, h, 10000)

	// Fund the ring-fence, then buy sandbox from it.
	if _, _, err := h.svc.FundGame(ctx, user, 4000, "idem-fund"); err != nil {
		t.Fatalf("FundGame: %v", err)
	}
	if _, _, err := h.svc.PurchaseSandbox(ctx, user, 1500, "idem-buy"); err != nil {
		t.Fatalf("PurchaseSandbox: %v", err)
	}

	real, game, sandbox, err := h.repo.LoadWallets(ctx, user)
	if err != nil {
		t.Fatalf("LoadWallets: %v", err)
	}
	// real is untouched by the purchase: 10000 - 4000 funded = 6000, and not a
	// centavo more.
	if real.Balance != 6000 {
		t.Errorf("real = %d, want 6000 — the purchase must NOT debit the real wallet", real.Balance)
	}
	if game.Balance != 2500 {
		t.Errorf("game = %d, want 2500 (4000 funded - 1500 spent)", game.Balance)
	}
	if sandbox.Balance != 1500 {
		t.Errorf("sandbox = %d, want 1500", sandbox.Balance)
	}
}

// A user with real money but an empty game wallet cannot buy sandbox at all.
// The real balance is not reachable from here.
func TestSandboxPurchaseCannotReachRealBalance(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := activated(t, h, 10000) // 10000 in REAL, 0 in game

	_, _, err := h.svc.PurchaseSandbox(ctx, user, 1000, "idem-bypass")
	var p *problem.Problem
	if !errors.As(err, &p) || p.Type != problem.TypeInsufficientBalance {
		t.Fatalf("PurchaseSandbox with empty game wallet = %v, want insufficient-balance", err)
	}

	real, game, sandbox, _ := h.repo.LoadWallets(ctx, user)
	if real.Balance != 10000 {
		t.Fatalf("BYPASS: the purchase debited the real wallet (%d, want 10000)", real.Balance)
	}
	if game.Balance != 0 || sandbox.Balance != 0 {
		t.Fatalf("failed purchase moved money: game=%d sandbox=%d", game.Balance, sandbox.Balance)
	}
}

func TestSandboxPurchaseRequiresActivation(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := "u-" + id.New()
	fund(t, h, user, 10000)

	_, _, err := h.svc.PurchaseSandbox(ctx, user, 1000, "idem-na")
	var p *problem.Problem
	if !errors.As(err, &p) || p.Type != problem.TypeGamblingNotActivated {
		t.Fatalf("PurchaseSandbox without activation = %v, want gambling-not-activated", err)
	}
}

// MIGRATION. Users created under the old two-wallet model already have a sandbox
// wallet, sometimes with a balance. They never consented to a gambling addendum
// that did not exist, so they must NOT be treated as activated: their sandbox is
// frozen (no purchase, no play) until they opt in. Nothing of value is trapped —
// sandbox holds no real money by definition.
func TestLegacySandboxHolderIsNotTreatedAsActivated(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := "u-" + id.New()
	fund(t, h, user, 10000)

	// Simulate a pre-migration user: a real wallet AND a sandbox wallet, but no
	// game wallet and no gambling consent.
	legacySandbox := createLegacySandboxWallet(t, h, user)

	// They are not activated: the sandbox is frozen.
	_, _, err := h.svc.PurchaseSandbox(ctx, user, 1000, "idem-legacy")
	var p *problem.Problem
	if !errors.As(err, &p) || p.Type != problem.TypeGamblingNotActivated {
		t.Fatalf("legacy sandbox holder PurchaseSandbox = %v, want gambling-not-activated", err)
	}
	if _, err := h.svc.DebitSandbox(ctx, user, 100, "idem-legacy-d", "bet"); !errors.As(err, &p) ||
		p.Type != problem.TypeGamblingNotActivated {
		t.Fatalf("legacy sandbox holder DebitSandbox = %v, want gambling-not-activated", err)
	}

	// Balances hide the frozen sandbox until they activate.
	_, game, _, err := h.svc.GetBalances(ctx, user)
	if err != nil {
		t.Fatalf("GetBalances: %v", err)
	}
	if game != nil {
		t.Fatal("a legacy sandbox holder must not appear activated")
	}

	// After consent + activation, the SAME sandbox wallet comes back — activation
	// must not orphan an existing balance by minting a second sandbox wallet.
	acceptGambling(t, h, user)
	_, sandbox, err := h.svc.ActivateGambling(ctx, user, "verified", "", "")
	if err != nil {
		t.Fatalf("ActivateGambling: %v", err)
	}
	if sandbox.WalletID != legacySandbox.WalletID {
		t.Fatalf("activation minted a NEW sandbox wallet (%s) and orphaned the legacy one (%s)",
			sandbox.WalletID, legacySandbox.WalletID)
	}
}
```

Add this helper to `api/tests/integration/gambling_test.go`. It writes a sandbox wallet + marker directly, reproducing what the old `EnsureWallets` used to create:

```go
// createLegacySandboxWallet reproduces a pre-migration row: a sandbox wallet with
// no game wallet and no gambling consent, as the old two-wallet EnsureWallets left.
func createLegacySandboxWallet(t *testing.T, h *harness, userID string) *wallet.Wallet {
	t.Helper()
	// Use the repository's own creation path for the sandbox type only, so the
	// wallet + marker shape matches production exactly. If createWallets is
	// unexported, add a small test-only export or call EnsureGamblingWallets and
	// then delete the game wallet + marker — whichever keeps the test honest.
	// The assertion that matters: activation must REUSE this wallet, not replace it.
	w, err := h.repo.CreateWalletForTest(context.Background(), userID, wallet.TypeSandbox)
	if err != nil {
		t.Fatalf("createLegacySandboxWallet: %v", err)
	}
	return w
}
```

**Implementer note:** `EnsureGamblingWallets` (Task 3) already loads existing markers before creating, so a legacy sandbox wallet is reused rather than duplicated — this test proves it. Export a `CreateWalletForTest` helper on `WalletRepository` (guarded to the sandbox type) or expose `createWallets` to the integration package; do **not** hand-roll a second wallet-creation path in the test, or the test stops testing production behaviour.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd api && DYNAMODB_ENDPOINT=http://localhost:8123 go test -tags integration -count=1 ./tests/integration/ -run TestSandboxPurchase -v`
Expected: FAIL — `TestSandboxPurchaseNeverDebitsRealWallet` fails on `real = 6000` (it will read 4500, because the purchase still debits `real`).

- [ ] **Step 3: Repoint the purchase source**

Replace `PurchaseSandbox` in `api/internal/services/wallet.go`:

```go
// PurchaseSandbox converts game money into sandbox credits (game → sandbox).
//
// The source is the GAME wallet, never `real`: real money reaches sandbox only by
// first crossing the metered real → game edge. Sandbox remains a sink — Invariant
// #6 — so this conversion is one-way and can never be undone.
func (s *WalletService) PurchaseSandbox(ctx context.Context, userID string, amount int64, idemKey string) (debit, credit *wallet.LedgerEntry, err error) {
	_, game, sandbox, err := s.requireActivated(ctx, userID)
	if err != nil {
		return nil, nil, err
	}
	return s.ringTransfer(ctx, game, sandbox, amount,
		wallet.EntrySandboxPurchase, wallet.EntrySandboxCredit, "sandbox_purchase", idemKey)
}
```

Update `sandboxOp` (used by the M2M credit/debit routes) to use `requireActivated` so a non-activated user's sandbox operations fail cleanly:

```go
func (s *WalletService) sandboxOp(ctx context.Context, userID string, amount int64, idemKey, reason, entryType string, credit bool) (*wallet.LedgerEntry, error) {
	_, _, sandbox, err := s.requireActivated(ctx, userID)
	if err != nil {
		return nil, err
	}
	release, ok, err := s.lock.Acquire(ctx, sandbox.WalletID)
	// ... rest unchanged from the current implementation
}
```

- [ ] **Step 4: Run the tests**

Run: `cd api && DYNAMODB_ENDPOINT=http://localhost:8123 go test -tags integration -count=1 ./tests/integration/ -v 2>&1 | grep -E "^(--- |ok|FAIL)"`
Expected: every test PASS. The pre-existing `TestSandboxPurchaseAtomic` will need updating — it funds `real` and expects a `real` debit. Rewrite it to fund the game wallet via `FundGame` first. **Do not delete it** — it is the atomicity test.

- [ ] **Step 5: Commit**

```bash
git add api/internal/services/wallet.go api/tests/integration/gambling_test.go api/tests/integration/wallet_test.go
git commit -m "feat(api): buy sandbox from game wallet, closing the real->sandbox bypass"
```

---

### Task 7: Routes, DTOs, handlers, and the feature flag

**Files:**
- Modify: `api/internal/config/config.go`
- Modify: `api/internal/api/v1/dto.go`
- Modify: `api/internal/api/v1/wallet.go`
- Modify: `api/internal/api/v1/router.go:44-50`
- Test: `api/internal/api/v1/router_test.go` (extend, or create if absent)

**Interfaces:**
- Consumes: `ActivateGambling`, `FundGame`, `ReturnFromGame`, `GetBalances` (Tasks 4–5)
- Produces: routes `POST /v1.0/wallet/gambling/activate`, `POST /v1.0/wallet/game/deposit`, `POST /v1.0/wallet/game/withdraw`; `cfg.GamblingEnabled`

The whole gambling surface is flag-gated. With `GAMBLING_ENABLED=false` (the production default until the limit engine ships) the routes are **not registered at all** — they 404. This is what makes it structurally impossible for a real user to reach an unlimited gambling wallet.

- [ ] **Step 1: Write the failing test**

Add to `api/internal/api/v1/router_test.go` (mirror the existing table-driven route test in that file; if none exists, create the file following the pattern in `api/internal/api/v1/health_test.go`):

```go
func TestGamblingRoutesAreNotRegisteredWhenFlagDisabled(t *testing.T) {
	app := newTestApp(t, &config.Config{GamblingEnabled: false})

	for _, path := range []string{
		"/v1.0/wallet/gambling/activate",
		"/v1.0/wallet/game/deposit",
		"/v1.0/wallet/game/withdraw",
	} {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		res, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test(%s): %v", path, err)
		}
		if res.StatusCode != http.StatusNotFound {
			t.Errorf("%s with flag off = %d, want 404", path, res.StatusCode)
		}
	}
}

func TestGamblingRoutesAreRegisteredWhenFlagEnabled(t *testing.T) {
	app := newTestApp(t, &config.Config{GamblingEnabled: true})

	// Unauthenticated → 401, not 404: the route exists.
	req := httptest.NewRequest(http.MethodPost, "/v1.0/wallet/gambling/activate", nil)
	res, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	if res.StatusCode == http.StatusNotFound {
		t.Error("activate route must be registered when the flag is on")
	}
}
```

Adapt `newTestApp` to the existing test helper in the package — read `health_test.go` and reuse its app construction rather than writing a new one.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd api && go test ./internal/api/v1/ -run TestGamblingRoutes -v`
Expected: FAIL — `GamblingEnabled` undefined.

- [ ] **Step 3: Add the config flag**

In `api/internal/config/config.go`, add to the `Config` struct, following the existing `env` tag style:

```go
	// GamblingEnabled gates the entire game-wallet surface. It stays FALSE in
	// production until the personal limit engine ships: activation must never be
	// reachable while a user could enter the ring-fence with no cap configured.
	GamblingEnabled bool `env:"GAMBLING_ENABLED" envDefault:"false"`
```

- [ ] **Step 4: Add the DTOs**

In `api/internal/api/v1/dto.go`:

```go
// GameTransferRequest is the body for both game-wallet edges (real → game and
// game → real). The idempotency key travels in the Idempotency-Key header.
type GameTransferRequest struct {
	Amount int64 `json:"amount" validate:"required,gt=0"`
}

// ActivateGamblingRequest carries the explicit consent. AcceptAddendum must be
// true — activation is opt-in, and a silent default would not be consent.
type ActivateGamblingRequest struct {
	AcceptAddendum bool `json:"accept_addendum" validate:"required"`
}
```

- [ ] **Step 5: Add the handlers**

In `api/internal/api/v1/wallet.go`, update `getWallet` and add the three new handlers:

```go
// getWallet returns the caller's balances. game and sandbox are omitted entirely
// until the user activates gambling — the frontend reads their absence to decide
// whether to show any gambling surface at all.
func (h *handlers) getWallet(c fiber.Ctx) error {
	userID := middleware.GetUserID(c)
	realw, gamew, sandboxw, err := h.svc.GetBalances(c.Context(), userID)
	if err != nil {
		return sendProblem(c, err)
	}
	out := fiber.Map{"real": realw, "activated": gamew != nil}
	if gamew != nil {
		out["game"] = gamew
		out["sandbox"] = sandboxw
	}
	return c.JSON(out)
}

// activateGambling opts the caller into the game + sandbox wallets.
func (h *handlers) activateGambling(c fiber.Ctx) error {
	var body ActivateGamblingRequest
	if p := bindJSON(c, &body); p != nil {
		return sendProblem(c, p)
	}
	cl := middleware.GetClaims(c)
	if err := h.userSvc.AcceptGamblingAddendum(c.Context(), cl.Sub); err != nil {
		return sendProblem(c, err)
	}
	game, sandbox, err := h.svc.ActivateGambling(c.Context(), cl.Sub, cl.KYCLevel, c.IP(), string(c.Request().Header.UserAgent()))
	if err != nil {
		return sendProblem(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"game": game, "sandbox": sandbox})
}

// gameDeposit moves real money into the ring-fence (real → game). Limited.
func (h *handlers) gameDeposit(c fiber.Ctx) error {
	return h.gameTransfer(c, h.svc.FundGame)
}

// gameWithdraw moves money back out of the ring-fence (game → real). Never limited.
func (h *handlers) gameWithdraw(c fiber.Ctx) error {
	return h.gameTransfer(c, h.svc.ReturnFromGame)
}

// gameTransfer is the shared body of both edges — same parse, same idempotency
// key, same response shape; only the service call differs.
func (h *handlers) gameTransfer(c fiber.Ctx, op func(ctx context.Context, userID string, amount int64, idemKey string) (*wallet.LedgerEntry, *wallet.LedgerEntry, error)) error {
	var body GameTransferRequest
	if p := bindJSON(c, &body); p != nil {
		return sendProblem(c, p)
	}
	idemKey, p := requireIdempotencyKey(c)
	if p != nil {
		return sendProblem(c, p)
	}
	cl := middleware.GetClaims(c)
	debit, credit, err := op(c.Context(), cl.Sub, body.Amount, idemKey)
	if err != nil {
		return sendProblem(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"debit": debit, "credit": credit})
}
```

`AcceptGamblingAddendum` must be added to `UserService` in `api/internal/services/user.go`, mirroring the existing `AcceptTermsAddendum` exactly (it delegates to the repository method from Task 4) and appending a `wallet.EventGamblingAddendumAccepted` audit event.

- [ ] **Step 6: Register the routes**

In `api/internal/api/v1/router.go`, inside the `w := v1.Group("/wallet", auth)` block, after the existing routes:

```go
	// Gambling ring-fence. The entire surface is flag-gated: with GAMBLING_ENABLED
	// off these routes do not exist (404). The flag flips only once the personal
	// limit engine is live — a user must never be able to activate a gambling
	// wallet with no limits configured.
	if cfg.GamblingEnabled {
		w.Post("/gambling/activate", middleware.RequireKYC(middleware.KYCVerified), h.activateGambling)
		w.Post("/game/deposit", middleware.RequireKYC(middleware.KYCVerified), h.gameDeposit)
		w.Post("/game/withdraw", middleware.RequireKYC(middleware.KYCVerified), h.gameWithdraw)
	}
```

Note `w.Post("/sandbox/purchase", h.purchaseSandbox)` stays where it is — the service now enforces activation, so it correctly 409s for non-activated users even with the flag off.

- [ ] **Step 7: Run the tests**

Run: `cd api && go build ./... && go test ./... && DYNAMODB_ENDPOINT=http://localhost:8123 go test -tags integration -count=1 ./tests/integration/ -v 2>&1 | grep -E "^(--- FAIL|ok|FAIL)"`
Expected: build OK, all unit + integration tests PASS.

- [ ] **Step 8: Commit**

```bash
git add api/internal/config/config.go api/internal/api/v1/ api/internal/services/user.go
git commit -m "feat(api): expose gambling activation and game transfer routes behind a flag"
```

---

### Task 8: CDK — the `wallet_audit` table

**Files:**
- Modify: `cdk/lib/` (the stack file defining the DynamoDB tables — find it with `rg -n "TableWallets|wallets" cdk/lib/`)

**Interfaces:**
- Consumes: `wallet.TableAudit` = `"wallet_audit"` (Task 1)

- [ ] **Step 1: Read the existing table definitions**

Run: `rg -n "Table|dynamodb" cdk/lib/*.ts | head -40`

Find how `wallets`, `ledger_entries`, and `idempotency` are declared — the env-prefixed naming, billing mode, removal policy, and PITR settings. Match them exactly.

- [ ] **Step 2: Add the table**

Following the existing pattern precisely (same helper/construct, same prefix convention):

```typescript
// Append-only audit log for non-money events: gambling activation, addendum
// acceptance, and every personal-limit change. Same durability posture as the
// ledger — this is evidence, and it must survive.
const auditTable = new dynamodb.Table(this, 'WalletAuditTable', {
  tableName: `${prefix}wallet_audit`,
  partitionKey: {name: 'pk', type: dynamodb.AttributeType.STRING},   // user_id
  sortKey: {name: 'sk', type: dynamodb.AttributeType.STRING},        // created_at#event_id
  billingMode: dynamodb.BillingMode.PAY_PER_REQUEST,
  pointInTimeRecovery: true,
  removalPolicy: cdk.RemovalPolicy.RETAIN,
});
```

Grant the API's task role read/write on it wherever the other tables are granted.

- [ ] **Step 3: Verify the stack synthesises**

Run: `cd cdk && npx cdk synth 2>&1 | tail -20`
Expected: synth succeeds; `grep -c wallet_audit` on the output is ≥ 1.

- [ ] **Step 4: Commit**

```bash
git add cdk/
git commit -m "feat(cdk): add append-only wallet_audit table"
```

---

### Task 9: Frontend — real-only by default, activation flow

**Files:**
- Modify: `ui/src/lib/types/api.ts`
- Modify: `ui/src/components/wallet/balance-cards.tsx`
- Modify: `ui/src/app/dashboard/page.tsx`
- Create: `ui/src/app/gambling-addendum/page.tsx`

**Interfaces:**
- Consumes: `GET /v1.0/wallet/` (now `{real, activated, game?, sandbox?}`), `POST /v1.0/wallet/gambling/activate`

A user who only pays for subscriptions must never see a gambling surface. `activated: false` means: show the real balance, and nothing else.

- [ ] **Step 1: Update the API types**

In `ui/src/lib/types/api.ts`:

```typescript
export type WalletType = 'real' | 'game' | 'sandbox'

// game and sandbox are absent until the user activates gambling — their absence
// is the signal, not a separate flag we could drift out of sync with.
export interface Balances {
  real: Wallet
  activated: boolean
  game?: Wallet
  sandbox?: Wallet
}
```

- [ ] **Step 2: Gate the balance cards on activation**

In `ui/src/components/wallet/balance-cards.tsx`, render the game and sandbox cards only when `balances.activated` is true. Read the existing component first and follow its ShadCN card structure; do not restyle it.

When `activated` is false, render the real card plus a single unobtrusive entry point ("Ativar carteira de jogos") that links to `/gambling-addendum`. Do not upsell — it is one link, not a banner.

- [ ] **Step 3: Build the gambling addendum page**

Create `ui/src/app/gambling-addendum/page.tsx`, mirroring `ui/src/app/terms-addendum/page.tsx` (read it first — same layout, same accept-button pattern, same version-in-sync comment).

It must:
- render the responsible-gambling addendum text,
- carry the comment `// Keep in sync with wallet.CurrentGamblingAddendumVersion in the Go API`,
- require an explicit checkbox before the accept button enables — activation is opt-in, and a pre-checked box is not consent,
- `POST /v1.0/wallet/gambling/activate` with `{accept_addendum: true}` on submit,
- map `/problems/kyc-not-verified` to a message directing the user to verify their identity first, and `/problems/gambling-terms-required` to a prompt to accept.

Add both problem types to the `problemMessage` switch in `ui/src/app/dashboard/page.tsx`:

```typescript
    case '/problems/gambling-not-activated':
      return 'Ative a carteira de jogos para usar essa operação.'
    case '/problems/gambling-terms-required':
      return 'Aceite o termo de jogo responsável para continuar.'
```

- [ ] **Step 4: Run the frontend gate**

Run: `cd ui && npx eslint src --ext .ts,.tsx && npx tsc --noEmit`
Expected: eslint exits 0 with **zero errors and zero warnings**; tsc reports nothing.

- [ ] **Step 5: Commit**

```bash
git add ui/src/
git commit -m "feat(ui): hide gambling wallets until activation, add addendum page"
```

---

### Task 10: Docs — invariants, spec, legal

**Files:**
- Modify: `CLAUDE.md` (+ mirrored `AGENTS.md`), `api/CLAUDE.md` (+ `api/AGENTS.md`)
- Modify: `docs/specs/2026-07-10-wallet-design.md`
- Create: `docs/legal/wallet-gambling-addendum.md`
- Modify: `OPERATIONS.md`

- [ ] **Step 1: Update the Financial Safety Invariants**

In `CLAUDE.md` (and the mirrored `AGENTS.md`), amend:

- **#5** → "Cross-wallet ops take locks in a fixed order (`real` → `game` → `sandbox`) to avoid deadlock. (`AcquireOrdered` sorts wallet IDs, so the order is total for any number of wallets.)"
- **#6** → keep as-is, and append: "**Real money enters the gambling ring-fence only via the `real → game` edge.** There is no `real → sandbox` path. Removing this choke point makes personal limits unenforceable."
- Add **#9**: "**`game` balance is real money.** It is withdrawable (via `real`), counts toward the user's real holdings, and is never expired or written off. The user's total real money is `real.balance + game.balance`."
- Add **#10**: "**Consent is auditable.** Gambling activation, addendum acceptance, and every personal-limit change append to `wallet_audit` — append-only, never updated, never deleted."

Update the three-wallet table in the Projects/overview section so the two-balance description ("two balances per user") becomes three.

- [ ] **Step 2: Update the design spec**

In `docs/specs/2026-07-10-wallet-design.md`, mark §A (wallet types) and §C (sandbox purchase) as superseded by `docs/specs/2026-07-12-three-wallet-topology-design.md`, with a one-line pointer at the top of each.

- [ ] **Step 3: Write the gambling addendum**

Create `docs/legal/wallet-gambling-addendum.md` — the text the UI renders. It must state plainly: game-wallet money is real money; sandbox credits have no monetary value and are never convertible back; personal limits exist and how the cooldown works (7 days daily/weekly, 14 days monthly, **decreases take effect immediately**); and how to seek help. Keep `CurrentGamblingAddendumVersion` (`1.0`) in sync.

**This is legal copy — flag it for human/legal review rather than treating your draft as final.**

- [ ] **Step 4: Update OPERATIONS.md**

Document the `GAMBLING_ENABLED` flag: what it gates, why it defaults to `false`, and the precondition for flipping it on (the personal limit engine is live). State explicitly that turning it on before limits exist is the one thing this design forbids.

- [ ] **Step 5: Commit**

```bash
git add CLAUDE.md AGENTS.md api/CLAUDE.md api/AGENTS.md docs/ OPERATIONS.md
git commit -m "docs: three-wallet topology invariants, gambling addendum, ops flag"
```

---

## Done criteria

- [ ] `cd api && go build ./... && go vet ./... && go test ./...` — all pass
- [ ] `cd api && DYNAMODB_ENDPOINT=http://localhost:8123 go test -tags integration -count=1 ./tests/integration/ -v` — all pass, and every new test name appears as a `--- PASS` line (no silent no-op)
- [ ] `cd ui && npx eslint src --ext .ts,.tsx` — zero errors, zero warnings; `npx tsc --noEmit` clean
- [ ] `cd cdk && npx cdk synth` — succeeds
- [ ] `TestSandboxPurchaseNeverDebitsRealWallet` and `TestSandboxPurchaseCannotReachRealBalance` pass — the bypass is closed
- [ ] `TestLegacySandboxHolderIsNotTreatedAsActivated` passes — no fabricated consent, no orphaned sandbox balance
- [ ] `TestAcceptingOneAddendumDoesNotEraseTheOther` passes — consent is never silently revoked
- [ ] `rg -n "EnsureWallets" api/` returns nothing — the old two-wallet creation path is gone
- [ ] `GAMBLING_ENABLED` defaults to `false`; with it off, the three gambling routes 404
- [ ] Cross-project impact reviewed: `api` ↔ `ui` ↔ `cdk` ↔ `ctech-account` (account needs **no** change — no new scopes, no new claims; the gambling addendum is wallet-side state)

## Next plan

The personal limit engine: daily/weekly/monthly caps metered on `FundGame` as **gross inflow**, hierarchy validation (daily ≤ weekly ≤ monthly), asymmetric cooldown (**decreases immediate**; increases delayed 7d daily/weekly, 14d monthly), every change appended to `wallet_audit`, and self-exclusion. Only when that is live does `GAMBLING_ENABLED` flip to `true`.
