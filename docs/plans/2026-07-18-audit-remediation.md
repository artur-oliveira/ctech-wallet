# Pre-Launch Audit Remediation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:
> executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close every open finding (F2–F6, plus the two cross-cutting duplications) from
`docs/specs/2026-07-18-audit-remediation.md`, in the same priority order the spec assigned.

**Architecture:** No new architecture — every fix reuses an existing pattern already proven
elsewhere in this codebase (conditional `TransactWriteItems` for F2, the sandbox-op `Mutation`
shape for F3, the `SERVICE_AUDIENCE`/`CTECH_URL` fail-closed pattern for F4, the existing SSM
`secrets.Store` pattern for F5, `ConfirmDeposit`'s own idempotent re-query for F6's sweep).

**Tech Stack:** Go 1.26 (Fiber v3, AWS SDK v2, `gopkg.aoctech.app/api-commons`), CDK v2/TypeScript.

## Global Constraints

- All amounts are integer centavos — never float.
- Every balance mutation goes through a conditional `TransactWriteItems`; debits carry
  `balance >= :amount`.
- Every mutation is idempotent via a guard item (`attribute_not_exists`) co-written in the same
  transaction as the balance/ledger write.
- One operation per wallet at a time via the Valkey `SETNX` lock; cross-wallet ops use
  `lock.AcquireOrdered`.
- All API errors go through `sendProblem(c, err)` / `problem.*` helpers — never raw errors or
  `fiber.Map`.
- Layer separation is strict: Repository = DynamoDB only, Service = business logic + lock/PIX/KYC,
  Route = parse + call one service method + respond.
- `go build ./...` and `make test` must pass before every commit; `make test-integration` (needs
  `docker compose -f docker-compose.test.yml up -d`) must pass for every task touching
  repository/lock/idempotency code.
- No emojis, no `Co-Authored-By` trailer in commits.

## Scope note

This plan covers the concrete, fully-designed items from the spec: **F2, F3, F4, F5, F6
(sweep-before-TTL only)**, the ALARM metric filter, the remaining concurrency-test gap, and the
two cross-cutting duplication extractions. **Not included** (see "Explicitly out of scope" at the
end, with reasons): F6's Inter-statement cross-check, F7, F8.

---

### Task 1: F2 — fix the `Withdraw` idempotency race

**Files:**

- Modify: `api/internal/repositories/wallet.go:421-427` (`PutWithdrawal`)
- Modify: `api/internal/services/wallet.go:475-549` (`Withdraw`)
- Test: `api/internal/services/wallet_test.go` (existing replay/busy tests must still pass)
- Test: `api/tests/integration/wallet_test.go` (new concurrency test)

**Interfaces:**

- Consumes: `repositories.IsConditionFailed(err error) bool` (already exported, aliases
  `dynamo.IsConditionFailed`, `api/internal/repositories/base.go:38`); `Base.BuildPutTxItemIfAbsent`
  / `Base.TransactWrite` (already used by `DebitWithFee`, same file).
- Produces: `repositories.ErrWithdrawalExists` (new sentinel error) — `Withdraw` is the only
  caller expected to check it via `errors.Is`.

- [ ] **Step 1: Write the failing integration test proving the race**

Add to `api/tests/integration/wallet_test.go` (mirror the existing `//go:build integration`
setup already in that package):

```go
func TestWithdrawConcurrentSameIdempotencyKeyExactlyOneTransfer(t *testing.T) {
ctx := context.Background()
repo := repositories.NewWalletRepository(db, testConfig(tablePrefix))
userID := "u-" + id.New()
real, err := repo.EnsureRealWallet(ctx, userID)
if err != nil {
t.Fatal(err)
}
if _, err := repo.Credit(ctx, repositories.Mutation{
WalletID: real.WalletID, Amount: 100000, EntryType: wallet.EntryDeposit,
Ref: "seed", IdempotencyKey: "seed#" + userID, ReqHash: "seed",
}); err != nil {
t.Fatal(err)
}

fake := pix.NewFake()
kyc := &fakeKYC{cpf: "12345678901"}
svc := services.NewWalletService(repo, repositories.NewUserRepository(db, testConfig(tablePrefix)),
repositories.NewAuditRepository(db, testConfig(tablePrefix)), lock.NewLocker(cache.NewMemoryBackend(16)),
fake, kyc)

const n = 10
var wg sync.WaitGroup
errs := make([]error, n)
for i := 0; i < n; i++ {
wg.Add(1)
go func (i int) {
defer wg.Done()
_, errs[i] = svc.Withdraw(ctx, userID, "verified", 5000, "idem-race")
}(i)
}
wg.Wait()

for i, err := range errs {
if err != nil {
t.Fatalf("goroutine %d: unexpected error: %v", i, err)
}
}
if fake.TransferCalls != 1 {
t.Fatalf("expected exactly 1 PIX transfer call, got %d", fake.TransferCalls)
}
w, err := repo.GetWallet(ctx, real.WalletID)
if err != nil {
t.Fatal(err)
}
if want := int64(100000 - 5000 - wallet.WithdrawalFee(5000, real)); w.Balance != want {
t.Fatalf("balance = %d, want %d (double-debit if lower)", w.Balance, want)
}
}
```

`pix.NewFake()` must expose a `TransferCalls int` counter incremented on every `Transfer` call —
check `api/internal/pix/fake.go` first; if it already counts calls under a different field name,
use that name instead of adding a new one. `fakeKYC` must implement the `KYCClient` interface
used elsewhere in this test file (`Get(ctx, userID) (*kycclient.KYC, error)`) — reuse the
existing fake KYC helper in this package if one exists, matching its constructor shape, instead
of declaring a second one.

- [ ] **Step 2: Run it to confirm it fails**

```bash
docker compose -f docker-compose.test.yml up -d
DYNAMODB_ENDPOINT=http://localhost:8000 go test ./tests/integration/... -run TestWithdrawConcurrentSameIdempotencyKeyExactlyOneTransfer -tags=integration -v -count=1
```

Expected: FAIL — either the transfer-count assertion (`fake.TransferCalls != 1`, likely 2) or the
balance assertion trips, proving the current unconditional `PutWithdrawal` and post-lock replay
check let two racing calls both debit and both transfer.

- [ ] **Step 3: Make `PutWithdrawal` conditional**

```go
// ErrWithdrawalExists means PutWithdrawal lost a race — another call already
// created this withdrawal record. The caller must re-fetch and return the
// winner's record instead of retrying the write or the PIX transfer.
var ErrWithdrawalExists = errors.New("repositories: withdrawal already exists")

func (r *WalletRepository) PutWithdrawal(ctx context.Context, w *wallet.Withdrawal) error {
av, err := Encode(w)
if err != nil {
return err
}
item := r.withdrawal.BuildPutTxItemIfAbsent(av)
if err := r.withdrawal.TransactWrite(ctx, []types.TransactWriteItem{item}); err != nil {
if IsConditionFailed(err) {
return ErrWithdrawalExists
}
return err
}
return nil
}
```

Add `"errors"` to `api/internal/repositories/wallet.go`'s import block.

- [ ] **Step 4: Move the lock before the replay check and honor `replayed`**

Replace `Withdraw` (`api/internal/services/wallet.go:475-549`) with:

```go
func (s *WalletService) Withdraw(ctx context.Context, userID, kycLevel string, amount int64, idemKey string) (*wallet.Withdrawal, error) {
if kycLevel != wallet.KYCVerified {
return nil, problem.KYCNotVerified()
}
withdrawalID := "withdraw#" + userID + "#" + idemKey

realw, err := s.repo.EnsureRealWallet(ctx, userID)
if err != nil {
return nil, err
}

release, ok, err := s.lock.Acquire(ctx, realw.WalletID)
if err != nil {
return nil, err
}
if !ok {
return nil, problem.WalletBusy()
}
defer release()

// Idempotent replay: same key → return the existing withdrawal. Checked
// under the wallet lock (not before it) so two concurrent identical calls
// can't both pass this check before either has written anything.
if existing, err := s.repo.GetWithdrawal(ctx, withdrawalID); err != nil {
return nil, err
} else if existing != nil {
return existing, nil
}

kyc, err := s.kyc.Get(ctx, userID)
if err != nil {
return nil, err
}
pixKey := kyc.CPF

fee := wallet.WithdrawalFee(amount, realw)
rh := reqHash(pixKey, amount)
_, _, replayed, err := s.repo.DebitWithFee(ctx, realw.WalletID, amount, fee, withdrawalID, rh, withdrawalID)
if err != nil {
return nil, err
}
if replayed {
// The debit itself was a replay (same idempotency key already
// committed) — someone else is mid-flight on this withdrawal. Never
// re-transfer; return whatever is on record.
return s.repo.GetWithdrawal(ctx, withdrawalID)
}

w := &wallet.Withdrawal{
WithdrawalID:   withdrawalID,
WalletID:       realw.WalletID,
UserID:         userID,
Amount:         amount,
Fee:            fee,
PixKey:         pixKey,
Status:         wallet.WithdrawProcessing,
IdempotencyKey: idemKey,
CreatedAt:      repositories.NowStr(),
UpdatedAt:      repositories.NowStr(),
}
if err := s.repo.PutWithdrawal(ctx, w); err != nil {
if errors.Is(err, repositories.ErrWithdrawalExists) {
return s.repo.GetWithdrawal(ctx, withdrawalID)
}
return nil, err
}

res, err := s.pix.Transfer(ctx, pixKey, amount, withdrawalID)
if err != nil {
if errors.Is(err, pix.ErrKeyNotFound) {
s.reverse(ctx, *w)
return nil, problem.PixKeyNotFound()
}
slog.Warn("withdrawal transfer failed, left in processing", "withdrawal_id", withdrawalID, "err", err)
return w, nil
}
w.Status = wallet.WithdrawCompleted
w.E2EID = res.E2EID
if err := s.repo.UpdateWithdrawal(ctx, withdrawalID, map[string]any{"status": wallet.WithdrawCompleted, "e2e_id": res.E2EID}); err != nil {
return nil, err
}
s.broadcastWithdrawal(ctx, userID, "withdraw_completed", withdrawalID, amount)
return w, nil
}
```

