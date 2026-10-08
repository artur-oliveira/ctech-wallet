# Mandatory Transaction Description Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Every M2M transaction that reaches the wallet carries a human-readable `description` ("Mesa #abc123, buy-in", "Fatura INV-42"), enforced by the wallet behind a feature flag that is flipped only after `ctech-poker` and `ctech-billing` send real descriptions.

**Architecture:** Three repos, one ordered rollout. (1) The wallet accepts `description` on every M2M route (adding it to game cashout, which lacks it) and gains a `REQUIRE_DESCRIPTION` flag, default OFF. (2) `ctech-poker` and `ctech-billing` start sending descriptions at every wallet call site. (3) After both are deployed, the flag is turned ON per environment. Descriptions stay display-only metadata: outside the idempotency hash, never parsed, never authority for an amount (spec `2026-08-29-transaction-description.md`).

**Tech Stack:** Go (Fiber v3) in all three repos; `caarlos0/env` config in the wallet.

**Spec:** `docs/specs/2026-10-08-wallet-restoration-design.md` (section 2) and `docs/specs/2026-08-29-transaction-description.md`.

## Global Constraints

- DEPLOY ORDER IS A HARD CONSTRAINT: wallet (accepts the field, flag OFF) -> poker and billing -> flag ON. The wallet's `bindJSON` uses `DisallowUnknownFields`, so poker sending `description` on cashout BEFORE the wallet accepts it gets `400`.
- `description` is `required`, 3 to 255 characters after trimming, only when the flag is on. When the flag is off the old optional behavior is unchanged.
- `description` NEVER enters `ReqHash`. Replaying a key with different text returns the original entry, never `409`.
- `reason`/`ref` stay the machine-readable key; `description` is the human sentence. Do not merge them.
- Descriptions are pt-BR (the product language), no leading/trailing spaces, no personal data (no CPF, e-mail or full names), no internal secrets or tokens. Table, hand, invoice and item ids are fine.
- Other repos: before touching ANY of them run `git checkout main && git pull origin main`, then create a branch. One branch and one PR per repo.
- Conventional Commits, no emojis, NO attribution trailers.

## Review Focus

- A whitespace-only description (`"   "`) must be rejected when the flag is on, not accepted because it is non-empty.
- A 256-character description is a `400`, never silently truncated (existing rule).
- Replaying the same `idempotency_key` with a different description returns the original entry and does not create a second ledger row.
- Poker cashout during a retry/reconcile sweep (`cmd/reconcile`, `cmd/tablecleanup`) must also send a description; those binaries are easy to forget because they are not in the request path.
- With the flag OFF, a legacy caller that sends no description keeps working (no regression during the rollout window).

---

## Part A: wallet (`ctech-wallet`)

### Task A1: Config flag and enforcement helper

**Files:**
- Modify: `api/internal/config/config.go` (after `GamblingEnabled`)
- Modify: `api/internal/api/v1/router.go:18-25` (`handlers` struct and `Register`)
- Create: `api/internal/api/v1/description.go`
- Test: `api/internal/api/v1/description_test.go`

**Interfaces:**
- Produces:
  - `config.Config.RequireDescription bool` (env `REQUIRE_DESCRIPTION`, default `false`)
  - `const (DescriptionMinLen = 3)` in `api/internal/api/v1/description.go` (the max stays `wallet.DescriptionMaxLen`)
  - `func (h *handlers) checkDescription(desc string) *problem.Problem` returns `nil` when valid or when the flag is off.

- [ ] **Step 1: Write the failing test**

`api/internal/api/v1/description_test.go`:

