# Poker Integration Fixes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a read-only M2M endpoint reporting `game`+`sandbox` balances, and let sandbox credit/debit
(poker's daily reward) work for users who haven't activated gambling, without weakening the `game`
wallet's KYC/consent gate.

**Architecture:** `sandboxOp` (the shared path behind `CreditSandbox`/`DebitSandbox`) stops requiring full
gambling activation and instead lazily creates the sandbox wallet row on first use via a new
`EnsureSandboxWallet` repo method (mirrors the existing `EnsureRealWallet`). All `game`-touching operations
(`FundGame`, `ReturnFromGame`, `PurchaseSandbox`, hold/cashout) keep calling `requireActivated` unchanged.
A new `GET /internal/wallet/balance/:user_id` route, gated by a new scope, reads `game`+`sandbox` balances
via `LoadWallets` (nil wallet → 0), no wallet creation on read.

**Tech Stack:** Go, Fiber v3, DynamoDB (aws-sdk-go-v2), `go test` (stdlib, no testify).

## Global Constraints

- All amounts are integer centavos, never float (root `CLAUDE.md`).
- All string scopes/constants named, never inline literals (root `CLAUDE.md`, `api/CLAUDE.md`).
- Route handlers: parse request, call ONE service method, respond via `sendProblem`/`c.JSON` — no business
  logic in the route layer (`api/CLAUDE.md` Layer Separation).
- Repository layer: DynamoDB access only, no business logic (`api/CLAUDE.md` Layer Separation).
- Every core service function needs both a unit test and an integration test (`api/CLAUDE.md` Testing
  table).
- `game`/`real → game` gambling-activation gate (`requireActivated`) MUST NOT be weakened by this work —
  only the sandbox-only path changes.

---

## File Structure

- Modify `internal/middleware/scope.go` — add `ScopeWalletBalance` constant.
- Modify `internal/repositories/wallet.go` — add `EnsureSandboxWallet` (mirrors `EnsureRealWallet`).
- Modify `internal/services/wallet.go` — add `EnsureSandboxWallet` to the `Repo` interface; change
  `sandboxOp` to call it instead of `requireActivated`; add `WalletBalances` struct + `BalancesFor` method.
- Modify `internal/services/wallet_test.go` — add `EnsureSandboxWallet` to `stubRepo` (with a
  `sandboxCreated` tracking field so `LoadWallets` reflects lazy creation correctly under
  `notActivated=true`); add unit tests.
- Modify `internal/api/v1/router.go` — register `GET /internal/wallet/balance/:user_id`.
- Modify `internal/api/v1/internal.go` — add `walletBalance` handler.
- Modify `internal/api/v1/internal_test.go` — add a route-registration test (matches existing style).
- Modify `tests/integration/wallet_test.go` — add integration tests for lazy sandbox creation, the
  now-unblocked sandbox ops, the still-blocked `game` ops, and the new balance endpoint's service method.
- Modify docs (no tests, pure text): `docs/specs/2026-07-12-three-wallet-topology-design.md`, root
  `CLAUDE.md`, `api/CLAUDE.md`, `api/ENDPOINTS.md`.

---

### Task 1: Repo layer — `EnsureSandboxWallet`

**Files:**
- Modify: `internal/repositories/wallet.go` (add method near `EnsureRealWallet`, `internal/repositories/wallet.go:121-127`)
- Test: `tests/integration/wallet_test.go` (new tests, appended near `TestEnsureGamblingWalletsIsAtomicAndIdempotent`, `tests/integration/wallet_test.go:196-225`)

**Interfaces:**
- Produces: `func (r *WalletRepository) EnsureSandboxWallet(ctx context.Context, userID string) (*wallet.Wallet, error)` — later tasks (2, 3) call this through the service's `Repo` interface.

- [ ] **Step 1: Write the failing integration test**

Append to `tests/integration/wallet_test.go` (build tag `//go:build integration` already at the top of the
file, needs `docker compose -f docker-compose.test.yml up -d` running):

```go
func TestEnsureSandboxWalletIsIdempotentAndSkipsActivation(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := "u-" + id.New()

	// No EnsureRealWallet, no ActivateGambling — sandbox must still be creatable.
	sandbox, err := h.repo.EnsureSandboxWallet(ctx, user)
	if err != nil {
		t.Fatalf("EnsureSandboxWallet: %v", err)
	}
	if sandbox == nil || sandbox.Type != wallet.TypeSandbox || sandbox.Balance != 0 {
		t.Fatalf("sandbox = %+v, want zero-balance type %q", sandbox, wallet.TypeSandbox)
	}

	// game must still be absent — this call must never create it.
	_, game, _, err := h.repo.LoadWallets(ctx, user)
	if err != nil {
		t.Fatalf("LoadWallets: %v", err)
	}
	if game != nil {
		t.Fatalf("EnsureSandboxWallet must not create game, got %+v", game)
	}

	// Idempotent: a second call converges on the SAME wallet, never a second one.
	sandbox2, err := h.repo.EnsureSandboxWallet(ctx, user)
	if err != nil {
		t.Fatalf("EnsureSandboxWallet replay: %v", err)
	}
	if sandbox2.WalletID != sandbox.WalletID {
		t.Fatalf("replay created a new wallet: %s vs %s", sandbox2.WalletID, sandbox.WalletID)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `docker compose -f docker-compose.test.yml up -d && make test-integration`
Expected: FAIL — `h.repo.EnsureSandboxWallet undefined (type *repositories.WalletRepository has no field or method EnsureSandboxWallet)`

- [ ] **Step 3: Implement `EnsureSandboxWallet`**

In `internal/repositories/wallet.go`, immediately after `EnsureRealWallet` (`internal/repositories/wallet.go:121-127`):

```go
// EnsureSandboxWallet lazily creates the caller's sandbox wallet if it does not
// already exist. Unlike EnsureGamblingWallets, this does NOT require gambling
// activation — sandbox is play currency and is created independently of the
// game wallet's KYC/consent gate.
func (r *WalletRepository) EnsureSandboxWallet(ctx context.Context, userID string) (*wallet.Wallet, error) {
	byType, err := r.EnsureWalletsOfType(ctx, userID, wallet.TypeSandbox)
	if err != nil {
		return nil, err
	}
	return byType[wallet.TypeSandbox], nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `make test-integration`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/repositories/wallet.go tests/integration/wallet_test.go
git commit -m "feat(wallet): add EnsureSandboxWallet for KYC-independent sandbox creation"
```

---

### Task 2: Service layer — decouple `sandboxOp` from `requireActivated`

**Files:**
- Modify: `internal/services/wallet.go` (`Repo` interface around `internal/services/wallet.go:52-54`; `sandboxOp` at `internal/services/wallet.go:777-788`)
- Modify: `internal/services/wallet_test.go` (`stubRepo` struct/methods, `internal/services/wallet_test.go:20-81`)
- Test: `internal/services/wallet_test.go` (new unit tests)

**Interfaces:**
- Consumes: `Repo.EnsureSandboxWallet(ctx, userID) (*wallet.Wallet, error)` from Task 1.
- Produces: `sandboxOp` no longer returns `problem.GamblingNotActivated()` — later tasks (poker) rely on this.

- [ ] **Step 1: Write the failing unit tests**

First, update `stubRepo` in `internal/services/wallet_test.go` so the stub can express "sandbox exists but
game doesn't" (needed for both this task's tests and to keep `TestPurchaseSandboxRequiresActivation` /
`TestHoldGameRequiresActivation` passing unchanged). Replace the `notActivated` field's `LoadWallets`/new
`EnsureSandboxWallet` handling:

```go
// internal/services/wallet_test.go:20-40 — add one field to the struct:
type stubRepo struct {
	real, game, sandbox wallet.Wallet
	notActivated        bool // no game/sandbox wallets — user never opted in
	sandboxCreated       bool // sandbox created via EnsureSandboxWallet despite notActivated
	deposit             *wallet.PixDeposit
	withdrawals         map[string]*wallet.Withdrawal
	depositStatus       string
	depositE2E          string
	depositPayerCPF     string
	depositPayerName    string
	creditCalls         []repositories.Mutation
	debitCalls          []repositories.Mutation
	debitErr            error
	debitFeeErr         error
	debitFeeCalled      bool
	transferErr         error
	transferCalled      bool
	holds               map[string]*wallet.Hold
	createHoldErr       error
	staleHolds          []wallet.Hold
	depositIdem         map[string]depositIdemGuard
}
```

Replace `LoadWallets` (`internal/services/wallet_test.go:76-81`) and add `EnsureSandboxWallet` right after it:

```go
func (s *stubRepo) LoadWallets(_ context.Context, _ string) (*wallet.Wallet, *wallet.Wallet, *wallet.Wallet, error) {
	if s.notActivated {
		var sandbox *wallet.Wallet
		if s.sandboxCreated {
			sandbox = &s.sandbox
		}
		return &s.real, nil, sandbox, nil
	}
	return &s.real, &s.game, &s.sandbox, nil
}
func (s *stubRepo) EnsureSandboxWallet(_ context.Context, _ string) (*wallet.Wallet, error) {
	s.sandboxCreated = true
	return &s.sandbox, nil
}
```

Add `EnsureSandboxWallet` to the `Repo` interface in `internal/services/wallet.go`, right after
`EnsureRealWallet` (around `internal/services/wallet.go:52-54`):

```go
	EnsureRealWallet(ctx context.Context, userID string) (*wallet.Wallet, error)
	EnsureSandboxWallet(ctx context.Context, userID string) (*wallet.Wallet, error)
```

Now add the tests, appended near `TestPurchaseSandboxRequiresActivation` (`internal/services/wallet_test.go:553-566`):

```go
func TestCreditSandboxWorksWithoutActivation(t *testing.T) {
	repo := newStubRepo()
	repo.notActivated = true
	svc := newSvc(repo, &stubLocker{}, pix.NewFake(), &stubKYC{rec: &kycclient.KYC{}})

	entry, err := svc.CreditSandbox(context.Background(), "u1", 500, "idem-1", "daily-reward")
	if err != nil {
		t.Fatalf("CreditSandbox without activation = %v, want success", err)
	}
	if entry.WalletID != "w-sand" || entry.Amount != 500 {
		t.Errorf("entry = %+v, want wallet w-sand amount 500", entry)
	}
	if !repo.sandboxCreated {
		t.Fatal("expected sandbox wallet to be lazily created")
	}
}

func TestDebitSandboxWorksWithoutActivation(t *testing.T) {
	repo := newStubRepo()
	repo.notActivated = true
	svc := newSvc(repo, &stubLocker{}, pix.NewFake(), &stubKYC{rec: &kycclient.KYC{}})

	entry, err := svc.DebitSandbox(context.Background(), "u1", 200, "idem-2", "bet")
	if err != nil {
		t.Fatalf("DebitSandbox without activation = %v, want success", err)
	}
	if entry.WalletID != "w-sand" || entry.Amount != 200 {
		t.Errorf("entry = %+v, want wallet w-sand amount 200", entry)
	}
}

// FundGame/ReturnFromGame/PurchaseSandbox/HoldGame remain gated — this change
// must not weaken the game wallet's KYC/consent gate.
func TestFundGameStillRequiresActivation(t *testing.T) {
	repo := newStubRepo()
	repo.notActivated = true
	users := &stubUserRepo{user: &wallet.User{GameLimits: &wallet.GameLimits{Daily: 10000, Weekly: 10000, Monthly: 10000}}}
	svc := NewWalletService(repo, users, &stubAudit{}, &stubLocker{}, pix.NewFake(), &stubKYC{rec: &kycclient.KYC{}})

	_, _, err := svc.FundGame(context.Background(), "u1", 3000, "idem-3")
	isProblem(t, err, problem.TypeGamblingNotActivated)
}

func TestReturnFromGameStillRequiresActivation(t *testing.T) {
	repo := newStubRepo()
	repo.notActivated = true
	svc := newSvc(repo, &stubLocker{}, pix.NewFake(), &stubKYC{rec: &kycclient.KYC{}})

	_, _, err := svc.ReturnFromGame(context.Background(), "u1", 3000, "idem-4")
	isProblem(t, err, problem.TypeGamblingNotActivated)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/services/... -run 'TestCreditSandboxWorksWithoutActivation|TestDebitSandboxWorksWithoutActivation|TestFundGameStillRequiresActivation|TestReturnFromGameStillRequiresActivation' -v`
Expected: FAIL — `TestCreditSandboxWorksWithoutActivation`/`TestDebitSandboxWorksWithoutActivation` get
`gambling-not-activated` instead of success (the other two should already pass — they document unchanged
behavior; if the build fails first because `EnsureSandboxWallet` isn't wired everywhere yet, that's also
expected at this point).

- [ ] **Step 3: Implement the `sandboxOp` change**

In `internal/services/wallet.go`, replace lines 777-781:

```go
func (s *WalletService) sandboxOp(ctx context.Context, userID string, amount int64, idemKey, reason, entryType string, credit bool) (*wallet.LedgerEntry, error) {
	sandbox, err := s.repo.EnsureSandboxWallet(ctx, userID)
	if err != nil {
		return nil, err
	}
```

(the rest of the function — `lock.Acquire`, the `Mutation`, etc., `internal/services/wallet.go:782` onward
— is unchanged).

Implement `EnsureSandboxWallet` on `*WalletRepository` was already done in Task 1 — this task only wires the
interface and the call site.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/services/... -v`
Expected: PASS (full package, to confirm no other test broke — `TestPurchaseSandboxRequiresActivation` and
`TestHoldGameRequiresActivation` in particular must still pass unchanged).

- [ ] **Step 5: Commit**

```bash
git add internal/services/wallet.go internal/services/wallet_test.go
git commit -m "feat(wallet): sandbox credit/debit no longer requires gambling activation"
```

---

### Task 3: Balance-read service method

**Files:**
- Modify: `internal/services/wallet.go` (new `WalletBalances` struct + `BalancesFor` method, placed near `GameEligibilityFor` in `internal/services/responsible.go:227-250` for consistency — put it in `wallet.go` since it belongs to `WalletService`'s core wallet concerns, not gambling-responsibility concerns)
- Test: `internal/services/wallet_test.go`

**Interfaces:**
- Consumes: `Repo.LoadWallets(ctx, userID) (real, game, sandbox *wallet.Wallet, err error)` (existing).
- Produces: `type WalletBalances struct { GameBalance, SandboxBalance int64 }` (JSON tags `game_balance`,
  `sandbox_balance`) and `func (s *WalletService) BalancesFor(ctx context.Context, userID string) (*WalletBalances, error)` — Task 4's HTTP handler calls this.

- [ ] **Step 1: Write the failing unit tests**

Append to `internal/services/wallet_test.go`:

```go
func TestBalancesForReturnsZeroForBrandNewUser(t *testing.T) {
	repo := newStubRepo()
	repo.notActivated = true
	svc := newSvc(repo, &stubLocker{}, pix.NewFake(), &stubKYC{rec: &kycclient.KYC{}})

	got, err := svc.BalancesFor(context.Background(), "u1")
	if err != nil {
		t.Fatalf("BalancesFor: %v", err)
	}
	if got.GameBalance != 0 || got.SandboxBalance != 0 {
		t.Fatalf("balances = %+v, want zero/zero for a user with no wallets", got)
	}
}

func TestBalancesForReturnsActualBalances(t *testing.T) {
	repo := newStubRepo()
	repo.game.Balance = 1500
	repo.sandbox.Balance = 30000
	svc := newSvc(repo, &stubLocker{}, pix.NewFake(), &stubKYC{rec: &kycclient.KYC{}})

	got, err := svc.BalancesFor(context.Background(), "u1")
	if err != nil {
		t.Fatalf("BalancesFor: %v", err)
	}
	if got.GameBalance != 1500 || got.SandboxBalance != 30000 {
		t.Fatalf("balances = %+v, want game=1500 sandbox=30000", got)
	}
}

func TestBalancesForSandboxOnlyUser(t *testing.T) {
	repo := newStubRepo()
	repo.notActivated = true
	repo.sandboxCreated = true
	repo.sandbox.Balance = 700
	svc := newSvc(repo, &stubLocker{}, pix.NewFake(), &stubKYC{rec: &kycclient.KYC{}})

	got, err := svc.BalancesFor(context.Background(), "u1")
	if err != nil {
		t.Fatalf("BalancesFor: %v", err)
	}
	if got.GameBalance != 0 || got.SandboxBalance != 700 {
		t.Fatalf("balances = %+v, want game=0 (never activated) sandbox=700", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/services/... -run 'TestBalancesFor' -v`
Expected: FAIL — `svc.BalancesFor undefined`

- [ ] **Step 3: Implement `BalancesFor`**

In `internal/services/wallet.go`, add near the top-level type declarations (or directly above
`requireActivated`, `internal/services/wallet.go:637`):

```go
// WalletBalances is the M2M balance snapshot a skill game reads to show a
// user how much they hold. real is deliberately excluded — poker never
// touches real money directly.
type WalletBalances struct {
	GameBalance    int64 `json:"game_balance"`
	SandboxBalance int64 `json:"sandbox_balance"`
}

// BalancesFor reports game+sandbox balances for a user. Read-only — it never
// creates a wallet; a wallet that doesn't exist yet reports as balance 0,
// which is the correct value (the user holds nothing there), not an error.
func (s *WalletService) BalancesFor(ctx context.Context, userID string) (*WalletBalances, error) {
	_, game, sandbox, err := s.repo.LoadWallets(ctx, userID)
	if err != nil {
		return nil, err
	}
	b := &WalletBalances{}
	if game != nil {
		b.GameBalance = game.Balance
	}
	if sandbox != nil {
		b.SandboxBalance = sandbox.Balance
	}
	return b, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/services/... -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/services/wallet.go internal/services/wallet_test.go
git commit -m "feat(wallet): add BalancesFor service method for M2M balance reads"
```

---

### Task 4: HTTP layer — new route, scope, handler

**Files:**
- Modify: `internal/middleware/scope.go` (new constant, `internal/middleware/scope.go:27` area)
- Modify: `internal/api/v1/router.go` (new route group, `internal/api/v1/router.go:88-93` area)
- Modify: `internal/api/v1/internal.go` (new handler)
- Modify: `internal/api/v1/internal_test.go` (new route-registration test)

**Interfaces:**
- Consumes: `middleware.RequireScope(scope string) fiber.Handler` (existing); `h.svc.BalancesFor(ctx, userID) (*services.WalletBalances, error)` from Task 3.
- Produces: `GET /internal/wallet/balance/:user_id`, scope `internal:wallet:balance`.

- [ ] **Step 1: Write the failing route test**

Append to `internal/api/v1/internal_test.go` (matches `TestGameStatusRouteShape`'s GET-route style,
`internal/api/v1/responsible_test.go:15-29`):

```go
// TestWalletBalanceRouteRegistered proves /internal/wallet/balance/:user_id is
// wired to walletBalance, mirroring TestGameStatusRouteShape's style.
func TestWalletBalanceRouteRegistered(t *testing.T) {
	app := fiber.New()
	app.Use(recover.New())
	h := &handlers{}
	app.Get("/internal/wallet/balance/:user_id", func(c fiber.Ctx) error {
		return h.walletBalance(c)
	})
	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/internal/wallet/balance/u1", nil))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode == http.StatusNotFound {
		t.Fatal("route not registered")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/api/v1/... -run TestWalletBalanceRouteRegistered -v`
Expected: FAIL — `h.walletBalance undefined`

- [ ] **Step 3: Implement the scope constant, handler, and route**

In `internal/middleware/scope.go`, append to the const block (after `ScopeWalletGameStatus`,
`internal/middleware/scope.go:27`):

```go
	// ScopeWalletBalance reads a user's game+sandbox balance (real excluded) —
	// consumed by skill games to show the user how much they hold. Read-only,
	// deliberately separate from ScopeWalletGameStatus (eligibility, not balance).
	ScopeWalletBalance = "internal:wallet:balance"
```

In `internal/api/v1/internal.go`, add the handler (mirrors `gameStatus`,
`internal/api/v1/internal.go:111-117`):

```go
// walletBalance reports a user's game+sandbox balance (M2M, scope
// internal:wallet:balance). real is never exposed here.
func (h *handlers) walletBalance(c fiber.Ctx) error {
	b, err := h.svc.BalancesFor(c.Context(), c.Params("user_id"))
	if err != nil {
		return sendProblem(c, err)
	}
	return c.JSON(b)
}
```

In `internal/api/v1/router.go`, add the group right after the `gw` (game) group
(`internal/api/v1/router.go:88-93`):

```go
	// Balance read for skill games (ctech-poker). Read-only, game+sandbox only.
	bg := internal.Group("/wallet/balance")
	bg.Get("/:user_id", middleware.RequireScope(middleware.ScopeWalletBalance), h.walletBalance)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/api/v1/... -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/middleware/scope.go internal/api/v1/router.go internal/api/v1/internal.go internal/api/v1/internal_test.go
git commit -m "feat(wallet): add GET /internal/wallet/balance/:user_id M2M endpoint"
```

---

### Task 5: Integration tests — full stack proof

**Files:**
- Modify: `tests/integration/wallet_test.go`

**Interfaces:**
- Consumes: `h.svc.CreditSandbox`/`DebitSandbox`/`FundGame`/`ReturnFromGame`/`BalancesFor` (existing +
  Task 3), `h.repo.EnsureSandboxWallet` (Task 1).

- [ ] **Step 1: Write the tests**

Append to `tests/integration/wallet_test.go`, near `TestSandboxDebitNoNegative`
(`tests/integration/wallet_test.go:459-465`):

```go
// The reported poker bug: a daily-reward sandbox credit must work for a user
// who never activated gambling.
func TestCreditSandboxSucceedsWithoutActivation(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := "u-" + id.New() // no EnsureRealWallet, no ActivateGambling at all

	entry, err := h.svc.CreditSandbox(ctx, user, 500, "idem-"+id.New(), "daily-reward")
	if err != nil {
		t.Fatalf("CreditSandbox without activation: %v", err)
	}
	if entry.Amount != 500 {
		t.Fatalf("entry.Amount = %d, want 500", entry.Amount)
	}

	_, game, sandbox, err := h.repo.LoadWallets(ctx, user)
	if err != nil {
		t.Fatalf("LoadWallets: %v", err)
	}
	if game != nil {
		t.Fatal("CreditSandbox must never create the game wallet")
	}
	if sandbox == nil || sandbox.Balance != 500 {
		t.Fatalf("sandbox = %+v, want balance 500", sandbox)
	}
}

// DebitSandbox on a never-activated, never-funded user still respects the
// no-negative-balance invariant — it gets insufficient-balance, not
// gambling-not-activated.
func TestDebitSandboxOnNeverActivatedUserRespectsBalanceFloor(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := "u-" + id.New()

	_, err := h.svc.DebitSandbox(ctx, user, 500, "round-1", "bet")
	wantProblem(t, err, problem.TypeInsufficientBalance)
}

// The game wallet's activation gate must remain fully intact.
func TestFundGameAndReturnFromGameStillGatedWithoutActivation(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := "u-" + id.New()

	_, _, err := h.svc.FundGame(ctx, user, 3000, "idem-"+id.New())
	wantProblem(t, err, problem.TypeGamblingNotActivated)

	_, _, err = h.svc.ReturnFromGame(ctx, user, 3000, "idem-"+id.New())
	wantProblem(t, err, problem.TypeGamblingNotActivated)
}

func TestBalancesForReflectsRealBalances(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := fundedAndActivated(t, h, 10000)

	if _, _, err := h.svc.FundGame(ctx, user, 4000, "idem-"+id.New()); err != nil {
		t.Fatalf("FundGame: %v", err)
	}
	if _, _, err := h.svc.PurchaseSandbox(ctx, user, 1000, "idem-"+id.New()); err != nil {
		t.Fatalf("PurchaseSandbox: %v", err)
	}

	got, err := h.svc.BalancesFor(ctx, user)
	if err != nil {
		t.Fatalf("BalancesFor: %v", err)
	}
	if got.GameBalance != 3000 {
		t.Fatalf("GameBalance = %d, want 3000 (4000 funded - 1000 spent)", got.GameBalance)
	}
	if got.SandboxBalance != 10000 {
		t.Fatalf("SandboxBalance = %d, want 10000 (1000¢ x 10 credits/centavo)", got.SandboxBalance)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `docker compose -f docker-compose.test.yml up -d && make test-integration`
Expected: FAIL on `TestCreditSandboxSucceedsWithoutActivation` and
`TestDebitSandboxOnNeverActivatedUserRespectsBalanceFloor` if run against pre-Task-2 code (they should
already pass at this point in the plan since Tasks 1-4 are done — this step is a sanity confirmation the
tests are meaningful; if all pass immediately, skip to Step 4 and note it in the commit).

- [ ] **Step 3: (No implementation change expected — Tasks 1-4 already cover this.)**

If any test fails here, it means something in Tasks 1-4 was missed — go back and fix the relevant task
before proceeding.

- [ ] **Step 4: Run tests to verify they pass**

Run: `make test-integration`
Expected: PASS — full suite, confirming `TestSandboxPurchaseNeverDebitsRealWallet` and all pre-existing
tests are still green.

- [ ] **Step 5: Commit**

```bash
git add tests/integration/wallet_test.go
git commit -m "test(wallet): integration coverage for sandbox activation decouple + balance endpoint"
```

---

### Task 6: Update docs

**Files:**
- Modify: `docs/specs/2026-07-12-three-wallet-topology-design.md` (lines 72, 78)
- Modify: `/home/artur/Documents/Projects/Ctech/ctech-wallet/CLAUDE.md` (invariant #10)
- Modify: `api/CLAUDE.md` (ring-fence section + M2M scope table)
- Modify: `api/ENDPOINTS.md` (new endpoint)

- [ ] **Step 1: Update the three-wallet topology spec**

In `docs/specs/2026-07-12-three-wallet-topology-design.md`, at the language around lines 72 and 78
(currently: *"`game` and `sandbox` do not exist until the user explicitly activates them... created
together, atomically, at activation"*), replace with: `game` requires gambling activation (verified KYC +
addendum) as before; `sandbox` no longer does — it is created either alongside `game` at activation, or
lazily on the first M2M sandbox credit/debit, whichever happens first. Cross-reference
`docs/specs/2026-07-22-poker-integration-fixes-design.md`.

- [ ] **Step 2: Update root CLAUDE.md invariant #10**

Change:
```
10. **Consent is opt-in and auditable.** `game`/`sandbox` do not exist until the user accepts the gambling
    addendum (a document distinct from the terms addendum) with verified KYC. ...
```
to:
```
10. **Consent is opt-in and auditable.** `game` does not exist until the user accepts the gambling
    addendum (a document distinct from the terms addendum) with verified KYC — `sandbox` is play currency
    and is created independently (lazily, on first use), with no KYC/consent requirement of its own. ...
```
(keep the rest of the paragraph — legacy-wallet and audit-trail language — unchanged, it still applies to
`game`).

- [ ] **Step 3: Update api/CLAUDE.md**

In the "The gambling ring-fence" section, apply the same `game`-only correction, and add a row to the M2M
scope table:

```
| `internal:wallet:balance` | `GET .../wallet/balance/:user_id` | read-only, game+sandbox only |
```

- [ ] **Step 4: Update ENDPOINTS.md**

Add the new endpoint entry: `GET /internal/wallet/balance/:user_id`, scope `internal:wallet:balance`,
response `{game_balance, sandbox_balance}` (centavos), following the existing entry format for
`GET /internal/wallet/game/status/:user_id`.

- [ ] **Step 5: Commit**

```bash
git add docs/specs/2026-07-12-three-wallet-topology-design.md ../CLAUDE.md CLAUDE.md ENDPOINTS.md
git commit -m "docs: reflect sandbox activation decouple and new balance endpoint"
```

---

## Completion Checklist

- [ ] `go build ./...` compiles
- [ ] `make test` passes (unit)
- [ ] `make test-integration` passes (needs `docker compose -f docker-compose.test.yml up -d`)
- [ ] `TestSandboxPurchaseNeverDebitsRealWallet` still green, unmodified
- [ ] No duplication introduced — `EnsureSandboxWallet` reuses `EnsureWalletsOfType`, no new repo primitive
- [ ] All new strings are named constants (`ScopeWalletBalance`)
- [ ] All new errors returned via `sendProblem`/`problem.*`
- [ ] Financial Safety Invariants upheld — `game`'s KYC/consent gate is provably unchanged (Task 2 and 5 tests)
- [ ] Docs updated (Task 6)