- [ ] **Step 5: Run the new integration test — confirm it passes**

```bash
DYNAMODB_ENDPOINT=http://localhost:8000 go test ./tests/integration/... -run TestWithdrawConcurrentSameIdempotencyKeyExactlyOneTransfer -tags=integration -v -count=1
```

Expected: PASS.

- [ ] **Step 6: Run the full unit + integration suites — confirm no regression**

```bash
go test ./... -v
DYNAMODB_ENDPOINT=http://localhost:8000 go test ./tests/integration/... -tags=integration -v
```

Expected: all pass, including the pre-existing `TestWithdrawBusy`, `TestWithdrawIdempotentReplay`,
`TestWithdrawHappyPath`, `TestWithdrawTransferFailureLeavesProcessing`,
`TestWithdrawKeyNotFoundRefundsImmediately`, `TestWithdrawUsesKYCCPFNotClientKey`.

- [ ] **Step 7: Commit**

```bash
git add api/internal/repositories/wallet.go api/internal/services/wallet.go api/tests/integration/wallet_test.go
git commit -m "fix(api): close Withdraw idempotency race (F2)

Lock acquisition now precedes the replay check, PutWithdrawal is a
conditional write, and DebitWithFee's replayed signal is no longer
discarded — a concurrent identical Withdraw can no longer double-transfer."
```

---

### Task 2: F3 — `internal:wallet:real/debit` endpoint

**Files:**

- Modify: `api/internal/api/v1/router.go:68-75`
- Modify: `api/internal/api/v1/internal.go`
- Modify: `api/internal/api/v1/dto.go`
- Modify: `api/internal/services/wallet.go` (new `DebitReal` method)
- Modify: `api/internal/domain/wallet/model.go` (new ledger entry type)
- Test: `api/internal/services/wallet_test.go`
- Test: `api/internal/api/v1/internal_test.go`
- Test: `api/tests/integration/wallet_test.go`

**Design decisions locked in (do not re-derive):**

- Route: `POST /internal/wallet/real/debit`, scope `internal:wallet:debit` (already seeded,
  currently enforced only on the sandbox route — this adds a second consumer of the same scope,
  same as `sandbox/credit` and `sandbox/debit` already share `ScopeWalletCredit`/`ScopeWalletDebit`
  respectively).
- Body: `{user_id, amount, idempotency_key, reason}` — identical shape to `SandboxOpRequest`;
  reuse it rather than declaring a new type (DRY — the fields are identical).
- Insufficient balance: standard `409 insufficient-balance` from the underlying conditional debit,
  no partial debit, no new problem type.
- No PIX leg, no `processing` state — this is a single conditional `TransactWriteItems` debit
  against `real`, same shape as `DebitSandbox` against `sandbox`.