```go
package v1

import "testing"

func TestCheckDescription(t *testing.T) {
	on := &handlers{requireDescription: true}
	off := &handlers{requireDescription: false}

	cases := []struct {
		name string
		h    *handlers
		desc string
		bad  bool
	}{
		{"on, empty", on, "", true},
		{"on, spaces only", on, "   ", true},
		{"on, too short", on, "ab", true},
		{"on, ok", on, "Mesa #abc", false},
		{"on, padded ok", on, "  Mesa #abc  ", false},
		{"off, empty", off, "", false},
		{"off, spaces", off, "   ", false},
	}
	for _, tc := range cases {
		if got := tc.h.checkDescription(tc.desc); (got != nil) != tc.bad {
			t.Errorf("%s: problem=%v want bad=%v", tc.name, got, tc.bad)
		}
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd api && go test ./internal/api/v1/ -run CheckDescription -v`
Expected: FAIL (`handlers` has no field `requireDescription`).

- [ ] **Step 3: Implement**

`config.go`, after the `GamblingEnabled` field:

```go
	// RequireDescription makes the `description` field mandatory (3-255 chars
	// after trimming) on every M2M money route. Keep it OFF until ctech-poker and
	// ctech-billing send real descriptions, then turn it on per environment.
	RequireDescription bool `env:"REQUIRE_DESCRIPTION" envDefault:"false"`
```

`router.go`: add the field and set it:

```go
type handlers struct {
	svc                *services.WalletService
	userSvc            *services.UserService
	requireDescription bool
}
```

and in `Register`: `h := &handlers{svc: svc, userSvc: userSvc, requireDescription: cfg.RequireDescription}`.

`api/internal/api/v1/description.go`:

```go
package v1

import (
	"strings"

	"gopkg.aoctech.app/wallet/api/internal/problem"
)

// DescriptionMinLen is the shortest acceptable description once the
// REQUIRE_DESCRIPTION flag is on (after trimming).
const DescriptionMinLen = 3

// checkDescription enforces the mandatory-description rule. A nil result means
// the description is acceptable, or the flag is off (legacy optional behavior).
// The max length is enforced by the DTO validator (`max=255`), so an oversize
// description is a 400, never silently truncated.
func (h *handlers) checkDescription(desc string) *problem.Problem {
	if !h.requireDescription {
		return nil
	}
	if len(strings.TrimSpace(desc)) < DescriptionMinLen {
		return problem.BadRequest("description é obrigatório (mínimo 3 caracteres)")
	}
	return nil
}
```

- [ ] **Step 4: Run to verify it passes, then commit**

Run: `cd api && go test ./internal/api/v1/ -run CheckDescription -v` -> PASS.

```bash
git checkout main && git pull origin main && git checkout -b feat/mandatory-description
git add api && git commit -m "feat(api): add REQUIRE_DESCRIPTION flag and description check helper"
```

