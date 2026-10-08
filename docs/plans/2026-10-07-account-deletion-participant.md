# Account Deletion — Wallet Participant Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make ctech-wallet a participant in the LGPD account-deletion saga. It reports blockers, locks a user's money from the start of grace, purges or retains the user's data per the inventory, carries self-exclusion over by CPF keyed hash, and acks ctech-account.

**Architecture:**
- An `erasure.Consumer` (ctech-go-common v1.13.1) runs inside the API process. It long-polls a wallet-owned SQS queue subscribed to ctech-account's `{env}-account-user-erasure` topic, keeps lock and tombstone state in `{env}_wallet_erasure_state` (`erasure.Store`) and calls `ErasureService.Purge` on `user.erase`.
- One service-level guard (`checkErasure`) refuses every operation that creates new exposure for a locked or erased `sub`. Inbound settlement that cannot be refused (a poker cash-out, a hold release, a legacy PIX confirm) is accepted and comes back as a blocker.
- User state-changing routes also re-check the JWT revocation list and fail closed. The list lives in Valkey DB 0, through a second cache backend.
- PII fields (payer CPF and name on deposits, PIX keys on withdrawals) are copied into a write-only retention table, then stripped from the operational rows.
- Money rows stay in place, keyed by the opaque `sub`: the ledger is append-only (Invariant #2).
- Self-exclusion survives as `SELFEXCL#{cpf_hmac}` in `wallet_users`, with a TTL.

**Tech Stack:** Go 1.27, Fiber v3, DynamoDB (aws-sdk-go-v2), SQS, Valkey, `gopkg.aoctech.app/api-commons` v1.13.1 (`erasure`, `jwtverify`, `oauth2client`, `dynamo`), AWS CDK v2 (TypeScript).

**Spec (ctech-account repo, read all four):**
- `ctech-account/docs/specs/2026-10-06-account-deletion-overview.md` — decisions D1–D14 in §11.
- `ctech-account/docs/specs/2026-10-06-account-deletion-data-inventory.md` — §2 cross-cutting, §4 wallet.
- `ctech-account/docs/specs/2026-10-06-account-deletion-saga-protocol.md` — §4 participant obligations, §5 token cut-off, §7 retries.
- `ctech-account/docs/plans/2026-10-07-account-deletion-phase3-participants.md` — wire contract: eligibility token, ack scope, SNS attribute filter.

Wallet-local context: `docs/specs/2026-10-07-asaas-removal.md`. The BaaS provider is gone: there is **no deposit and no withdrawal rail**, no Asaas sub-account, no transfer intents, no MED receivables.

## Global Constraints

- Implementation branch `feat/account-deletion-participant` from `main`. Conventional Commits. **Never** a `Co-Authored-By` trailer or any Claude/Anthropic attribution.
- `gopkg.aoctech.app/api-commons` stays at **v1.13.1** in `api/` and `pix-gateway/` (already pinned; do not bump).
- Service id: `wallet`. Inbound scope: `internal:wallet:erasure-eligibility`. Outbound ack scope: `internal:account:erasure-ack`.
- Eligibility route: `GET /v1.0/internal/erasure/eligibility/:sub`. It returns `erasure.Eligibility`. An unknown `sub` returns `{"eligible":true,"blockers":[]}` and creates nothing. It must answer in under 2 s.
- Ack URL: `{CTECH_URL}/v1.0/internal/erasure/ack`. Token URL: `{CTECH_URL}/v1.0/token`. Client: the wallet's own M2M client (`WALLET_CLIENT_ID`).
- SNS topic ARN comes from SSM `/ctech/{env}/account/erasure-topic-arn`. The subscription uses raw delivery and FilterPolicy `{"services":["wallet"]}` at the default **MessageAttributes** scope.
- Queue `{env}-ctech-wallet-erasure`: visibility timeout 300 s, `maxReceiveCount` 5, then DLQ `{env}-ctech-wallet-erasure-dlq`. DLQ depth > 0 alarms on the ctech-cdk alerts topic.
- Tables:
  - `{env}_wallet_erasure_state` (pk `pk`, TTL `ttl`), read and written through `erasure.NewStore(db, TABLE_PREFIX+"_wallet", ErasureTombstoneTTL)`.
  - `{env}_wallet_erasure_retention` (pk `pk`, sk `sk`, TTL `ttl`). The API role may only `PutItem` it.
  - `{env}_wallet_users` gains TTL on `ttl`.
- Retention horizon: **5 years from the purge** (`ErasureRetentionYears = 5`), for retained PII and for a carried indefinite self-exclusion (D13).
- Revocation entries are read from Valkey **DB 0** (`VALKEY_REVOCATION_URL` = the shared base URL). The check fails open on every route (D3) and fails **closed** on user state-changing routes.
- The erasure feature is ON if and only if `ERASURE_QUEUE_URL` is set. In `ENVIRONMENT=prod`, both `ERASURE_QUEUE_URL` and `VALKEY_REVOCATION_URL` are required (fail closed at boot).
- Blocker codes (stable, translated by the account UI): `wallet.balance_nonzero`, `wallet.hold_open`, `wallet.deposit_pending`, `wallet.withdrawal_pending`, `wallet.purchase_pending`.
- Financial Safety Invariants are untouched. Erasure moves **no** money and never updates or deletes a ledger entry. The only change to an append-only table is REMOVE of `ip`/`user_agent` on `wallet_audit`, enforced in IAM.
- Never log a CPF, a `cpf_hmac`, a payer name or a PIX key.
- After every Go task: `cd api && go vet ./... && go test ./... -race` is green. Integration tasks also run `make test-integration` (DynamoDB-local). After the CDK task: `cd cdk && npx tsc --noEmit && npm test`.
- Every change documents itself in the same commit (root `CLAUDE.md` Mandatory Documentation Policy). Task 11 holds the cross-cutting docs.

## Prerequisites outside this repo (gate before enabling in prod)

1. ctech-account phases 1–3 merged and deployed: topic, revocation, eligibility client, ack endpoint.
2. **ctech-account adds `cpf_hmac` to `GET /v1.0/internal/kyc/:user_id`** (ruling R5 and Open question Q1). It is one line in `ctech-account/api/internal/handler/kyc.go` `internalGet`: `"cpf_hmac": h.cpfMAC(u.CPF)`, where `cpfMAC` is the phase-2a `sealer.MAC("cpf-hmac", cpf)` and returns `""` for an empty CPF. Without it, the purge of a self-excluded user fails and is redelivered (it never drops the exclusion).
3. Operational seeding, documented in Task 11:
   - grant `internal:account:erasure-ack` to the wallet M2M client;
   - add the wallet entry to account's `ERASURE_PARTICIPANTS`;
   - publish `internal:wallet:erasure-eligibility` through the wallet scope manifest.

## Rulings (each with its cost if wrong)

- **R1 No go-common upgrade task.**
  - `api/go.mod` and `pix-gateway/go.mod` already pin v1.13.1.
  - pix-gateway verifies no user JWT (it only *calls* the API with `oauth2client`), so it needs no revocation.
  - The only module change is adding `aws-sdk-go-v2/service/sqs`, which the `erasure` package imports.
  - Cost if wrong: none; the full suite gates Task 5.
- **R2 Segregation = copy PII to a write-only table, strip it from operational rows. Money rows stay in place.**
  - DynamoDB has no item tags, and `dynamodb:LeadingKeys` cannot express "this user's rows", so "tag + IAM deny" is not implementable.
  - Moving the ledger would break Invariant #2 and its IAM DENY.
  - What stays in place is keyed by the opaque `sub`, which the spec allows ("the `sub` stays as an opaque key"): ledger, wallets, holds, purchases, idempotency guards. No route serves an erased `sub`.
  - The person's identity (name, CPF) is retained by ctech-account under D6 (`KYCRET#{sub}`). The `sub` links it to these rows for a PLD request.
  - Cost if wrong: if legal demands a physical move of the money rows, the Invariant #2 amendment and the copy migration come later. No data is lost, because nothing here is deleted.
- **R3 `wallet_users` is anonymized, not deleted.**
  - REMOVE: `self_exclusion`, `game_limits`, `game_deposit_counters`. SET `erased_at`.
  - Kept: terms and gambling addendum versions and timestamps, as proof of consent. This mirrors account's tombstone keeping ToS acceptance.
  - Cost if wrong: one more REMOVE.
- **R4 `wallet_audit` is anonymized through a narrowly conditioned IAM allow**, `UpdateItem` only when `dynamodb:Attributes ⊆ {pk, sk, ip, user_agent}`. The append-only DENY is not lifted.
  - Cost if wrong: if `dynamodb:Attributes` does not behave as documented, the purge gets AccessDenied, goes to the DLQ and alarms (fails safe). Then fix the IAM.
- **R5 The self-exclusion key is ctech-account's `cpf_hmac`.**
  - Account serves it from its existing internal KYC endpoint (new field, prerequisite 2). The wallet never holds account's `SECRET_ENC_KEY`.
  - It is stored as `wallet_users` item `SELFEXCL#{cpf_hmac}` with `ttl`: the end of the timed exclusion, or purge + 5 y for an indefinite one. The longer exclusion wins.
  - It is checked in `ActivateGambling` only. Real-money play, poker included, requires activation.
  - An indefinite exclusion is re-applied as indefinite.
  - Cost if wrong: if account refuses the field, switch to a wallet-owned key **before** the first prod purge. Rows written with one key cannot be re-keyed later (the CPF is gone). That is why this is a deploy gate.
- **R6 No Asaas closure, no transfer-intent or MED blockers.** The provider was removed (`asaas-removal.md`). The retired tables never held prod data and get no IAM access, so they are not purged.
  - Cost if wrong: none until a new custody provider ships. That plan adds its closure call and blockers.
- **R7 D7 (closing withdrawal has no minimum) is already satisfied by `wallet.ValidateWithdrawalAmount(amount, w, fullBalance=true, …)`.**
  - It waives the minimum whenever the withdrawal empties the wallet, so a closing withdrawal needs no UI flag: the API derives it from `amount == balance`.
  - No withdrawal rail exists today. Task 11 writes this into the "future provider" requirements.
  - Cost if wrong: a future rail that forgets it traps dust balances; the doc line and the existing domain test guard it.
- **R8 `wallet.balance_nonzero` action_url.**
  - `game` → the wallet home (`SERVICE_AUDIENCE`), where `game → real` is always open (Invariant #9).
  - `real` → **no** action_url: there is no withdrawal rail, so support settles it.
  - Cost if wrong: the account UI shows a link to nothing, or no link (Q2).
- **R9 The lock is one guard (`checkErasure`) at the top of 19 service methods** that open new exposure.
  - Accepted for a locked `sub`: `ConfirmDeposit`, `ConfirmSandboxPurchase`, `ConfirmProductPurchase`, `ReleaseHold`, `CashoutGame`, every reconcile sweep. Refusing them would strand money (saga §4.2: inbound money that cannot be refused becomes a blocker).
  - Cost if wrong in the strict direction: refusing a poker cash-out leaves real money in limbo (Invariant #14).
- **R10 Fail-closed revocation = middleware `RequireUnrevoked`.**
  - It calls `jwtverify.CheckRevoked` on the claims the auth middleware already verified, instead of a second `VerifyClaimsStrict` parse. Same semantics, one parse.
  - Cost: one extra Valkey GET per money request.
- **R11 The feature switch is `ERASURE_QUEUE_URL`.** Unset means no gate, no consumer and no carry-over check (local dev only). Prod refuses to boot without it.
  - Cost if wrong: a prod deploy without the CDK env fails to boot, which is the intended fail-closed behaviour.
- **R12 A `sandbox` balance is not a blocker.** It is virtual and has no monetary value (Invariant #6), so it is forfeited.
  - Cost if wrong: one more blocker line.
- **R13 State table prefix `{env}_wallet`.** `erasure.NewStore` appends `_erasure_state`, and a bare `{env}_erasure_state` would collide with a sibling service in the same AWS account.
  - Cost: none.
- **R14 A pending sandbox/product purchase blocks only while its charge is payable** (`created_at` within `sandboxPurchaseTTLMinutes` = 60 min). Product rows never leave `pending` when unpaid. A sandbox `refund_pending` always blocks.
  - Cost if wrong: a late payment after purge is confirmed into a purged account and must be refunded by support.
- **R15 Rows with no PII beyond the opaque `sub` are untouched:** idempotency guards, closed holds, purchase rows, ledger `description`/`ref`.
  - Cost if wrong: one more erase step later.
- **R16 Service-scope unlink (`Store.Clear` on re-consent) is out of scope.** ctech-account phase 3 ships account scope only; the unlink plan adds `Clear` to the terms-addendum accept path.

## Review Focus

1. **Inbound money for a locked `sub` (poker cash-out, hold release, legacy PIX confirm).** It must be accepted, never refused, and must surface as a blocker so the purge acks `blocked`. Tests: Task 4 `TestErasureGateLeavesSettlementOpen`, Task 5 `TestPurge_BlockerAcksBlockedAndTouchesNothing`.
2. **Valkey DB 0 unreachable on a money route.** The request gets a 503, never a pass. Test: Task 7 `TestRequireUnrevoked`.
3. **Purge crashes between "retain" and "strip", then reruns.** The retention row keeps the full PII copy (write-once), and the rerun completes. Tests: Task 2 `TestErasureRetainIsWriteOnceAndStripRemovesPII`, Task 5 `TestPurge_RetainsThenStripsAndIsIdempotent`.
4. **Self-excluded user while account does not yet serve `cpf_hmac`.** The purge returns an error (redelivery or DLQ) **before** erasing the profile; the exclusion is never silently dropped. Test: Task 5 `TestPurge_SelfExcludedWithoutHMACIsRetriedNotDropped`.
5. **Unpaid product purchase older than its charge validity.** It does not block deletion forever; a recent one does. Test: Task 5 `TestBlockers_ReportsEveryMoneyCondition`.

---

### Task 1: Domain constants, carried self-exclusion, problem types

**Files:**
- Create: `api/internal/domain/wallet/erasure.go`
- Create: `api/internal/domain/wallet/erasure_test.go`
- Modify: `api/internal/domain/wallet/audit.go` (event constant)
- Modify: `api/internal/problem/problem.go` (two types, two constructors)

**Interfaces:**
- Produces:
  - constants `wallet.ErasureServiceID`, `ErasureRetentionYears`, `ErasureTombstoneTTL`, `ErasureStateTableSegment`, `TableErasureRetention`, `SelfExclusionCarryPrefix`, `RetentionSubPrefix`, `ScopeAccountErasureAck`, `PathAccountErasureAck`, `ActorErasureCarryOver`, the five `Blocker*` codes and the five `Count*` keys;
  - `func wallet.CarriedSelfExclusion(ex *SelfExclusion, now time.Time) (SelfExclusion, time.Time, bool)`;
  - `wallet.EventSelfExclusionCarried`;
  - `problem.TypeAccountErasurePending`, `problem.TypeServiceUnavailable`, `func problem.AccountErasurePending() *Problem`, `func problem.ServiceUnavailable(detail string) *Problem`.

- [ ] **Step 1: Write the failing test** — `api/internal/domain/wallet/erasure_test.go`:

```go
package wallet

import (
	"testing"
	"time"
)

func TestCarriedSelfExclusion(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)

	if _, _, ok := CarriedSelfExclusion(nil, now); ok {
		t.Fatal("no exclusion must carry nothing")
	}

	timed := &SelfExclusion{Period: "30d", RequestedAt: now.Add(-24 * time.Hour).Format(time.RFC3339),
		Until: now.Add(29 * 24 * time.Hour).Format(time.RFC3339)}
	ex, exp, ok := CarriedSelfExclusion(timed, now)
	if !ok || ex != *timed || !exp.Equal(now.Add(29*24*time.Hour)) {
		t.Fatalf("timed: got %+v %v %v, want the same exclusion until its end", ex, exp, ok)
	}

	expired := &SelfExclusion{Period: "30d", Until: now.Add(-time.Second).Format(time.RFC3339)}
	if _, _, ok := CarriedSelfExclusion(expired, now); ok {
		t.Fatal("an expired exclusion must not be carried")
	}

	indefinite := &SelfExclusion{Period: "indefinite", RequestedAt: now.Format(time.RFC3339)}
	ex, exp, ok = CarriedSelfExclusion(indefinite, now)
	if !ok || ex.Until != "" || !exp.Equal(now.AddDate(ErasureRetentionYears, 0, 0)) {
		t.Fatalf("indefinite: got %+v %v %v, want indefinite kept %d years", ex, exp, ok, ErasureRetentionYears)
	}

	// Same fail-closed rule as User.SelfExcluded: an unparseable end date
	// keeps the exclusion, for the longest horizon.
	garbage := &SelfExclusion{Period: "30d", Until: "not-a-date"}
	if _, exp, ok := CarriedSelfExclusion(garbage, now); !ok || !exp.Equal(now.AddDate(ErasureRetentionYears, 0, 0)) {
		t.Fatalf("unparseable until: got %v %v, want carried for %d years", exp, ok, ErasureRetentionYears)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `cd api && go test ./internal/domain/wallet/ -run TestCarriedSelfExclusion -v`
Expected: FAIL — `undefined: CarriedSelfExclusion` and `undefined: ErasureRetentionYears`.

- [ ] **Step 3: Implement** — `api/internal/domain/wallet/erasure.go`:

```go
package wallet

import "time"

// Account-deletion (LGPD) participant constants. Contract:
// ctech-account docs/specs/2026-10-06-account-deletion-saga-protocol.md;
// plan: docs/plans/2026-10-07-account-deletion-participant.md.
const (
	// ErasureServiceID is this service's name in the saga (message
	// `services[]`, SNS filter policy, ack `service`).
	ErasureServiceID = "wallet"

	// ErasureRetentionYears is the PLD retention horizon (Lei 9.613 art. 10)
	// counted from the purge, and the horizon of a carried indefinite
	// self-exclusion (D13).
	ErasureRetentionYears = 5

	// ErasureTombstoneTTL is erasure.NewStore's erasedTTL: never shorter than
	// ErasureRetentionYears calendar years (366-day years absorb leap days).
	ErasureTombstoneTTL = ErasureRetentionYears * 366 * 24 * time.Hour

	// ErasureStateTableSegment is appended to TABLE_PREFIX for
	// erasure.NewStore, so its {prefix}_erasure_state becomes the
	// wallet-owned {env}_wallet_erasure_state, never a name a sibling service
	// in the same AWS account could also claim.
	ErasureStateTableSegment = "wallet"

	// TableErasureRetention holds the PII copied out of operational rows at
	// purge. The API role may only PutItem it (IAM); support reads it.
	TableErasureRetention = "wallet_erasure_retention"

	// RetentionSubPrefix keys a retention partition by the erased sub.
	RetentionSubPrefix = "SUB#"

	// SelfExclusionCarryPrefix keys a self-exclusion that outlives its
	// account (D13) in wallet_users, by ctech-account's CPF keyed hash.
	SelfExclusionCarryPrefix = "SELFEXCL#"

	// ScopeAccountErasureAck is requested by the wallet's own M2M client to
	// ack a purge to ctech-account.
	ScopeAccountErasureAck = "internal:account:erasure-ack"
	// PathAccountErasureAck is appended to CTECH_URL.
	PathAccountErasureAck = "/v1.0/internal/erasure/ack"

	// ActorErasureCarryOver is the audit actor of a carried self-exclusion
	// re-applied at activation.
	ActorErasureCarryOver = "erasure-carry-over"
)

// Blocker codes reported by GET /internal/erasure/eligibility/:sub. Stable:
// the ctech-account UI translates them.
const (
	BlockerBalanceNonzero    = "wallet.balance_nonzero"
	BlockerHoldOpen          = "wallet.hold_open"
	BlockerDepositPending    = "wallet.deposit_pending"
	BlockerWithdrawalPending = "wallet.withdrawal_pending"
	BlockerPurchasePending   = "wallet.purchase_pending"
)

// Ack count keys (saga protocol §4.3: per-store counts in the ack).
const (
	CountDepositsRetained     = "deposits_pii_retained"
	CountWithdrawalsRetained  = "withdrawals_pii_retained"
	CountUserProfileErased    = "wallet_users_erased"
	CountAuditAnonymized      = "wallet_audit_anonymized"
	CountSelfExclusionCarried = "self_exclusion_carried"
)

// CarriedSelfExclusion decides whether ex outlives the account (D13) and
// until when. A timed exclusion is kept until its end. An indefinite one, or
// one whose end cannot be parsed (fail closed, like User.SelfExcluded), is
// kept ErasureRetentionYears from now. An expired one carries nothing.
func CarriedSelfExclusion(ex *SelfExclusion, now time.Time) (SelfExclusion, time.Time, bool) {
	if ex == nil {
		return SelfExclusion{}, time.Time{}, false
	}
	if ex.Until == "" {
		return *ex, now.AddDate(ErasureRetentionYears, 0, 0), true
	}
	until, err := time.Parse(time.RFC3339, ex.Until)
	if err != nil {
		return *ex, now.AddDate(ErasureRetentionYears, 0, 0), true
	}
	if !now.Before(until) {
		return SelfExclusion{}, time.Time{}, false
	}
	return *ex, until, true
}
```

In `api/internal/domain/wallet/audit.go`, add to the event-type const block, after `EventTermsAddendumAccepted`:

```go
	// EventSelfExclusionCarried: a self-exclusion kept from an erased account
	// (same CPF) was re-applied when this account tried to activate gambling.
	EventSelfExclusionCarried = "self_exclusion_carried"
```

In `api/internal/problem/problem.go`, add to the type-URI const block, after `TypeSandboxPurchaseUsed`:

```go
	TypeAccountErasurePending = "/problems/account-erasure-pending"
	TypeServiceUnavailable    = "/problems/service-unavailable"
```

and append the constructors to the wallet-specific section:

```go
// AccountErasurePending refuses a state-changing operation for a user whose
// account deletion is in grace or done (saga protocol §4.2, decision D1).
func AccountErasurePending() *Problem {
	return New(http.StatusConflict, TypeAccountErasurePending, "Account Erasure Pending",
		"a conta está em processo de exclusão; nenhuma operação pode ser feita")
}

// ServiceUnavailable is a fail-closed refusal: a dependency needed to decide
// safely (e.g. the JWT revocation list) is unreachable.
func ServiceUnavailable(detail string) *Problem {
	return New(http.StatusServiceUnavailable, TypeServiceUnavailable, "Service Unavailable", detail)
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd api && go test ./internal/domain/wallet/ ./internal/problem/ -v -run 'TestCarriedSelfExclusion|TestProblem' && go vet ./...`
Expected: `ok  gopkg.aoctech.app/wallet/api/internal/domain/wallet`, with the problem package `ok` or `[no tests to run]`, and vet silent.

- [ ] **Step 5: Commit**

```bash
git add api/internal/domain/wallet/erasure.go api/internal/domain/wallet/erasure_test.go api/internal/domain/wallet/audit.go api/internal/problem/problem.go
git commit -m "feat(api): erasure domain constants and carried self-exclusion rule"
```

---

### Task 2: Erasure repository — per-user lookups, write-once retention, PII strip

**Files:**
- Create: `api/internal/repositories/erasure.go`
- Create: `api/tests/integration/erasure_test.go`
- Modify: `api/tests/integration/setup_test.go` (retention table in `createTables`/`dropTables`)

**Interfaces:**
- Consumes: Task 1 constants.
- Produces:
  - `func repositories.NewErasureRepository(db *dynamodb.Client, cfg *config.Config) *ErasureRepository`;
  - `(*ErasureRepository) DepositsForUser(ctx, userID string) ([]wallet.PixDeposit, error)`;
  - `(*ErasureRepository) WithdrawalsForUser(ctx, userID string) ([]wallet.Withdrawal, error)`;
  - `(*ErasureRepository) OpenHoldsForUser(ctx, userID string) ([]wallet.Hold, error)`;
  - `(*ErasureRepository) Retain(ctx, sub, source, sourceKey string, item any, until time.Time) error`;
  - `(*ErasureRepository) StripDepositPII(ctx, txid string) error`;
  - `(*ErasureRepository) StripWithdrawalPII(ctx, withdrawalID string) error`;
  - `func repositories.RetentionSK(source, sourceKey string) string`.

- [ ] **Step 1: Add the retention table to the integration harness.** In `api/tests/integration/setup_test.go` `createTables`, append to `defs`:

```go
		{
			TableName:            aws.String(table(wallet.TableErasureRetention)),
			AttributeDefinitions: []dtypes.AttributeDefinition{s("pk"), s("sk")},
			KeySchema:            []dtypes.KeySchemaElement{hashKey("pk"), rangeKey("sk")},
			BillingMode:          dtypes.BillingModePayPerRequest,
		},
```

and add `wallet.TableErasureRetention` to the slice in `dropTables`.

- [ ] **Step 2: Write the failing integration test** — `api/tests/integration/erasure_test.go`:

```go
//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/wallet/api/internal/config"
	"gopkg.aoctech.app/wallet/api/internal/domain/id"
	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
	"gopkg.aoctech.app/wallet/api/internal/repositories"
)

func putRaw(t *testing.T, tableName string, v any) {
	t.Helper()
	av, err := attributevalue.MarshalMap(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if _, err := db.PutItem(context.Background(), &dynamodb.PutItemInput{TableName: aws.String(table(tableName)), Item: av}); err != nil {
		t.Fatalf("put %s: %v", tableName, err)
	}
}

func getRaw(t *testing.T, tableName, pk, sk string) map[string]dtypes.AttributeValue {
	t.Helper()
	key := map[string]dtypes.AttributeValue{"pk": &dtypes.AttributeValueMemberS{Value: pk}}
	if sk != "" {
		key["sk"] = &dtypes.AttributeValueMemberS{Value: sk}
	}
	out, err := db.GetItem(context.Background(), &dynamodb.GetItemInput{TableName: aws.String(table(tableName)), Key: key, ConsistentRead: aws.Bool(true)})
	if err != nil {
		t.Fatalf("get %s: %v", tableName, err)
	}
	return out.Item
}

type retainedDeposit struct {
	Item wallet.PixDeposit `dynamodbav:"item"`
	TTL  int64             `dynamodbav:"ttl"`
}

func erasureRepo() *repositories.ErasureRepository {
	return repositories.NewErasureRepository(db, &config.Config{TablePrefix: tablePrefix})
}

func TestErasureRepositoryFindsUserRowsAcrossStatusPages(t *testing.T) {
	ctx := context.Background()
	er := erasureRepo()
	user, other := "u-"+id.New(), "u-"+id.New()
	// 120 rows of someone else in the same status partition force a second
	// GSI page (the repository pages at 100).
	for range 120 {
		putRaw(t, wallet.TablePixDeposits, wallet.PixDeposit{Txid: "tx-" + id.New(), UserID: other, Status: wallet.DepositConfirmed, CreatedAt: repositories.NowStr()})
	}
	mine := wallet.PixDeposit{Txid: "tx-" + id.New(), UserID: user, Status: wallet.DepositConfirmed, PayerCPF: cpf, PayerName: "Fulano", AmountExpected: 500, CreatedAt: repositories.NowStr()}
	putRaw(t, wallet.TablePixDeposits, mine)
	putRaw(t, wallet.TableWithdrawals, wallet.Withdrawal{WithdrawalID: "wd-" + id.New(), UserID: user, Status: wallet.WithdrawCompleted, PixKey: cpf, Amount: 300})
	putRaw(t, wallet.TableHolds, wallet.Hold{HoldID: "h-" + id.New(), UserID: user, WalletID: "WALLET#g-" + id.New(), Status: wallet.HoldHeld, Amount: 100})
	putRaw(t, wallet.TableHolds, wallet.Hold{HoldID: "h-" + id.New(), UserID: user, WalletID: "WALLET#g-" + id.New(), Status: wallet.HoldSettled, Amount: 100})

	deps, err := er.DepositsForUser(ctx, user)
	if err != nil || len(deps) != 1 || deps[0].Txid != mine.Txid {
		t.Fatalf("DepositsForUser = %v, %v; want exactly %s", deps, err, mine.Txid)
	}
	wds, err := er.WithdrawalsForUser(ctx, user)
	if err != nil || len(wds) != 1 {
		t.Fatalf("WithdrawalsForUser = %v, %v; want 1", wds, err)
	}
	holds, err := er.OpenHoldsForUser(ctx, user)
	if err != nil || len(holds) != 1 || holds[0].Status != wallet.HoldHeld {
		t.Fatalf("OpenHoldsForUser = %v, %v; want only the held one", holds, err)
	}
}

func TestErasureRetainIsWriteOnceAndStripRemovesPII(t *testing.T) {
	ctx := context.Background()
	er := erasureRepo()
	walletRepo := repositories.NewWalletRepository(db, &config.Config{TablePrefix: tablePrefix})
	user := "u-" + id.New()
	mine := wallet.PixDeposit{Txid: "tx-" + id.New(), UserID: user, Status: wallet.DepositConfirmed, PayerCPF: cpf, PayerName: "Fulano", AmountExpected: 500, CreatedAt: repositories.NowStr()}
	putRaw(t, wallet.TablePixDeposits, mine)
	until := time.Now().AddDate(wallet.ErasureRetentionYears, 0, 0)

	if err := er.Retain(ctx, user, wallet.TablePixDeposits, mine.Txid, mine, until); err != nil {
		t.Fatalf("Retain: %v", err)
	}
	// A rerun after the original was stripped must never overwrite the full copy.
	stripped := mine
	stripped.PayerCPF, stripped.PayerName = "", ""
	if err := er.Retain(ctx, user, wallet.TablePixDeposits, mine.Txid, stripped, until); err != nil {
		t.Fatalf("second Retain must be a no-op, got %v", err)
	}
	var rec retainedDeposit
	item := getRaw(t, wallet.TableErasureRetention, wallet.RetentionSubPrefix+user, repositories.RetentionSK(wallet.TablePixDeposits, mine.Txid))
	if err := attributevalue.UnmarshalMap(item, &rec); err != nil {
		t.Fatalf("unmarshal retention: %v", err)
	}
	if rec.Item.PayerCPF != cpf || rec.Item.PayerName != "Fulano" || rec.TTL != until.Unix() {
		t.Fatalf("retention = %+v, want the full PII copy with ttl %d", rec, until.Unix())
	}

	if err := er.StripDepositPII(ctx, mine.Txid); err != nil {
		t.Fatalf("StripDepositPII: %v", err)
	}
	d, err := walletRepo.GetDeposit(ctx, mine.Txid)
	if err != nil || d.PayerCPF != "" || d.PayerName != "" || d.AmountExpected != 500 || d.Status != wallet.DepositConfirmed {
		t.Fatalf("stripped deposit = %+v, %v; want PII gone, money fields intact", d, err)
	}
}
```

- [ ] **Step 3: Run it to verify it fails**

Run: `cd api && docker compose -f docker-compose.test.yml up -d && DYNAMODB_ENDPOINT=http://localhost:8123 go test -tags integration -count=1 ./tests/integration/ -run 'TestErasureRepository|TestErasureRetain' -v`
Expected: build FAIL — `undefined: repositories.NewErasureRepository` and `undefined: repositories.RetentionSK`.

- [ ] **Step 4: Implement** — `api/internal/repositories/erasure.go`:

```go
package repositories

import (
	"context"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/wallet/api/internal/config"
	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
)

// Attribute names the purge removes or sets. Mirrored by cdk/lib/constants.ts
// AUDIT_ANONYMIZABLE_ATTRIBUTES for the audit pair.
const (
	attrPayerCPF            = "payer_cpf"
	attrPayerName           = "payer_name"
	attrPixKey              = "pix_key"
	attrSelfExclusion       = "self_exclusion"
	attrGameLimits          = "game_limits"
	attrGameDepositCounters = "game_deposit_counters"
	attrErasedAt            = "erased_at"
	attrAuditIP             = "ip"
	attrAuditUserAgent      = "user_agent"
	attrTTL                 = "ttl"
	attrStatus              = "status"
)

const erasurePageSize = 100

var (
	allDepositStatuses = []string{
		wallet.DepositPending, wallet.DepositConfirmed, wallet.DepositRejectedCPF, wallet.DepositRefundPending,
		wallet.DepositExpired, wallet.DepositRefunded, wallet.DepositRefundFailed,
	}
	allWithdrawalStatuses = []string{
		wallet.WithdrawProcessing, wallet.WithdrawCompleted, wallet.WithdrawReversed, wallet.WithdrawRefundFail,
	}
)

// ErasureRepository is the persistence side of the account-deletion purge
// (docs/plans/2026-10-07-account-deletion-participant.md). Every write is
// idempotent: the purge may run any number of times or stop anywhere.
type ErasureRepository struct {
	deposits    Base
	withdrawals Base
	holds       Base
	users       Base
	audit       Base
	retention   Base
}

func NewErasureRepository(db *dynamodb.Client, cfg *config.Config) *ErasureRepository {
	return &ErasureRepository{
		deposits:    NewBase(db, cfg, wallet.TablePixDeposits),
		withdrawals: NewBase(db, cfg, wallet.TableWithdrawals),
		holds:       NewBase(db, cfg, wallet.TableHolds),
		users:       NewBase(db, cfg, wallet.TableUsers),
		audit:       NewBase(db, cfg, wallet.TableAudit),
		retention:   NewBase(db, cfg, wallet.TableErasureRetention),
	}
}

// allByStatus pages through a status GSI for every listed status and keeps
// the rows keep accepts.
// ponytail: filter-after-GSI over whole status partitions, the same shape as
// ScanStaleHolds. Fine while deposits and withdrawals are frozen (no rail
// since asaas-removal) and held holds are few; add a user_id GSI when a
// deposit/withdrawal rail returns.
func allByStatus[T any](ctx context.Context, b Base, index string, statuses []string, keep func(*T) bool) ([]T, error) {
	var out []T
	for _, status := range statuses {
		var start map[string]types.AttributeValue
		for {
			res, err := b.QueryGSI(ctx, index, attrStatus, status, erasurePageSize, start)
			if err != nil {
				return nil, err
			}
			for _, it := range res.Items {
				v, err := Decode[T](it)
				if err != nil {
					return nil, err
				}
				if keep(v) {
					out = append(out, *v)
				}
			}
			if len(res.LastEvaluatedKey) == 0 {
				break
			}
			start = res.LastEvaluatedKey
		}
	}
	return out, nil
}

// DepositsForUser returns every PIX deposit row of userID, any status.
func (r *ErasureRepository) DepositsForUser(ctx context.Context, userID string) ([]wallet.PixDeposit, error) {
	return allByStatus(ctx, r.deposits, wallet.GSIStatus, allDepositStatuses,
		func(d *wallet.PixDeposit) bool { return d.UserID == userID })
}

// WithdrawalsForUser returns every withdrawal row of userID, any status.
func (r *ErasureRepository) WithdrawalsForUser(ctx context.Context, userID string) ([]wallet.Withdrawal, error) {
	return allByStatus(ctx, r.withdrawals, wallet.GSIStatus, allWithdrawalStatuses,
		func(w *wallet.Withdrawal) bool { return w.UserID == userID })
}

// OpenHoldsForUser returns userID's holds still `held`, across every page
// (ListOpenHoldsForWallet reads one page only, which is not enough here).
func (r *ErasureRepository) OpenHoldsForUser(ctx context.Context, userID string) ([]wallet.Hold, error) {
	return allByStatus(ctx, r.holds, wallet.GSIHoldStatus, []string{wallet.HoldHeld},
		func(h *wallet.Hold) bool { return h.UserID == userID })
}

// RetentionSK is the retention sort key of one source row.
func RetentionSK(source, sourceKey string) string { return source + "#" + sourceKey }

type retainedRecord struct {
	PK          string `dynamodbav:"pk"`
	SK          string `dynamodbav:"sk"`
	Source      string `dynamodbav:"source"`
	Item        any    `dynamodbav:"item"`
	RetainedAt  string `dynamodbav:"retained_at"`
	RetainUntil string `dynamodbav:"retain_until"`
	TTL         int64  `dynamodbav:"ttl"`
}

// Retain copies item into the write-only retention table, write-once: a
// second call for the same source row is a no-op, so a rerun after the
// original was stripped can never overwrite the full copy with the stripped
// one. until is both retain_until and the DynamoDB TTL.
func (r *ErasureRepository) Retain(ctx context.Context, sub, source, sourceKey string, item any, until time.Time) error {
	av, err := attributevalue.MarshalMap(retainedRecord{
		PK: wallet.RetentionSubPrefix + sub, SK: RetentionSK(source, sourceKey), Source: source, Item: item,
		RetainedAt: NowStr(), RetainUntil: until.UTC().Format(time.RFC3339), TTL: until.Unix(),
	})
	if err != nil {
		return err
	}
	_, err = r.retention.PutItemRaw(ctx, &dynamodb.PutItemInput{
		Item:                av,
		ConditionExpression: aws.String("attribute_not_exists(pk)"),
	})
	if IsConditionFailed(err) {
		return nil
	}
	return err
}

// StripDepositPII removes the payer identity from a deposit row; the money
// fields stay. Missing row: no-op.
func (r *ErasureRepository) StripDepositPII(ctx context.Context, txid string) error {
	_, err := r.deposits.UpdateItem(ctx, txid, nil, map[string]any{
		attrPayerCPF: nil, attrPayerName: nil, attrErasedAt: NowStr(),
	})
	return err
}

// StripWithdrawalPII removes the destination PIX key from a withdrawal row.
func (r *ErasureRepository) StripWithdrawalPII(ctx context.Context, withdrawalID string) error {
	_, err := r.withdrawals.UpdateItem(ctx, withdrawalID, nil, map[string]any{
		attrPixKey: nil, attrErasedAt: NowStr(),
	})
	return err
}
```

(`attrSelfExclusion`, `attrGameLimits`, `attrGameDepositCounters`, `attrAuditIP`, `attrAuditUserAgent` and `attrTTL` are used in Task 3. If `go vet`/the linter flags them as unused between tasks, add them in Task 3 instead.)

- [ ] **Step 5: Run the tests to verify they pass**

Run: `cd api && go vet ./... && DYNAMODB_ENDPOINT=http://localhost:8123 go test -tags integration -count=1 ./tests/integration/ -run 'TestErasureRepository|TestErasureRetain' -v`
Expected: `--- PASS: TestErasureRepositoryFindsUserRowsAcrossStatusPages`, `--- PASS: TestErasureRetainIsWriteOnceAndStripRemovesPII`, `ok`.

- [ ] **Step 6: Commit**

```bash
git add api/internal/repositories/erasure.go api/tests/integration/erasure_test.go api/tests/integration/setup_test.go
git commit -m "feat(api): erasure repository lookups and write-once PII retention"
```

---

### Task 3: Erasure repository — profile, audit anonymization, carried self-exclusion

**Files:**
- Modify: `api/internal/repositories/erasure.go`
- Modify: `api/tests/integration/erasure_test.go`

**Interfaces:**
- Produces:
  - `(*ErasureRepository) EraseUserProfile(ctx, userID string) (bool, error)`;
  - `(*ErasureRepository) AnonymizeAudit(ctx, userID string) (int, error)`;
  - `(*ErasureRepository) PutCarriedSelfExclusion(ctx, cpfHMAC string, ex wallet.SelfExclusion, expiresAt time.Time) error`;
  - `(*ErasureRepository) GetCarriedSelfExclusion(ctx, cpfHMAC string, now time.Time) (*wallet.SelfExclusion, error)`.

- [ ] **Step 1: Write the failing tests** — append to `api/tests/integration/erasure_test.go`:

```go
func TestErasureProfileAndAuditAreAnonymizedIdempotently(t *testing.T) {
	ctx := context.Background()
	er := erasureRepo()
	cfg := &config.Config{TablePrefix: tablePrefix}
	users := repositories.NewUserRepository(db, cfg)
	audit := repositories.NewAuditRepository(db, cfg)
	user := "u-" + id.New()

	if ok, err := er.EraseUserProfile(ctx, "ghost-"+id.New()); err != nil || ok {
		t.Fatalf("EraseUserProfile(missing) = %v, %v; want false, nil", ok, err)
	}

	_ = users.AcceptTerms(ctx, user)
	_ = users.SetSelfExclusion(ctx, user, &wallet.SelfExclusion{Period: "indefinite", RequestedAt: repositories.NowStr()})
	_ = users.SetGameLimits(ctx, user, &wallet.GameLimits{Daily: 1, Weekly: 2, Monthly: 3})
	_ = audit.Append(ctx, &wallet.AuditEvent{UserID: user, EventType: wallet.EventSelfExcluded, Actor: user, IP: "203.0.113.9", UserAgent: "UA/1"})
	_ = audit.Append(ctx, &wallet.AuditEvent{UserID: user, EventType: wallet.EventTermsAddendumAccepted, Actor: user})

	if ok, err := er.EraseUserProfile(ctx, user); err != nil || !ok {
		t.Fatalf("EraseUserProfile = %v, %v", ok, err)
	}
	u, err := users.Get(ctx, user)
	if err != nil || u.SelfExclusion != nil || u.GameLimits != nil || !u.TermsAccepted() {
		t.Fatalf("profile = %+v, %v; want limits/exclusion gone, consent kept", u, err)
	}

	if n, err := er.AnonymizeAudit(ctx, user); err != nil || n != 1 {
		t.Fatalf("AnonymizeAudit = %d, %v; want 1", n, err)
	}
	if n, err := er.AnonymizeAudit(ctx, user); err != nil || n != 0 {
		t.Fatalf("second AnonymizeAudit = %d, %v; want 0", n, err)
	}
	evs, err := audit.List(ctx, user, 10)
	if err != nil || len(evs) != 2 {
		t.Fatalf("audit rows = %v, %v; want both events kept", evs, err)
	}
	for _, e := range evs {
		if e.IP != "" || e.UserAgent != "" || e.EventType == "" {
			t.Fatalf("audit row %+v still carries ip/ua or lost its event", e)
		}
	}
}

func TestErasureCarriedSelfExclusionKeepsTheLongerOne(t *testing.T) {
	ctx := context.Background()
	er := erasureRepo()
	now := time.Now().Truncate(time.Second)
	hmac := "hmac-" + id.New()
	long := wallet.SelfExclusion{Period: "indefinite", RequestedAt: now.Format(time.RFC3339)}
	short := wallet.SelfExclusion{Period: "30d", RequestedAt: now.Format(time.RFC3339), Until: now.Add(24 * time.Hour).Format(time.RFC3339)}

	if err := er.PutCarriedSelfExclusion(ctx, hmac, long, now.Add(10*24*time.Hour)); err != nil {
		t.Fatalf("put long: %v", err)
	}
	if err := er.PutCarriedSelfExclusion(ctx, hmac, short, now.Add(24*time.Hour)); err != nil {
		t.Fatalf("a shorter exclusion must be a no-op, got %v", err)
	}
	got, err := er.GetCarriedSelfExclusion(ctx, hmac, now)
	if err != nil || got == nil || *got != long {
		t.Fatalf("carried = %+v, %v; want the longer one", got, err)
	}
	if got, err := er.GetCarriedSelfExclusion(ctx, hmac, now.Add(11*24*time.Hour)); err != nil || got != nil {
		t.Fatalf("after ttl = %+v, %v; want nil (TTL deletes lazily)", got, err)
	}
	if got, err := er.GetCarriedSelfExclusion(ctx, "hmac-unknown-"+id.New(), now); err != nil || got != nil {
		t.Fatalf("unknown = %+v, %v; want nil", got, err)
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `cd api && DYNAMODB_ENDPOINT=http://localhost:8123 go test -tags integration -count=1 ./tests/integration/ -run 'TestErasureProfile|TestErasureCarried' -v`
Expected: build FAIL — `er.EraseUserProfile undefined`.

- [ ] **Step 3: Implement** — append to `api/internal/repositories/erasure.go` (add `"strconv"` to the imports):

```go
// EraseUserProfile removes the responsible-gambling state from the user row
// and stamps erased_at. Consent versions and timestamps stay (proof of
// consent). Returns false when the user never had a row.
func (r *ErasureRepository) EraseUserProfile(ctx context.Context, userID string) (bool, error) {
	return r.users.UpdateItem(ctx, userID, nil, map[string]any{
		attrSelfExclusion: nil, attrGameLimits: nil, attrGameDepositCounters: nil, attrErasedAt: NowStr(),
	})
}

// AnonymizeAudit removes ip and user_agent from every audit row of userID and
// returns how many rows it changed. It touches nothing else: IAM allows
// UpdateItem on wallet_audit for exactly {pk, sk, ip, user_agent}.
func (r *ErasureRepository) AnonymizeAudit(ctx context.Context, userID string) (int, error) {
	n := 0
	var start map[string]types.AttributeValue
	for {
		res, err := r.audit.Query(ctx, QueryOpts{PK: userID, Limit: erasurePageSize, ExclusiveStartKey: start})
		if err != nil {
			return n, err
		}
		for _, it := range res.Items {
			e, err := Decode[wallet.AuditEvent](it)
			if err != nil {
				return n, err
			}
			if e.IP == "" && e.UserAgent == "" {
				continue
			}
			sk := e.SK
			if _, err := r.audit.UpdateItem(ctx, userID, &sk, map[string]any{attrAuditIP: nil, attrAuditUserAgent: nil}); err != nil {
				return n, err
			}
			n++
		}
		if len(res.LastEvaluatedKey) == 0 {
			return n, nil
		}
		start = res.LastEvaluatedKey
	}
}

type carriedExclusion struct {
	PK            string               `dynamodbav:"pk"`
	SelfExclusion wallet.SelfExclusion `dynamodbav:"self_exclusion"`
	TTL           int64                `dynamodbav:"ttl"`
}

// PutCarriedSelfExclusion keeps ex past the account's erasure (D13), keyed by
// ctech-account's CPF keyed hash, until expiresAt (DynamoDB TTL). If one is
// already stored for the same CPF, the one that lasts longer wins: a person
// can delete more than one account.
func (r *ErasureRepository) PutCarriedSelfExclusion(ctx context.Context, cpfHMAC string, ex wallet.SelfExclusion, expiresAt time.Time) error {
	av, err := attributevalue.MarshalMap(carriedExclusion{PK: wallet.SelfExclusionCarryPrefix + cpfHMAC, SelfExclusion: ex, TTL: expiresAt.Unix()})
	if err != nil {
		return err
	}
	_, err = r.users.PutItemRaw(ctx, &dynamodb.PutItemInput{
		Item:                     av,
		ConditionExpression:      aws.String("attribute_not_exists(pk) OR #ttl < :ttl"),
		ExpressionAttributeNames: map[string]string{"#ttl": attrTTL},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":ttl": &types.AttributeValueMemberN{Value: strconv.FormatInt(expiresAt.Unix(), 10)},
		},
	})
	if IsConditionFailed(err) {
		return nil
	}
	return err
}

// GetCarriedSelfExclusion returns the exclusion carried for cpfHMAC, or nil
// when there is none or it has expired (TTL deletion lags by up to days).
func (r *ErasureRepository) GetCarriedSelfExclusion(ctx context.Context, cpfHMAC string, now time.Time) (*wallet.SelfExclusion, error) {
	item, err := r.users.GetItem(ctx, wallet.SelfExclusionCarryPrefix+cpfHMAC)
	if err != nil || item == nil {
		return nil, err
	}
	c, err := Decode[carriedExclusion](item)
	if err != nil {
		return nil, err
	}
	if c.TTL <= now.Unix() {
		return nil, nil
	}
	return &c.SelfExclusion, nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd api && go vet ./... && DYNAMODB_ENDPOINT=http://localhost:8123 go test -tags integration -count=1 ./tests/integration/ -run 'TestErasure' -v`
Expected: all four `TestErasure*` PASS, `ok`.

- [ ] **Step 5: Commit**

```bash
git add api/internal/repositories/erasure.go api/tests/integration/erasure_test.go
git commit -m "feat(api): erasure repository profile, audit and carried self-exclusion"
```

---

### Task 4: Lock gate — refuse new exposure for a locked or erased user

**Files:**
- Create: `api/internal/services/erasure_gate.go`
- Create: `api/internal/services/erasure_gate_test.go`
- Modify: `api/internal/services/wallet.go` (struct fields; guards in `ActivateGambling`, `FundGame`, `ReturnFromGame`, `PurchaseSandbox`, `CreditSandbox`, `DebitSandbox`, `DebitReal`, `HoldGame`)
- Modify: `api/internal/services/sandbox_purchase.go` (`PurchaseSandboxDirect`, `RefundSandboxPurchase`)
- Modify: `api/internal/services/product_purchase.go` (`PurchaseProductDirect`, `RefundProductPurchase`)
- Modify: `api/internal/services/charge_amount.go` (`OpenCharge`)
- Modify: `api/internal/services/responsible.go` (`SelfExclude`, `RevokeSelfExclusion`, `SetGameLimits`, `CancelPendingLimits`)
- Modify: `api/internal/services/user.go` (struct field, setter, guards in `AcceptTermsAddendum`, `AcceptGamblingAddendum`)

**Interfaces:**
- Consumes: `problem.AccountErasurePending`, `wallet.SelfExclusion`.
- Produces:
  - `type services.ErasureGate interface { Blocked(ctx context.Context, sub string) (bool, error) }` (satisfied by `*erasure.Store`);
  - `type services.CarriedExclusionStore interface { GetCarriedSelfExclusion(ctx context.Context, cpfHMAC string, now time.Time) (*wallet.SelfExclusion, error) }`;
  - `func (s *WalletService) SetErasure(gate ErasureGate, carried CarriedExclusionStore)`;
  - `func (s *UserService) SetErasureGate(gate ErasureGate)`.

- [ ] **Step 1: Write the failing test** — `api/internal/services/erasure_gate_test.go`:

```go
package services

import (
	"context"
	"errors"
	"testing"

	"gopkg.aoctech.app/wallet/api/internal/kycclient"
	"gopkg.aoctech.app/wallet/api/internal/pix"
	"gopkg.aoctech.app/wallet/api/internal/problem"
)

type stubGate struct {
	blocked bool
	err     error
	calls   []string
}

func (g *stubGate) Blocked(_ context.Context, sub string) (bool, error) {
	g.calls = append(g.calls, sub)
	return g.blocked, g.err
}

func gatedServices(gate ErasureGate) (*WalletService, *UserService) {
	users := &stubUserRepo{}
	svc := NewWalletService(newStubRepo(), users, &stubAudit{}, &stubLocker{}, pix.NewFake(), &stubKYC{rec: &kycclient.KYC{}})
	svc.SetSandboxPurchases(newStubSandboxPurchaseRepo())
	svc.SetProductPurchases(newStubProductPurchaseRepo())
	svc.SetErasure(gate, nil)
	userSvc := NewUserService(users, &stubAudit{})
	userSvc.SetErasureGate(gate)
	return svc, userSvc
}

// D1: from the start of grace, nothing new may happen to the user's money,
// whatever token the caller (user or M2M on the user's behalf) holds.
func TestErasureGateRefusesEveryNewExposure(t *testing.T) {
	ctx := context.Background()
	svc, userSvc := gatedServices(&stubGate{blocked: true})
	ops := map[string]func() error{
		"ActivateGambling": func() error { _, _, err := svc.ActivateGambling(ctx, "u1", "basic", "", "", 100, 200, 300); return err },
		"FundGame":         func() error { _, _, err := svc.FundGame(ctx, "u1", 100, "k"); return err },
		"ReturnFromGame":   func() error { _, _, err := svc.ReturnFromGame(ctx, "u1", 100, "k"); return err },
		"PurchaseSandbox":  func() error { _, _, err := svc.PurchaseSandbox(ctx, "u1", 100, "k"); return err },
		"CreditSandbox":    func() error { _, err := svc.CreditSandbox(ctx, "u1", 100, "k", "r", ""); return err },
		"DebitSandbox":     func() error { _, err := svc.DebitSandbox(ctx, "u1", 100, "k", "r", ""); return err },
		"DebitReal":        func() error { _, err := svc.DebitReal(ctx, "u1", 100, "k", "r", ""); return err },
		"HoldGame":         func() error { _, err := svc.HoldGame(ctx, "u1", 100, "t1", "k"); return err },
		"PurchaseSandboxDirect": func() error {
			_, _, err := svc.PurchaseSandboxDirect(ctx, "u1", "sku", "k", "", "")
			return err
		},
		"RefundSandboxPurchase": func() error { _, err := svc.RefundSandboxPurchase(ctx, "u1", "p1", "k", ""); return err },
		"PurchaseProductDirect": func() error {
			_, _, err := svc.PurchaseProductDirect(ctx, "u1", "sku", "k", "", "")
			return err
		},
		"RefundProductPurchase": func() error { _, err := svc.RefundProductPurchase(ctx, "u1", "p1", "k", ""); return err },
		"OpenCharge": func() error {
			_, _, err := svc.OpenCharge(ctx, OpenChargeInput{UserID: "u1", AmountCents: 100, Reference: "inv", IdempotencyKey: "k", RequestingClient: "billing"})
			return err
		},
		"SelfExclude":            func() error { _, err := svc.SelfExclude(ctx, "u1", "30d", "", ""); return err },
		"RevokeSelfExclusion":    func() error { return svc.RevokeSelfExclusion(ctx, "u1", "", "") },
		"SetGameLimits":          func() error { _, err := svc.SetGameLimits(ctx, "u1", 1, 2, 3, "", ""); return err },
		"CancelPendingLimits":    func() error { _, err := svc.CancelPendingLimits(ctx, "u1"); return err },
		"AcceptTermsAddendum":    func() error { return userSvc.AcceptTermsAddendum(ctx, "u1") },
		"AcceptGamblingAddendum": func() error { return userSvc.AcceptGamblingAddendum(ctx, "u1", "", "") },
	}
	for name, op := range ops {
		var p *problem.Problem
		if err := op(); !errors.As(err, &p) || p.Type != problem.TypeAccountErasurePending {
			t.Errorf("%s: err = %v, want %s", name, err, problem.TypeAccountErasurePending)
		}
	}
}

// Saga protocol §4.2: inbound money that cannot be refused is accepted and
// becomes a blocker. Refusing a cash-out would strand real money (Invariant #14).
func TestErasureGateLeavesSettlementOpen(t *testing.T) {
	ctx := context.Background()
	gate := &stubGate{blocked: true}
	svc, _ := gatedServices(gate)
	_, _ = svc.ReleaseHold(ctx, "u1", "h-missing", "k")
	_, _ = svc.CashoutGame(ctx, "u1", 100, "t1", []string{"h-missing"}, "k")
	_ = svc.ConfirmDeposit(ctx, "tx-missing", "", "", false)
	_ = svc.ConfirmSandboxPurchase(ctx, "sbxp-missing", false)
	_ = svc.ConfirmProductPurchase(ctx, "prdp-missing", false)
	if len(gate.calls) != 0 {
		t.Fatalf("settlement paths consulted the erasure gate %v; they must never be refused", gate.calls)
	}
}

func TestErasureGateFailsClosedOnStoreError(t *testing.T) {
	gate := &stubGate{err: errors.New("dynamodb down")}
	svc, _ := gatedServices(gate)
	if _, _, err := svc.FundGame(context.Background(), "u1", 100, "k"); err == nil {
		t.Fatal("a gate read error must refuse the operation")
	}
}

func TestErasureGateOpenUserProceeds(t *testing.T) {
	gate := &stubGate{}
	svc, _ := gatedServices(gate)
	_, err := svc.CreditSandbox(context.Background(), "u1", 100, "k", "r", "")
	var p *problem.Problem
	if errors.As(err, &p) && p.Type == problem.TypeAccountErasurePending {
		t.Fatal("an active user must not be refused")
	}
	if len(gate.calls) != 1 || gate.calls[0] != "u1" {
		t.Fatalf("gate calls = %v, want [u1]", gate.calls)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `cd api && go test ./internal/services/ -run TestErasureGate -v`
Expected: build FAIL — `svc.SetErasure undefined`, `userSvc.SetErasureGate undefined`.

- [ ] **Step 3: Implement the gate** — `api/internal/services/erasure_gate.go`:

```go
package services

import (
	"context"
	"fmt"
	"time"

	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
	"gopkg.aoctech.app/wallet/api/internal/problem"
)

// ErasureGate reports whether the account-deletion saga has locked or erased
// a user (erasure.Store satisfies it). Decision D1: from the start of the
// grace period nothing new may happen to that user's money, whatever valid
// token the caller holds.
type ErasureGate interface {
	Blocked(ctx context.Context, sub string) (bool, error)
}

// CarriedExclusionStore reads a self-exclusion kept from an erased account of
// the same CPF (D13).
type CarriedExclusionStore interface {
	GetCarriedSelfExclusion(ctx context.Context, cpfHMAC string, now time.Time) (*wallet.SelfExclusion, error)
}

// checkErasure refuses a state-changing operation for a locked or erased
// user. A nil gate (cmd/reconcile, unit tests, local dev without
// ERASURE_QUEUE_URL) allows everything. A store error fails closed.
//
// Only operations that open NEW exposure call it. Settlement of what already
// exists (confirm a paid charge, release a hold, cash out) never does: that
// money cannot be refused, so it is accepted and reported as a blocker.
func checkErasure(ctx context.Context, gate ErasureGate, userID string) error {
	if gate == nil {
		return nil
	}
	blocked, err := gate.Blocked(ctx, userID)
	if err != nil {
		return fmt.Errorf("erasure gate: %w", err)
	}
	if blocked {
		return problem.AccountErasurePending()
	}
	return nil
}

// SetErasure wires the deletion lock and the carried self-exclusion lookup.
// Same setter pattern as SetBroadcaster: unset in cmd/reconcile and tests.
func (s *WalletService) SetErasure(gate ErasureGate, carried CarriedExclusionStore) {
	s.erasureGate = gate
	s.carried = carried
}

// SetErasureGate wires the deletion lock into the consent writes.
func (s *UserService) SetErasureGate(gate ErasureGate) {
	s.erasureGate = gate
}
```

- [ ] **Step 4: Add the struct fields.**

In `api/internal/services/wallet.go`, add to `WalletService` after `m2mClients`:

```go
	erasureGate      ErasureGate           // optional; see SetErasure
	carried          CarriedExclusionStore // optional; see SetErasure
```

In `api/internal/services/user.go`, change `UserService` to:

```go
type UserService struct {
	repo        UserRepo
	audit       Auditor
	erasureGate ErasureGate // optional; see SetErasureGate
}
```

- [ ] **Step 5: Insert the guard as the FIRST statement of each method body** (before any existing validation or comment):

| Method (file) | Statement inserted |
|---|---|
| `ActivateGambling` (wallet.go) | `if err := checkErasure(ctx, s.erasureGate, userID); err != nil { return nil, nil, err }` |
| `FundGame` (wallet.go) | `if err := checkErasure(ctx, s.erasureGate, userID); err != nil { return nil, nil, err }` |
| `ReturnFromGame` (wallet.go) | `if err := checkErasure(ctx, s.erasureGate, userID); err != nil { return nil, nil, err }` |
| `PurchaseSandbox` (wallet.go) | `if err := checkErasure(ctx, s.erasureGate, userID); err != nil { return nil, nil, err }` |
| `CreditSandbox` (wallet.go) | `if err := checkErasure(ctx, s.erasureGate, userID); err != nil { return nil, err }` |
| `DebitSandbox` (wallet.go) | `if err := checkErasure(ctx, s.erasureGate, userID); err != nil { return nil, err }` |
| `DebitReal` (wallet.go) | `if err := checkErasure(ctx, s.erasureGate, userID); err != nil { return nil, err }` |
| `HoldGame` (wallet.go) | `if err := checkErasure(ctx, s.erasureGate, userID); err != nil { return nil, err }` |
| `PurchaseSandboxDirect` (sandbox_purchase.go) | `if err := checkErasure(ctx, s.erasureGate, userID); err != nil { return nil, nil, err }` |
| `RefundSandboxPurchase` (sandbox_purchase.go) | `if err := checkErasure(ctx, s.erasureGate, userID); err != nil { return nil, err }` |
| `PurchaseProductDirect` (product_purchase.go) | `if err := checkErasure(ctx, s.erasureGate, userID); err != nil { return nil, nil, err }` |
| `RefundProductPurchase` (product_purchase.go) | `if err := checkErasure(ctx, s.erasureGate, userID); err != nil { return nil, err }` |
| `OpenCharge` (charge_amount.go) | `if err := checkErasure(ctx, s.erasureGate, in.UserID); err != nil { return nil, nil, err }` |
| `SelfExclude` (responsible.go) | `if err := checkErasure(ctx, s.erasureGate, userID); err != nil { return nil, err }` |
| `RevokeSelfExclusion` (responsible.go) | `if err := checkErasure(ctx, s.erasureGate, userID); err != nil { return err }` |
| `SetGameLimits` (responsible.go) | `if err := checkErasure(ctx, s.erasureGate, userID); err != nil { return nil, err }` |
| `CancelPendingLimits` (responsible.go) | `if err := checkErasure(ctx, s.erasureGate, userID); err != nil { return nil, err }` |
| `AcceptTermsAddendum` (user.go) | `if err := checkErasure(ctx, s.erasureGate, userID); err != nil { return err }` |
| `AcceptGamblingAddendum` (user.go) | `if err := checkErasure(ctx, s.erasureGate, userID); err != nil { return err }` |

Format each with `gofmt` (a multi-line `if` block). Do **not** add the guard to `ConfirmDeposit`, `ConfirmSandboxPurchase`, `ConfirmProductPurchase`, `ReleaseHold`, `CashoutGame`, any `Sweep*`/`Reconcile*`, or any read (`GetBalances`, `Statement`, `*History`, `GameEligibilityFor`, `BalancesFor`). `RefundSandboxPurchase` is also called by `SweepRefundPendingSandboxPurchases`; that runs only in `cmd/reconcile`, which never sets a gate.

- [ ] **Step 6: Run the tests to verify they pass**

Run: `cd api && go vet ./... && go test ./internal/services/ -race -run 'TestErasureGate' -v && go test ./... -race`
Expected: the four `TestErasureGate*` PASS, and the whole suite is `ok` (existing tests set no gate, so behaviour is unchanged).

- [ ] **Step 7: Commit**

```bash
git add api/internal/services/
git commit -m "feat(api): refuse new money exposure for users under account deletion"
```

---

### Task 5: ErasureService — blockers and idempotent purge

**Files:**
- Create: `api/internal/services/erasure.go`
- Create: `api/internal/services/erasure_test.go`
- Modify: `api/internal/kycclient/kycclient.go` (field `CPFHMAC`)
- Modify: `api/tests/integration/erasure_test.go` (end-to-end purge)
- Modify: `api/go.mod`, `api/go.sum` (`github.com/aws/aws-sdk-go-v2/service/sqs`, which the `erasure` package imports)

**Interfaces:**
- Consumes:
  - Task 2/3 repository methods, through the `ErasureRepo` interface;
  - `WalletStore.LoadWallets`, `SandboxPurchaseRepo.ListByUser`, `ProductPurchaseRepo.ListByUser`, `UserRepo.Get`, `KYCClient.Get`;
  - `erasure.Blocker`, `erasure.Ack`, `erasure.Message` (go-common v1.13.1).
- Produces:
  - `type services.ErasureRepo interface{…}` (below);
  - `func services.NewErasureService(wallets WalletStore, users UserRepo, sandbox SandboxPurchaseRepo, products ProductPurchaseRepo, repo ErasureRepo, kyc KYCClient, walletURL string) *ErasureService`;
  - `(*ErasureService) Blockers(ctx, sub string) ([]erasure.Blocker, error)`;
  - `(*ErasureService) Purge(ctx context.Context, m erasure.Message) (erasure.Ack, error)` — the `erasure.PurgeFunc` signature;
  - `kycclient.KYC.CPFHMAC string` (json `cpf_hmac`).

- [ ] **Step 1: Add the KYC field and the dependency.** In `api/internal/kycclient/kycclient.go`, add to `KYC` after `CPF`:

```go
	// CPFHMAC is ctech-account's CPF keyed hash (hex HMAC-SHA256, the same
	// value as its deletion tombstone). Used only as the key of a carried
	// self-exclusion (D13); never logged. Empty until ctech-account serves it.
	CPFHMAC string `json:"cpf_hmac"`
```

Run: `cd api && go get github.com/aws/aws-sdk-go-v2/service/sqs && go mod tidy`
Expected: `go.mod` gains `github.com/aws/aws-sdk-go-v2/service/sqs` (the version go-common v1.13.1 already requires), and `api-commons` stays `v1.13.1`.

- [ ] **Step 2: Write the failing unit tests** — `api/internal/services/erasure_test.go`:

```go
package services

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"gopkg.aoctech.app/api-commons/erasure"
	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
	"gopkg.aoctech.app/wallet/api/internal/kycclient"
)

type fakeErasureRepo struct {
	deposits     []wallet.PixDeposit
	withdrawals  []wallet.Withdrawal
	holds        []wallet.Hold
	retained     map[string]any
	profileCalls int
	auditRows    int
	carried      map[string]wallet.SelfExclusion
	carriedUntil map[string]time.Time
}

func newFakeErasureRepo() *fakeErasureRepo {
	return &fakeErasureRepo{retained: map[string]any{}, carried: map[string]wallet.SelfExclusion{}, carriedUntil: map[string]time.Time{}}
}

func (f *fakeErasureRepo) DepositsForUser(context.Context, string) ([]wallet.PixDeposit, error) {
	return append([]wallet.PixDeposit(nil), f.deposits...), nil
}
func (f *fakeErasureRepo) WithdrawalsForUser(context.Context, string) ([]wallet.Withdrawal, error) {
	return append([]wallet.Withdrawal(nil), f.withdrawals...), nil
}
func (f *fakeErasureRepo) OpenHoldsForUser(context.Context, string) ([]wallet.Hold, error) {
	return f.holds, nil
}
func (f *fakeErasureRepo) Retain(_ context.Context, _, source, key string, item any, _ time.Time) error {
	if _, ok := f.retained[source+"#"+key]; !ok {
		f.retained[source+"#"+key] = item
	}
	return nil
}
func (f *fakeErasureRepo) StripDepositPII(_ context.Context, txid string) error {
	for i := range f.deposits {
		if f.deposits[i].Txid == txid {
			f.deposits[i].PayerCPF, f.deposits[i].PayerName = "", ""
		}
	}
	return nil
}
func (f *fakeErasureRepo) StripWithdrawalPII(_ context.Context, id string) error {
	for i := range f.withdrawals {
		if f.withdrawals[i].WithdrawalID == id {
			f.withdrawals[i].PixKey = ""
		}
	}
	return nil
}
func (f *fakeErasureRepo) EraseUserProfile(context.Context, string) (bool, error) {
	f.profileCalls++
	return true, nil
}
func (f *fakeErasureRepo) AnonymizeAudit(context.Context, string) (int, error) {
	n := f.auditRows
	f.auditRows = 0
	return n, nil
}
func (f *fakeErasureRepo) PutCarriedSelfExclusion(_ context.Context, h string, ex wallet.SelfExclusion, until time.Time) error {
	f.carried[h], f.carriedUntil[h] = ex, until
	return nil
}

var testNow = time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)

func newErasureSvc(repo *stubRepo, users *stubUserRepo, sp *stubSandboxPurchaseRepo, pp *stubProductPurchaseRepo, er *fakeErasureRepo, kyc *kycclient.KYC) *ErasureService {
	e := NewErasureService(repo, users, sp, pp, er, &stubKYC{rec: kyc}, "https://wallet.test")
	e.now = func() time.Time { return testNow }
	return e
}

func eraseMsg() erasure.Message {
	return erasure.Message{Version: erasure.Version, Type: erasure.TypeErase, RequestID: "req-1", Sub: "u1",
		Scope: erasure.ScopeAccount, Services: []string{wallet.ErasureServiceID}, IssuedAt: testNow}
}

func codes(bs []erasure.Blocker) []string {
	out := make([]string, 0, len(bs))
	for _, b := range bs {
		out = append(out, b.Code)
	}
	slices.Sort(out)
	return out
}

func TestBlockers_ZeroBalancesAndNothingPendingIsEligible(t *testing.T) {
	e := newErasureSvc(newStubRepo(), &stubUserRepo{}, newStubSandboxPurchaseRepo(), newStubProductPurchaseRepo(), newFakeErasureRepo(), &kycclient.KYC{})
	bs, err := e.Blockers(context.Background(), "u1")
	if err != nil || len(bs) != 0 {
		t.Fatalf("Blockers = %v, %v; want none", bs, err)
	}
}

func TestBlockers_ReportsEveryMoneyCondition(t *testing.T) {
	repo := newStubRepo()
	repo.real.Balance, repo.game.Balance, repo.sandbox.Balance = 1250, 300, 999 // sandbox never blocks (R12)
	er := newFakeErasureRepo()
	er.holds = []wallet.Hold{{HoldID: "h1", UserID: "u1", Status: wallet.HoldHeld}}
	er.deposits = []wallet.PixDeposit{{Txid: "t1", UserID: "u1", Status: wallet.DepositRefundFailed}}
	er.withdrawals = []wallet.Withdrawal{{WithdrawalID: "w1", UserID: "u1", Status: wallet.WithdrawProcessing}}
	sp := newStubSandboxPurchaseRepo()
	sp.purchases["s1"] = &wallet.SandboxPurchase{PurchaseID: "s1", UserID: "u1", Status: wallet.SandboxPurchasePending,
		CreatedAt: testNow.Add(-5 * time.Minute).Format(time.RFC3339Nano)}
	pp := newStubProductPurchaseRepo()
	// Unpaid and past its charge validity: can never be paid, must not block forever.
	pp.purchases["p1"] = &wallet.ProductPurchase{PurchaseID: "p1", UserID: "u1", Status: wallet.ProductPurchasePending,
		CreatedAt: testNow.Add(-3 * time.Hour).Format(time.RFC3339Nano)}
	e := newErasureSvc(repo, &stubUserRepo{}, sp, pp, er, &kycclient.KYC{})

	bs, err := e.Blockers(context.Background(), "u1")
	if err != nil {
		t.Fatalf("Blockers: %v", err)
	}
	want := []string{wallet.BlockerBalanceNonzero, wallet.BlockerBalanceNonzero, wallet.BlockerDepositPending,
		wallet.BlockerHoldOpen, wallet.BlockerPurchasePending, wallet.BlockerWithdrawalPending}
	slices.Sort(want)
	if got := codes(bs); !slices.Equal(got, want) {
		t.Fatalf("codes = %v, want %v", got, want)
	}
	for _, b := range bs {
		if b.Code != wallet.BlockerBalanceNonzero {
			continue
		}
		switch b.Detail["wallet"] {
		case wallet.TypeGame:
			if b.ActionURL != "https://wallet.test" || b.Detail["amount_cents"] != int64(300) {
				t.Fatalf("game blocker = %+v, want amount 300 and the wallet home link", b)
			}
		case wallet.TypeReal:
			if b.ActionURL != "" || b.Detail["amount_cents"] != int64(1250) {
				t.Fatalf("real blocker = %+v, want amount 1250 and no link (no withdrawal rail)", b)
			}
		}
	}

	pp.purchases["p2"] = &wallet.ProductPurchase{PurchaseID: "p2", UserID: "u1", Status: wallet.ProductPurchasePending,
		CreatedAt: testNow.Add(-time.Minute).Format(time.RFC3339Nano)}
	sp.purchases = map[string]*wallet.SandboxPurchase{}
	bs, _ = e.Blockers(context.Background(), "u1")
	if !slices.Contains(codes(bs), wallet.BlockerPurchasePending) {
		t.Fatal("a payable product charge must block")
	}
}

func TestPurge_BlockerAcksBlockedAndTouchesNothing(t *testing.T) {
	repo := newStubRepo()
	repo.real.Balance = 5 // e.g. a PIX deposit that raced the lock
	er := newFakeErasureRepo()
	er.deposits = []wallet.PixDeposit{{Txid: "t1", UserID: "u1", Status: wallet.DepositConfirmed, PayerCPF: "52998224725"}}
	e := newErasureSvc(repo, &stubUserRepo{}, newStubSandboxPurchaseRepo(), newStubProductPurchaseRepo(), er, &kycclient.KYC{})

	ack, err := e.Purge(context.Background(), eraseMsg())
	if err != nil || ack.Result != erasure.ResultBlocked || len(ack.Blockers) == 0 {
		t.Fatalf("Purge = %+v, %v; want blocked with blockers", ack, err)
	}
	if len(er.retained) != 0 || er.profileCalls != 0 || er.deposits[0].PayerCPF == "" {
		t.Fatal("a blocked purge must not touch any data")
	}
}

func TestPurge_RetainsThenStripsAndIsIdempotent(t *testing.T) {
	er := newFakeErasureRepo()
	er.deposits = []wallet.PixDeposit{
		{Txid: "t1", UserID: "u1", Status: wallet.DepositConfirmed, PayerCPF: "52998224725", PayerName: "Fulano", AmountExpected: 100},
		{Txid: "t2", UserID: "u1", Status: wallet.DepositExpired}, // never paid: no PII, nothing to retain
	}
	er.withdrawals = []wallet.Withdrawal{{WithdrawalID: "w1", UserID: "u1", Status: wallet.WithdrawCompleted, PixKey: "52998224725"}}
	er.auditRows = 3
	e := newErasureSvc(newStubRepo(), &stubUserRepo{}, newStubSandboxPurchaseRepo(), newStubProductPurchaseRepo(), er, &kycclient.KYC{})

	ack, err := e.Purge(context.Background(), eraseMsg())
	if err != nil || ack.Result != erasure.ResultDone {
		t.Fatalf("Purge = %+v, %v", ack, err)
	}
	if ack.Counts[wallet.CountDepositsRetained] != 1 || ack.Counts[wallet.CountWithdrawalsRetained] != 1 ||
		ack.Counts[wallet.CountAuditAnonymized] != 3 || ack.Counts[wallet.CountUserProfileErased] != 1 {
		t.Fatalf("counts = %v", ack.Counts)
	}
	if d, ok := er.retained[wallet.TablePixDeposits+"#t1"].(wallet.PixDeposit); !ok || d.PayerCPF != "52998224725" {
		t.Fatalf("retained deposit = %#v, want the full copy", er.retained[wallet.TablePixDeposits+"#t1"])
	}
	if er.deposits[0].PayerCPF != "" || er.withdrawals[0].PixKey != "" {
		t.Fatal("operational rows still carry PII")
	}

	ack, err = e.Purge(context.Background(), eraseMsg())
	if err != nil || ack.Result != erasure.ResultDone || ack.Counts[wallet.CountDepositsRetained] != 0 {
		t.Fatalf("rerun = %+v, %v; want done with nothing left to retain", ack, err)
	}
	if d := er.retained[wallet.TablePixDeposits+"#t1"].(wallet.PixDeposit); d.PayerCPF != "52998224725" {
		t.Fatal("rerun overwrote the retained copy")
	}
}

func TestPurge_SelfExclusionCarriedByCPFHMAC(t *testing.T) {
	er := newFakeErasureRepo()
	users := &stubUserRepo{user: &wallet.User{SelfExclusion: &wallet.SelfExclusion{Period: "indefinite", RequestedAt: testNow.Format(time.RFC3339)}}}
	e := newErasureSvc(newStubRepo(), users, newStubSandboxPurchaseRepo(), newStubProductPurchaseRepo(), er,
		&kycclient.KYC{Level: "basic", CPF: "52998224725", CPFHMAC: "h1"})

	ack, err := e.Purge(context.Background(), eraseMsg())
	if err != nil || ack.Counts[wallet.CountSelfExclusionCarried] != 1 {
		t.Fatalf("Purge = %+v, %v", ack, err)
	}
	if er.carried["h1"].Period != "indefinite" || !er.carriedUntil["h1"].Equal(testNow.AddDate(wallet.ErasureRetentionYears, 0, 0)) {
		t.Fatalf("carried = %+v until %v", er.carried["h1"], er.carriedUntil["h1"])
	}
}

func TestPurge_SelfExcludedWithoutHMACIsRetriedNotDropped(t *testing.T) {
	er := newFakeErasureRepo()
	users := &stubUserRepo{user: &wallet.User{SelfExclusion: &wallet.SelfExclusion{Period: "indefinite", RequestedAt: testNow.Format(time.RFC3339)}}}
	e := newErasureSvc(newStubRepo(), users, newStubSandboxPurchaseRepo(), newStubProductPurchaseRepo(), er,
		&kycclient.KYC{Level: "basic", CPF: "52998224725"}) // account not upgraded yet

	if _, err := e.Purge(context.Background(), eraseMsg()); !errors.Is(err, errCPFHMACMissing) {
		t.Fatalf("err = %v, want errCPFHMACMissing (message redelivered)", err)
	}
	if er.profileCalls != 0 {
		t.Fatal("the profile (and its exclusion) must not be erased before the exclusion is carried")
	}
}

func TestPurge_SelfExcludedWithoutCPFCompletes(t *testing.T) {
	er := newFakeErasureRepo()
	users := &stubUserRepo{user: &wallet.User{SelfExclusion: &wallet.SelfExclusion{Period: "30d", Until: testNow.Add(time.Hour).Format(time.RFC3339)}}}
	e := newErasureSvc(newStubRepo(), users, newStubSandboxPurchaseRepo(), newStubProductPurchaseRepo(), er, &kycclient.KYC{})

	ack, err := e.Purge(context.Background(), eraseMsg())
	if err != nil || ack.Result != erasure.ResultDone || len(er.carried) != 0 {
		t.Fatalf("Purge = %+v, %v, carried %v; want done, nothing carried (no CPF to key on)", ack, err, er.carried)
	}
}
```

- [ ] **Step 3: Run them to verify they fail**

Run: `cd api && go test ./internal/services/ -run 'TestBlockers|TestPurge' -v`
Expected: build FAIL — `undefined: NewErasureService`, `undefined: errCPFHMACMissing`.

- [ ] **Step 4: Implement** — `api/internal/services/erasure.go`:

```go
package services

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/api-commons/erasure"
	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
)

// ErasureRepo is the persistence side of the purge
// (*repositories.ErasureRepository).
type ErasureRepo interface {
	DepositsForUser(ctx context.Context, userID string) ([]wallet.PixDeposit, error)
	WithdrawalsForUser(ctx context.Context, userID string) ([]wallet.Withdrawal, error)
	OpenHoldsForUser(ctx context.Context, userID string) ([]wallet.Hold, error)
	Retain(ctx context.Context, sub, source, sourceKey string, item any, until time.Time) error
	StripDepositPII(ctx context.Context, txid string) error
	StripWithdrawalPII(ctx context.Context, withdrawalID string) error
	EraseUserProfile(ctx context.Context, userID string) (bool, error)
	AnonymizeAudit(ctx context.Context, userID string) (int, error)
	PutCarriedSelfExclusion(ctx context.Context, cpfHMAC string, ex wallet.SelfExclusion, expiresAt time.Time) error
}

var (
	pendingDepositStatuses    = []string{wallet.DepositPending, wallet.DepositRefundPending, wallet.DepositRejectedCPF, wallet.DepositRefundFailed}
	pendingWithdrawalStatuses = []string{wallet.WithdrawProcessing, wallet.WithdrawRefundFail}

	errCPFHMACMissing = errors.New("erasure: ctech-account returned no cpf_hmac for a self-excluded user; retry once it serves the field")
)

const erasurePageSize = 100

// ErasureService is the wallet's side of the account-deletion saga
// (docs/plans/2026-10-07-account-deletion-participant.md): blockers for the
// eligibility endpoint, and the idempotent, resumable purge.
type ErasureService struct {
	wallets   WalletStore
	users     UserRepo
	sandbox   SandboxPurchaseRepo
	products  ProductPurchaseRepo
	repo      ErasureRepo
	kyc       KYCClient
	walletURL string // wallet UI home; action_url of a game-balance blocker
	now       func() time.Time
}

func NewErasureService(wallets WalletStore, users UserRepo, sandbox SandboxPurchaseRepo, products ProductPurchaseRepo, repo ErasureRepo, kyc KYCClient, walletURL string) *ErasureService {
	return &ErasureService{wallets: wallets, users: users, sandbox: sandbox, products: products, repo: repo,
		kyc: kyc, walletURL: walletURL, now: time.Now}
}

// Blockers lists what stops sub's data from being erased (inventory §4). It
// never creates a wallet: an unknown sub has no blockers.
func (e *ErasureService) Blockers(ctx context.Context, sub string) ([]erasure.Blocker, error) {
	var out []erasure.Blocker
	realW, gameW, _, err := e.wallets.LoadWallets(ctx, sub)
	if err != nil {
		return nil, err
	}
	// sandbox is virtual (Invariant #6): forfeited, never a blocker.
	for _, w := range []*wallet.Wallet{realW, gameW} {
		if w == nil || w.Balance == 0 {
			continue
		}
		b := erasure.Blocker{Code: wallet.BlockerBalanceNonzero, Detail: map[string]any{"wallet": w.Type, "amount_cents": w.Balance}}
		// game → real is always open (Invariant #9). real has no withdrawal
		// rail today (asaas-removal), so support settles it: no link.
		if w.Type == wallet.TypeGame && e.walletURL != "" {
			b.ActionURL = e.walletURL
		}
		out = append(out, b)
	}

	holds, err := e.repo.OpenHoldsForUser(ctx, sub)
	if err != nil {
		return nil, err
	}
	if len(holds) > 0 {
		out = append(out, erasure.Blocker{Code: wallet.BlockerHoldOpen, Detail: map[string]any{"count": len(holds)}})
	}

	deps, err := e.repo.DepositsForUser(ctx, sub)
	if err != nil {
		return nil, err
	}
	if n := countIn(deps, func(d wallet.PixDeposit) string { return d.Status }, pendingDepositStatuses); n > 0 {
		out = append(out, erasure.Blocker{Code: wallet.BlockerDepositPending, Detail: map[string]any{"count": n}})
	}

	wds, err := e.repo.WithdrawalsForUser(ctx, sub)
	if err != nil {
		return nil, err
	}
	if n := countIn(wds, func(w wallet.Withdrawal) string { return w.Status }, pendingWithdrawalStatuses); n > 0 {
		out = append(out, erasure.Blocker{Code: wallet.BlockerWithdrawalPending, Detail: map[string]any{"count": n}})
	}

	n, err := e.pendingPurchases(ctx, sub)
	if err != nil {
		return nil, err
	}
	if n > 0 {
		out = append(out, erasure.Blocker{Code: wallet.BlockerPurchasePending, Detail: map[string]any{"count": n}})
	}
	return out, nil
}

func countIn[T any](rows []T, status func(T) string, statuses []string) int {
	n := 0
	for _, r := range rows {
		if slices.Contains(statuses, status(r)) {
			n++
		}
	}
	return n
}

// pendingPurchases counts purchases whose money may still move: a charge that
// can still be paid (within its TTL; product rows never leave `pending` when
// unpaid) and a sandbox refund still in flight.
func (e *ErasureService) pendingPurchases(ctx context.Context, sub string) (int, error) {
	payableSince := e.now().Add(-sandboxPurchaseTTLMinutes * time.Minute)
	n := 0
	var start map[string]types.AttributeValue
	for {
		page, err := e.sandbox.ListByUser(ctx, sub, erasurePageSize, start)
		if err != nil {
			return 0, err
		}
		for _, p := range page.Items {
			if p.Status == wallet.SandboxPurchaseRefundPending ||
				(p.Status == wallet.SandboxPurchasePending && createdAfter(p.CreatedAt, payableSince)) {
				n++
			}
		}
		if len(page.LastEvaluatedKey) == 0 {
			break
		}
		start = page.LastEvaluatedKey
	}
	start = nil
	for {
		page, err := e.products.ListByUser(ctx, sub, erasurePageSize, start)
		if err != nil {
			return 0, err
		}
		for _, p := range page.Items {
			if p.Status == wallet.ProductPurchasePending && createdAfter(p.CreatedAt, payableSince) {
				n++
			}
		}
		if len(page.LastEvaluatedKey) == 0 {
			break
		}
		start = page.LastEvaluatedKey
	}
	return n, nil
}

// createdAfter fails closed: an unparseable timestamp counts as recent.
func createdAfter(createdAt string, t time.Time) bool {
	c, err := time.Parse(time.RFC3339Nano, createdAt)
	return err != nil || c.After(t)
}

// Purge is the erasure.PurgeFunc. Idempotent and resumable: every step
// re-reads from DynamoDB, retention is write-once, and the self-exclusion is
// carried BEFORE the profile that holds it is erased. It moves no money and
// never touches the ledger (Invariant #2). m.Organizations is ignored: the
// wallet holds no organization data.
func (e *ErasureService) Purge(ctx context.Context, m erasure.Message) (erasure.Ack, error) {
	blockers, err := e.Blockers(ctx, m.Sub)
	if err != nil {
		return erasure.Ack{}, err
	}
	if len(blockers) > 0 {
		return erasure.Ack{Result: erasure.ResultBlocked, Blockers: blockers}, nil
	}

	now := e.now()
	until := now.AddDate(wallet.ErasureRetentionYears, 0, 0)
	counts := map[string]int{}

	carried, err := e.carrySelfExclusion(ctx, m.Sub, now)
	if err != nil {
		return erasure.Ack{}, err
	}
	if carried {
		counts[wallet.CountSelfExclusionCarried] = 1
	}

	deps, err := e.repo.DepositsForUser(ctx, m.Sub)
	if err != nil {
		return erasure.Ack{}, err
	}
	for _, d := range deps {
		if d.PayerCPF == "" && d.PayerName == "" {
			continue
		}
		if err := e.repo.Retain(ctx, m.Sub, wallet.TablePixDeposits, d.Txid, d, until); err != nil {
			return erasure.Ack{}, err
		}
		if err := e.repo.StripDepositPII(ctx, d.Txid); err != nil {
			return erasure.Ack{}, err
		}
		counts[wallet.CountDepositsRetained]++
	}

	wds, err := e.repo.WithdrawalsForUser(ctx, m.Sub)
	if err != nil {
		return erasure.Ack{}, err
	}
	for _, w := range wds {
		if w.PixKey == "" {
			continue
		}
		if err := e.repo.Retain(ctx, m.Sub, wallet.TableWithdrawals, w.WithdrawalID, w, until); err != nil {
			return erasure.Ack{}, err
		}
		if err := e.repo.StripWithdrawalPII(ctx, w.WithdrawalID); err != nil {
			return erasure.Ack{}, err
		}
		counts[wallet.CountWithdrawalsRetained]++
	}

	erased, err := e.repo.EraseUserProfile(ctx, m.Sub)
	if err != nil {
		return erasure.Ack{}, err
	}
	if erased {
		counts[wallet.CountUserProfileErased] = 1
	}
	n, err := e.repo.AnonymizeAudit(ctx, m.Sub)
	if err != nil {
		return erasure.Ack{}, err
	}
	counts[wallet.CountAuditAnonymized] = n
	return erasure.Ack{Result: erasure.ResultDone, Counts: counts}, nil
}

// carrySelfExclusion keeps an active self-exclusion past the erasure, keyed
// by ctech-account's CPF keyed hash (D13). A user who never gave a CPF has
// nothing to key it on.
func (e *ErasureService) carrySelfExclusion(ctx context.Context, sub string, now time.Time) (bool, error) {
	u, err := e.users.Get(ctx, sub)
	if err != nil {
		return false, err
	}
	var cur *wallet.SelfExclusion
	if u != nil {
		cur = u.SelfExclusion
	}
	ex, expiresAt, ok := wallet.CarriedSelfExclusion(cur, now)
	if !ok {
		return false, nil
	}
	k, err := e.kyc.Get(ctx, sub)
	if err != nil {
		return false, fmt.Errorf("erasure: kyc for self-exclusion carry-over: %w", err)
	}
	if k == nil || k.CPF == "" {
		return false, nil
	}
	if k.CPFHMAC == "" {
		return false, errCPFHMACMissing
	}
	return true, e.repo.PutCarriedSelfExclusion(ctx, k.CPFHMAC, ex, expiresAt)
}
```

- [ ] **Step 5: Run the unit tests to verify they pass**

Run: `cd api && go vet ./... && go test ./internal/services/ -race -run 'TestBlockers|TestPurge' -v`
Expected: all `TestBlockers_*` and `TestPurge_*` PASS.

- [ ] **Step 6: Write the end-to-end integration test** — append to `api/tests/integration/erasure_test.go`. Add the imports `"gopkg.aoctech.app/api-commons/erasure"`, `"gopkg.aoctech.app/wallet/api/internal/kycclient"` and `"gopkg.aoctech.app/wallet/api/internal/services"`.

```go
func newErasureService(h *harness, kyc *kycclient.KYC) *services.ErasureService {
	cfg := &config.Config{TablePrefix: tablePrefix}
	return services.NewErasureService(h.repo, h.userRepo,
		repositories.NewSandboxPurchaseRepository(db, cfg), repositories.NewProductPurchaseRepository(db, cfg),
		erasureRepo(), &stubKYC{rec: kyc}, "https://wallet.test")
}

func TestErasureBlockersForUnknownUserCreateNothing(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	ghost := "ghost-" + id.New()
	bs, err := newErasureService(h, verified()).Blockers(ctx, ghost)
	if err != nil || len(bs) != 0 {
		t.Fatalf("Blockers(unknown) = %v, %v; want eligible", bs, err)
	}
	if ws, err := h.repo.GetWalletsByUser(ctx, ghost); err != nil || len(ws) != 0 {
		t.Fatalf("an eligibility check created wallets: %v, %v", ws, err)
	}
}

func TestErasurePurgeEndToEndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := "u-" + id.New()
	real := fund(t, h, user, 1000)
	if _, _, err := h.repo.Debit(ctx, repositories.Mutation{WalletID: real.WalletID, Amount: 1000, EntryType: wallet.EntryWithdraw,
		IdempotencyKey: "drain#" + id.New(), ReqHash: "drain"}); err != nil {
		t.Fatalf("drain: %v", err)
	}
	dep := wallet.PixDeposit{Txid: "tx-" + id.New(), UserID: user, WalletID: real.WalletID, Status: wallet.DepositConfirmed,
		PayerCPF: cpf, PayerName: "Fulano de Tal", AmountExpected: 1000, CreatedAt: repositories.NowStr()}
	putRaw(t, wallet.TablePixDeposits, dep)
	wd := wallet.Withdrawal{WithdrawalID: "wd-" + id.New(), UserID: user, WalletID: real.WalletID, Status: wallet.WithdrawCompleted, PixKey: cpf, Amount: 1000}
	putRaw(t, wallet.TableWithdrawals, wd)
	_ = h.userRepo.AcceptTerms(ctx, user)
	_ = h.userRepo.SetSelfExclusion(ctx, user, &wallet.SelfExclusion{Period: "indefinite", RequestedAt: repositories.NowStr()})
	_ = h.audit.Append(ctx, &wallet.AuditEvent{UserID: user, EventType: wallet.EventSelfExcluded, Actor: user, After: "indefinite", IP: "203.0.113.9", UserAgent: "UA/1"})
	before, _ := h.repo.Statement(ctx, real.WalletID, 100, nil)

	kyc := verified()
	kyc.CPFHMAC = "hmac-" + id.New()
	svc := newErasureService(h, kyc)
	msg := erasure.Message{Version: erasure.Version, Type: erasure.TypeErase, RequestID: "req-" + id.New(), Sub: user,
		Scope: erasure.ScopeAccount, Services: []string{wallet.ErasureServiceID}, IssuedAt: time.Now()}
	for run := 1; run <= 2; run++ {
		ack, err := svc.Purge(ctx, msg)
		if err != nil || ack.Result != erasure.ResultDone {
			t.Fatalf("run %d: Purge = %+v, %v", run, ack, err)
		}
		if run == 1 && (ack.Counts[wallet.CountDepositsRetained] != 1 || ack.Counts[wallet.CountWithdrawalsRetained] != 1 ||
			ack.Counts[wallet.CountAuditAnonymized] != 1 || ack.Counts[wallet.CountSelfExclusionCarried] != 1) {
			t.Fatalf("run 1 counts = %v", ack.Counts)
		}
	}

	// Erased / anonymized.
	if d, _ := h.repo.GetDeposit(ctx, dep.Txid); d.PayerCPF != "" || d.PayerName != "" || d.AmountExpected != 1000 {
		t.Fatalf("deposit = %+v", d)
	}
	if w, _ := h.repo.GetWithdrawal(ctx, wd.WithdrawalID); w.PixKey != "" || w.Amount != 1000 {
		t.Fatalf("withdrawal = %+v", w)
	}
	if u, _ := h.userRepo.Get(ctx, user); u.SelfExclusion != nil || !u.TermsAccepted() {
		t.Fatalf("profile = %+v", u)
	}
	if evs, _ := h.audit.List(ctx, user, 10); len(evs) != 1 || evs[0].IP != "" || evs[0].UserAgent != "" {
		t.Fatalf("audit = %+v", evs)
	}
	// Retained.
	var rec retainedDeposit
	_ = attributevalue.UnmarshalMap(getRaw(t, wallet.TableErasureRetention, wallet.RetentionSubPrefix+user,
		repositories.RetentionSK(wallet.TablePixDeposits, dep.Txid)), &rec)
	if rec.Item.PayerCPF != cpf {
		t.Fatalf("retained deposit = %+v, want the payer CPF kept for PLD", rec.Item)
	}
	if ex, err := erasureRepo().GetCarriedSelfExclusion(ctx, kyc.CPFHMAC, time.Now()); err != nil || ex == nil || ex.Period != "indefinite" {
		t.Fatalf("carried exclusion = %+v, %v", ex, err)
	}
	if after, _ := h.repo.Statement(ctx, real.WalletID, 100, nil); len(after.Items) != len(before.Items) {
		t.Fatalf("ledger changed: %d → %d entries (Invariant #2)", len(before.Items), len(after.Items))
	}
}

func TestErasurePurgeBlockedByBalanceErasesNothing(t *testing.T) {
	ctx := context.Background()
	h := newHarness(verified())
	user := "u-" + id.New()
	real := fund(t, h, user, 500)
	dep := wallet.PixDeposit{Txid: "tx-" + id.New(), UserID: user, WalletID: real.WalletID, Status: wallet.DepositConfirmed,
		PayerCPF: cpf, AmountExpected: 500, CreatedAt: repositories.NowStr()}
	putRaw(t, wallet.TablePixDeposits, dep)
	msg := erasure.Message{Version: erasure.Version, Type: erasure.TypeErase, RequestID: "req-" + id.New(), Sub: user,
		Scope: erasure.ScopeAccount, Services: []string{wallet.ErasureServiceID}, IssuedAt: time.Now()}
	ack, err := newErasureService(h, verified()).Purge(ctx, msg)
	if err != nil || ack.Result != erasure.ResultBlocked || ack.Blockers[0].Code != wallet.BlockerBalanceNonzero {
		t.Fatalf("Purge = %+v, %v; want blocked by balance", ack, err)
	}
	if d, _ := h.repo.GetDeposit(ctx, dep.Txid); d.PayerCPF != cpf {
		t.Fatal("a blocked purge stripped data")
	}
}
```

- [ ] **Step 7: Run everything**

Run: `cd api && go vet ./... && go test ./... -race && DYNAMODB_ENDPOINT=http://localhost:8123 go test -tags integration -race -count=1 ./tests/integration/ -v 2>&1 | tail -40`
Expected: unit suite `ok`. Integration: every `TestErasure*` PASS and every pre-existing test PASS (this gates the new sqs dependency, R1).

- [ ] **Step 8: Commit**

```bash
git add api/go.mod api/go.sum api/internal/kycclient/kycclient.go api/internal/services/erasure.go api/internal/services/erasure_test.go api/tests/integration/erasure_test.go
git commit -m "feat(api): erasure blockers and idempotent purge with PLD retention"
```

---

### Task 6: Re-apply a carried self-exclusion at gambling activation

**Files:**
- Modify: `api/internal/services/wallet.go` (`ActivateGambling`)
- Modify: `api/internal/services/responsible.go` (new `applyCarriedExclusion`)
- Create: `api/internal/services/erasure_carry_test.go`

**Interfaces:**
- Consumes: `s.carried CarriedExclusionStore` (Task 4), `kycclient.KYC.CPFHMAC` (Task 5), `wallet.EventSelfExclusionCarried`, `wallet.ActorErasureCarryOver`.
- Produces: `func (s *WalletService) applyCarriedExclusion(ctx context.Context, userID string) error`.

- [ ] **Step 1: Write the failing test** — `api/internal/services/erasure_carry_test.go`:

```go
package services

import (
	"context"
	"testing"
	"time"

	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
	"gopkg.aoctech.app/wallet/api/internal/kycclient"
	"gopkg.aoctech.app/wallet/api/internal/problem"
)

type stubCarried struct {
	ex      *wallet.SelfExclusion
	gotHMAC string
}

func (c *stubCarried) GetCarriedSelfExclusion(_ context.Context, h string, _ time.Time) (*wallet.SelfExclusion, error) {
	c.gotHMAC = h
	return c.ex, nil
}

func carrySvc(kyc *kycclient.KYC, carried *stubCarried) (*WalletService, *stubUserRepo, *stubAudit) {
	users := &stubUserRepo{user: &wallet.User{GamblingAddendumVersion: wallet.CurrentGamblingAddendumVersion}}
	audit := &stubAudit{}
	svc := NewWalletService(newStubRepo(), users, audit, &stubLocker{}, nil, &stubKYC{rec: kyc})
	svc.SetErasure(nil, carried)
	return svc, users, audit
}

// D13: deleting an account must not be a way out of one's own self-exclusion.
func TestActivateGamblingReappliesCarriedSelfExclusion(t *testing.T) {
	until := time.Now().Add(20 * 24 * time.Hour).Format(time.RFC3339)
	carried := &stubCarried{ex: &wallet.SelfExclusion{Period: "30d", RequestedAt: time.Now().Add(-10 * 24 * time.Hour).Format(time.RFC3339), Until: until}}
	svc, users, audit := carrySvc(&kycclient.KYC{Level: "basic", CPF: "52998224725", CPFHMAC: "h1"}, carried)

	_, _, err := svc.ActivateGambling(context.Background(), "u-new", "basic", "", "", 100, 200, 300)
	isProblem(t, err, problem.TypeSelfExcluded)
	if carried.gotHMAC != "h1" {
		t.Fatalf("looked up %q, want h1", carried.gotHMAC)
	}
	if users.user.SelfExclusion == nil || users.user.SelfExclusion.Until != until {
		t.Fatalf("exclusion not re-applied: %+v", users.user.SelfExclusion)
	}
	last := audit.events[len(audit.events)-1]
	if last.EventType != wallet.EventSelfExclusionCarried || last.Actor != wallet.ActorErasureCarryOver {
		t.Fatalf("audit = %+v, want a carried-exclusion event", last)
	}
}

func TestActivateGamblingWithoutCarriedExclusionProceeds(t *testing.T) {
	carried := &stubCarried{}
	svc, _, _ := carrySvc(&kycclient.KYC{Level: "basic", CPF: "52998224725", CPFHMAC: "h1"}, carried)
	if _, _, err := svc.ActivateGambling(context.Background(), "u-new", "basic", "", "", 100, 200, 300); err != nil {
		t.Fatalf("ActivateGambling: %v", err)
	}
}

func TestActivateGamblingSkipsLookupWithoutHMAC(t *testing.T) {
	carried := &stubCarried{ex: &wallet.SelfExclusion{Period: "indefinite"}}
	svc, _, _ := carrySvc(&kycclient.KYC{Level: "basic", CPF: "52998224725"}, carried)
	if _, _, err := svc.ActivateGambling(context.Background(), "u-new", "basic", "", "", 100, 200, 300); err != nil {
		t.Fatalf("ActivateGambling: %v", err)
	}
	if carried.gotHMAC != "" {
		t.Fatal("no cpf_hmac served: there is nothing to look up")
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `cd api && go test ./internal/services/ -run TestActivateGambling -v`
Expected: `TestActivateGamblingReappliesCarriedSelfExclusion` FAILs (activation succeeds, `gotHMAC` is empty). The other two PASS.

- [ ] **Step 3: Implement.** Append to `api/internal/services/responsible.go`:

```go
// applyCarriedExclusion re-applies a self-exclusion kept from an erased
// account of the same CPF (D13) and refuses activation while it lasts. It is
// a no-op when the deletion feature is off (no store) or account serves no
// cpf_hmac. A KYC read error fails activation: activation happens once per
// user, and letting it through unchecked is the one outcome D13 forbids.
func (s *WalletService) applyCarriedExclusion(ctx context.Context, userID string) error {
	if s.carried == nil {
		return nil
	}
	k, err := s.kyc.Get(ctx, userID)
	if err != nil {
		return err
	}
	if k == nil || k.CPFHMAC == "" {
		return nil
	}
	ex, err := s.carried.GetCarriedSelfExclusion(ctx, k.CPFHMAC, time.Now())
	if err != nil || ex == nil {
		return err
	}
	if err := s.users.SetSelfExclusion(ctx, userID, ex); err != nil {
		return err
	}
	if err := s.audit.Append(ctx, &wallet.AuditEvent{
		UserID: userID, EventType: wallet.EventSelfExclusionCarried, Actor: wallet.ActorErasureCarryOver, After: ex.Period,
	}); err != nil {
		return err
	}
	return problem.SelfExcluded(ex.Until)
}
```

In `ActivateGambling` (`api/internal/services/wallet.go`), immediately after the existing

```go
	u, err := s.requireNotExcluded(ctx, userID)
	if err != nil {
		return nil, nil, err
	}
```

insert:

```go
	if err := s.applyCarriedExclusion(ctx, userID); err != nil {
		return nil, nil, err
	}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd api && go vet ./... && go test ./internal/services/ -race -v -run 'TestActivateGambling|TestExclusion|TestErasureGate' && go test ./... -race`
Expected: all PASS, suite `ok`.

- [ ] **Step 5: Commit**

```bash
git add api/internal/services/
git commit -m "feat(api): re-apply a self-exclusion carried from an erased account"
```

---

### Task 7: Fail-closed revocation on user state-changing routes

**Files:**
- Create: `api/internal/middleware/revocation.go`
- Create: `api/internal/middleware/revocation_test.go`
- Modify: `api/internal/api/v1/router.go` (`Register` signature, verifier, `fresh` on routes)
- Modify: `api/internal/api/v1/router_test.go` (call site)
- Modify: `api/internal/app/app.go` (`registerRoutes` passes the backend; full wiring in Task 9)

**Interfaces:**
- Consumes: `jwtverify.CheckRevoked`, `jwtverify.ErrTokenRevoked`, `problem.ServiceUnavailable`.
- Produces:
  - `func middleware.RequireUnrevoked(revocation cache.Backend) fiber.Handler`;
  - `func v1.Register(app *fiber.App, c cache.Backend, revocation cache.Backend, cfg *config.Config, clients *awsclient.Clients, pixClient pix.PixClient, svc *services.WalletService, userSvc *services.UserService, erasureSvc *services.ErasureService, wsRegistry ws.Registry)`. The `erasureSvc` parameter is used in Task 8 and is added now so the signature changes once.

- [ ] **Step 1: Write the failing test** — `api/internal/middleware/revocation_test.go`:

```go
package middleware

import (
	"context"
	"errors"
	"testing"
	"time"

	"gopkg.aoctech.app/api-commons/cache"
	"gopkg.aoctech.app/api-commons/jwtverify"
)

type downCache struct{ cache.Backend }

func (downCache) Get(context.Context, string) ([]byte, bool, error) {
	return nil, false, errors.New("valkey down")
}

func TestRequireUnrevoked(t *testing.T) {
	rev := cache.NewMemoryBackend(10)
	cutoff := time.Now()
	if err := jwtverify.Revoke(context.Background(), rev, "u-locked", cutoff, jwtverify.RevocationTTL); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name  string
		claim *Claims
		rev   cache.Backend
		want  int
	}{
		{"active user", &Claims{Sub: "u-ok", SID: "s", IssuedAt: cutoff.Unix()}, rev, 200},
		{"token of a locked user", &Claims{Sub: "u-locked", SID: "s", IssuedAt: cutoff.Unix()}, rev, 401},
		{"token issued after the cut-off (request cancelled)", &Claims{Sub: "u-locked", SID: "s", IssuedAt: cutoff.Add(time.Minute).Unix()}, rev, 200},
		{"revocation list unreachable fails closed", &Claims{Sub: "u-ok", SID: "s", IssuedAt: cutoff.Unix()}, downCache{}, 503},
		{"no backend configured (route tests)", &Claims{Sub: "u-ok", SID: "s"}, nil, 200},
		{"no claims", nil, rev, 401},
	}
	for _, tc := range cases {
		if got := gateApp(t, tc.claim, RequireUnrevoked(tc.rev)); got != tc.want {
			t.Errorf("%s: got %d want %d", tc.name, got, tc.want)
		}
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `cd api && go test ./internal/middleware/ -run TestRequireUnrevoked -v`
Expected: build FAIL — `undefined: RequireUnrevoked`.

- [ ] **Step 3: Implement** — `api/internal/middleware/revocation.go`:

```go
package middleware

import (
	"errors"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/api-commons/cache"
	"gopkg.aoctech.app/api-commons/jwtverify"
	"gopkg.aoctech.app/wallet/api/internal/problem"
)

// RequireUnrevoked re-checks ctech-account's JWT revocation list for a route
// that changes a user's money or consent, and FAILS CLOSED (saga protocol §5,
// decision D3): an unreachable list is a 503, never a pass. The auth
// middleware already verified the token and ran the same check fail-open;
// this gives jwtverify.VerifyClaimsStrict's semantics without parsing the
// token twice. A nil backend disables it (route tests only: config.Load
// requires VALKEY_REVOCATION_URL in prod). Register after the auth middleware.
func RequireUnrevoked(revocation cache.Backend) fiber.Handler {
	return func(c fiber.Ctx) error {
		if revocation == nil {
			return c.Next()
		}
		cl := GetClaims(c)
		if cl == nil {
			return problem.Unauthorized("credenciais ausentes").Send(c)
		}
		err := jwtverify.CheckRevoked(c.Context(), revocation, cl.Sub, cl.IssuedAt)
		switch {
		case err == nil:
			return c.Next()
		case errors.Is(err, jwtverify.ErrTokenRevoked):
			return problem.Unauthorized("sessão encerrada").Send(c)
		default:
			return problem.ServiceUnavailable("não foi possível validar a sessão; tente novamente").WithCause(err).Send(c)
		}
	}
}
```

- [ ] **Step 4: Wire it into the router.** In `api/internal/api/v1/router.go`:

  Change the `handlers` struct and `Register` signature to:

```go
type handlers struct {
	svc        *services.WalletService
	userSvc    *services.UserService
	erasureSvc *services.ErasureService
}

// Register mounts all wallet routes under /v1.0. revocation is the shared
// Valkey DB 0 holding ctech-account's JWT revocation list (nil in route tests).
func Register(app *fiber.App, c cache.Backend, revocation cache.Backend, cfg *config.Config, clients *awsclient.Clients, pixClient pix.PixClient, svc *services.WalletService, userSvc *services.UserService, erasureSvc *services.ErasureService, wsRegistry ws.Registry) {
	h := &handlers{svc: svc, userSvc: userSvc, erasureSvc: erasureSvc}
	verifier := middleware.NewVerifier(cfg.CtechJWKSURL, cfg.ServiceAudience, cfg.CtechIssuerURL, c)
	if revocation != nil {
		// Fail-open on every route (D3); money routes re-check fail-closed via fresh.
		verifier.WithRevocation(revocation)
	}
	auth := verifier.Middleware()
	// fresh: user routes that change money or consent refuse a locked user's
	// token even when Valkey is down (saga protocol §5).
	fresh := middleware.RequireUnrevoked(revocation)
```

  (Keep the rest of the existing body.) Insert `fresh,` as the middleware immediately before the handler on exactly these routes:

```go
	a.Post("/terms-addendum/accept", middleware.RequireUserScope(middleware.ScopeWalletTermsWrite), fresh, h.acceptTermsAddendum)
	w.Post("/sandbox/purchase", middleware.RequireUserScope(middleware.ScopeWalletSandboxPurchasesWrite), fresh, h.purchaseSandbox)
	w.Post("/sandbox/purchases", middleware.RequireUserScope(middleware.ScopeWalletSandboxPurchasesWrite), fresh, h.purchaseSandboxDirect)
	w.Post("/sandbox/purchases/:id/refund", middleware.RequireUserScope(middleware.ScopeWalletSandboxPurchasesWrite), fresh, h.refundSandboxPurchase)
	w.Post("/game/withdraw", middleware.RequireUserScope(middleware.ScopeWalletGameWrite), fresh, h.gameWithdraw)
	w.Post("/gambling/self-exclude", middleware.RequireUserScope(middleware.ScopeWalletGamblingWrite), fresh, h.selfExclude)
	w.Post("/gambling/self-exclude/revoke", middleware.RequireUserScope(middleware.ScopeWalletGamblingWrite), fresh, h.revokeSelfExclusion)
	w.Put("/gambling/limits", middleware.RequireUserScope(middleware.ScopeWalletGamblingWrite), fresh, h.putGameLimits)
	w.Delete("/gambling/limits/pending", middleware.RequireUserScope(middleware.ScopeWalletGamblingWrite), fresh, h.cancelPendingLimits)
	w.Post("/gambling/activate", middleware.RequireUserScope(middleware.ScopeWalletGamblingWrite), middleware.RequireKYC(middleware.KYCBasic), fresh, h.activateGambling)
	// inside if cfg.GamblingEnabled:
		w.Post("/game/deposit", middleware.RequireUserScope(middleware.ScopeWalletGameWrite), middleware.RequireKYC(middleware.KYCVerified), fresh, h.gameDeposit)
```

  Internal M2M routes do not get `fresh`: their token's `sub` is the client, which is never revoked. The locked *target* user is refused by the service gate (Task 4).

  In `api/internal/api/v1/router_test.go`, change the call to:

```go
	Register(app, cache.NewMemoryBackend(testCacheSize), nil, cfg, nil, nil, nil, nil, nil, nil)
```

  In `api/internal/app/app.go`, temporarily keep it compiling: in `registerRoutes`, change the call to `apiv1.Register(app, c, nil, cfg, clients, pixClient, svc, userSvc, nil, wsRegistry)`. Task 9 replaces both `nil`s.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `cd api && go vet ./... && go test ./internal/middleware/ ./internal/api/v1/ -race -v -run 'TestRequireUnrevoked|TestGated|TestReturnRoute|TestRingFence' && go test ./... -race`
Expected: all PASS, suite `ok`.

- [ ] **Step 6: Commit**

```bash
git add api/internal/middleware/revocation.go api/internal/middleware/revocation_test.go api/internal/api/v1/router.go api/internal/api/v1/router_test.go api/internal/app/app.go
git commit -m "feat(api): fail-closed JWT revocation check on money and consent routes"
```

---

### Task 8: Eligibility endpoint and its scope

**Files:**
- Create: `api/internal/api/v1/erasure.go`
- Modify: `api/internal/api/v1/router.go` (route)
- Modify: `api/internal/api/v1/router_test.go` (route test)
- Modify: `api/internal/middleware/scope.go` (constant)
- Modify: `api/internal/middleware/scope_manifest_test.go` (`wantInternal`)
- Modify: `api/internal/oauthresource/scope-manifest.json` (scope entry)

**Interfaces:**
- Consumes: `(*services.ErasureService).Blockers`, `erasure.NewEligibility`.
- Produces: `middleware.ScopeWalletErasureEligibility = "internal:wallet:erasure-eligibility"`, route `GET /v1.0/internal/erasure/eligibility/:sub`.

- [ ] **Step 1: Write the failing tests.** Append to `api/internal/api/v1/router_test.go`:

```go
// ctech-account calls {participant url}/internal/erasure/eligibility/{sub}
// with url = https://wallet-api.aoctech.app/v1.0 (account-deletion phase 3).
func TestErasureEligibilityRouteIsRegisteredAtTheContractPath(t *testing.T) {
	const path = "/v1.0/internal/erasure/eligibility/:sub"
	for _, r := range routerApp(t, false).GetRoutes() {
		if r.Method == http.MethodGet && r.Path == path {
			return
		}
	}
	t.Fatalf("%s is missing", path)
}
```

In `api/internal/middleware/scope_manifest_test.go`, add `ScopeWalletErasureEligibility` to `wantInternal`.

- [ ] **Step 2: Run them to verify they fail**

Run: `cd api && go test ./internal/api/v1/ ./internal/middleware/ -run 'TestErasureEligibilityRoute|TestScopeManifest' -v`
Expected: FAIL — `undefined: ScopeWalletErasureEligibility` (build).

- [ ] **Step 3: Implement.**

  In `api/internal/middleware/scope.go`, add to the const block after `ScopeWalletChargeAmount`:

```go
	// ScopeWalletErasureEligibility is carried by the short-lived token
	// ctech-account mints to ask whether a user can be erased (account-
	// deletion saga §4.1). Read-only; granted to no client.
	ScopeWalletErasureEligibility = "internal:wallet:erasure-eligibility"
```

  In `api/internal/oauthresource/scope-manifest.json`, append to `scopes`:

```json
    {
      "name": "internal:wallet:erasure-eligibility",
      "descriptions": {
        "pt-BR": "Consultar se um usuario pode ter a conta excluida (bloqueios da carteira).",
        "en": "Check whether a user's account can be deleted (wallet blockers)."
      },
      "visibility": "internal",
      "status": "active"
    }
```

  Create `api/internal/api/v1/erasure.go`:

```go
package v1

import (
	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/api-commons/erasure"
)

// erasureEligibility answers ctech-account's deletion pre-check (saga
// protocol §4.1). An unknown sub is eligible; an error is a 500 problem,
// which account treats as "not eligible, retry".
func (h *handlers) erasureEligibility(c fiber.Ctx) error {
	blockers, err := h.erasureSvc.Blockers(c.Context(), c.Params("sub"))
	if err != nil {
		return sendProblem(c, err)
	}
	return c.JSON(erasure.NewEligibility(blockers...))
}
```

  In `api/internal/api/v1/router.go`, after the `internal := v1.Group("/internal", auth)` line, add:

```go
	// Account-deletion saga pre-check, called by ctech-account with a token it
	// mints itself (docs/plans/2026-10-07-account-deletion-participant.md).
	internal.Get("/erasure/eligibility/:sub", middleware.RequireScope(middleware.ScopeWalletErasureEligibility), h.erasureEligibility)
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd api && go vet ./... && go test ./... -race`
Expected: `ok` for every package, including `TestErasureEligibilityRouteIsRegisteredAtTheContractPath` and `TestScopeManifestMatchesEnforcedScopes`.

- [ ] **Step 5: Commit**

```bash
git add api/internal/api/v1/erasure.go api/internal/api/v1/router.go api/internal/api/v1/router_test.go api/internal/middleware/scope.go api/internal/middleware/scope_manifest_test.go api/internal/oauthresource/scope-manifest.json
git commit -m "feat(api): erasure eligibility endpoint for ctech-account"
```

---

### Task 9: Config and process wiring — store, gate, consumer, acks, revocation backend

**Files:**
- Modify: `api/internal/config/config.go`
- Modify: `api/internal/config/config_test.go`
- Modify: `api/internal/kycclient/kycclient.go` (export `PathToken`)
- Modify: `api/internal/app/app.go`

**Interfaces:**
- Consumes:
  - `erasure.NewStore`, `erasure.NewConsumer`, `erasure.NewAckClient`, `oauth2client.New`;
  - `services.NewErasureService`, `(*WalletService).SetErasure`, `(*UserService).SetErasureGate`, `repositories.NewErasureRepository`.
- Produces: env `ERASURE_QUEUE_URL`, `VALKEY_REVOCATION_URL` (config fields `ErasureQueueURL`, `ValkeyRevocationURL`); `kycclient.PathToken`.

- [ ] **Step 1: Write the failing config tests.** In `api/internal/config/config_test.go`, add `t.Setenv("ERASURE_QUEUE_URL", "https://sqs.us-east-1.amazonaws.com/868899309401/prod-ctech-wallet-erasure")` and `t.Setenv("VALKEY_REVOCATION_URL", "redis://valkey.internal:6379")` to `TestLoadSucceedsWithValkeyURLInProd` and `TestLoadFailsClosedWithoutValkeyURLInProd`. Then append:

```go
func prodEnv(t *testing.T) {
	t.Helper()
	t.Setenv("ENVIRONMENT", "prod")
	t.Setenv("SERVICE_AUDIENCE", "https://wallet.aoctech.app")
	t.Setenv("CTECH_URL", "https://account.aoctech.app")
	t.Setenv("CTECH_ISSUER_URL", "https://account.aoctech.app")
	t.Setenv("CORS_ALLOWED_ORIGINS", "https://wallet.aoctech.app")
	t.Setenv("TABLE_PREFIX", "prod")
	t.Setenv("PIX_GATEWAY_FUNCTION_NAME", "prod-pix-gateway-outbound")
	t.Setenv("VALKEY_URL", "redis://valkey.internal:6379/2")
	t.Setenv("ERASURE_QUEUE_URL", "https://sqs.us-east-1.amazonaws.com/868899309401/prod-ctech-wallet-erasure")
	t.Setenv("VALKEY_REVOCATION_URL", "redis://valkey.internal:6379")
}

// D1/D3: in prod the wallet must neither skip the deletion lock nor read the
// revocation list from the wrong Valkey DB.
func TestLoadFailsClosedWithoutErasureConfigInProd(t *testing.T) {
	for _, unset := range []string{"ERASURE_QUEUE_URL", "VALKEY_REVOCATION_URL"} {
		prodEnv(t)
		t.Setenv(unset, "")
		if _, err := Load(); err == nil {
			t.Errorf("Load succeeded in prod with %s unset", unset)
		}
	}
}

func TestLoadReconcileNeedsNoErasureConfig(t *testing.T) {
	prodEnv(t)
	t.Setenv("ERASURE_QUEUE_URL", "")
	t.Setenv("VALKEY_REVOCATION_URL", "")
	if _, err := LoadReconcile(); err != nil {
		t.Fatalf("LoadReconcile: %v", err)
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `cd api && go test ./internal/config/ -v`
Expected: `TestLoadFailsClosedWithoutErasureConfigInProd` FAILs (Load succeeds).

- [ ] **Step 3: Implement config.** In `api/internal/config/config.go`, add to `Config` after `RedisURL`:

```go
	// ValkeyRevocationURL is the SHARED Valkey base URL (DB 0), where
	// ctech-account writes the JWT revocation list. VALKEY_URL is the wallet's
	// own DB 2 and cannot see those keys (saga protocol §5).
	ValkeyRevocationURL string `env:"VALKEY_REVOCATION_URL"`

	// ErasureQueueURL is the wallet's account-deletion SQS queue. Set = the
	// deletion lock, the purge consumer and the self-exclusion carry-over
	// are ON; empty = OFF (local dev only).
	ErasureQueueURL string `env:"ERASURE_QUEUE_URL"`
```

and in `Load`, before `return cfg, nil`:

```go
	if cfg.ErasureQueueURL == "" && cfg.Env == "prod" {
		// Fail closed: without it a user in deletion grace keeps moving money
		// through M2M routes (decision D1), and no purge ever runs.
		return nil, fmt.Errorf("config: ERASURE_QUEUE_URL must be set in production (account-deletion lock)")
	}
	if cfg.ValkeyRevocationURL == "" && cfg.Env == "prod" {
		return nil, fmt.Errorf("config: VALKEY_REVOCATION_URL must be set in production (JWT revocation list, Valkey DB 0)")
	}
```

In `api/internal/kycclient/kycclient.go`, rename the const `pathToken` to `PathToken` (and its one use), with the comment `// PathToken is ctech-account's OAuth token endpoint, appended to CTECH_URL.`

- [ ] **Step 4: Implement app wiring** in `api/internal/app/app.go`.

  Add imports: `"net/http"`, `"strings"`, `"github.com/aws/aws-sdk-go-v2/service/sqs"`, `"gopkg.aoctech.app/api-commons/erasure"`, `"gopkg.aoctech.app/api-commons/oauth2client"`, `"gopkg.aoctech.app/wallet/api/internal/domain/wallet"`.

  In `fx.Provide(...)`, add `newRevocationCache`, `newSQSClient`, `newErasureStore`, `repositories.NewErasureRepository`, `newErasureService`. Add `fx.Invoke(startErasureConsumer)` after `fx.Invoke(registerRoutes)`.

  Add:

```go
// RevocationCache is the shared Valkey DB 0 where ctech-account writes the JWT
// revocation list. A distinct type so fx never confuses it with the wallet's
// own cache.Backend (DB 2).
type RevocationCache interface{ cache.Backend }

func newRevocationCache(lc fx.Lifecycle, cfg *config.Config) (RevocationCache, error) {
	if cfg.ValkeyRevocationURL == "" {
		slog.Warn("VALKEY_REVOCATION_URL not set — no token is ever seen as revoked (dev only)")
		return cache.NewMemoryBackend(1), nil
	}
	rb, err := cache.NewRedisBackend(cfg.ValkeyRevocationURL)
	if err != nil {
		return nil, fmt.Errorf("revocation valkey: %w", err)
	}
	lc.Append(fx.Hook{OnStop: func(context.Context) error { rb.Client().Close(); return nil }})
	return rb, nil
}

func newSQSClient(cfg *config.Config) (*sqs.Client, error) {
	awsCfg, err := awscfg.LoadDefaultConfig(context.Background(), awscfg.WithRegion(cfg.AWSRegion))
	if err != nil {
		return nil, fmt.Errorf("aws config: %w", err)
	}
	return sqs.NewFromConfig(awsCfg), nil
}

// newErasureStore is nil when the deletion feature is off (ERASURE_QUEUE_URL
// unset): local dev may lack the table, and a store error fails every money
// operation closed.
func newErasureStore(cfg *config.Config, db *dynamodb.Client) *erasure.Store {
	if cfg.ErasureQueueURL == "" {
		return nil
	}
	return erasure.NewStore(db, cfg.TablePrefix+"_"+wallet.ErasureStateTableSegment, wallet.ErasureTombstoneTTL)
}

func newErasureService(repo *repositories.WalletRepository, users *repositories.UserRepository, sp *repositories.SandboxPurchaseRepository, pp *repositories.ProductPurchaseRepository, er *repositories.ErasureRepository, k services.KYCClient, cfg *config.Config) *services.ErasureService {
	return services.NewErasureService(repo, users, sp, pp, er, k, cfg.ServiceAudience)
}

// startErasureConsumer runs the account-deletion SQS consumer for the process
// lifetime. Every API instance runs one: the consumer takes one message per
// receive and erasure.Store uses optimistic concurrency, so replicas are safe.
func startErasureConsumer(lc fx.Lifecycle, cfg *config.Config, sqsClient *sqs.Client, store *erasure.Store, svc *services.ErasureService) {
	if store == nil {
		slog.Warn("ERASURE_QUEUE_URL not set — account-deletion lock and purge are OFF (dev only)")
		return
	}
	httpClient := &http.Client{Timeout: 10 * time.Second}
	base := strings.TrimRight(cfg.CtechURL, "/")
	acks := erasure.NewAckClient(httpClient, base+wallet.PathAccountErasureAck,
		oauth2client.New(httpClient, nil, base+kycclient.PathToken, cfg.WalletClientID, cfg.WalletClientSecret, wallet.ScopeAccountErasureAck))
	consumer := erasure.NewConsumer(sqsClient, cfg.ErasureQueueURL, wallet.ErasureServiceID, store, svc.Purge, acks)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			go func() {
				defer close(done)
				_ = consumer.Run(ctx)
			}()
			return nil
		},
		OnStop: func(stopCtx context.Context) error {
			cancel()
			select {
			case <-done:
			case <-stopCtx.Done():
			}
			return nil
		},
	})
}
```

  Replace `newWalletService` and `newUserService` with:

```go
func newWalletService(repo *repositories.WalletRepository, users *repositories.UserRepository, audit *repositories.AuditRepository, l *lock.Locker, p pix.PixClient, k services.KYCClient, sandboxPurchases *repositories.SandboxPurchaseRepository, productPurchases *repositories.ProductPurchaseRepository, m2mClients map[string]services.M2MClient, store *erasure.Store, er *repositories.ErasureRepository) *services.WalletService {
	svc := services.NewWalletService(repo, users, audit, l, p, k)
	svc.SetSandboxPurchases(sandboxPurchases)
	svc.SetProductPurchases(productPurchases)
	svc.SetM2MClients(m2mClients)
	if store != nil { // never pass a nil *Store through the interface
		svc.SetErasure(store, er)
	}
	return svc
}

func newUserService(repo *repositories.UserRepository, audit *repositories.AuditRepository, store *erasure.Store) *services.UserService {
	svc := services.NewUserService(repo, audit)
	if store != nil {
		svc.SetErasureGate(store)
	}
	return svc
}
```

  Replace `registerRoutes` with:

```go
func registerRoutes(app *fiber.App, c cache.Backend, rev RevocationCache, cfg *config.Config, clients *awsclient.Clients, pixClient pix.PixClient, svc *services.WalletService, userSvc *services.UserService, erasureSvc *services.ErasureService, wsRegistry ws.Registry) {
	svc.SetBroadcaster(wsRegistry)
	apiv1.Register(app, c, rev, cfg, clients, pixClient, svc, userSvc, erasureSvc, wsRegistry)
}
```

  Check: `cmd/reconcile/main.go` builds its own `WalletService` and must stay without a gate. Run `grep -n "SetErasure" cmd/` and expect no output.

- [ ] **Step 5: Run everything**

Run: `cd api && go build ./... && go vet ./... && go test ./... -race && grep -n "SetErasure" cmd/ ; echo "exit=$?"`
Expected: build, vet and tests `ok`; the grep prints nothing and `exit=1`.

- [ ] **Step 6: Commit**

```bash
git add api/internal/config/ api/internal/kycclient/kycclient.go api/internal/app/app.go
git commit -m "feat(api): wire the erasure consumer, lock gate and revocation backend"
```

---

### Task 10: CDK — tables, queue + DLQ + subscription, IAM, env

**Files:**
- Create: `cdk/lib/erasure-stack.ts`
- Create: `cdk/test/erasure.test.ts`
- Modify: `cdk/lib/constants.ts`
- Modify: `cdk/lib/dynamodb-stack.ts`
- Modify: `cdk/lib/iam-stack.ts`
- Modify: `cdk/lib/api-stack.ts`
- Modify: `cdk/test/api-stack.test.ts`
- Modify: `cdk/bin/ctech-wallet-cdk.ts`

**Interfaces:**
- Produces:
  - `ErasureStack` exposing `queueArn`, `queueUrl`;
  - `DynamoDBStack.retentionTable`;
  - IAMStack props `erasureQueueArn: string`, `retentionTableArn: string`;
  - ApiStack prop `erasureQueueUrl: string`;
  - instance env `ERASURE_QUEUE_URL` and `VALKEY_REVOCATION_URL`.

- [ ] **Step 1: Write the failing CDK tests** — `cdk/test/erasure.test.ts`:

```ts
import assert from 'node:assert/strict';
import {test} from 'node:test';
import * as cdk from 'aws-cdk-lib';
import {Template} from 'aws-cdk-lib/assertions';
import {ErasureStack} from '../lib/erasure-stack';
import {DynamoDBStack} from '../lib/dynamodb-stack';
import {IAMStack} from '../lib/iam-stack';

const env = {account: '868899309401', region: 'us-east-1'};

test('erasure queue: subscribed to the account topic, filtered to wallet on the message attribute, raw delivery, DLQ alarm', () => {
  const t = Template.fromStack(new ErasureStack(new cdk.App(), 'E', {env, environment: 'prod'}));
  t.hasResourceProperties('AWS::SNS::Subscription', {
    Protocol: 'sqs',
    RawMessageDelivery: true,
    FilterPolicy: {services: ['wallet']},
  });
  for (const s of Object.values(t.findResources('AWS::SNS::Subscription')) as any[]) {
    // Absent = MessageAttributes, where ctech-account publishes `services`.
    assert.notEqual(s.Properties.FilterPolicyScope, 'MessageBody');
  }
  t.hasResourceProperties('AWS::SQS::Queue', {
    QueueName: 'prod-ctech-wallet-erasure',
    VisibilityTimeout: 300,
    RedrivePolicy: {maxReceiveCount: 5},
  });
  t.hasResourceProperties('AWS::SQS::Queue', {QueueName: 'prod-ctech-wallet-erasure-dlq'});
  t.hasResourceProperties('AWS::CloudWatch::Alarm', {Threshold: 0, ComparisonOperator: 'GreaterThanThreshold'});
});

test('erasure tables: state with TTL, retention kept out of the generic grants, wallet_users TTL', () => {
  const db = new DynamoDBStack(new cdk.App(), 'D', {env, environment: 'prod', tablePrefix: 'prod'});
  const t = Template.fromStack(db);
  t.hasResourceProperties('AWS::DynamoDB::GlobalTable', {
    TableName: 'prod_wallet_erasure_state', TimeToLiveSpecification: {AttributeName: 'ttl', Enabled: true},
  });
  t.hasResourceProperties('AWS::DynamoDB::GlobalTable', {
    TableName: 'prod_wallet_erasure_retention',
    KeySchema: [{AttributeName: 'pk', KeyType: 'HASH'}, {AttributeName: 'sk', KeyType: 'RANGE'}],
    TimeToLiveSpecification: {AttributeName: 'ttl', Enabled: true},
  });
  t.hasResourceProperties('AWS::DynamoDB::GlobalTable', {
    TableName: 'prod_wallet_users', TimeToLiveSpecification: {AttributeName: 'ttl', Enabled: true},
  });
  assert.ok(db.tables.has('wallet_erasure_state'));
  assert.ok(![...db.tables.keys()].includes('wallet_erasure_retention' as any), 'retention must never get the generic table grants');
});

test('api role: retention write-only, audit updatable only on ip/user_agent, ledger never updated, queue consumable', () => {
  const app = new cdk.App();
  const db = new DynamoDBStack(app, 'D', {env, environment: 'prod', tablePrefix: 'prod'});
  const iamStack = new IAMStack(app, 'I', {
    env, environment: 'prod',
    deploymentsBucketArn: 'arn:aws:s3:::d', logsBucketArn: 'arn:aws:s3:::l',
    dynamoDBTables: db.tables,
    pixGatewayOutboundFunctionArn: 'arn:aws:lambda:us-east-1:868899309401:function:x',
    erasureQueueArn: 'arn:aws:sqs:us-east-1:868899309401:prod-ctech-wallet-erasure',
    retentionTableArn: db.retentionTable.tableArn,
  });
  const statements = Object.values(Template.fromStack(iamStack).findResources('AWS::IAM::ManagedPolicy'))
    .flatMap((p: any) => p.Properties.PolicyDocument.Statement);
  const bySid = (sid: string) => {
    const s = statements.find((x: any) => x.Sid === sid);
    assert.ok(s, `missing statement ${sid}`);
    return s;
  };
  const actions = (s: any): string[] => [].concat(s.Action);

  assert.deepEqual(actions(bySid('RetentionWriteOnly')), ['dynamodb:PutItem']);
  const noRead = bySid('RetentionNoRead');
  assert.equal(noRead.Effect, 'Deny');
  for (const a of ['dynamodb:GetItem', 'dynamodb:Query', 'dynamodb:Scan', 'dynamodb:BatchGetItem']) {
    assert.ok(actions(noRead).includes(a), `retention must deny ${a}`);
  }
  const auditAllow = bySid('AuditAnonymizeIpUaOnly');
  assert.deepEqual(auditAllow.Condition['ForAllValues:StringEquals']['dynamodb:Attributes'], ['pk', 'sk', 'ip', 'user_agent']);
  assert.equal(bySid('AuditNoOtherUpdate').Effect, 'Deny');
  assert.equal(bySid('AuditNoUnscopedUpdate').Effect, 'Deny');
  const ledger = bySid('LedgerNeverUpdated');
  assert.equal(ledger.Effect, 'Deny');
  assert.equal(ledger.Condition, undefined, 'the ledger deny must stay unconditional (Invariant #2)');
  assert.ok(actions(bySid('ErasureQueueConsume')).includes('sqs:ReceiveMessage'));
});
```

In `cdk/test/api-stack.test.ts`, add `erasureQueueUrl: 'https://sqs.us-east-1.amazonaws.com/868899309401/prod-ctech-wallet-erasure',` to the `ApiStack` props in `synth()`, and append:

```ts
test('user data wires the erasure queue and the revocation Valkey (DB 0 base URL)', () => {
  const text = userDataText();
  assert.match(text, /ERASURE_QUEUE_URL=https:\/\/sqs\./);
  assert.match(text, /VALKEY_REVOCATION_URL="\$VALKEY_BASE"/);
  assert.match(text, /export VALKEY_URL VALKEY_REVOCATION_URL CORS_ALLOWED_ORIGINS/);
});
```

- [ ] **Step 2: Run them to verify they fail**

Run: `cd cdk && npm test 2>&1 | tail -20`
Expected: FAIL — `Cannot find module '../lib/erasure-stack'` (plus TS errors for the new props).

- [ ] **Step 3: Constants** — append to `cdk/lib/constants.ts`:

```ts
// ── Account deletion (LGPD) participant ─────────────────────────────────────
// docs/plans/2026-10-07-account-deletion-participant.md. Mirrors
// api/internal/domain/wallet/erasure.go.

/** This service's name in the saga: SNS filter value and ack `service`. */
export const ERASURE_SERVICE_ID = 'wallet';
/** Owned by ctech-account (its iam-stack publishes the topic ARN here). */
export const SSM_ACCOUNT_ERASURE_TOPIC_ARN = (env: Environment) => `/ctech/${env}/account/erasure-topic-arn`;
export const erasureQueueName = (env: Environment) => `${env}-${SERVICE}-erasure`;
/** erasure.Store table: TABLE_PREFIX + "_wallet" + "_erasure_state". */
export const TABLE_ERASURE_STATE = 'wallet_erasure_state';
/** PII copied out at purge; the API role may only PutItem it. */
export const TABLE_ERASURE_RETENTION = 'wallet_erasure_retention';
/**
 * The only attributes the purge may touch on wallet_audit (REMOVE ip,
 * user_agent; keys for the condition). Mirrors api/internal/repositories/erasure.go.
 */
export const AUDIT_ANONYMIZABLE_ATTRIBUTES = ['pk', 'sk', 'ip', 'user_agent'];
```

- [ ] **Step 4: Tables** — in `cdk/lib/dynamodb-stack.ts`:
  - import `TABLE_ERASURE_RETENTION`, `TABLE_ERASURE_STATE` from `./constants`;
  - add `typeof TABLE_ERASURE_STATE` to the `TableName` union;
  - add `type RestrictedTableName = typeof TABLE_ERASURE_RETENTION;`;
  - add to `TableOptions`:

```ts
  /** Restricted: kept out of `tables` (no generic grants); IAMStack grants it explicitly. Always RETAIN. */
  restricted?: boolean;
```

  - add the field `public readonly retentionTable: dynamodb.TableV2;`;
  - change the `table` helper's parameter to `name: TableName | RetiredTableName | RestrictedTableName`, its `removalPolicy` to `opts.retired || opts.restricted ? RemovalPolicy.RETAIN : removalPolicy`, and its registration to:

```ts
      if (opts.retired) this.exportValue(t.tableArn);
      else if (!opts.restricted) this.tables.set(name as TableName, t);
```

  - change `const usersTable = table('wallet_users');` to `const usersTable = table('wallet_users', {ttl: true}); // ttl: carried self-exclusions (SELFEXCL#)`;
  - after the audit table, add:

```ts
    // ── Account deletion (LGPD) ───────────────────────────────────────────────
    // erasure.Store's lock/tombstone state (pk only, TTL `ttl`; schema owned by
    // ctech-go-common/erasure).
    table(TABLE_ERASURE_STATE, {ttl: true});
    // PII copied out of operational rows at purge, kept 5 years (Lei 9.613).
    // Restricted: the API role may only PutItem it; support reads it.
    this.retentionTable = table(TABLE_ERASURE_RETENTION, {sortKey: true, ttl: true, restricted: true});
```

- [ ] **Step 5: Queue stack** — `cdk/lib/erasure-stack.ts`:

```ts
import * as cdk from 'aws-cdk-lib';
import * as cloudwatch from 'aws-cdk-lib/aws-cloudwatch';
import * as cwActions from 'aws-cdk-lib/aws-cloudwatch-actions';
import * as sns from 'aws-cdk-lib/aws-sns';
import * as subs from 'aws-cdk-lib/aws-sns-subscriptions';
import * as sqs from 'aws-cdk-lib/aws-sqs';
import * as ssm from 'aws-cdk-lib/aws-ssm';
import {SSM as CtechSSM} from '@aoctech/cdk';
import {Construct} from 'constructs';
import {ERASURE_SERVICE_ID, erasureQueueName, SSM_ACCOUNT_ERASURE_TOPIC_ARN} from './constants';
import {Environment} from './types';

interface ErasureStackProps extends cdk.StackProps {
  environment: Environment;
}

/**
 * The wallet's participant queue in the account-deletion saga
 * (ctech-account docs/specs/2026-10-06-account-deletion-saga-protocol.md §3, §7).
 */
export class ErasureStack extends cdk.Stack {
  public readonly queueArn: string;
  public readonly queueUrl: string;

  constructor(scope: Construct, id: string, props: ErasureStackProps) {
    super(scope, id, props);
    const {environment} = props;

    const dlq = new sqs.Queue(this, 'ErasureDLQ', {
      queueName: `${erasureQueueName(environment)}-dlq`,
      retentionPeriod: cdk.Duration.days(14),
      encryption: sqs.QueueEncryption.SQS_MANAGED,
    });
    const queue = new sqs.Queue(this, 'ErasureQueue', {
      queueName: erasureQueueName(environment),
      // ≥ 2× the slowest purge (the consumer has no visibility heartbeat).
      visibilityTimeout: cdk.Duration.minutes(5),
      retentionPeriod: cdk.Duration.days(14),
      encryption: sqs.QueueEncryption.SQS_MANAGED,
      deadLetterQueue: {queue: dlq, maxReceiveCount: 5},
    });

    const topic = sns.Topic.fromTopicArn(this, 'AccountErasureTopic',
      ssm.StringParameter.valueForStringParameter(this, SSM_ACCOUNT_ERASURE_TOPIC_ARN(environment)));
    // ctech-account sets MessageAttributes.services on every publish; the
    // default filter scope (MessageAttributes) is the one that matches.
    topic.addSubscription(new subs.SqsSubscription(queue, {
      rawMessageDelivery: true,
      filterPolicy: {services: sns.SubscriptionFilter.stringFilter({allowlist: [ERASURE_SERVICE_ID]})},
    }));

    const alerts = sns.Topic.fromTopicArn(this, 'AlertsTopic',
      ssm.StringParameter.valueForStringParameter(this, CtechSSM.alerts(environment).topicArn));
    const alarm = new cloudwatch.Alarm(this, 'ErasureDLQNotEmpty', {
      alarmName: `${erasureQueueName(environment)}-dlq-not-empty`,
      alarmDescription: 'A wallet erasure message failed 5 times. Runbook: OPERATIONS.md §7.',
      metric: dlq.metricApproximateNumberOfMessagesVisible({period: cdk.Duration.minutes(5)}),
      threshold: 0,
      comparisonOperator: cloudwatch.ComparisonOperator.GREATER_THAN_THRESHOLD,
      evaluationPeriods: 1,
      treatMissingData: cloudwatch.TreatMissingData.NOT_BREACHING,
    });
    alarm.addAlarmAction(new cwActions.SnsAction(alerts));

    this.queueArn = queue.queueArn;
    this.queueUrl = queue.queueUrl;
  }
}
```

- [ ] **Step 6: IAM** — in `cdk/lib/iam-stack.ts`:
  - add `erasureQueueArn: string;` and `retentionTableArn: string;` to `IAMStackProps` and destructure them;
  - import `AUDIT_ANONYMIZABLE_ATTRIBUTES`, `TABLE_AUDIT`, `TABLE_LEDGER`;
  - after `appendOnlyArns`, add:

```ts
    const arnsOf = (name: string) => {
      const t = dynamoDBTables.get(name)!;
      return [t.tableArn, `${t.tableArn}/index/*`];
    };
```

  Replace the single DENY statement (`actions: ['dynamodb:UpdateItem', 'dynamodb:DeleteItem'], resources: appendOnlyArns`) with:

```ts
        new iam.PolicyStatement({
          effect: iam.Effect.DENY,
          actions: ['dynamodb:DeleteItem'],
          resources: appendOnlyArns,
        }),
        // The ledger is never updated (Invariant #2) — unconditional.
        new iam.PolicyStatement({
          sid: 'LedgerNeverUpdated',
          effect: iam.Effect.DENY,
          actions: ['dynamodb:UpdateItem'],
          resources: arnsOf(TABLE_LEDGER),
        }),
        // wallet_audit: the account-deletion purge may REMOVE ip/user_agent
        // (LGPD anonymization) and nothing else. Any other attribute, or a
        // request without dynamodb:Attributes, stays denied.
        new iam.PolicyStatement({
          sid: 'AuditAnonymizeIpUaOnly',
          actions: ['dynamodb:UpdateItem'],
          resources: [dynamoDBTables.get(TABLE_AUDIT)!.tableArn],
          conditions: {
            'ForAllValues:StringEquals': {'dynamodb:Attributes': AUDIT_ANONYMIZABLE_ATTRIBUTES},
            Null: {'dynamodb:Attributes': 'false'},
          },
        }),
        new iam.PolicyStatement({
          sid: 'AuditNoOtherUpdate',
          effect: iam.Effect.DENY,
          actions: ['dynamodb:UpdateItem'],
          resources: arnsOf(TABLE_AUDIT),
          conditions: {'ForAnyValue:StringNotEquals': {'dynamodb:Attributes': AUDIT_ANONYMIZABLE_ATTRIBUTES}},
        }),
        new iam.PolicyStatement({
          sid: 'AuditNoUnscopedUpdate',
          effect: iam.Effect.DENY,
          actions: ['dynamodb:UpdateItem'],
          resources: arnsOf(TABLE_AUDIT),
          conditions: {Null: {'dynamodb:Attributes': 'true'}},
        }),
```

  After the DynamoPolicy, add a separate managed policy (keeps DynamoPolicy under the 6 KB limit):

```ts
    // ── Account deletion (LGPD) ───────────────────────────────────────────────
    this.apiRole.addManagedPolicy(new iam.ManagedPolicy(this, 'ErasurePolicy', {
      managedPolicyName: `${environment}-${SERVICE}-erasure-policy`,
      statements: [
        new iam.PolicyStatement({
          sid: 'ErasureQueueConsume',
          actions: ['sqs:ReceiveMessage', 'sqs:DeleteMessage', 'sqs:ChangeMessageVisibility', 'sqs:GetQueueAttributes'],
          resources: [erasureQueueArn],
        }),
        // Retained PII (Lei 9.613, 5 years) is written once at purge and never
        // read back by the application: support reads it with its own role.
        new iam.PolicyStatement({
          sid: 'RetentionWriteOnly',
          actions: ['dynamodb:PutItem'],
          resources: [retentionTableArn],
        }),
        new iam.PolicyStatement({
          sid: 'RetentionNoRead',
          effect: iam.Effect.DENY,
          actions: ['dynamodb:GetItem', 'dynamodb:BatchGetItem', 'dynamodb:Query', 'dynamodb:Scan',
            'dynamodb:UpdateItem', 'dynamodb:DeleteItem', 'dynamodb:BatchWriteItem'],
          resources: [retentionTableArn, `${retentionTableArn}/index/*`],
        }),
      ],
    }));
```

  `{env}_wallet_erasure_state` is in `dynamoDBTables`, so it already receives the mutable grant (Get/Put/Update/Query) that `erasure.Store` needs.

- [ ] **Step 7: API env** — in `cdk/lib/api-stack.ts`:
  - add the prop `/** The wallet's account-deletion SQS queue (ErasureStack). */ erasureQueueUrl: string;` and destructure it;
  - in the static env heredoc, after `PIX_GATEWAY_FUNCTION_NAME=...`, add the line `` `ERASURE_QUEUE_URL=${erasureQueueUrl}`, ``;
  - replace the `service-env.sh` lines:

```ts
      `if [ -n "$VALKEY_BASE" ]; then VALKEY_URL="\${VALKEY_BASE%/}/${VALKEY_DB}"; else VALKEY_URL=""; fi`,
      // ctech-account's JWT revocation list lives in DB 0 = the base URL itself.
      `VALKEY_REVOCATION_URL="$VALKEY_BASE"`,
      `CORS_ALLOWED_ORIGINS="$SERVICE_AUDIENCE"`,
      `export VALKEY_URL VALKEY_REVOCATION_URL CORS_ALLOWED_ORIGINS`,
```

- [ ] **Step 8: App wiring** — in `cdk/bin/ctech-wallet-cdk.ts`, import `ErasureStack` and, before `const iamStack = ...`, add:

```ts
// Account-deletion saga participant queue (subscribes to ctech-account's topic).
const erasureStack = new ErasureStack(app, id('Erasure'), {
  env,
  environment: ENVIRONMENT,
  description: `CTech Wallet account-deletion participant queue - ${ENVIRONMENT}`,
});
```

  Pass `erasureQueueArn: erasureStack.queueArn, retentionTableArn: dynamodbStack.retentionTable.tableArn,` to `IAMStack`, followed by `iamStack.addStackDependency(erasureStack);`. Pass `erasureQueueUrl: erasureStack.queueUrl,` to `ApiStack`, followed by `apiStack.addStackDependency(erasureStack);`.

- [ ] **Step 9: Run the CDK checks**

Run: `cd cdk && npx tsc --noEmit && npm test 2>&1 | tail -20 && ENVIRONMENT=dev npx cdk synth "CtechWallet-Dev-*" --profile ctech > /dev/null && echo SYNTH_OK`
Expected: no TS errors; `# pass` covering every test in `api-stack.test.ts`, `reconcile-stack.test.ts` and `erasure.test.ts`, with `# fail 0`; `SYNTH_OK`. If synth cannot resolve SSM lookups offline, the `cdk synth` part may be skipped; `npm test` is the gate.

- [ ] **Step 10: Commit**

```bash
git add cdk/lib/ cdk/test/ cdk/bin/ctech-wallet-cdk.ts
git commit -m "feat(cdk): erasure queue, state and retention tables, scoped IAM, env"
```

---

### Task 11: Documentation

**Files:**
- Modify: `api/ENDPOINTS.md`
- Modify: `OPERATIONS.md`
- Modify: `CLAUDE.md` (root)
- Modify: `api/CLAUDE.md`
- Modify: `README.md`
- Modify: `docs/specs/2026-10-07-asaas-removal.md`

- [ ] **Step 1: `api/ENDPOINTS.md`.**
  - In §4 (Internal M2M routes), add a table row:

```markdown
| GET    | `/v1.0/internal/erasure/eligibility/:sub`          | `internal:wallet:erasure-eligibility`             | `erasure.go`      | —                                                                           | Account-deletion pre-check, called by **ctech-account** with a token it mints (aud = wallet, M2M). Returns `{eligible, blockers[]}` (`erasure.Eligibility`). Codes: `wallet.balance_nonzero` (`detail.wallet`, `detail.amount_cents`; `action_url` only for `game`), `wallet.hold_open`, `wallet.deposit_pending`, `wallet.withdrawal_pending`, `wallet.purchase_pending`. Unknown `sub` → eligible, nothing created. Never creates a wallet. |
```

  - After the §4 scope note, add:

```markdown
> **Account deletion lock.** While ctech-account's deletion saga holds a user (grace, purging or erased), every
> operation that opens new exposure for that `user_id` answers `409 /problems/account-erasure-pending`, on user
> AND internal routes (`sandbox/credit|debit`, `real/debit`, `game/hold`, purchases, `charge`). Settlement is never
> refused: `game/hold/:id/release`, `game/cashout`, and the `pix/confirm-*` routes. User routes that change money or
> consent also re-check the JWT revocation list fail-closed and answer `503 /problems/service-unavailable` when it is
> unreachable. Plan: `docs/plans/2026-10-07-account-deletion-participant.md`.
```

- [ ] **Step 2: `OPERATIONS.md`.**
  - In §2, change `allowed_scopes: ["internal:account:kyc"]` to `allowed_scopes: ["internal:account:kyc", "internal:account:erasure-ack"]`, and add the sentence: "The wallet acks account-deletion purges with this same client (`POST {CTECH_URL}/v1.0/internal/erasure/ack`)."
  - Append a new section:

```markdown
## 7. Account deletion (LGPD) participant

Plan: `docs/plans/2026-10-07-account-deletion-participant.md`. Contract: ctech-account
`docs/specs/2026-10-06-account-deletion-saga-protocol.md`.

**Prerequisites (in this order):**

1. ctech-account deployed with the deletion topic (SSM `/ctech/{env}/account/erasure-topic-arn` exists) and serving
   `cpf_hmac` on `GET /v1.0/internal/kyc/:user_id`.
2. `cdk deploy` the wallet (`CtechWallet-{Env}-Erasure`, `-DynamoDB`, `-IAM`, `-API`). The API refuses to boot in prod
   without `ERASURE_QUEUE_URL` and `VALKEY_REVOCATION_URL`.
3. Wallet M2M client granted `internal:account:erasure-ack` (§2).
4. Publish the scope manifest (§1). It now includes `internal:wallet:erasure-eligibility`.
5. In ctech-account, add to `ERASURE_PARTICIPANTS`:
   `{"service":"wallet","url":"https://wallet-api.aoctech.app/v1.0","audience":"<value of /ctech-wallet/{env}/app-url>","client_id":"<WALLET_CLIENT_ID>"}`.
   The token account mints must have `iss` equal to the wallet's `CTECH_ISSUER_URL`.

**What the purge does.** It moves no money and never touches the ledger.

- Payer CPF/name (deposits) and PIX keys (withdrawals) are copied to `{env}_wallet_erasure_retention`
  (pk `SUB#{sub}`, sk `{table}#{id}`, TTL 5 years), then removed from the rows.
- `wallet_users` loses limits, counters and self-exclusion. Consent versions stay.
- `wallet_audit` loses `ip`/`user_agent`.
- An active self-exclusion is kept as `wallet_users` `SELFEXCL#{cpf_hmac}` until it ends (indefinite: 5 years) and is
  re-applied if the same CPF activates gambling again.
- A `sandbox` balance is forfeited (virtual).

**Reading retained data (PLD/COAF request, legal).** The API role cannot read the retention table (IAM deny). Use
the console/CLI with an admin role, `--profile ctech`:
`aws dynamodb query --table-name prod_wallet_erasure_retention --key-condition-expression "pk = :p" --expression-attribute-values '{":p":{"S":"SUB#<sub>"}}'`.
Join with ctech-account's `KYCRET#{sub}` for the identity. Never copy results into tickets or logs.

**DLQ alarm (`{env}-ctech-wallet-erasure-dlq-not-empty`).**

- Read the message: `aws sqs receive-message --queue-url <dlq-url> --profile ctech`.
- Check the API logs for `erasure: message left for redelivery` with the same `message_id`.
- Typical causes: `cpf_hmac` missing (account not upgraded), the IAM audit condition (AccessDenied on
  `wallet_audit`), KYC endpoint down.
- Fix, then redrive: `aws sqs start-message-move-task --source-arn <dlq-arn> --profile ctech`.
- The purge is idempotent; redriving is always safe.

**Blocked ack after the point of no return (request `stalled` in ctech-account).** A blocker appeared after
LOCKED, typically a legacy PIX deposit confirmed or a poker cash-out credited after the lock. The user cannot act
(no tokens).

- Support settles the money: PIX refund to the payer, or a manual payout to an account of the same CPF, with the
  usual reconciliation records.
- Confirm `GET /v1.0/internal/erasure/eligibility/<sub>` returns eligible.
- Redrive from ctech-account admin.

**Backup restore (PITR).** Restoring any wallet table can bring back erased PII. After a restore, list the requests
purged after the restore point (ctech-account deletion-requests table, see its runbook) and re-publish `user.erase`
for each. The purge reruns idempotently.

**`real` balance blocker.** There is no withdrawal rail today (`docs/specs/2026-10-07-asaas-removal.md`), so a user
with `real` balance cannot reach zero alone. Support pays out to an account of the same CPF and debits the wallet
with a reconciled ledger entry. A `game` balance can be returned to `real` from the wallet UI.
```

- [ ] **Step 3: Root `CLAUDE.md`.** In "Cross-project contract (`ctech-account`)", append:

```markdown
- **Account deletion (LGPD):** the wallet is a saga participant
  (`docs/plans/2026-10-07-account-deletion-participant.md`).
  - It serves `GET /v1.0/internal/erasure/eligibility/:sub` (scope `internal:wallet:erasure-eligibility`, token
    minted by account).
  - It consumes `{env}-ctech-wallet-erasure` (SNS filter `services=wallet`) and acks with `internal:account:erasure-ack`.
  - It reads the JWT revocation list from Valkey DB 0 (`VALKEY_REVOCATION_URL`).
  - From the start of grace, every operation that opens new exposure for the user is refused (`checkErasure`);
    settlement is never refused.
  - Retained PII goes to the write-only `wallet_erasure_retention` table. The ledger is never touched.
  - `cpf_hmac` comes from account's internal KYC response and keys carried self-exclusions.
```

- [ ] **Step 4: `api/CLAUDE.md`.** In "Money & ledger (CRITICAL)", append the bullet:

```markdown
- **Account-deletion lock:** every NEW service method that opens exposure for a user (debit, credit, hold, charge,
  purchase, consent or limit write) starts with `checkErasure(ctx, s.erasureGate, userID)`, and its test joins
  `TestErasureGateRefusesEveryNewExposure`. Settlement of what already exists (confirm, release, cash-out, sweeps)
  never calls it: that money cannot be refused, so it surfaces as an erasure blocker instead.
```

- [ ] **Step 5: `README.md`.** In "## Documentação", add the bullet: `- [Exclusão de conta (LGPD) — participante](docs/plans/2026-10-07-account-deletion-participant.md)`.

- [ ] **Step 6: `docs/specs/2026-10-07-asaas-removal.md`.** In "O que um provedor futuro precisa reintroduzir", append:

```markdown
- **Saque de encerramento sem mínimo (exclusão de conta, decisão D7):** saque do saldo integral passa por
  `wallet.ValidateWithdrawalAmount(..., fullBalance=true, ...)`, que dispensa o mínimo; o cliente não sinaliza nada.
  O saque precisa ser possível **antes** do pedido de exclusão (a carteira trava no início da carência).
- O blocker `wallet.balance_nonzero` de `real` passa a ter `action_url` para a tela de saque.
- Bloqueadores próprios do provedor (subconta não encerrável, MED aberto, transfer intent) entram em
  `ErasureService.Blockers`, e o encerramento da subconta no `Purge`, com o id da solicitação de encerramento nos
  counts do ack.
```

- [ ] **Step 7: Verify nothing else changed and the suites are green**

Run: `cd api && go vet ./... && go test ./... -race && cd ../cdk && npm test 2>&1 | tail -3 && cd .. && git status --short`
Expected: green; `git status` lists only the six doc files.

- [ ] **Step 8: Commit**

```bash
git add api/ENDPOINTS.md OPERATIONS.md CLAUDE.md api/CLAUDE.md README.md docs/specs/2026-10-07-asaas-removal.md
git commit -m "docs: wallet account-deletion participant contract and runbooks"
```

---

## Cross-project impact

- **ctech-account:**
  - add `cpf_hmac` to the internal KYC response (prerequisite 2, Q1);
  - add a `wallet` entry to `ERASURE_PARTICIPANTS`;
  - grant `internal:account:erasure-ack` to the wallet client;
  - add UI copy for `wallet.hold_open`, `wallet.deposit_pending`, `wallet.withdrawal_pending`,
    `wallet.purchase_pending`, and for `wallet.balance_nonzero` **without** `action_url` (Q2).
  - Its phase-4 UI example link `https://ledger.aoctech.app/withdraw` does not exist.
- **ctech-poker:** `internal/wallet/game/hold`, `sandbox/credit|debit` and `sandbox-purchase` answer
  `409 /problems/account-erasure-pending` for a locked user. Poker must treat that as "not eligible" and must keep
  sending cash-outs and releases, which are always accepted.
- **ctech-billing:** `real/debit` and `wallet/charge` answer 409 for a locked user. Billing's own deletion blockers
  (open invoice) run first.
- **ctech-dfe:** none.
- **ctech-go-common:** none required. Follow-up: its README "Account erasure" still says filter scope `MessageBody`
  and scope `account:erasure:ack`. Both are outdated: attribute scope and `internal:account:erasure-ack` are
  authoritative.
- **ctech-cdk:** none. Uses its alerts topic SSM (`CtechSSM.alerts`).
- **wallet ui:** no change. Users in deletion have no session; problem details render generically.
- **pix-gateway:** none. It verifies no user JWT and stays on go-common v1.13.1.

## Open questions

- **Q1 (blocking for prod, ctech-account owner):** approve adding `cpf_hmac` to `GET /v1.0/internal/kyc/:user_id`.
  - It is the minimal contract change: it reuses account's existing `Sealer.MAC("cpf-hmac", …)` and needs no secret
    shared with the wallet.
  - The alternative is a wallet-owned HMAC key. It must be chosen before the first prod purge, because rows cannot be
    re-keyed afterwards.
- **Q2 (account UI):** copy for `wallet.balance_nonzero` without `action_url` ("fale com o suporte para sacar"). There
  is no withdrawal rail, so support settles `real` balances by hand. Who owns that procedure?
- **Q3 (legal):** does pseudonymous in-place retention of money rows (keyed by the opaque `sub`, identity retained by
  account under D6) satisfy "segregated"? Physical move would need an Invariant #2 amendment.
- **Q4 (verify in dev before prod):** confirm IAM `dynamodb:Attributes` evaluates the REMOVE attributes of an
  `UpdateItem` as expected. Two checks against `dev_wallet_audit` with the API role: a purge succeeds, and a manual
  `UpdateItem SET event_type` is denied.
- **Q5:** service-scope unlink. The wallet needs `Store.Clear(sub)` on re-consent once ctech-account ships scope
  `service`.