- Reversal: out of scope for this task — `ctech-billing` reverses via a future call to the
  existing `internal:wallet:credit` → `real` route (does not exist yet either; not part of this
  audit's findings, do not build it here).

**Interfaces:**

- Consumes: `s.repo.Debit(ctx, repositories.Mutation) (*wallet.LedgerEntry, bool, error)`
  (existing, `api/internal/repositories/wallet.go:235-237`); `s.repo.EnsureRealWallet` (existing).
- Produces:
  `WalletService.DebitReal(ctx, userID string, amount int64, idemKey, reason string) (*wallet.LedgerEntry, error)`
  — later tasks/services calling into the real wallet debit path use this exact signature.

- [ ] **Step 1: Add the ledger entry type constant**

In `api/internal/domain/wallet/model.go`, in the ledger entry types block (after
`EntryGameReturnCredit`):

```go
    EntryBillingDebit = "billing_debit" // real debited by an authorized M2M client (ctech-billing)
```

- [ ] **Step 2: Write the failing unit test for the service method**

Add to `api/internal/services/wallet_test.go`:

```go
func TestDebitRealHappyPath(t *testing.T) {
repo := newStubRepo()
svc := newSvc(repo, &stubLocker{}, pix.NewFake(), &stubKYC{rec: &kycclient.KYC{CPF: "1"}})
entry, err := svc.DebitReal(context.Background(), "u1", 5000, "charge-1", "subscription")
if err != nil {
t.Fatalf("unexpected error: %v", err)
}
if entry.WalletID != "w-real" {
t.Fatalf("debited wallet = %q, want w-real", entry.WalletID)
}
if entry.Amount != -5000 {
t.Fatalf("entry amount = %d, want -5000", entry.Amount)
}
}

func TestDebitRealInsufficientBalance(t *testing.T) {
repo := newStubRepo()
repo.debitErr = problem.InsufficientBalance()
svc := newSvc(repo, &stubLocker{}, pix.NewFake(), &stubKYC{rec: &kycclient.KYC{CPF: "1"}})
_, err := svc.DebitReal(context.Background(), "u1", 5000, "charge-1", "subscription")
isProblem(t, err, problem.TypeInsufficientBalance)
}

func TestDebitRealWalletBusy(t *testing.T) {
svc := newSvc(newStubRepo(), &stubLocker{busy: true}, pix.NewFake(), &stubKYC{rec: &kycclient.KYC{CPF: "1"}})
_, err := svc.DebitReal(context.Background(), "u1", 5000, "charge-1", "subscription")
isProblem(t, err, problem.TypeWalletBusy)
}
```

Confirm `stubRepo.debitErr` and `stubRepo.Debit` already exist and behave as expected (they do —
same fields `DebitSandbox`'s tests already exercise via `sandboxOp`); if `stubRepo` has no
`EnsureRealWallet` stub yet, check the existing `Withdraw` tests, which already depend on it,
before adding one.

- [ ] **Step 3: Run to confirm failure**

```bash
go test ./internal/services/... -run TestDebitReal -v
```

Expected: FAIL — `svc.DebitReal` undefined.

- [ ] **Step 4: Implement `DebitReal`**

Add to `api/internal/services/wallet.go`, near `sandboxOp`/`DebitSandbox`:

```go
// DebitReal debits the real wallet for an authorized M2M client (e.g.
// ctech-billing charging a subscription). No PIX leg — this only moves money
// within the ledger, same shape as DebitSandbox but against `real`.
func (s *WalletService) DebitReal(ctx context.Context, userID string, amount int64, idemKey, reason string) (*wallet.LedgerEntry, error) {
realw, err := s.repo.EnsureRealWallet(ctx, userID)
if err != nil {
return nil, err
}
release, ok, err := s.lock.Acquire(ctx, realw.WalletID)
if err != nil {
return nil, err
}
if !ok {
return nil, problem.WalletBusy()
}
defer release()

entry, _, err := s.repo.Debit(ctx, repositories.Mutation{
WalletID:       realw.WalletID,
Amount:         amount,
EntryType:      wallet.EntryBillingDebit,
Ref:            reason,
IdempotencyKey: wallet.EntryBillingDebit + "#" + idemKey,
ReqHash:        reqHash(reason, amount),
})
return entry, err
}
```

- [ ] **Step 5: Run unit tests — confirm pass**

```bash
go test ./internal/services/... -run TestDebitReal -v
```

Expected: PASS.

- [ ] **Step 6: Wire the route**

In `api/internal/api/v1/router.go`, after the `sb` (sandbox) group (line 74):

```go
    rw := internal.Group("/wallet/real")
rw.Post("/debit", middleware.RequireScope(middleware.ScopeWalletDebit), h.realDebit)
```

- [ ] **Step 7: Add the handler**

In `api/internal/api/v1/internal.go`, after `sandboxDebit`:

```go
// realDebit debits the real wallet (M2M, scope internal:wallet:debit,
// e.g. ctech-billing charging a subscription). No PIX leg.
func (h *handlers) realDebit(c fiber.Ctx) error {
var body SandboxOpRequest
if p := bindJSON(c, &body); p != nil {
return sendProblem(c, p)
}
entry, err := h.svc.DebitReal(c.Context(), body.UserID, body.Amount, body.IdempotencyKey, body.Reason)
if err != nil {
return sendProblem(c, err)
}
return c.Status(fiber.StatusCreated).JSON(entry)
}
```

Reuses `SandboxOpRequest` from `dto.go` — do not declare a new DTO for an identical shape.

- [ ] **Step 8: Write the wiring test**

Add to `api/internal/api/v1/internal_test.go`, mirroring `TestConfirmDepositRequiresScope`:

```go
func TestRealDebitRouteRegistered(t *testing.T) {
app := fiber.New()
app.Use(recover.New())
h := &handlers{}
app.Post("/internal/wallet/real/debit", func (c fiber.Ctx) error {
return h.realDebit(c)
})

body, _ := json.Marshal(SandboxOpRequest{UserID: "u1", Amount: 5000, IdempotencyKey: "k1"})
req := httptest.NewRequest(http.MethodPost, "/internal/wallet/real/debit", bytes.NewReader(body))
req.Header.Set("Content-Type", "application/json")
resp, err := app.Test(req)
if err != nil {
t.Fatalf("app.Test: %v", err)
}
if resp.StatusCode == http.StatusNotFound {
t.Fatal("route not registered")
}
}
```

- [ ] **Step 9: Regression — sandbox routes still cannot touch `real`**

Add to `api/tests/integration/wallet_test.go` (mirrors the existing
`TestSandboxPurchaseNeverDebitsRealWallet` pattern — read that test first and match its setup
style exactly):

```go
func TestRealDebitNeverTouchesSandboxWallet(t *testing.T) {
ctx := context.Background()
repo := repositories.NewWalletRepository(db, testConfig(tablePrefix))
svc := services.NewWalletService(repo, repositories.NewUserRepository(db, testConfig(tablePrefix)),
repositories.NewAuditRepository(db, testConfig(tablePrefix)), lock.NewLocker(cache.NewMemoryBackend(16)),
pix.NewFake(), &fakeKYC{cpf: "12345678901"})
userID := "u-" + id.New()
real, err := repo.EnsureRealWallet(ctx, userID)
if err != nil {
t.Fatal(err)
}
if _, err := repo.Credit(ctx, repositories.Mutation{
WalletID: real.WalletID, Amount: 10000, EntryType: wallet.EntryDeposit,
Ref: "seed", IdempotencyKey: "seed#" + userID, ReqHash: "seed",
}); err != nil {
t.Fatal(err)
}

entry, err := svc.DebitReal(ctx, userID, 5000, "charge-1", "subscription")
if err != nil {
t.Fatal(err)
}
if entry.WalletID != real.WalletID {
t.Fatalf("debited %q, want the real wallet %q", entry.WalletID, real.WalletID)
}
w, err := repo.GetWallet(ctx, real.WalletID)
if err != nil {
t.Fatal(err)
}
if w.Balance != 5000 {
t.Fatalf("real balance = %d, want 5000", w.Balance)
}
}
```

- [ ] **Step 10: Run everything**

```bash
go build ./...
go test ./... -v
DYNAMODB_ENDPOINT=http://localhost:8000 go test ./tests/integration/... -tags=integration -v
```

Expected: all pass.

- [ ] **Step 11: Commit**

```bash
git add api/internal/domain/wallet/model.go api/internal/services/wallet.go api/internal/api/v1/router.go api/internal/api/v1/internal.go api/internal/services/wallet_test.go api/internal/api/v1/internal_test.go api/tests/integration/wallet_test.go
git commit -m "feat(api): add internal:wallet:real/debit endpoint (F3)

DebitReal lets an M2M client with internal:wallet:debit scope debit
the real wallet directly — no PIX leg, standard conditional debit.
Unblocks ctech-billing's subscription-charge integration."
```

**Cross-project follow-up (not part of this task, flag to the user):** `ctech-account`'s scope
seeding must clamp `ctech-billing`'s M2M client to this new route in addition to sandbox —
coordinate before `ctech-billing` actually calls it.

---

### Task 3: F4 — fail closed on missing `VALKEY_URL` in prod

**Files:**

- Modify: `api/internal/config/config.go:54-75`
- Test: `api/internal/config/config_test.go` (create if it doesn't exist — check first)

**Interfaces:**

- Consumes: nothing new.
- Produces: nothing new — `Load()`'s existing signature and error-return contract are unchanged.

- [ ] **Step 1: Check for an existing config test file**

```bash
ls api/internal/config/
```

If `config_test.go` exists, read it and match its existing test style (likely env-var
set/unset table tests around the `SERVICE_AUDIENCE`/`CTECH_URL` fail-closed checks). If it
doesn't exist, create it fresh per Step 2.

- [ ] **Step 2: Write the failing test**

```go
package config

import "testing"

func TestLoadFailsClosedWithoutValkeyURLInProd(t *testing.T) {
	t.Setenv("ENVIRONMENT", "prod")
	t.Setenv("SERVICE_AUDIENCE", "https://wallet-api.aoctech.app")
	t.Setenv("CTECH_URL", "https://account.aoctech.app")
	t.Setenv("TABLE_PREFIX", "prod")
	t.Setenv("PIX_GATEWAY_FUNCTION_NAME", "prod-pix-gateway-outbound")
	t.Setenv("VALKEY_URL", "")

	if _, err := Load(); err == nil {
		t.Fatal("expected Load to fail closed with VALKEY_URL unset in prod")
	}
}

func TestLoadSucceedsWithValkeyURLInProd(t *testing.T) {
	t.Setenv("ENVIRONMENT", "prod")
	t.Setenv("SERVICE_AUDIENCE", "https://wallet-api.aoctech.app")
	t.Setenv("CTECH_URL", "https://account.aoctech.app")
	t.Setenv("TABLE_PREFIX", "prod")
	t.Setenv("PIX_GATEWAY_FUNCTION_NAME", "prod-pix-gateway-outbound")
	t.Setenv("VALKEY_URL", "redis://valkey.internal:6379/0")

	if _, err := Load(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadSucceedsWithoutValkeyURLOutsideProd(t *testing.T) {
	t.Setenv("ENVIRONMENT", "dev")
	t.Setenv("TABLE_PREFIX", "dev")
	t.Setenv("PIX_GATEWAY_FUNCTION_NAME", "dev-pix-gateway-outbound")
	t.Setenv("VALKEY_URL", "")

	if _, err := Load(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
```

Match required env vars exactly to what `Config` declares (`TablePrefix` and
`PixGatewayFunctionName` are `,required`) — re-check `config.go`'s struct tags before running, in
case another required field was added since this plan was written.

- [ ] **Step 3: Run to confirm the first test fails**

```bash
go test ./internal/config/... -run TestLoadFailsClosedWithoutValkeyURLInProd -v
```

Expected: FAIL — `Load()` currently returns no error.

- [ ] **Step 4: Add the fail-closed check**

In `api/internal/config/config.go`, in `Load()`, after the existing `CtechURL` check
(line ~72-73):

```go
    if cfg.RedisURL == "" && cfg.Env == "prod" {
// Fail closed: an empty VALKEY_URL in prod means per-wallet locking
// silently degrades to an in-memory store that is NOT shared across
// the ASG's other instances — Invariant #4 ("one operation per wallet
// at a time") stops holding fleet-wide with no signal. Never boot into
// that state; mirror the SERVICE_AUDIENCE/CTECH_URL guards above.
return nil, fmt.Errorf("config: VALKEY_URL must be set in production so wallet locking is fleet-shared")
}
```

- [ ] **Step 5: Run all three tests — confirm pass**

```bash
go test ./internal/config/... -v
```

Expected: all PASS.

- [ ] **Step 6: Run the full unit suite for regressions**

```bash
go test ./... -v
```

Expected: PASS (no other code path constructs a prod `Config` without `VALKEY_URL` set).

- [ ] **Step 7: Commit**

```bash
git add api/internal/config/config.go api/internal/config/config_test.go
git commit -m "fix(api): fail closed without VALKEY_URL in prod (F4)

An empty VALKEY_URL silently downgraded per-wallet locking to an
in-memory store not shared across ASG instances. Boot now refuses to
start in prod without it, mirroring the existing SERVICE_AUDIENCE/
CTECH_URL fail-closed guards."
```

---

### Task 4: F5 — verify Inter's webhook `hmac` query parameter + fix invariant wording

**Context confirmed via Inter/PIX ecosystem docs (not guessed):** Inter's webhook signature
scheme is a **static query-string parameter**, not a body signature. The webhook URL is
registered with Inter as `https://pix.wallet.aoctech.app/webhook?hmac=<secret>`, and Inter echoes
that exact query string back on every callback. The check is "does the incoming request's `hmac`
query param match the secret we registered with," not an HMAC computed over the payload.

**Files:**

- Modify: `pix-gateway/internal/secrets/store.go` (or wherever `Store` lives — confirmed above)
- Modify: `cdk/lib/pix-gateway-stack.ts:109-141` (webhook role + function)
- Modify: `pix-gateway/cmd/webhook/main.go`
- Modify: `CLAUDE.md:73-75` (invariant #11 wording)
- Test: `pix-gateway/cmd/webhook/main_test.go` (check whether it exists first)

**Interfaces:**

- Consumes: nothing new from other tasks.
- Produces: `secrets.Store.LoadInterWebhookSecret(ctx) (string, error)`; `handler.webhookSecret string`
  field — no other task depends on these.

- [ ] **Step 1: Check for an existing webhook handler test file**

```bash
ls pix-gateway/cmd/webhook/
```

If `main_test.go` exists, read it fully and match its existing style for constructing `handler`
and fake `confirmer`. If not, create it in Step 2.

- [ ] **Step 2: Write the failing test**

```go
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/aws/aws-lambda-go/events"
)

type fakeConfirmer struct{ calls int }

func (f *fakeConfirmer) ConfirmDeposit(_ context.Context, _, _, _ string) error {
	f.calls++
	return nil
}

func TestHandleRejectsWrongHMAC(t *testing.T) {
	h := &handler{confirmer: &fakeConfirmer{}, webhookSecret: "correct-secret"}
	body, _ := json.Marshal(pixWebhookPayload{Pix: []pixWebhookPayloadDetail{{Txid: "tx1"}}})
	req := events.APIGatewayV2HTTPRequest{
		Body:                  string(body),
		QueryStringParameters: map[string]string{"hmac": "wrong-secret"},
	}
	resp, err := h.handle(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	if resp.StatusCode != 401 {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
	if h.confirmer.(*fakeConfirmer).calls != 0 {
		t.Fatal("ConfirmDeposit must not be called on a bad hmac")
	}
}

func TestHandleAcceptsCorrectHMAC(t *testing.T) {
	fc := &fakeConfirmer{}
	h := &handler{confirmer: fc, webhookSecret: "correct-secret"}
	body, _ := json.Marshal(pixWebhookPayload{Pix: []pixWebhookPayloadDetail{{Txid: "tx1"}}})
	req := events.APIGatewayV2HTTPRequest{
		Body:                  string(body),
		QueryStringParameters: map[string]string{"hmac": "correct-secret"},
	}
	resp, err := h.handle(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if fc.calls != 1 {
		t.Fatalf("ConfirmDeposit calls = %d, want 1", fc.calls)
	}
}
```

Remove the stray `bytes` import if unused once the test is finalized (it's listed above only
because some repos' existing webhook tests build the body as raw bytes — verify against Step 1's
findings and drop what isn't needed).

- [ ] **Step 3: Run to confirm failure**

```bash
go test ./cmd/webhook/... -run TestHandleRejectsWrongHMAC -v
```

Expected: FAIL — `handler` has no `webhookSecret` field yet, compile error.

- [ ] **Step 4: Add `LoadInterWebhookSecret` to the secrets store**

In `pix-gateway/internal/secrets/store.go` (the file with `interSecretParamFmt` etc.):

```go
    interWebhookSecretParamFmt = "/ctech-wallet/%s/inter/webhook-secret"
```

(add to the existing `const (...)` block), and:

```go
// LoadInterWebhookSecret fetches the static hmac value registered with Inter's
// webhook configuration — Inter echoes it back as a query parameter
// (?hmac=<secret>) on every callback; this is not a body signature.
func (s *Store) LoadInterWebhookSecret(ctx context.Context) (string, error) {
return s.get(ctx, fmt.Sprintf(interWebhookSecretParamFmt, s.env))
}
```

- [ ] **Step 5: Load the secret at cold start and check it in `handle`**

In `pix-gateway/cmd/webhook/main.go`:

```go
type handler struct {
confirmer     confirmer
webhookSecret string
}
```

In `main()`, after `newWalletClient`:

```go
    secret, err := loadWebhookSecret(context.Background(), cfg)
if err != nil {
slog.Error("webhook secret load failed", "err", err)
os.Exit(1)
}
h := &handler{confirmer: client, webhookSecret: secret}
```

Add the loader helper (mirrors `newWalletClient`'s shape):

```go
func loadWebhookSecret(ctx context.Context, cfg *config.Config) (string, error) {
awsCfg, err := awscfg.LoadDefaultConfig(ctx, awscfg.WithRegion(cfg.AWSRegion))
if err != nil {
return "", err
}
store := secrets.NewStore(ssm.NewFromConfig(awsCfg), cfg.Env)
return store.LoadInterWebhookSecret(ctx)
}
```

At the top of `handle()`, before parsing the body:

```go
func (h *handler) handle(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
if subtle.ConstantTimeCompare([]byte(req.QueryStringParameters["hmac"]), []byte(h.webhookSecret)) != 1 {
slog.WarnContext(ctx, "webhook rejected: hmac mismatch")
return events.APIGatewayV2HTTPResponse{StatusCode: 401, Body: "unauthorized"}, nil
}
var body pixWebhookPayload
// ... rest unchanged
```

Add `"crypto/subtle"` to the import block.

- [ ] **Step 6: Run the new tests — confirm pass**

```bash
go test ./cmd/webhook/... -v
```

Expected: PASS.

- [ ] **Step 7: Grant the webhook Lambda's role read access to the new parameter**

In `cdk/lib/pix-gateway-stack.ts`, in `webhookRole`'s policy statement (line ~120-123), add the
new resource:

```ts
        webhookRole.addToPolicy(new iam.PolicyStatement({
    actions: ['ssm:GetParameter'],
    resources: [
        `arn:aws:ssm:*:*:parameter${pixGatewaySsm.clientSecret}`,
        `arn:aws:ssm:*:*:parameter${walletSsm.interWebhookSecret}`,
    ],
}));
```

No environment-variable change needed — the secret is fetched via SDK at cold start, same
pattern as `interClientSecret`, never passed through Lambda env vars.

- [ ] **Step 8: CDK synth check**

```bash
cd cdk && npx cdk synth --quiet
```

Expected: synthesizes without error; diff the `WebhookRole`'s policy in the output to confirm the
new `ssm:GetParameter` resource is present.

- [ ] **Step 9: Update the invariant wording**

In root `CLAUDE.md`, replace invariant #11 (lines 73-75):

```markdown
11. **The PIX webhook is never the source of truth for money movement.** A deposit credits only
    after re-querying the charge by `txid` at the Inter API (confirming amount and status). The
    webhook is a "wake up and re-check" signal for those, authenticated by mTLS at the transport
    layer plus a static `hmac` query parameter Inter echoes back on every callback. Payer CPF is
    the one field Inter's re-query does not return — it is sourced from the webhook body itself
    (persisted on first sight) and used only for the CPF-match anti-fraud check, never to
    authorize crediting.
```

- [ ] **Step 10: Update `OPERATIONS.md`'s webhook-secret row**

The row at `OPERATIONS.md:56` already documents the parameter; add one line after the existing
"Also register the webhook secret..." sentence (around line 79) making the mechanism explicit:

```markdown
Register the webhook URL with Inter as `https://pix.wallet.aoctech.app/webhook?hmac=<the same
value stored in /ctech-wallet/{env}/inter/webhook-secret>` — Inter echoes this query string back
on every callback and `pix-gateway`'s webhook Lambda now rejects any request where it doesn't
match.
```

- [ ] **Step 11: Run the full pix-gateway test suite**

```bash
cd pix-gateway && go build ./... && go test ./... -v
```

Expected: PASS.

- [ ] **Step 12: Commit**

```bash
git add pix-gateway/internal/secrets pix-gateway/cmd/webhook cdk/lib/pix-gateway-stack.ts CLAUDE.md OPERATIONS.md
git commit -m "fix(pix-gateway,cdk): verify Inter webhook hmac query param (F5)

The interWebhookSecret SSM parameter was provisioned but never read.
The webhook Lambda now rejects any callback whose ?hmac= query
parameter doesn't match the registered secret. Invariant #11's
wording is corrected: payer CPF comes from the webhook body, not
Inter's charge re-query, which doesn't return it."
```

---

### Task 5: F6 — sweep pending deposits before TTL expiry

**Scope note:** this task covers only the sweep half of F6 (re-query a pending deposit before its
TTL deletes the row). The statement/extract cross-check half needs research into Inter's actual
PIX statement API shape, which isn't available in this codebase — see "Explicitly out of scope"
at the end.

**Files:**

- Modify: `cdk/lib/dynamodb-stack.ts:112-113` (add a status GSI to the deposits table)
- Modify: `api/internal/domain/wallet/model.go` (`GSIStatus` doc comment only — no new constant)
- Modify: `api/internal/repositories/wallet.go` (new `ListPendingDepositsOlderThan`)
- Modify: `api/internal/services/reconcile.go` (new `SweepPendingDeposits`)
- Modify: `api/cmd/reconcile/main.go` (wire it into `run()`)
- Test: `api/tests/integration/wallet_test.go` (new sweep test)

**Interfaces:**

- Consumes: `WalletService.ConfirmDeposit(ctx, txid, payerCPF, payerName string) error` (existing,
  already fully idempotent — reused as-is, no changes to it).
- Produces:
  `WalletRepository.ListPendingDepositsOlderThan(ctx, cutoff time.Time, limit int) ([]wallet.PixDeposit, error)`;
  `WalletService.SweepPendingDeposits(ctx) (swept int, err error)` — `cmd/reconcile/main.go`'s
  `Result` struct gains a `SweptDeposits` field consuming this.

- [ ] **Step 1: Add the GSI to the deposits table**

In `cdk/lib/dynamodb-stack.ts`, change line 113:

```ts
        // ── wallet_pix_deposits: in-flight charges keyed by txid, expire via TTL ───
        // gsi_status backs the pre-TTL sweep (F6): find pending deposits close to
        // expiry and re-query Inter once before the row is lost.
const depositsTable = table('wallet_pix_deposits', {ttl: true});
gsi(depositsTable, GSI_STATUS, ATTR_STATUS);
```

- [ ] **Step 2: Deploy the GSI to the test/dev DynamoDB-local schema**

Check `docker-compose.test.yml` / whatever script provisions DynamoDB-local's tables for
integration tests (likely `api/tests/integration/setup_test.go`'s `createTables`) and add the
matching GSI there — integration tests run against DynamoDB-local, not real CDK-deployed tables,
so the local schema must mirror this change or Step 6's test will fail with
`ValidationException: index not found`. Read `createTables` in
`api/tests/integration/setup_test.go` before editing, and add a `GlobalSecondaryIndexes` entry
for `wallet_pix_deposits` matching however `wallet_withdrawals`' `gsi_status` index is already
declared there.

- [ ] **Step 3: Write the failing repository test**

Add to `api/tests/integration/wallet_test.go`:

```go
func TestListPendingDepositsOlderThanFindsAgedPending(t *testing.T) {
ctx := context.Background()
repo := repositories.NewWalletRepository(db, testConfig(tablePrefix))
old := &wallet.PixDeposit{
Txid: "old-" + id.New(), WalletID: "w1", UserID: "u1",
AmountExpected: 1000, Status: wallet.DepositPending,
CreatedAt: time.Now().Add(-4 * time.Minute).UTC().Format(time.RFC3339Nano),
TTL:       time.Now().Add(1 * time.Minute).Unix(),
}
fresh := &wallet.PixDeposit{
Txid: "fresh-" + id.New(), WalletID: "w1", UserID: "u1",
AmountExpected: 1000, Status: wallet.DepositPending,
CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
TTL:       time.Now().Add(5 * time.Minute).Unix(),
}
if err := repo.PutDeposit(ctx, old); err != nil {
t.Fatal(err)
}
if err := repo.PutDeposit(ctx, fresh); err != nil {
t.Fatal(err)
}

found, err := repo.ListPendingDepositsOlderThan(ctx, time.Now().Add(-3*time.Minute), 50)
if err != nil {
t.Fatal(err)
}
var sawOld, sawFresh bool
for _, d := range found {
if d.Txid == old.Txid {
sawOld = true
}
if d.Txid == fresh.Txid {
sawFresh = true
}
}
if !sawOld {
t.Fatal("expected the aged pending deposit in the sweep list")
}
if sawFresh {
t.Fatal("fresh pending deposit should not be swept yet")
}
}
```

- [ ] **Step 4: Run to confirm failure**

```bash
DYNAMODB_ENDPOINT=http://localhost:8000 go test ./tests/integration/... -run TestListPendingDepositsOlderThan -tags=integration -v
```

Expected: FAIL — method undefined.

- [ ] **Step 5: Implement the repository query**

Add to `api/internal/repositories/wallet.go`, near `ListProcessingWithdrawals`:

```go
// ListPendingDepositsOlderThan returns pending deposits created before cutoff —
// the sweep's work queue (F6): a pending deposit that never got a webhook has
// no fallback path to eventual consistency before its TTL deletes the row, so
// the sweep re-queries Inter once before that happens.
func (r *WalletRepository) ListPendingDepositsOlderThan(ctx context.Context, cutoff time.Time, limit int) ([]wallet.PixDeposit, error) {
res, err := r.deposits.QueryGSI(ctx, wallet.GSIStatus, "status", wallet.DepositPending, limit, nil)
if err != nil {
return nil, err
}
out := make([]wallet.PixDeposit, 0, len(res.Items))
for _, it := range res.Items {
d, err := Decode[wallet.PixDeposit](it)
if err != nil {
return nil, err
}
createdAt, err := time.Parse(time.RFC3339Nano, d.CreatedAt)
if err != nil {
continue // malformed timestamp — skip rather than fail the whole sweep
}
if createdAt.Before(cutoff) {
out = append(out, *d)
}
}
return out, nil
}
```

`GSIStatus`'s existing doc comment (`api/internal/domain/wallet/model.go:72`) should be updated
to mention it now also drives the deposit sweep, not just withdrawal reconciliation:

```go
    GSIStatus = "gsi_status" // withdrawals.status → reconciliation scan; deposits.status → pending sweep
```

- [ ] **Step 6: Run the repository test — confirm pass**

```bash
DYNAMODB_ENDPOINT=http://localhost:8000 go test ./tests/integration/... -run TestListPendingDepositsOlderThan -tags=integration -v
```

Expected: PASS.

- [ ] **Step 7: Write the failing service-level sweep test**

Add to `api/tests/integration/wallet_test.go`:

```go
func TestSweepPendingDepositsCreditsOnceInterConfirms(t *testing.T) {
ctx := context.Background()
repo := repositories.NewWalletRepository(db, testConfig(tablePrefix))
fake := pix.NewFake()
svc := services.NewWalletService(repo, repositories.NewUserRepository(db, testConfig(tablePrefix)),
repositories.NewAuditRepository(db, testConfig(tablePrefix)), lock.NewLocker(cache.NewMemoryBackend(16)),
fake, &fakeKYC{cpf: "12345678901"})

userID := "u-" + id.New()
real, err := repo.EnsureRealWallet(ctx, userID)
if err != nil {
t.Fatal(err)
}
txid := "sweep-" + id.New()
dep := &wallet.PixDeposit{
Txid: txid, WalletID: real.WalletID, UserID: userID,
AmountExpected: 5000, Status: wallet.DepositPending, PayerCPF: "12345678901",
CreatedAt: time.Now().Add(-4 * time.Minute).UTC().Format(time.RFC3339Nano),
TTL:       time.Now().Add(1 * time.Minute).Unix(),
}
if err := repo.PutDeposit(ctx, dep); err != nil {
t.Fatal(err)
}
fake.Charges[txid] = &pix.Charge{Txid: txid, Amount: 5000, Status: pix.ChargeCompleted, PayerCPF: "12345678901"}

swept, err := svc.SweepPendingDeposits(ctx)
if err != nil {
t.Fatal(err)
}
if swept != 1 {
t.Fatalf("swept = %d, want 1", swept)
}
w, err := repo.GetWallet(ctx, real.WalletID)
if err != nil {
t.Fatal(err)
}
if w.Balance != 5000 {
t.Fatalf("balance = %d, want 5000 (deposit should have been credited)", w.Balance)
}
}
```

Check `pix.NewFake()`'s actual field name for pre-seeding charge lookups (`Charges map[string]*pix.Charge`
is the expected shape based on `QueryCharge`'s signature — confirm against `api/internal/pix/fake.go`
before relying on it, and adjust the field name in this test if it differs).

- [ ] **Step 8: Run to confirm failure**

```bash
DYNAMODB_ENDPOINT=http://localhost:8000 go test ./tests/integration/... -run TestSweepPendingDepositsCreditsOnceInterConfirms -tags=integration -v
```

Expected: FAIL — `svc.SweepPendingDeposits` undefined.

- [ ] **Step 9: Implement the sweep**

Add to `api/internal/services/reconcile.go`:

```go
// sweepAgeThreshold: a pending PixDeposit gets one re-query once it's within
// this margin of its depositTTLMinutes TTL, so a missed webhook still has a
// fallback path to eventual consistency before the row is lost (F6).
const sweepAgeThreshold = 3 * time.Minute

// SweepPendingDeposits re-queries Inter once for every pending deposit
// approaching its TTL, reusing ConfirmDeposit's own idempotent credit logic —
// a webhook that never arrives (network issue, cold-start timeout, mTLS
// handshake failure) is the only case this changes: it used to silently
// expire, uncredited and unaccounted for.
func (s *WalletService) SweepPendingDeposits(ctx context.Context) (swept int, err error) {
cutoff := time.Now().Add(-sweepAgeThreshold)
deps, err := s.repo.ListPendingDepositsOlderThan(ctx, cutoff, reconcileBatch)
if err != nil {
return 0, err
}
for i := range deps {
d := deps[i]
if err := s.ConfirmDeposit(ctx, d.Txid, "", ""); err != nil {
slog.Warn("sweep: confirm-deposit failed, will retry next run", "txid", d.Txid, "err", err)
continue
}
swept++
}
return swept, nil
}
```

Add `"time"` to `api/internal/services/reconcile.go`'s import block if not already present, and
add `ListPendingDepositsOlderThan(ctx context.Context, cutoff time.Time, limit int) ([]wallet.PixDeposit, error)`
to the `Repo` interface in `api/internal/services/wallet.go` (alongside `ListProcessingWithdrawals`)
so the service compiles against the interface, not the concrete repository type — and add a
matching stub method to `stubRepo` in `wallet_test.go` (return `nil, nil` is sufficient; no
existing unit test exercises the sweep path).

- [ ] **Step 10: Run the sweep test — confirm pass**

```bash
DYNAMODB_ENDPOINT=http://localhost:8000 go test ./tests/integration/... -run TestSweepPendingDepositsCreditsOnceInterConfirms -tags=integration -v
```

Expected: PASS.

- [ ] **Step 11: Wire the sweep into the reconcile command**

In `api/cmd/reconcile/main.go`, add a field to `Result`:

```go
type Result struct {
Resolved      int `json:"resolved"`
Reversed      int `json:"reversed"`
Alarmed       int `json:"alarmed"`
SweptDeposits int `json:"swept_deposits"`
}
```

In `run()`, after `ReconcileWithdrawals`:

```go
    resolved, reversed, alarmed, err := svc.ReconcileWithdrawals(ctx)
if err != nil {
return nil, err
}
swept, err := svc.SweepPendingDeposits(ctx)
if err != nil {
return nil, err
}
return &Result{Resolved: resolved, Reversed: reversed, Alarmed: alarmed, SweptDeposits: swept}, nil
```

- [ ] **Step 12: Run the full unit + integration suites**

```bash
go build ./...
go test ./... -v
DYNAMODB_ENDPOINT=http://localhost:8000 go test ./tests/integration/... -tags=integration -v
```

Expected: all pass.

- [ ] **Step 13: Commit**

```bash
git add cdk/lib/dynamodb-stack.ts api/internal/domain/wallet/model.go api/internal/repositories/wallet.go api/internal/services/wallet.go api/internal/services/reconcile.go api/cmd/reconcile/main.go api/tests/integration/wallet_test.go api/tests/integration/setup_test.go
git commit -m "feat(api,cdk): sweep pending deposits before TTL expiry (F6 sweep)

A pending PixDeposit with no webhook previously just expired,
uncredited and unaccounted for. SweepPendingDeposits re-queries Inter
once for deposits nearing their TTL, reusing ConfirmDeposit's
existing idempotent credit logic. The Inter-statement cross-check
half of F6 is a separate follow-up (needs Inter's statement API
shape researched first)."
```

---

### Task 6: Observability — CloudWatch alarm on the `ALARM` log literal

**Files:**

- Modify: `cdk/lib/api-stack.ts` (near where `service.appLogGroup` is defined/exported, ~line 455-477)

**Interfaces:** none — pure CDK addition, no application code changes.

- [ ] **Step 1: Add the metric filter + alarm**

In `cdk/lib/api-stack.ts`, after the stack has a reference to `service.appLogGroup` (the same
log group the `AppLogGroupName` `CfnOutput` at line 472 already references), add:

```ts
        const alarmMetricFilter = service.appLogGroup.addMetricFilter('AlarmLogFilter', {
    filterPattern: logs.FilterPattern.literal('"ALARM"'),
    metricNamespace: `${SERVICE}/${environment}`,
    metricName: 'AlarmLogLines',
    metricValue: '1',
    defaultValue: 0,
});
new cloudwatch.Alarm(this, 'AlarmLogAlarm', {
    alarmName: `${environment}-${SERVICE}-alarm-log-lines`,
    alarmDescription: 'A wallet ALARM log line was emitted (refund/reversal failure, deposit amount mismatch, or statement drift) — needs manual reconciliation.',
    metric: alarmMetricFilter.metric({statistic: 'Sum', period: Duration.minutes(5)}),
    threshold: 1,
    evaluationPeriods: 1,
    comparisonOperator: cloudwatch.ComparisonOperator.GREATER_THAN_OR_EQUAL_TO_THRESHOLD,
    treatMissingData: cloudwatch.TreatMissingData.NOT_BREACHING,
});
```

Add the missing imports at the top of the file if not already present:

```ts
import * as logs from 'aws-cdk-lib/aws-logs';
import * as cloudwatch from 'aws-cdk-lib/aws-cloudwatch';
```

Check whether `Duration` is already imported (it's used elsewhere in this file for timeouts) —
reuse the existing import rather than adding a duplicate.

- [ ] **Step 2: Synth to confirm it compiles and produces the expected resources**

```bash
cd cdk && npx cdk synth --quiet
```

Expected: synthesizes without error. Grep the synthesized template for the new metric filter and
alarm logical IDs to confirm they were emitted:

```bash
npx cdk synth 2>/dev/null | grep -A3 "AlarmLogAlarm\|AlarmLogFilter"
```

- [ ] **Step 3: Commit**

```bash
git add cdk/lib/api-stack.ts
git commit -m "feat(cdk): alarm on the ALARM log literal in the app log group

The slog ALARM lines for refund/reversal failures already existed
but paged nobody. A metric filter + CloudWatch alarm now fires
whenever one is emitted."
```

---

### Task 7: cross-cutting — extract the `rpc_types.go` ↔ `pix-gateway/internal/rpc/types.go` mirror

**Files:**

- Create: `rpc-contract/go.mod`, `rpc-contract/types.go` (new Go module at the repo root)
- Modify: `api/go.mod` (add `replace` + `require` for the new module)
- Modify: `pix-gateway/go.mod` (same)
- Modify: `api/internal/pix/rpc_types.go` → delete, replaced by imports of the new module
- Modify: `api/internal/pix/lambda_client.go`, `intertoken.go`, `lambda_client_test.go`, `intertoken_test.go`
- Modify: `pix-gateway/internal/rpc/types.go` → delete
- Modify: every file in `pix-gateway` importing `internal/rpc` (`pix-gateway/internal/inter/bearer.go`,
  `pix-gateway/cmd/outbound/main.go`, `pix-gateway/cmd/outbound/main_test.go`)

**Interfaces:**

- Consumes: nothing.
- Produces: package `rpccontract` (module `gopkg.aoctech.app/wallet/rpc-contract`) exporting
  `Op`, `Request`, `Response`, `GetTokenResult`, `CreateChargeArgs`, `QueryChargeArgs`,
  `ChargeResult`, `RefundResult`, `PaymentResult`, `DictLookupArgs`, `DictResult`, `TransferArgs`,
  `QueryTransferArgs`, `RefundArgs`, `TransferResult`, `ErrKeyNotFoundSentinel`,
  `ErrUnauthorizedSentinel`, and the `Op*` constants — identical names to what
  `pix-gateway/internal/rpc/types.go` already exports today (api's call sites change; pix-gateway's
  don't, beyond the import path).

- [ ] **Step 1: Create the shared module**

```bash
mkdir -p rpc-contract
```

`rpc-contract/go.mod`:

```
module gopkg.aoctech.app/wallet/rpc-contract

go 1.26
```

`rpc-contract/types.go` — copy `pix-gateway/internal/rpc/types.go` verbatim (it's already fully
exported and already carries the right doc comments), only changing the package name if needed
(keep it `rpc` for the package identifier, imported as `rpccontract "gopkg.aoctech.app/wallet/rpc-contract"`
at call sites to avoid a bare `rpc` import name colliding with `pix-gateway/internal/rpc` during
the migration — or rename the package to `rpccontract` outright since nothing else needs the
short name once both consumers import it externally). Use the package name `rpccontract` for
clarity at call sites in both modules:

```go
// Package rpccontract defines the wire contract between api's LambdaPixClient
// and pix-gateway's outbound Lambda. Both modules import this package instead
// of hand-mirroring it (see docs/specs/2026-07-18-audit-remediation.md).
package rpccontract

import "encoding/json"

// Op names the PixClient method being invoked. One Lambda function handles all
// of them so api makes exactly one kind of Invoke call.
type Op string

const (
	OpCreateCharge  Op = "CreateCharge"
	OpQueryCharge   Op = "QueryCharge"
	OpTransfer      Op = "Transfer"
	OpQueryTransfer Op = "QueryTransfer"
	OpRefund        Op = "Refund"
	OpPing          Op = "Ping"
	OpGetToken      Op = "GetToken"
)

const ErrKeyNotFoundSentinel = "key_not_found"
const ErrUnauthorizedSentinel = "unauthorized"

type Request struct {
	Op         Op              `json:"op"`
	OAuthToken string          `json:"oauth_token"`
	Payload    json.RawMessage `json:"payload"`
}

type GetTokenResult struct {
	Token     string `json:"token"`
	ExpiresIn int    `json:"expires_in"`
}

type Response struct {
	Error   string          `json:"error,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

type CreateChargeArgs struct {
	Txid         string `json:"txid"`
	Amount       int64  `json:"amount"`
	PayerHintCPF string `json:"payer_hint_cpf"`
}

type QueryChargeArgs struct {
	Txid string `json:"txid"`
}

type ChargeResult struct {
	Txid      string          `json:"txid"`
	Amount    int64           `json:"amount"`
	QRCode    string          `json:"qr_code"`
	QRCodeB64 string          `json:"qr_code_b64"`
	Status    string          `json:"status"`
	PayerCPF  string          `json:"payer_cpf"`
	E2EID     string          `json:"e2e_id"`
	Refunds   []RefundResult  `json:"refunds,omitempty"`
	Payments  []PaymentResult `json:"payments,omitempty"`
}

type RefundResult struct {
	RtrID  string `json:"rtr_id"`
	Amount int64  `json:"amount"`
	Status string `json:"status"`
}

type PaymentResult struct {
	E2EID    string         `json:"e2e_id"`
	Amount   int64          `json:"amount"`
	PayerCPF string         `json:"payer_cpf"`
	Refunds  []RefundResult `json:"refunds,omitempty"`
}

type DictLookupArgs struct {
	PixKey string `json:"pix_key"`
}

type DictResult struct {
	Key  string `json:"key"`
	CPF  string `json:"cpf"`
	Name string `json:"name"`
}

type TransferArgs struct {
	PixKey  string `json:"pix_key"`
	Amount  int64  `json:"amount"`
	IdemKey string `json:"idem_key"`
}

type QueryTransferArgs struct {
	IdemKey string `json:"idem_key"`
}

type RefundArgs struct {
	E2EID   string `json:"e2e_id"`
	Amount  int64  `json:"amount"`
	IdemKey string `json:"idem_key"`
}

type TransferResult struct {
	E2EID  string `json:"e2e_id"`
	Status string `json:"status"`
}
```

- [ ] **Step 2: Point both consumer modules at it**

`pix-gateway/go.mod` and `api/go.mod` each get:

```
require gopkg.aoctech.app/wallet/rpc-contract v0.0.0

replace gopkg.aoctech.app/wallet/rpc-contract => ../rpc-contract
```

- [ ] **Step 3: Switch `pix-gateway` to the shared package**

Delete `pix-gateway/internal/rpc/types.go` (the whole `internal/rpc` directory, since it now
contains nothing). In every file importing it (`pix-gateway/internal/inter/bearer.go`,
`pix-gateway/cmd/outbound/main.go`, `pix-gateway/cmd/outbound/main_test.go`), change:

```go
    "gopkg.aoctech.app/wallet/pix-gateway/internal/rpc"
```

to:

```go
    rpc "gopkg.aoctech.app/wallet/rpc-contract"
```

The `rpc.` call-site prefix stays identical (`rpc.OpCreateCharge`, `rpc.Request`, etc.) because
of the import alias — no other line in these three files needs to change.

- [ ] **Step 4: Switch `api` to the shared package**

Delete `api/internal/pix/rpc_types.go`. In `api/internal/pix/lambda_client.go`,
`intertoken.go`, `lambda_client_test.go`, `intertoken_test.go`, add the import:

```go
    rpccontract "gopkg.aoctech.app/wallet/rpc-contract"
```

Then replace every unexported identifier with its exported, `rpccontract`-qualified equivalent
per this exact mapping (case-sensitive, whole-identifier — do not substring-replace):

| Old (unexported, `pix` package) | New                                   |
|---------------------------------|---------------------------------------|
| `rpcOp`                         | `rpccontract.Op`                      |
| `opCreateCharge`                | `rpccontract.OpCreateCharge`          |
| `opQueryCharge`                 | `rpccontract.OpQueryCharge`           |
| `opTransfer`                    | `rpccontract.OpTransfer`              |
| `opQueryTransfer`               | `rpccontract.OpQueryTransfer`         |
| `opRefund`                      | `rpccontract.OpRefund`                |
| `opPing`                        | `rpccontract.OpPing`                  |
| `opGetToken`                    | `rpccontract.OpGetToken`              |
| `errUnauthorizedSentinel`       | `rpccontract.ErrUnauthorizedSentinel` |
| `errKeyNotFoundSentinel`        | `rpccontract.ErrKeyNotFoundSentinel`  |
| `rpcRequest`                    | `rpccontract.Request`                 |
| `rpcGetTokenResult`             | `rpccontract.GetTokenResult`          |
| `rpcResponse`                   | `rpccontract.Response`                |
| `rpcCreateChargeArgs`           | `rpccontract.CreateChargeArgs`        |
| `rpcQueryChargeArgs`            | `rpccontract.QueryChargeArgs`         |
| `rpcChargeResult`               | `rpccontract.ChargeResult`            |
| `rpcRefundResult`               | `rpccontract.RefundResult`            |
| `rpcPaymentResult`              | `rpccontract.PaymentResult`           |
| `rpcTransferArgs`               | `rpccontract.TransferArgs`            |
| `rpcQueryTransferArgs`          | `rpccontract.QueryTransferArgs`       |
| `rpcRefundArgs`                 | `rpccontract.RefundArgs`              |
| `rpcTransferResult`             | `rpccontract.TransferResult`          |

- [ ] **Step 5: Build and run both modules**

```bash
cd rpc-contract && go build ./...
cd ../pix-gateway && go build ./... && go test ./... -v
cd ../api && go build ./... && go test ./... -v
```

Expected: all build and all tests pass unchanged — this is a pure rename, no behavior change.

- [ ] **Step 6: Run integration tests too**

```bash
cd api && DYNAMODB_ENDPOINT=http://localhost:8000 go test ./tests/integration/... -tags=integration -v
```

Expected: PASS.

- [ ] **Step 7: Update the CI duplicate-code grep from commit `e20cf04`**

Check `.github/workflows/infra.yml` (added by `e20cf04`) for what it greps for — if it detects
hand-mirrored type definitions by name/pattern, confirm it doesn't now false-positive on
`rpc-contract/` being imported from two modules (that's the fix, not the duplication).

- [ ] **Step 8: Commit**

```bash
git add rpc-contract api/go.mod api/go.sum api/internal/pix pix-gateway/go.mod pix-gateway/go.sum pix-gateway/internal
git commit -m "refactor(api,pix-gateway): extract rpc-contract shared module

api/internal/pix/rpc_types.go and pix-gateway/internal/rpc/types.go
were hand-mirrored field-for-field across two Go modules. Both now
import a new local rpc-contract module instead."
```

---

### Task 8: cross-cutting — extract `tokenManager` into `ctech-go-common`

**Note:** this task edits a sibling repository (`~/Documents/Projects/Ctech/ctech-go-common`),
not `ctech-wallet`. Confirm with the user before starting if that repo has other in-flight work —
`git status` there first.

**Files:**

- Create: `ctech-go-common/oauth2client/client.go` (new package)
- Modify: `ctech-go-common/README.md` (package table)
- Modify: `ctech-go-common` version tag (new minor/patch release)
- Modify: `ctech-wallet/api/go.mod` (bump `api-commons`)
- Modify: `ctech-wallet/api/internal/kycclient/kycclient.go` (use the shared client)
- Modify: `ctech-wallet/pix-gateway/go.mod` (add `api-commons` dependency — currently absent)
- Modify: `ctech-wallet/pix-gateway/internal/walletclient/walletclient.go` (use the shared client)

**Interfaces:**

- Produces:
  `oauth2client.New(httpClient *http.Client, tokenURL, clientID, clientSecret, scope string) *oauth2client.TokenManager`
  with method `Get(ctx context.Context) (string, error)` — both consumers' existing `.get(ctx)`
  call sites become `.Get(ctx)` (exported).

- [ ] **Step 1: Write the failing test in `ctech-go-common`**

`ctech-go-common/oauth2client/client_test.go`:

```go
package oauth2client

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetCachesTokenUntilExpiry(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"tok-1","expires_in":3600}`))
	}))
	defer srv.Close()

	tm := New(srv.Client(), srv.URL, "client-id", "secret", "scope:a")
	t1, err := tm.Get(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t2, err := tm.Get(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if t1 != "tok-1" || t2 != "tok-1" {
		t.Fatalf("token = %q, %q, want tok-1 both times", t1, t2)
	}
	if calls != 1 {
		t.Fatalf("token endpoint called %d times, want 1 (second Get should hit cache)", calls)
	}
}

func TestGetFailsOnNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	tm := New(srv.Client(), srv.URL, "client-id", "wrong-secret", "scope:a")
	if _, err := tm.Get(t.Context()); err == nil {
		t.Fatal("expected an error on a 401 token response")
	}
}
```

- [ ] **Step 2: Run to confirm failure**

```bash
cd ~/Documents/Projects/Ctech/ctech-go-common
go test ./oauth2client/... -v
```

Expected: FAIL — package doesn't exist.

- [ ] **Step 3: Implement, copying the existing logic verbatim (behavior must not change)**

`ctech-go-common/oauth2client/client.go`:

```go
// Package oauth2client provides a cached OAuth2 client_credentials token
// fetcher, shared by every CTech Go service that calls another service's M2M
// token endpoint (previously duplicated independently in ctech-wallet's
// kycclient and walletclient packages).
package oauth2client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// TokenManager fetches and caches an OAuth2 client_credentials bearer token,
// refreshing 30 seconds before its reported expiry.
type TokenManager struct {
	client       *http.Client
	tokenURL     string
	clientID     string
	clientSecret string
	scope        string

	mu     sync.Mutex
	token  string
	expiry time.Time
}

// New builds a TokenManager. tokenURL is the full token endpoint URL.
func New(httpClient *http.Client, tokenURL, clientID, clientSecret, scope string) *TokenManager {
	return &TokenManager{client: httpClient, tokenURL: tokenURL, clientID: clientID, clientSecret: clientSecret, scope: scope}
}

// Get returns a cached valid bearer token, fetching a new one if absent or
// close to expiry.
func (t *TokenManager) Get(ctx context.Context) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.token != "" && time.Now().Before(t.expiry) {
		return t.token, nil
	}
	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {t.clientID},
		"client_secret": {t.clientSecret},
		"scope":         {t.scope},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := t.client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("oauth2client: token endpoint status %d: %s", resp.StatusCode, string(raw))
	}
	var tr struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(raw, &tr); err != nil {
		return "", err
	}
	t.token = tr.AccessToken
	t.expiry = time.Now().Add(time.Duration(tr.ExpiresIn-30) * time.Second)
	return t.token, nil
}
```

- [ ] **Step 4: Run to confirm pass**

```bash
go test ./oauth2client/... -v
```

Expected: PASS.

- [ ] **Step 5: Update the README table**

Add a row to `ctech-go-common/README.md`'s package table:

```markdown
| `oauth2client` | Cached OAuth2 client_credentials token fetcher, shared across M2M callers |
```

- [ ] **Step 6: Tag and release `ctech-go-common`**

```bash
cd ~/Documents/Projects/Ctech/ctech-go-common
git add oauth2client README.md
git commit -m "feat(oauth2client): extract cached client_credentials token fetcher"
git tag v1.1.0
git push origin main --tags
```

Confirm the actual next version number against the current latest tag first
(`git tag --sort=-v:refname | head -1`) — `v1.1.0` above assumes the current latest is `v1.0.x`.

- [ ] **Step 7: Bump `api`'s dependency and switch `kycclient` over**

```bash
cd ~/Documents/Projects/Ctech/ctech-wallet/api
go get gopkg.aoctech.app/api-commons@v1.1.0
```

In `api/internal/kycclient/kycclient.go`, delete the local `tokenManager` type and its `get`
method entirely, replace the `Client` struct's `tokens *tokenManager` field with
`tokens *oauth2client.TokenManager`, and in `New`:

```go
    return &Client{
base: base,
http: httpClient,
tokens: oauth2client.New(httpClient, base+pathToken, cfg.WalletClientID, cfg.WalletClientSecret, scopeKYC),
}
```

And in `authedRequest`, change `c.tokens.get(ctx)` to `c.tokens.Get(ctx)`. Add the import
`"gopkg.aoctech.app/api-commons/oauth2client"`.

- [ ] **Step 8: Run `api`'s tests**

```bash
go build ./... && go test ./... -v
```

Expected: PASS (kycclient's existing tests, if any, must still pass against the same observable
behavior — only the internal token-fetch implementation moved).

- [ ] **Step 9: Add `api-commons` to `pix-gateway` and switch `walletclient` over**

```bash
cd ~/Documents/Projects/Ctech/ctech-wallet/pix-gateway
go get gopkg.aoctech.app/api-commons@v1.1.0
```

In `pix-gateway/internal/walletclient/walletclient.go`, delete the local `tokenManager` type and
its `get` method, replace the field holding it with `*oauth2client.TokenManager`, and update the
constructor call and the one call site using `.get(ctx)` → `.Get(ctx)`, same pattern as Step 7.

- [ ] **Step 10: Run `pix-gateway`'s tests**

```bash
go build ./... && go test ./... -v
```

Expected: PASS.

- [ ] **Step 11: Commit the `ctech-wallet` side**

```bash
cd ~/Documents/Projects/Ctech/ctech-wallet
git add api/go.mod api/go.sum api/internal/kycclient pix-gateway/go.mod pix-gateway/go.sum pix-gateway/internal/walletclient
git commit -m "refactor(api,pix-gateway): use shared oauth2client.TokenManager

Both kycclient and walletclient hand-rolled an identical M2M
client_credentials token fetcher. Both now use
api-commons/oauth2client instead."
```

---

### Task 9: testing gap — remaining concurrency tests (Credit/Debit, FundGame, PurchaseSandbox)

The audit's testing gap ("no test spins up real goroutines against DynamoDB-local") is only
half-closed by Task 1's `Withdraw` test. Close the rest — these are additive tests only, no
production code changes.

**Files:**

- Test: `api/tests/integration/wallet_test.go`

**Interfaces:** consumes only existing, already-implemented service methods (`Credit`, `Debit`,
`FundGame`, `PurchaseSandbox`) — no new production code.

- [ ] **Step 1: Write the three tests**

```go
func TestConcurrentCreditSameIdempotencyKeyAppliesOnce(t *testing.T) {
ctx := context.Background()
repo := repositories.NewWalletRepository(db, testConfig(tablePrefix))
userID := "u-" + id.New()
real, err := repo.EnsureRealWallet(ctx, userID)
if err != nil {
t.Fatal(err)
}

const n = 10
var wg sync.WaitGroup
errs := make([]error, n)
for i := 0; i < n; i++ {
wg.Add(1)
go func (i int) {
defer wg.Done()
_, _, errs[i] = repo.Credit(ctx, repositories.Mutation{
WalletID: real.WalletID, Amount: 1000, EntryType: wallet.EntryDeposit,
Ref: "concurrent-credit", IdempotencyKey: "credit-race#" + userID, ReqHash: "same-hash",
})
}(i)
}
wg.Wait()
for i, err := range errs {
if err != nil {
t.Fatalf("goroutine %d: %v", i, err)
}
}
w, err := repo.GetWallet(ctx, real.WalletID)
if err != nil {
t.Fatal(err)
}
if w.Balance != 1000 {
t.Fatalf("balance = %d, want 1000 (double-credit if higher)", w.Balance)
}
}

func TestConcurrentFundGameSameIdempotencyKeyAppliesOnce(t *testing.T) {
ctx := context.Background()
repo := repositories.NewWalletRepository(db, testConfig(tablePrefix))
svc := services.NewWalletService(repo, repositories.NewUserRepository(db, testConfig(tablePrefix)),
repositories.NewAuditRepository(db, testConfig(tablePrefix)), lock.NewLocker(cache.NewMemoryBackend(16)),
pix.NewFake(), &fakeKYC{cpf: "12345678901"})
userID := "u-" + id.New()
if _, _, err := repo.EnsureGamblingWallets(ctx, userID); err != nil {
t.Fatal(err)
}
real, err := repo.EnsureRealWallet(ctx, userID)
if err != nil {
t.Fatal(err)
}
if _, err := repo.Credit(ctx, repositories.Mutation{
WalletID: real.WalletID, Amount: 50000, EntryType: wallet.EntryDeposit,
Ref: "seed", IdempotencyKey: "seed#" + userID, ReqHash: "seed",
}); err != nil {
t.Fatal(err)
}

const n = 10
var wg sync.WaitGroup
errs := make([]error, n)
for i := 0; i < n; i++ {
wg.Add(1)
go func (i int) {
defer wg.Done()
_, _, errs[i] = svc.FundGame(ctx, userID, 5000, "fund-race")
}(i)
}
wg.Wait()
for i, err := range errs {
if err != nil {
t.Fatalf("goroutine %d: %v", i, err)
}
}
realAfter, err := repo.GetWallet(ctx, real.WalletID)
if err != nil {
t.Fatal(err)
}
if realAfter.Balance != 45000 {
t.Fatalf("real balance = %d, want 45000 (double-fund if lower)", realAfter.Balance)
}
}

func TestConcurrentPurchaseSandboxSameIdempotencyKeyAppliesOnce(t *testing.T) {
ctx := context.Background()
repo := repositories.NewWalletRepository(db, testConfig(tablePrefix))
svc := services.NewWalletService(repo, repositories.NewUserRepository(db, testConfig(tablePrefix)),
repositories.NewAuditRepository(db, testConfig(tablePrefix)), lock.NewLocker(cache.NewMemoryBackend(16)),
pix.NewFake(), &fakeKYC{cpf: "12345678901"})
userID := "u-" + id.New()
game, sandbox, err := repo.EnsureGamblingWallets(ctx, userID)
if err != nil {
t.Fatal(err)
}
_ = sandbox
if _, err := repo.Credit(ctx, repositories.Mutation{
WalletID: game.WalletID, Amount: 20000, EntryType: wallet.EntryGameFundCredit,
Ref: "seed", IdempotencyKey: "seed-game#" + userID, ReqHash: "seed",
}); err != nil {
t.Fatal(err)
}

const n = 10
var wg sync.WaitGroup
errs := make([]error, n)
for i := 0; i < n; i++ {
wg.Add(1)
go func (i int) {
defer wg.Done()
_, _, errs[i] = svc.PurchaseSandbox(ctx, userID, 5000, "purchase-race")
}(i)
}
wg.Wait()
for i, err := range errs {
if err != nil {
t.Fatalf("goroutine %d: %v", i, err)
}
}
gameAfter, err := repo.GetWallet(ctx, game.WalletID)
if err != nil {
t.Fatal(err)
}
if gameAfter.Balance != 15000 {
t.Fatalf("game balance = %d, want 15000 (double-purchase if lower)", gameAfter.Balance)
}
}
```

Confirm `repo.EnsureGamblingWallets(ctx, userID) (game, sandbox *wallet.Wallet, err error)`'s
exact return order against `api/internal/repositories/wallet.go` before relying on it (used
elsewhere in this same file per the `Repo` interface listed in Task 2).

- [ ] **Step 2: Run — these should all pass immediately**

```bash
DYNAMODB_ENDPOINT=http://localhost:8000 go test ./tests/integration/... -run 'TestConcurrent' -tags=integration -v
```

Expected: PASS — `Credit`/`Debit`/`FundGame`/`PurchaseSandbox` already use the same
co-transactional idempotency-guard pattern that Task 1 fixed `Withdraw` to match, so no
production code change is expected here. If any of these fail, that is a **new** finding beyond
this audit's scope — stop and report it rather than silently patching it inside this test-only
task.

- [ ] **Step 3: Commit**

```bash
git add api/tests/integration/wallet_test.go
git commit -m "test(api): add concurrent-goroutine coverage for Credit/FundGame/PurchaseSandbox

Closes the remaining part of the audit's testing gap — Withdraw's
concurrency test was added in the F2 fix; these three prove the
already-correct co-transactional idempotency pattern holds under
real contention too."
```

---

## Explicitly out of scope (do not build without a follow-up spec)

- **F6's Inter-statement cross-check.** Needs Inter's actual PIX account statement/extract API
  request/response shape researched first (endpoint, auth, pagination, field names) — none of
  that is discoverable from this repo, and inventing a plausible-looking API contract here would
  be worse than leaving the gap explicit. Spike this against Inter's developer docs/sandbox before
  writing a plan for it.
- **F7 (aggregate/velocity deposit limits)** and **F8 (ledger contra-account for cash-in-transit)**
  — the spec itself defers these ("no design decision needed now, flag for a future spec"). Do not
  implement ahead of that design work.

## Task ordering

Tasks 1–2 (F2, F3) are P0 — do these first, in either order (no dependency between them). Tasks
3–5 (F4, F5, F6) are P1 — no dependency between them or on 1–2; can be done in parallel by
different engineers. Task 6 (observability) has no dependency on anything else. Task 9 (remaining
concurrency tests) has no dependency on 3–8 but should follow Task 1, since it reuses the same
goroutine/`WaitGroup` pattern Task 1 establishes. Tasks 7–8 (cross-cutting extractions) touch the
most files outside `api/internal/services` and `api/internal/repositories` that the other tasks
also touch (`rpc_types.go`'s consumer, `kycclient`) — do them **last**, after 1–6 and 9 have
landed, to avoid merge conflicts against tasks still touching those same packages.