(If this plan runs after the Inter-rail branch merged, branch from the updated `main`; the two plans touch different files except `router.go`'s `handlers` struct, which is a trivial merge.)

---

### Task A2: Accept and persist `description` on game cashout

**Files:**
- Modify: `api/internal/api/v1/dto.go:152-158` (`CashoutRequest`)
- Modify: `api/internal/api/v1/internal.go:97` (`cashoutGame`)
- Modify: `api/internal/services/wallet.go` (`CashoutGame` signature and call)
- Modify: `api/internal/repositories/holds.go` (`CashoutHoldsAtomic`)
- Modify: `api/internal/services/wallet.go` `HoldStore` interface
- Modify callers: `api/internal/services/wallet_test.go:797,823,843`, `api/tests/integration/holds_test.go:187,239,267,295`
- Test: `api/tests/integration/holds_test.go`

**Interfaces:**
- Consumes: nothing new.
- Produces: `CashoutGame(ctx, userID string, amount int64, tableRef string, holdIDs []string, idemKey, description string) (*wallet.LedgerEntry, error)` and the ledger entry's `description` set.

- [ ] **Step 1: Write the failing integration test**

Append to `api/tests/integration/holds_test.go`, modeled on the neighbouring cashout test (same setup: activate, fund, hold):

```go
func TestCashoutPersistsDescriptionOutsideIdempotencyHash(t *testing.T) {
	h := newHarness(t)
	user := h.activatedFundedUser("u-desc", 20000)
	hold := h.hold(user, 10000, "table-1", "idem-hold-d")

	e1, err := h.svc.CashoutGame(ctx, user, 10000, "table-1", []string{hold.HoldID}, "idem-cash-d", "Mesa #t1, cashout")
	if err != nil {
		t.Fatal(err)
	}
	if e1.Description != "Mesa #t1, cashout" {
		t.Fatalf("description = %q", e1.Description)
	}
	// Same key, different text: original entry, no conflict, no second credit.
	e2, err := h.svc.CashoutGame(ctx, user, 10000, "table-1", []string{hold.HoldID}, "idem-cash-d", "outro texto")
	if err != nil {
		t.Fatalf("replay with different description must not conflict: %v", err)
	}
	if e2.EntryID != e1.EntryID || e2.Description != "Mesa #t1, cashout" {
		t.Fatalf("replay returned %+v", e2)
	}
}
```

Use the real helper names from `holds_test.go` (read the existing cashout test first and mirror its setup).

- [ ] **Step 2: Run to verify it fails**

Run: `cd api && docker compose -f docker-compose.test.yml up -d && go test -tags integration ./tests/integration/ -run CashoutPersistsDescription -v`
Expected: FAIL (compile error: too many arguments in call to `CashoutGame`).

- [ ] **Step 3: Plumb the parameter**

- `CashoutRequest` gets (same wording as `MovementOpRequest.Description`):

```go
	// Description is the human sentence shown on the statement ("Mesa #abc, cashout").
	// Display metadata only: never parsed, never part of the idempotency hash.
	Description string `json:"description" validate:"max=255"`
```

- `cashoutGame` handler:

```go
	if p := h.checkDescription(body.Description); p != nil {
		return sendProblem(c, p)
	}
	entry, err := h.svc.CashoutGame(c.Context(), body.UserID, body.Amount, body.TableRef, body.HoldIDs, body.IdempotencyKey, body.Description)
```

- `WalletService.CashoutGame` gets a trailing `description string` and passes it to `s.repo.CashoutHoldsAtomic(..., reqHash(...), description)`.
- `CashoutHoldsAtomic` (in `repositories/holds.go`) gets a trailing `description string` and forwards it to `r.newEntry(..., description)` for the credit entry (read the function; `newEntry` already takes a description argument, see `wallet.go:611`). The description must NOT be added to `reqHash`.
- Update the `HoldStore` interface, every fake/stub implementing it, and every call site listed under Files (pass `""` in tests that do not care, `"Mesa #t1, cashout"` in the new one).

- [ ] **Step 4: Run to verify**

Run: `cd api && go build ./... && go vet ./... && go test ./... && go test -tags integration ./tests/integration/ -run 'Cashout|Hold' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add api && git commit -m "feat(api): accept a description on game cashout"
```

---

### Task A3: Enforce the check on every M2M money route

**Files:**
- Modify: `api/internal/api/v1/internal.go` (`movement`), `m2m_sandbox_purchase.go:54`, `m2m_product_purchase.go:48`, `m2m_charge.go:15`
- Test: `api/internal/api/v1/internal_test.go`, `m2m_sandbox_purchase_test.go`, `m2m_product_purchase_test.go`

**Interfaces:**
- Consumes: `h.checkDescription` (A1). Routes covered: `sandbox/credit`, `sandbox/debit`, `real/debit` (all via `movement`), `game/cashout` (A2), `sandbox-purchase` create, `product-purchase` create, `charge`. Hold and release are NOT covered (a hold writes no ledger entry).

- [ ] **Step 1: Write the failing tests**

In each of the three test files, mirroring that file's existing request helper, add a case per route:

```go
func TestMovementRejectsMissingDescriptionWhenRequired(t *testing.T) {
	app := newInternalTestApp(t, withRequireDescription(true)) // existing builder; add the option if absent
	for _, path := range []string{
		"/v1.0/internal/wallet/sandbox/credit",
		"/v1.0/internal/wallet/sandbox/debit",
		"/v1.0/internal/wallet/real/debit",
	} {
		code := postM2M(t, app, path, `{"user_id":"u1","amount":100,"idempotency_key":"k1"}`)
		if code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", path, code)
		}
		code = postM2M(t, app, path, `{"user_id":"u1","amount":100,"idempotency_key":"k2","description":"Prêmio da mesa #t1"}`)
		if code == http.StatusBadRequest {
			t.Errorf("%s: a valid description was rejected", path)
		}
	}
}

func TestMovementFlagOffKeepsDescriptionOptional(t *testing.T) {
	app := newInternalTestApp(t, withRequireDescription(false))
	code := postM2M(t, app, "/v1.0/internal/wallet/sandbox/credit", `{"user_id":"u1","amount":100,"idempotency_key":"k1"}`)
	if code == http.StatusBadRequest {
		t.Fatal("flag off must keep description optional")
	}
}
```

Add one equivalent test each for the sandbox-purchase, product-purchase, charge and cashout routes (missing description -> 400 with the flag on). Reuse each file's existing app/auth helper; add a `withRequireDescription` option to the shared test builder where one does not exist.

- [ ] **Step 2: Run to verify they fail**

Run: `cd api && go test ./internal/api/v1/ -run 'RejectsMissingDescription|FlagOff' -v` -> FAIL.

- [ ] **Step 3: Enforce**

In `movement` (`internal.go`), immediately after `bindJSON`:

```go
	if p := h.checkDescription(body.Description); p != nil {
		return sendProblem(c, p)
	}
```

Add the identical three-line guard right after `bindJSON` in the create handlers of `m2m_sandbox_purchase.go` (line 54), `m2m_product_purchase.go` (line 48) and `m2m_charge.go` (line 15), each using that handler's `body.Description`. Do not guard the refund/release/get handlers in those files (they carry no description).

- [ ] **Step 4: Run and commit**

Run: `cd api && go test ./internal/api/... -v 2>&1 | tail -30` -> PASS.

```bash
git add api && git commit -m "feat(api): require a description on M2M money routes when REQUIRE_DESCRIPTION is on"
```

---

### Task A4: Docs and deploy configuration

**Files:**
- Modify: `docs/specs/2026-08-29-transaction-description.md`, `api/ENDPOINTS.md`, `CLAUDE.md` (Backend error handling note), `OPERATIONS.md`
- Modify: `cdk/lib/api-stack.ts` (environment) only if the stack lists env vars explicitly; otherwise document the variable

- [ ] **Step 1:** In the 2026-08-29 spec, add a section "Obrigatoriedade (2026-10-08)": the flag, the rollout order, the cashout field, and that hold/release are exempt. In `ENDPOINTS.md`, mark `description` as "required when `REQUIRE_DESCRIPTION=true`, 3-255" on each covered route and add `description` to `POST .../game/cashout`.
- [ ] **Step 2:** In `OPERATIONS.md`, add a runbook paragraph: "Turning on `REQUIRE_DESCRIPTION`: confirm poker and billing are deployed with this feature (check their latest release), set `REQUIRE_DESCRIPTION=true`, redeploy the wallet, watch for `400` with `description é obrigatório` in the access log for 24h; roll back by setting it to `false`."
- [ ] **Step 3:** `rg "GAMBLING_ENABLED" cdk` to find how env vars reach the API; add `REQUIRE_DESCRIPTION` the same way (default `false`) and extend `cdk/test/api-stack.test.ts` if it asserts the environment.
- [ ] **Step 4: Commit**

```bash
git add -A && git commit -m "docs: document the mandatory description flag and rollout order"
```

Deploy the wallet branch now (flag OFF). Only then start Parts B and C.

---

## Part B: `ctech-poker`

Working directory: `/home/artur-revgas/Documents/Projects/Ctech/ctech-poker`.

### Task B1: Wallet client sends `description`

**Files:**
- Modify: `api/internal/walletclient/client.go` (`MovementRequest` at line 103, `Credit`/`Debit`/`DebitReal` at 375-391, `HoldGame` at 397 (no field: hold has no ledger entry), `CashoutGame` at 463, `movement`/`movementWithResponse` at 560-565, `PurchaseSandbox` at 661, `PurchaseProduct` at 821)
- Test: `api/internal/walletclient/client_test.go`, `gamewallet_test.go`, `sandboxpurchase_test.go`, `productpurchase_test.go`

**Interfaces:**
- Produces (every new parameter is the LAST one, named `description`):
  - `Credit(ctx, userID string, amount int64, idempotencyKey, reason, description string) error`
  - `Debit(...same...) error`, `DebitReal(...same...) error`
  - `CashoutGame(ctx, userID string, amount int64, tableRef string, holdIDs []string, idempotencyKey, reason, description string) error`
  - `PurchaseSandbox(ctx, userID, sku, idempotencyKey, description string) (*SandboxPurchase, error)`
  - `PurchaseProduct(ctx, userID, sku, idempotencyKey, description string) (*ProductPurchase, error)`
  - `MovementRequest` gains `Description string `json:"description"``.
  - `HoldGame` and `ReleaseHold` are unchanged.

- [ ] **Step 1: Sync and branch**

```bash
cd /home/artur-revgas/Documents/Projects/Ctech/ctech-poker
git checkout main && git pull origin main && git checkout -b feat/wallet-transaction-descriptions
```

- [ ] **Step 2: Write the failing tests**

In `client_test.go`, extend `TestCreditSendsExpectedRequestBody` and `TestDebitSendsExpectedRequestBody` (and the equivalents in `TestDebitRealSendsExpectedRequestBody`, and the cashout/purchase tests in the sibling files) so the fake wallet server decodes `MovementRequest` and asserts the field:

```go
	if err := c.Credit(ctx, "u1", 500, "idem-1", "daily_reward", "Recompensa diária"); err != nil {
		t.Fatal(err)
	}
	if got.Description != "Recompensa diária" {
		t.Fatalf("description on the wire = %q", got.Description)
	}
```

For cashout, decode the body into `map[string]any` and assert `body["description"] == "Mesa #t1, cashout"`. For the purchases, assert the JSON body contains `"description"`.

- [ ] **Step 3: Run to verify they fail**

Run: `cd api && go test ./internal/walletclient/ -v 2>&1 | tail -20` -> FAIL (signature mismatch).

- [ ] **Step 4: Implement**

- `MovementRequest`: add `Description string `json:"description"``.
- `Credit`, `Debit`, `DebitReal`: add `description string`, pass to `movement`, which passes to `movementWithResponse`, which builds `MovementRequest{..., Reason: reason, Description: description}`.
- `CashoutGame`: add `description string`; add `"description": description,` to the `map[string]any` body.
- `PurchaseSandbox` and `PurchaseProduct`: add `description string`; include `"description": description` in the request body next to `sku`. Read each function's body-building code first; if it marshals a struct, add the field to that struct.

- [ ] **Step 5: Run and commit (compile errors elsewhere are expected until B2)**

Run: `cd api && go test ./internal/walletclient/ -v 2>&1 | tail -20` -> PASS.

```bash
git add api/internal/walletclient && git commit -m "feat(api): send a description on every wallet movement"
```

### Task B2: Fill the description at every call site

**Files and call sites** (line numbers from `main` as of 2026-10-08; re-find with `rg`):
- `api/internal/dailyreward/service.go:123`: `s.wallet.Credit(ctx, playerID, record.Amount, idemKey, "daily_reward")`
- `api/internal/handreveal/service.go:51,55`: buyer `Debit`, winner `Credit` (the interface `walletMover` in that package and its test fakes also change)
- `api/internal/cosmeticpurchase/service.go:228,432,551,656`: `PurchaseProduct`, `Debit`, refund `Credit`, retry `Debit`
- `api/internal/reactionpurchase/service.go:213,418,520,605`: same four
- `api/internal/sandboxpurchase/service.go:137`: `PurchaseSandbox`
- `api/internal/buyin/service.go:34-36` (interface), `885` (`CashoutGame`)
- `api/cmd/reconcile/main.go:36,70` (interface + `CashoutGame`)
- `api/cmd/tablecleanup/main.go:57,127,174` (interface + `Credit`, `CashoutGame`)
- every `_test.go` fake that implements those interfaces (compile errors list them)

**Interfaces:** consumes B1's signatures.

- [ ] **Step 1: Let the compiler list the work**

Run: `cd api && go build ./... && go vet ./... 2>&1 | head -60`
Expected: errors at every call site and fake. That list IS the checklist.

- [ ] **Step 2: Write one failing assertion per package**

For each package, extend an existing test that already captures what the fake wallet received so it also asserts the description. Example for `dailyreward`:

```go
	if w.lastDescription != "Recompensa diária" {
		t.Fatalf("description = %q", w.lastDescription)
	}
```

Add a `lastDescription string` field to that package's fake wallet and record it in the fake's method. Do the same in `handreveal`, `cosmeticpurchase`, `reactionpurchase`, `sandboxpurchase`, `buyin`, `cmd/reconcile` and `cmd/tablecleanup` tests.

- [ ] **Step 3: Write the descriptions**

Define one named constant or small formatter per message in the owning package (no inline string literals scattered through the logic; the project rule is no magic strings). Use these texts; fill the bracketed value from the identifier already in scope at that call site (read it; do not invent new lookups):

| Call site | Description |
|---|---|
| dailyreward Credit | `Recompensa diária` |
| handreveal buyer Debit | `Revelação de mão #<handID>` |
| handreveal winner Credit | `Parte da revelação de mão #<handID>` |
| cosmeticpurchase Debit | `Cosmético <kind>: <itemID>` |
| cosmeticpurchase refund Credit | `Estorno do cosmético <kind>: <itemID>` |
| reactionpurchase Debit | `Reação: <reactionID>` |
| reactionpurchase refund Credit | `Estorno da reação: <reactionID>` |
| cosmetic/reaction PurchaseProduct | `Compra de cosmético <itemID>` / `Compra de reação <reactionID>` |
| sandboxpurchase PurchaseSandbox | `Compra de fichas (<sku>)` |
| buyin CashoutGame | `Mesa #<roomID>: saída com fichas` |
| reconcile CashoutGame | `Mesa #<tableRef>: saída conciliada` |
| tablecleanup Credit / CashoutGame | `Mesa #<tableID>: devolução de mesa encerrada` |

If a call site has no identifier in scope (for example `handreveal` has no hand id), use the nearest available one (room, table, player-visible item) and note it in the PR; never leave the text generic like "Prêmio" or "Jogada".

`HoldGame` is unchanged: the wallet does not take a description on holds. Its `reason` stays.

- [ ] **Step 4: Run to verify**

Run: `cd api && go build ./... && go vet ./... && go test ./... 2>&1 | tail -30`
Expected: PASS everywhere, including the `cmd/` packages.

- [ ] **Step 5: Sanity check for leftovers**

```bash
rg -n "wallet\.(Credit|Debit|DebitReal|CashoutGame|PurchaseProduct|PurchaseSandbox)\(" api --glob '!**/*_test.go'
```
Each hit must show a description argument.

- [ ] **Step 6: Docs and commit**

Add a short section to `ctech-poker/api/CLAUDE.md` (or the wallet-integration doc it points to): "Every wallet movement must carry a `description`; the wallet rejects empty ones once `REQUIRE_DESCRIPTION` is on." Then:

```bash
git add -A && git commit -m "feat(api): describe every wallet transaction (table, hand, item)"
```

Open the PR; deploy poker. Do not wait for billing.

---

## Part C: `ctech-billing`

Working directory: `/home/artur-revgas/Documents/Projects/Ctech/ctech-billing`.

### Task C1: Describe invoice charges

**Files:**
- Modify: `api/internal/wallet/client.go:157` (`OpenChargeInput`)
- Modify: `api/internal/services/collecting.go:197` (the `OpenCharge` call)
- Test: `api/internal/wallet/client_test.go` (create if absent), `api/internal/services/collecting_test.go`

**Interfaces:**
- Produces: `OpenChargeInput.Description string `json:"description"``; the invoice charge description `Fatura <invoice number or id>`.

- [ ] **Step 1: Sync and branch**

```bash
cd /home/artur-revgas/Documents/Projects/Ctech/ctech-billing
git checkout main && git pull origin main && git checkout -b feat/wallet-charge-description
```

- [ ] **Step 2: Write the failing test**

In `collecting_test.go`, extend the existing test that uses a fake `charges` (the one that captures `OpenChargeInput`):

```go
	if got := fake.lastOpenCharge.Description; got != "Fatura "+inv.ID {
		t.Fatalf("description = %q", got)
	}
```

In `wallet/client_test.go` assert the JSON sent to the wallet contains `"description":"Fatura INV-1"` (use the existing httptest server helper in that package; read how the file tests `OpenCharge`).

- [ ] **Step 3: Run to verify it fails**

Run: `cd api && go test ./internal/wallet/ ./internal/services/ -run 'Charge|Collect' -v` -> FAIL.

- [ ] **Step 4: Implement**

`OpenChargeInput`: add after `PayerTaxID`:

```go
	// Description is the human sentence on the customer's wallet statement
	// ("Fatura INV-42"). Display only; the wallet never uses it as authority
	// for the amount. Required by the wallet once REQUIRE_DESCRIPTION is on.
	Description string `json:"description"`
```

`collecting.go`, in the `OpenChargeInput{...}` literal:

```go
		Description:    invoiceChargeDescription(inv.ID),
```

and a small named formatter in the same package:

```go
const invoiceChargeDescriptionPrefix = "Fatura "

func invoiceChargeDescription(invoiceID string) string {
	return invoiceChargeDescriptionPrefix + invoiceID
}
```

If the invoice has a customer-facing number field (`rg -n "Number" api/internal/domain/billing/invoice.go`), use it instead of the id.

- [ ] **Step 5: Check the other wallet touchpoints**

`rg -n "wallet\." api/internal/services/reconciling.go api/internal/api/v1/checkout.go api/internal/domain/billing/invoice.go` and confirm none of them calls a wallet MONEY route other than `OpenCharge` (billing uses the charge route; the product-purchase and debit routes are not used here). If one does, give it a description in the same way and add its test.

- [ ] **Step 6: Run, document, commit**

Run: `cd api && go build ./... && go test ./... 2>&1 | tail -20` -> PASS.

Add one line to billing's wallet-integration doc ("charges carry `Fatura <id>` as description"), then:

```bash
git add -A && git commit -m "feat(api): send an invoice description when opening a wallet charge"
```

Open the PR; deploy billing.

---

## Part D: turn enforcement on

### Task D1: Flip the flag

**Files:** deployment config only (SSM/CDK env for the wallet).

- [ ] **Step 1: Verify the preconditions**

```bash
# poker and billing are deployed with the description changes
git -C /home/artur-revgas/Documents/Projects/Ctech/ctech-poker log origin/main --oneline -5
git -C /home/artur-revgas/Documents/Projects/Ctech/ctech-billing log origin/main --oneline -5
```
Expected: both show the `describe every wallet transaction` and `invoice description` commits merged AND deployed (confirm in the deploy pipeline, not only in git).

- [ ] **Step 2: Dev first.** Set `REQUIRE_DESCRIPTION=true` in dev, redeploy the wallet, and exercise: a poker daily reward, a table buy-in and cashout, a cosmetic purchase, a sandbox purchase, a billing checkout. Expected: no `400 description é obrigatório` in any wallet access log.
- [ ] **Step 3: Production.** Same change; watch the wallet log for 24h; roll back by setting the flag to `false`.
- [ ] **Step 4:** Update the wallet spec status line: "Obrigatoriedade ativa em prod desde <date>".
