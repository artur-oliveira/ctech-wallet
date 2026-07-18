# ctech-wallet API Backend — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the complete Go REST backend (`api/`) for ctech-wallet — a two-balance digital wallet (real PIX via Inter + non-convertible sandbox) with an append-only ledger, atomic no-negative-balance transactions, idempotency, per-wallet locking, PIX deposit/withdraw flows, sandbox M2M credit/debit, and a withdrawal reconciliation job.

**Architecture:** Mirrors `ctech-dfe/api` exactly — layered `handler → service → repository → DynamoDB`, `go.uber.org/fx` dependency injection, RFC 7807 problems, AWS SDK v2, `caarlos0/env` config, Valkey/Redis cache. It is **not** multi-tenant: no org header, no RBAC. Access control is user JWT (JWKS from ctech-account) + `client_credentials` M2M scopes + step-up MFA. Money is integer centavos throughout. All balance mutations are conditional `TransactWriteItems` that co-write the ledger entry and an idempotency guard item in one atomic transaction.

**Tech Stack:** Go 1.26 · Fiber v3.3.0 · aws-sdk-go-v2 (dynamodb) · golang-jwt/v5 · redis/go-redis v9 (Valkey) · caarlos0/env/v11 · go.uber.org/fx · go-playground/validator/v10. Inter PIX API over mTLS + OAuth2 client_credentials. DynamoDB-local for integration tests.

## Global Constraints

- Module path: `gopkg.aoctech.app/api`. Go `1.26`.
- Deployed binary MUST be named `app` (CDK userdata expects `/opt/app/current/app`).
- Auth is **RS256 only**. No HS256, no `SECRET_KEY`.
- **All money is integer centavos.** Never float. Fee = `max(min(amount*2/100, 1000), 100)` computed in integer math.
- **Every balance mutation** is a conditional `TransactWriteItems`; debits carry `ConditionExpression: balance >= :amount`; no read-then-write.
- **Ledger is append-only** — `ledger_entries` rows are never updated or deleted.
- **Idempotency is mandatory** on every mutation; enforced atomically by a guard item `pk=IDEM#{key}` with `attribute_not_exists(pk)` inside the same transaction.
- **One op per wallet at a time** via Valkey `SETNX wallet:{id}` TTL 10s; contention → `409 wallet-busy`.
- **Cross-wallet ops lock in fixed order `real` → `sandbox`.**
- **Sandbox never withdrawable/convertible** — enforced at the handler (`type=sandbox` rejected on withdraw).
- **PIX webhook is never trusted** — always re-query the charge by `txid` at Inter before crediting.
- All errors are RFC 7807 via `problem.*` / `sendProblem` — never raw errors or `fiber.Map`.
- Every string key, scope, header, cache-key prefix, attribute name, and enum is a named constant.
- Every core service function gets a unit test AND an integration test (DynamoDB-local).
- Conventional Commits, no emojis, **no `Co-Authored-By` trailer** (user global rule).
- Never commit secrets: Inter mTLS certs/client secret, webhook secret, JWT keys, real CPFs.

## Cross-project contract (ctech-account) — copied verbatim from the design spec + integration report

- JWKS: `GET {CTECH_URL}/.well-known/jwks.json`. Verify RS256 by `kid`, `aud` contains `SERVICE_AUDIENCE`, `iss == CTECH_URL`.
- Access-token claims used: `sub` (user_id), `scope` (space-joined), `azp` (client_id), `kyc_level` (`""|basic|verified`, present only when kyc scope granted), `last_mfa_at` (unix seconds; omitted if 0), `sid` (session id; **empty for M2M `client_credentials` tokens**).
- Internal-scope gate: reject any token with non-empty `sid` (users can never hit `/internal/*`), then require the scope.
- KYC confirm: `POST {CTECH_URL}/v1.0/internal/kyc/confirm` body `{"user_id","cpf"}` → `200 {"confirmed":true}`; mismatch `409 kyc-cpf-mismatch`; not submitted `409 kyc-not-submitted`; idempotent when already verified.
- KYC read (payer/withdrawal CPF matching): `GET {CTECH_URL}/v1.0/internal/kyc/:user_id` → `{level,cpf,legal_name,birth_date}` (unmasked). Both KYC calls require the wallet's own M2M token with scope `internal:account:kyc`.
- Token endpoint (wallet→account M2M): `POST {CTECH_URL}/v1.0/token` `grant_type=client_credentials&client_id&client_secret&scope=internal:account:kyc`. Wallet client must be seeded confidential + `first_party:true` + `allowed_scopes:["internal:account:kyc"]`.
- Scopes the wallet DEFINES for its own callers (poker/dominó/billing): `internal:wallet:credit`, `internal:wallet:debit`. Seeded into account's global catalog via `cmd/seedscopes` (operational, Task 8.3).

---

## File Structure

```
api/
├── go.mod, go.sum
├── Makefile, Dockerfile, docker-compose.test.yml, .env.example
├── CLAUDE.md, AGENTS.md                     # api-scoped guidelines (mirror ctech-dfe/api/CLAUDE.md)
├── cmd/
│   ├── server/main.go                       # slog JSON + fx.New(app.Module).Run()
│   └── reconcile/main.go                     # withdrawal reconciliation job entrypoint
├── internal/
│   ├── app/app.go                           # fx wiring
│   ├── config/config.go                     # caarlos0/env
│   ├── problem/problem.go                    # RFC 7807 + wallet codes
│   ├── validation/validation.go              # go-playground/validator singleton
│   ├── cache/{cache.go,memory.go,redis.go}   # copied from ctech-dfe
│   ├── awsclient/client.go                    # AWS SDK v2 bundle (DynamoDB only)
│   ├── lock/lock.go                          # Valkey SETNX per-wallet lock
│   ├── middleware/
│   │   ├── auth.go                           # JWKS verifier + typed claims (adapt ctech-dfe)
│   │   ├── claims.go                         # Claims struct + Locals accessors
│   │   ├── scope.go                          # RequireScope (M2M), RequireKYC
│   │   └── stepup.go                         # RequireRecentMFA
│   ├── pix/
│   │   ├── client.go                         # PixClient interface + DTOs
│   │   ├── fake.go                           # FakePixClient (tests)
│   │   └── inter.go                          # real Inter client (mTLS + OAuth2)
│   ├── kycclient/kycclient.go                # account internal KYC client (confirm/get)
│   ├── domain/wallet/
│   │   ├── model.go                          # Wallet, LedgerEntry, PixDeposit, enums, constants
│   │   └── fee.go                            # WithdrawalFee(amount) pure func
│   ├── repositories/
│   │   ├── base.go, marshal.go               # copied from ctech-dfe
│   │   └── wallet.go                          # WalletRepository (Credit/Debit/Transfer/Statement)
│   ├── services/
│   │   └── wallet.go                          # WalletService (all business flows)
│   └── api/v1/
│       ├── router.go                          # Register + route groups
│       ├── helpers.go                         # sendProblem/sendItem/sendPage/bindJSON/cursor (copied)
│       ├── dto.go                             # request/response DTOs
│       ├── wallet.go                          # user route handlers
│       └── internal.go                        # /internal handlers (webhook, sandbox credit/debit)
└── tests/integration/
    ├── setup_test.go                          # dynamodb-local harness, table+GSI creation
    ├── deposit_test.go, withdraw_test.go, sandbox_test.go, idempotency_test.go
```

### DynamoDB tables (env-prefixed `{TABLE_PREFIX}_{table}`)

- `wallets` — PK `pk`=wallet_id (ULID). Attrs: `user_id`, `type` (`real`|`sandbox`), `balance` (N, centavos), `version` (N), `created_at`, `updated_at`. GSI `gsi_user` on `user_id` → both wallets of a user.
- `ledger_entries` — PK `pk`=wallet_id, SK `sk`=`{ts}#{entry_id}` (ULID). Attrs: `type`, `amount` (N, signed centavos), `balance_after` (N), `idempotency_key`, `ref`, `created_at`. GSI `gsi_idem` on `idempotency_key` → replay lookup of prior entry.
- `idempotency` — PK `pk`=`IDEM#{key}`. Attrs: `wallet_id`, `entry_sk`, `req_hash` (sha256 of canonical request), `created_at`, `ttl` (N, epoch). Guard for atomic idempotency; TTL 7 days.
- `pix_deposits` — PK `pk`=txid. Attrs: `wallet_id`, `user_id`, `amount_expected` (N), `status` (`pending`|`confirmed`|`rejected_cpf_mismatch`|`expired`), `e2e_id`, `created_at`, `ttl` (N, epoch, 15min → Dynamo TTL). GSI not required.
- `withdrawals` — PK `pk`=withdrawal_id (ULID). Attrs: `wallet_id`, `user_id`, `amount`, `fee`, `pix_key`, `status` (`processing`|`completed`|`reversed`|`refund_failed`), `e2e_id`, `idempotency_key`, `created_at`, `updated_at`. GSI `gsi_status` on `status` → reconciliation scan of `processing`.

---

## Phase 0 — Scaffolding & infrastructure

### Task 0.1: Bootstrap module + infra files (copied from ctech-dfe, de-DFe'd)

**Files:**
- Create: `api/go.mod`, `api/cmd/server/main.go`, `api/internal/config/config.go`, `api/internal/problem/problem.go`, `api/internal/validation/validation.go`, `api/internal/cache/{cache.go,memory.go,redis.go}`, `api/internal/awsclient/client.go`, `api/Makefile`, `api/Dockerfile`, `api/docker-compose.test.yml`, `api/.env.example`, `api/CLAUDE.md`, `api/AGENTS.md`

**Interfaces:**
- Produces: `config.Config`, `config.Load() (*Config, error)`; `cache.Backend`; `awsclient.Clients{DynamoDB *dynamodb.Client}`, `awsclient.New(ctx, cfg)`; `problem.*` constructors; `validation.Struct(dst) *problem.Problem`.

- [ ] **Step 1: Copy boilerplate verbatim, change module path.** Copy these files from `/home/artur/Documents/Projects/Ctech/ctech-dfe/api/` preserving content, then `sed`-replace the import prefix `github.com/artur-oliveira/ctech-dfe/api` → `gopkg.aoctech.app/api`:
  - `internal/cache/cache.go`, `internal/cache/memory.go`, `internal/cache/redis.go` (verbatim — the `cache.Backend` interface is reused unchanged).
  - `internal/problem/problem.go` (see Step 3 for wallet-specific edits).
  - `internal/validation/validation.go` + any `validators.go` (keep generic validators: required, email; **drop** SEFAZ/BR-fiscal validators cfop/ncm/etc. — but KEEP `cpf` if present, wallet uses it).

- [ ] **Step 2: Write `internal/config/config.go`** — wallet fields only:

```go
package config

import (
	"fmt"
	"log/slog"

	"github.com/caarlos0/env/v11"
)

type Config struct {
	AppVersion string `env:"APP_VERSION" envDefault:"0.0.1"`
	Port       int    `env:"PORT" envDefault:"8000"`
	Env        string `env:"ENVIRONMENT" envDefault:"dev"`

	ReadTimeout        int64    `env:"READ_TIMEOUT" envDefault:"10"`
	IdleTimeout        int64    `env:"IDLE_TIMEOUT" envDefault:"60"`
	WriteTimeout       int64    `env:"WRITE_TIMEOUT" envDefault:"10"`
	TrustedProxies     []string `env:"TRUSTED_PROXIES"`
	CorsAllowedOrigins []string `env:"CORS_ALLOWED_ORIGINS"`

	AWSRegion        string `env:"AWS_REGION" envDefault:"us-east-1"`
	TablePrefix      string `env:"TABLE_PREFIX,required"`
	DynamoDBEndpoint string `env:"DYNAMODB_ENDPOINT"` // local override

	// Auth (ctech-account)
	CtechURL        string `env:"CTECH_URL"`
	CtechJWKSURL    string `env:"CTECH_JWKS_URL"`
	ServiceAudience string `env:"SERVICE_AUDIENCE" envDefault:"https://wallet-api.aoctech.app"`

	// Wallet's own M2M client (to call account internal:account:kyc)
	WalletClientID     string `env:"WALLET_CLIENT_ID"`
	WalletClientSecret string `env:"WALLET_CLIENT_SECRET"`

	// PIX / Inter
	InterBaseURL      string `env:"INTER_BASE_URL" envDefault:"https://cdpj.partners.bancointer.com.br"`
	InterClientID     string `env:"INTER_CLIENT_ID"`
	InterClientSecret string `env:"INTER_CLIENT_SECRET"`
	InterCertPath     string `env:"INTER_CERT_PATH"`     // mTLS client cert (PEM)
	InterKeyPath      string `env:"INTER_KEY_PATH"`      // mTLS client key (PEM)
	InterPixKey       string `env:"INTER_PIX_KEY"`       // receiving key for cob
	InterWebhookSecret string `env:"INTER_WEBHOOK_SECRET"`

	RedisURL string `env:"VALKEY_URL"`
}

func Load() (*Config, error) {
	cfg := &Config{}
	if err := env.Parse(cfg); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	if cfg.CtechJWKSURL == "" && cfg.CtechURL != "" {
		cfg.CtechJWKSURL = cfg.CtechURL + "/.well-known/jwks.json"
	}
	if cfg.ServiceAudience == "" && cfg.Env == "production" {
		return nil, fmt.Errorf("config: SERVICE_AUDIENCE must be set in production so the aud claim is verified")
	}
	if cfg.CtechURL == "" && cfg.Env == "production" {
		slog.Warn("CTECH_URL is empty in production — the iss claim is not being checked")
	}
	return cfg, nil
}
```

- [ ] **Step 3: Edit `internal/problem/problem.go`** — replace DFe/SEFAZ type URIs with wallet codes. Keep the `Problem` struct, `Send`, `New`, `FromFiber`, and generic constructors (`BadRequest/Unauthorized/Forbidden/NotFound/Conflict/UnprocessableEntity/TooManyRequests/InternalServer/Validation`). Replace the `Type*` block and add wallet constructors:

```go
const (
	TypeBadRequest          = "/problems/bad-request"
	TypeUnauthorized        = "/problems/unauthorized"
	TypeForbidden           = "/problems/forbidden"
	TypeNotFound            = "/problems/not-found"
	TypeConflict            = "/problems/conflict"
	TypeUnprocessableEntity = "/problems/unprocessable-entity"
	TypeValidation          = "/problems/validation-error"
	TypeTooManyRequests     = "/problems/too-many-requests"
	TypeInternalServer      = "/problems/internal-server-error"
	// wallet-specific
	TypeInsufficientBalance = "/problems/insufficient-balance"
	TypeWalletBusy          = "/problems/wallet-busy"
	TypeWithdrawCPFMismatch = "/problems/withdraw-cpf-mismatch"
	TypeKYCNotVerified      = "/problems/kyc-not-verified"
	TypeIdempotencyConflict = "/problems/idempotency-conflict"
	TypeStepUpRequired      = "/problems/step-up-required"
)

func InsufficientBalance() *Problem { return New(http.StatusConflict, TypeInsufficientBalance, "Insufficient Balance", "saldo insuficiente para a operação") }
func WalletBusy() *Problem          { return New(http.StatusConflict, TypeWalletBusy, "Wallet Busy", "já existe uma operação em andamento nesta carteira") }
func WithdrawCPFMismatch() *Problem { return New(http.StatusForbidden, TypeWithdrawCPFMismatch, "Withdraw CPF Mismatch", "a chave PIX de destino pertence a outro CPF") }
func KYCNotVerified() *Problem      { return New(http.StatusForbidden, TypeKYCNotVerified, "KYC Not Verified", "verificação de identidade necessária para esta operação") }
func IdempotencyConflict() *Problem { return New(http.StatusConflict, TypeIdempotencyConflict, "Idempotency Conflict", "mesma Idempotency-Key usada com payload diferente") }

// StepUpRequired mirrors ctech-account: adds a max_age_seconds hint field.
func StepUpRequired(maxAgeSeconds int) *Problem {
	p := New(http.StatusForbidden, TypeStepUpRequired, "Step-Up Required", "esta operação exige autenticação MFA recente")
	p.MaxAgeSeconds = maxAgeSeconds
	return p
}
```

  Add `MaxAgeSeconds int \`json:"max_age_seconds,omitempty"\`` to the `Problem` struct.

- [ ] **Step 4: Write `internal/awsclient/client.go`** — DynamoDB-only bundle (drop S3/SQS/SNS/Lambda from the dfe version):

```go
package awsclient

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscfg "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"

	"gopkg.aoctech.app/api/internal/config"
)

type Clients struct{ DynamoDB *dynamodb.Client }

func New(ctx context.Context, cfg *config.Config) (*Clients, error) {
	awsCfg, err := awscfg.LoadDefaultConfig(ctx, awscfg.WithRegion(cfg.AWSRegion))
	if err != nil {
		return nil, err
	}
	opts := func(o *dynamodb.Options) {
		if cfg.DynamoDBEndpoint != "" {
			o.BaseEndpoint = aws.String(cfg.DynamoDBEndpoint)
		}
	}
	return &Clients{DynamoDB: dynamodb.NewFromConfig(awsCfg, opts)}, nil
}
```

- [ ] **Step 5: Write `cmd/server/main.go`:**

```go
package main

import (
	"log/slog"
	"os"

	"gopkg.aoctech.app/api/internal/app"
	"go.uber.org/fx"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	fx.New(app.Module).Run()
}
```

  (`app.Module` is completed in Task 6.1; until then, stub `internal/app/app.go` with `var Module = fx.Options()` so it compiles.)

- [ ] **Step 6: Write `Makefile`, `Dockerfile`, `docker-compose.test.yml`, `.env.example`** — copy from `ctech-dfe/api`, changing the binary/app name references to wallet and dropping SEFAZ/S3 env in `.env.example`. `Makefile` targets: `build` (linux/arm64, `-ldflags="-s -w"`, output `dist/app`), `test` (`go test ./... -race`), `test-integration` (`DYNAMODB_ENDPOINT=http://localhost:8123 go test ./tests/integration/... -tags integration -race`), `lint` (`golangci-lint run ./...`), `vet`. `docker-compose.test.yml` runs `amazon/dynamodb-local` on host port `8123`.

- [ ] **Step 7: Write `api/CLAUDE.md` and copy to `api/AGENTS.md`** — mirror `ctech-dfe/api/CLAUDE.md` structure (Role, Directory Structure, Mandatory Workflow, Engineering Rules, Testing, Known Constraints, Critical Areas, Completion Checklist), adapted: no multi-tenant/RBAC; add the Financial Safety Invariants reference; Critical Areas = ledger mutation, idempotency, locking, PIX confirm/refund, withdrawal reconciliation.

- [ ] **Step 8: `go mod tidy` and verify build.**

Run: `cd api && go mod tidy && go build ./...`
Expected: compiles (empty fx module runs but exits; that's fine).

- [ ] **Step 9: Commit**

```bash
git add api/
git commit -m "chore: scaffold wallet api (config, problem, cache, awsclient, infra)"
```

### Task 0.2: Base repository + marshal helpers

**Files:**
- Create: `api/internal/repositories/base.go`, `api/internal/repositories/marshal.go`
- Test: `api/internal/repositories/base_test.go`

**Interfaces:**
- Produces: `repositories.Base`, `NewBase(db,cfg,table)`, `GetItem/PutItem/UpdateItem/Query/QueryGSI/TransactWrite/BuildPutTxItemIfAbsent/BuildUpdateTxItem`, `Decode[T]`, `Encode`, `NowStr()`, `IsConditionFailed(err)`, `MarshalMapOmitNull`.

- [ ] **Step 1: Copy `base.go` verbatim** from `ctech-dfe/api/internal/repositories/base.go`, changing the module import path. Copy `marshal.go` (contains `MarshalMapOmitNull`) the same way. These are generic DynamoDB helpers reused unchanged.

- [ ] **Step 2: Write a smoke unit test** `base_test.go` for `NewBase` table prefixing and `buildUpdateExpr` (SET+REMOVE), matching the dfe test if present.

```go
func TestNewBasePrefixesTable(t *testing.T) {
	b := NewBase(nil, &config.Config{TablePrefix: "test"}, "wallets")
	if b.TableName != "test_wallets" {
		t.Fatalf("got %q", b.TableName)
	}
}
```

- [ ] **Step 3: Run & commit.**

Run: `cd api && go test ./internal/repositories/... -run TestNewBase`
Expected: PASS

```bash
git add api/internal/repositories/
git commit -m "feat: add base DynamoDB repository helpers"
```

---

## Phase 1 — Authentication & authorization

### Task 1.1: JWKS verifier + typed claims

**Files:**
- Create: `api/internal/middleware/auth.go`, `api/internal/middleware/claims.go`
- Test: `api/internal/middleware/auth_test.go`

**Interfaces:**
- Produces: `middleware.Verifier`, `NewVerifier(jwksURL, audience, issuer string, c cache.Backend) *Verifier`, `(*Verifier).Middleware() fiber.Handler`, `(*Verifier).VerifyClaims(ctx, token string) (*Claims, error)`; `middleware.Claims{Sub,Scope,SID,KYCLevel string; LastMFAAt int64}`; Locals accessors `GetClaims(c) *Claims`, `GetUserID(c) string`.

- [ ] **Step 1: Copy `auth.go`** from `ctech-dfe/api/internal/middleware/auth.go` (module path swap). Then extend `Verify` into `VerifyClaims` returning the full typed claims instead of just `sub`. Add:

```go
// claims.go
package middleware

import "github.com/gofiber/fiber/v3"

const (
	ClaimsKey    = "claims"
	UserIDKey    = "user_id"
	ScopeSep     = " "
)

type Claims struct {
	Sub       string
	Scope     string
	SID       string
	KYCLevel  string
	LastMFAAt int64
	AZP       string
}

func (cl *Claims) HasScope(want string) bool {
	for _, s := range splitScopes(cl.Scope) {
		if s == want {
			return true
		}
	}
	return false
}

func splitScopes(s string) []string {
	if s == "" {
		return nil
	}
	return strings_Fields(s) // strings.Fields — collapses multiple spaces
}

func GetClaims(c fiber.Ctx) *Claims {
	cl, _ := c.Locals(ClaimsKey).(*Claims)
	return cl
}

func GetUserID(c fiber.Ctx) string {
	if cl := GetClaims(c); cl != nil {
		return cl.Sub
	}
	return ""
}
```

  (Replace `strings_Fields` with `strings.Fields`; import `strings`.) In `VerifyClaims`, after `jwt.Parse`, read from `jwt.MapClaims`: `sub`, `scope`, `sid`, `azp`, `kyc_level` (strings), and `last_mfa_at` (float64→int64). `Middleware()` calls `VerifyClaims`, stores `c.Locals(ClaimsKey, claims)`.

- [ ] **Step 2: Write failing test** `auth_test.go` — sign a token with a test RSA key, serve a JWKS from an `httptest.Server`, assert `VerifyClaims` returns the right `Sub`, `Scope`, `KYCLevel`, `LastMFAAt`, and that an unknown `kid` or wrong `aud` fails.

```go
func TestVerifyClaimsExtractsAllFields(t *testing.T) {
	// arrange: gen RSA key, build JWKS httptest server, sign token with sub/scope/kyc_level/last_mfa_at
	// act: v.VerifyClaims(ctx, token)
	// assert: cl.Sub=="user_1", cl.KYCLevel=="verified", cl.LastMFAAt==<ts>, cl.HasScope("internal:wallet:credit")
}
```

- [ ] **Step 3: Run to verify fail, implement, run to verify pass.**

Run: `cd api && go test ./internal/middleware/... -run TestVerifyClaims -v`
Expected: FAIL → (implement) → PASS

- [ ] **Step 4: Commit** — `feat: add JWKS verifier with typed access-token claims`

### Task 1.2: RequireScope (M2M internal gate) + RequireKYC

**Files:**
- Create: `api/internal/middleware/scope.go`
- Test: `api/internal/middleware/scope_test.go`

**Interfaces:**
- Consumes: `middleware.GetClaims`, `problem.Forbidden`, `problem.KYCNotVerified`.
- Produces: `RequireScope(scope string) fiber.Handler`, `RequireKYC(min string) fiber.Handler`. Constants `ScopeWalletCredit = "internal:wallet:credit"`, `ScopeWalletDebit = "internal:wallet:debit"`; KYC levels `KYCBasic="basic"`, `KYCVerified="verified"`.

- [ ] **Step 1: Write failing test** `scope_test.go`: a token with empty `sid` + the scope passes; a token with non-empty `sid` (user token) is rejected 403 even if it carries the scope (users can never use internal routes); a token missing the scope is rejected 403. For `RequireKYC("verified")`: `kyc_level=="verified"` passes, `"basic"`/`""` → 403 `kyc-not-verified`.

- [ ] **Step 2: Run fail. Implement `scope.go`:**

```go
package middleware

import "github.com/gofiber/fiber/v3"
import "gopkg.aoctech.app/api/internal/problem"

const (
	ScopeWalletCredit = "internal:wallet:credit"
	ScopeWalletDebit  = "internal:wallet:debit"
	KYCBasic          = "basic"
	KYCVerified       = "verified"
)

// RequireScope gates an /internal route on an M2M client_credentials token.
// A non-empty sid means a user/session token — never allowed on internal routes.
func RequireScope(scope string) fiber.Handler {
	return func(c fiber.Ctx) error {
		cl := GetClaims(c)
		if cl == nil || cl.SID != "" || !cl.HasScope(scope) {
			return problem.Forbidden("scope insuficiente para rota interna").Send(c)
		}
		return c.Next()
	}
}

func RequireKYC(min string) fiber.Handler {
	return func(c fiber.Ctx) error {
		cl := GetClaims(c)
		if cl == nil {
			return problem.Unauthorized("credenciais ausentes").Send(c)
		}
		if min == KYCVerified && cl.KYCLevel != KYCVerified {
			return problem.KYCNotVerified().Send(c)
		}
		if min == KYCBasic && cl.KYCLevel == "" {
			return problem.KYCNotVerified().Send(c)
		}
		return c.Next()
	}
}
```

- [ ] **Step 3: Run pass. Commit** — `feat: add scope and KYC gate middleware`

### Task 1.3: RequireRecentMFA (step-up)

**Files:**
- Create: `api/internal/middleware/stepup.go`
- Test: `api/internal/middleware/stepup_test.go`

**Interfaces:**
- Produces: `StepUpMaxAge = 5*time.Minute`, `RequireRecentMFA(maxAge time.Duration) fiber.Handler`.

- [ ] **Step 1: Failing test** — token with `last_mfa_at` = now passes; `last_mfa_at` = 0 or older than maxAge → 403 `step-up-required` with `max_age_seconds` set.

- [ ] **Step 2: Implement** (mirror ctech-account, use wallet's `problem.StepUpRequired`):

```go
func RequireRecentMFA(maxAge time.Duration) fiber.Handler {
	return func(c fiber.Ctx) error {
		cl := GetClaims(c)
		if cl == nil || cl.LastMFAAt == 0 || time.Since(time.Unix(cl.LastMFAAt, 0)) > maxAge {
			return problem.StepUpRequired(int(maxAge.Seconds())).Send(c)
		}
		return c.Next()
	}
}
```

- [ ] **Step 3: Run pass. Commit** — `feat: add step-up MFA middleware for withdrawals`

---

## Phase 2 — Domain model, money math, ledger repository

### Task 2.1: Domain models & constants

**Files:**
- Create: `api/internal/domain/wallet/model.go`
- Test: none (types only; covered by later tasks)

**Interfaces:**
- Produces: types `Wallet`, `LedgerEntry`, `PixDeposit`, `Withdrawal`; constants for wallet types, ledger entry types, statuses, table names.

- [ ] **Step 1: Write `model.go`:**

```go
package wallet

const (
	TypeReal    = "real"
	TypeSandbox = "sandbox"

	EntryDeposit         = "deposit"
	EntryWithdraw        = "withdraw"
	EntryFee             = "fee"
	EntryGameDebit       = "game_debit"
	EntryGameCredit      = "game_credit"
	EntrySandboxPurchase = "sandbox_purchase"
	EntrySandboxCredit   = "sandbox_credit"

	DepositPending      = "pending"
	DepositConfirmed    = "confirmed"
	DepositRejectedCPF  = "rejected_cpf_mismatch"
	DepositExpired      = "expired"

	WithdrawProcessing  = "processing"
	WithdrawCompleted   = "completed"
	WithdrawReversed    = "reversed"
	WithdrawRefundFail  = "refund_failed"

	TableWallets     = "wallets"
	TableLedger      = "ledger_entries"
	TableIdempotency = "idempotency"
	TablePixDeposits = "pix_deposits"
	TableWithdrawals = "withdrawals"

	GSIUser   = "gsi_user"   // wallets.user_id
	GSIIdem   = "gsi_idem"   // ledger_entries.idempotency_key
	GSIStatus = "gsi_status" // withdrawals.status

	IdemPrefix = "IDEM#"
)

type Wallet struct {
	WalletID  string `dynamodbav:"pk" json:"wallet_id"`
	UserID    string `dynamodbav:"user_id" json:"user_id"`
	Type      string `dynamodbav:"type" json:"type"`
	Balance   int64  `dynamodbav:"balance" json:"balance"`
	Version   int64  `dynamodbav:"version" json:"version"`
	CreatedAt string `dynamodbav:"created_at" json:"created_at"`
	UpdatedAt string `dynamodbav:"updated_at" json:"updated_at"`
}

type LedgerEntry struct {
	WalletID       string `dynamodbav:"pk" json:"wallet_id"`
	SK             string `dynamodbav:"sk" json:"-"`
	EntryID        string `dynamodbav:"entry_id" json:"entry_id"`
	Type           string `dynamodbav:"type" json:"type"`
	Amount         int64  `dynamodbav:"amount" json:"amount"`
	BalanceAfter   int64  `dynamodbav:"balance_after" json:"balance_after"`
	IdempotencyKey string `dynamodbav:"idempotency_key" json:"-"`
	Ref            string `dynamodbav:"ref" json:"ref,omitempty"`
	CreatedAt      string `dynamodbav:"created_at" json:"created_at"`
}

type PixDeposit struct {
	Txid           string `dynamodbav:"pk" json:"txid"`
	WalletID       string `dynamodbav:"wallet_id" json:"wallet_id"`
	UserID         string `dynamodbav:"user_id" json:"user_id"`
	AmountExpected int64  `dynamodbav:"amount_expected" json:"amount_expected"`
	Status         string `dynamodbav:"status" json:"status"`
	E2EID          string `dynamodbav:"e2e_id" json:"e2e_id,omitempty"`
	CreatedAt      string `dynamodbav:"created_at" json:"created_at"`
	TTL            int64  `dynamodbav:"ttl" json:"-"`
}

type Withdrawal struct {
	WithdrawalID   string `dynamodbav:"pk" json:"withdrawal_id"`
	WalletID       string `dynamodbav:"wallet_id" json:"wallet_id"`
	UserID         string `dynamodbav:"user_id" json:"user_id"`
	Amount         int64  `dynamodbav:"amount" json:"amount"`
	Fee            int64  `dynamodbav:"fee" json:"fee"`
	PixKey         string `dynamodbav:"pix_key" json:"pix_key"`
	Status         string `dynamodbav:"status" json:"status"`
	E2EID          string `dynamodbav:"e2e_id" json:"e2e_id,omitempty"`
	IdempotencyKey string `dynamodbav:"idempotency_key" json:"-"`
	CreatedAt      string `dynamodbav:"created_at" json:"created_at"`
	UpdatedAt      string `dynamodbav:"updated_at" json:"updated_at"`
}
```

- [ ] **Step 2: Commit** — `feat: add wallet domain models and constants`

### Task 2.2: Withdrawal fee calculation (pure)

**Files:**
- Create: `api/internal/domain/wallet/fee.go`
- Test: `api/internal/domain/wallet/fee_test.go`

**Interfaces:**
- Produces: `WithdrawalFee(amount int64) int64`. Fee = 2% clamped to [100, 1000] centavos.

- [ ] **Step 1: Write failing table test** covering boundaries:

```go
func TestWithdrawalFee(t *testing.T) {
	cases := []struct{ amount, want int64 }{
		{100, 100},     // 2% = 2 → clamp to min 100 (R$1)
		{5000, 100},    // 2% = 100 → exactly min
		{5001, 100},    // 2% = 100.02 → 100 (int floor) still min
		{10000, 200},   // 2% = 200
		{50000, 1000},  // 2% = 1000 → exactly max
		{60000, 1000},  // 2% = 1200 → clamp to max 1000 (R$10)
		{1000000, 1000},// clamp max
	}
	for _, tc := range cases {
		if got := WithdrawalFee(tc.amount); got != tc.want {
			t.Errorf("WithdrawalFee(%d)=%d want %d", tc.amount, got, tc.want)
		}
	}
}
```

- [ ] **Step 2: Run FAIL. Implement `fee.go`:**

```go
package wallet

const (
	FeeBps = 200  // 2.00% in basis points
	FeeMin = 100  // R$ 1,00
	FeeMax = 1000 // R$ 10,00
)

// WithdrawalFee returns the fee in centavos: 2% of amount, clamped to [FeeMin, FeeMax].
// Integer math only — never float.
func WithdrawalFee(amount int64) int64 {
	fee := amount * FeeBps / 10000
	if fee < FeeMin {
		return FeeMin
	}
	if fee > FeeMax {
		return FeeMax
	}
	return fee
}
```

- [ ] **Step 3: Run PASS. Commit** — `feat: add withdrawal fee calculation`

### Task 2.3: WalletRepository — get/list/ensure

**Files:**
- Create: `api/internal/repositories/wallet.go`
- Test: `api/tests/integration/wallet_repo_test.go` (integration, `//go:build integration`) — deferred to Task 8.2 for the harness; write the unit-testable parts now.

**Interfaces:**
- Consumes: `repositories.Base`, wallet models.
- Produces: `WalletRepository`, `NewWalletRepository(db, cfg) *WalletRepository`; methods `GetWallet(ctx, walletID) (*wallet.Wallet, error)`, `GetWalletsByUser(ctx, userID) ([]wallet.Wallet, error)`, `EnsureWallets(ctx, userID) (real, sandbox *wallet.Wallet, error)` (idempotent create of both wallets on first access, using ULID ids + `attribute_not_exists` guard by a `USER#{userID}#{type}` uniqueness item).

- [ ] **Step 1: Add ULID dep.** `go get github.com/oklog/ulid/v2`. Create a small `internal/id/id.go` helper `New() string` returning a monotonic ULID (seeded from crypto/rand; guard against `Date.now` issues by using `ulid.Now()` at runtime — this is production code, not the workflow sandbox).

- [ ] **Step 2: Implement `wallet.go`** with `GetWallet` (Base.GetItem + Decode), `GetWalletsByUser` (Base.QueryGSI on `gsi_user`), and `EnsureWallets`. `EnsureWallets` derives deterministic uniqueness by storing a `USER#{userID}#real` / `USER#{userID}#sandbox` marker item in the `wallets` table alongside the ULID-keyed wallet rows; if `GetWalletsByUser` already returns two wallets, return them. Use a `TransactWrite` with two `BuildPutTxItemIfAbsent` for the marker items to avoid double-creation on a race.

  Interface signature to produce (later tasks depend on it):

```go
func (r *WalletRepository) GetWallet(ctx context.Context, walletID string) (*wallet.Wallet, error)
func (r *WalletRepository) GetWalletsByUser(ctx context.Context, userID string) ([]wallet.Wallet, error)
func (r *WalletRepository) EnsureWallets(ctx context.Context, userID string) (real *wallet.Wallet, sandbox *wallet.Wallet, err error)
```

- [ ] **Step 3: Commit** — `feat: add wallet repository (get/list/ensure)` (integration test lands in Phase 8).

### Task 2.4: Atomic Credit / Debit with idempotency guard

**Files:**
- Modify: `api/internal/repositories/wallet.go`
- Test: `api/tests/integration/idempotency_test.go` (Phase 8)

**Interfaces:**
- Produces:

```go
type Mutation struct {
	WalletID       string
	Amount         int64  // positive magnitude
	EntryType      string // wallet.EntryDeposit, etc.
	Ref            string
	IdempotencyKey string
	ReqHash        string // sha256 of canonical request; guards same-key/different-payload
}

// Credit adds Amount to the wallet balance. Debit subtracts it with a
// balance>=Amount condition. Both are a single TransactWriteItems that also
// writes the ledger entry and the idempotency guard.
// On replay (same IdempotencyKey), returns the prior LedgerEntry and replayed=true.
// On same key + different ReqHash, returns problem.IdempotencyConflict.
func (r *WalletRepository) Credit(ctx context.Context, m Mutation) (entry *wallet.LedgerEntry, replayed bool, err error)
func (r *WalletRepository) Debit(ctx context.Context, m Mutation) (entry *wallet.LedgerEntry, replayed bool, err error)
```

- [ ] **Step 1: Design the transaction (documented in code comment).** A debit builds a `TransactWriteItems` of exactly three items:
  1. `Update wallets pk=walletID`: `SET balance = balance - :amt, version = version + :one, updated_at = :now` with `ConditionExpression: attribute_exists(pk) AND balance >= :amt`. (Use `Base.UpdateItemRaw`-style raw update via a new `BuildRawUpdateTxItem` helper, or extend base — add a helper that builds a `types.Update` with an arbitrary expression.)
  2. `Put ledger_entries pk=walletID sk={ts}#{entryID}` (via `BuildPutTxItemIfAbsent` — sk collision impossible but keep create-only).
  3. `Put idempotency pk=IDEM#{key}` with `ConditionExpression: attribute_not_exists(pk)`, storing `wallet_id`, `entry_sk`, `req_hash`, `ttl`.
  A credit is identical but `balance + :amt` and no `balance >=` condition.

- [ ] **Step 2: Implement replay-before-write.** Before building the transaction, `GetItem` the guard `IDEM#{key}`. If present:
  - if `req_hash` differs → return `problem.IdempotencyConflict()`.
  - else load the referenced ledger entry (`GetItem ledger_entries pk=wallet_id sk=entry_sk`) and return it with `replayed=true`.

- [ ] **Step 3: Implement the write.** Run `TransactWrite`. On `IsConditionFailed(err)`, disambiguate: re-`GetItem` the guard — if it now exists, a concurrent identical request won the race → treat as replay; otherwise the failed condition was the balance check → return `problem.InsufficientBalance()`. Compute `balance_after` from the wallet's pre-read balance ± amount for the ledger row (read wallet once at start; the conditional guarantees consistency).

- [ ] **Step 4: Add the `balance_after` correctness note.** Because the `Update` is relative (`balance - :amt`), read the wallet immediately before building the transaction to compute `balance_after`; the `balance >= :amt` condition guarantees no other writer slipped between read and transaction only if the wallet is lock-held (Phase 3). The ledger `balance_after` is advisory audit data, not authoritative — the authoritative balance is always `wallets.balance`. Document this in the code.

- [ ] **Step 5: Commit** — `feat: add atomic credit/debit with idempotency guard` (integration coverage in Phase 8).

### Task 2.5: Cross-wallet atomic transfer (sandbox purchase)

**Files:**
- Modify: `api/internal/repositories/wallet.go`

**Interfaces:**
- Produces:

```go
// Transfer debits `fromWalletID` (with balance>=amount) and credits
// `toWalletID` by the same amount in ONE TransactWriteItems, writing two ledger
// entries and one idempotency guard. Used for sandbox purchase (real→sandbox).
func (r *WalletRepository) Transfer(ctx context.Context, fromWalletID, toWalletID string, amount int64, debitType, creditType, ref, idemKey, reqHash string) (debit, credit *wallet.LedgerEntry, replayed bool, err error)
```

- [ ] **Step 1: Implement** as a 5-item transaction: update-from (balance>=), update-to (add), put debit ledger entry, put credit ledger entry, put idempotency guard (`attribute_not_exists`). Same replay-before-write and condition-disambiguation logic as Task 2.4.

- [ ] **Step 2: Commit** — `feat: add atomic cross-wallet transfer`

### Task 2.6: Statement (paginated ledger)

**Files:**
- Modify: `api/internal/repositories/wallet.go`

**Interfaces:**
- Produces: `Statement(ctx, walletID string, limit int, startKey map[string]types.AttributeValue) (*repositories.QueryResult, error)` — `Base.Query` on `ledger_entries` PK=walletID, `ScanIndexForward:false` (newest first).

- [ ] **Step 1: Implement + commit** — `feat: add ledger statement query`

---

## Phase 3 — Per-wallet locking

### Task 3.1: Valkey SETNX lock

**Files:**
- Create: `api/internal/lock/lock.go`
- Test: `api/internal/lock/lock_test.go` (uses a fake/in-memory backend or miniredis)

**Interfaces:**
- Consumes: `cache.Backend` (or `*cache.RedisBackend` client for native SETNX).
- Produces: `Locker`, `NewLocker(c cache.Backend) *Locker`, `(*Locker).Acquire(ctx, walletID string) (release func(), ok bool, err error)`, constant `LockTTL = 10*time.Second`, key `wallet:{id}`.

- [ ] **Step 1: Failing test** — first `Acquire` returns ok; a second `Acquire` on the same wallet before release returns `ok=false`; after `release()` a new `Acquire` succeeds; acquiring two DIFFERENT wallets both succeed.

- [ ] **Step 2: Implement with a per-acquire random token** so `release()` only deletes the lock it owns (compare-and-delete). SETNX via the cache backend's underlying client (`SET key token NX EX 10`). `release` runs a Lua CAS delete (or GET-then-DEL guarded by token). Add `AcquireOrdered(ctx, ids ...string)` that sorts ids and acquires in order (for the fixed `real`→`sandbox` ordering) and returns a single combined release.

```go
const LockTTL = 10 * time.Second
const lockKeyFmt = "wallet:%s"

func (l *Locker) Acquire(ctx context.Context, walletID string) (release func(), ok bool, err error)
func (l *Locker) AcquireOrdered(ctx context.Context, walletIDs ...string) (release func(), ok bool, err error) // sorts, all-or-nothing
```

- [ ] **Step 3: Run pass. Commit** — `feat: add Valkey per-wallet lock with fixed-order acquisition`

---

## Phase 4 — PIX client (Inter)

### Task 4.1: PixClient interface + DTOs

**Files:**
- Create: `api/internal/pix/client.go`

**Interfaces:**
- Produces:

```go
package pix

import "context"

type Charge struct {
	Txid       string
	Amount     int64  // centavos
	QRCode     string // copia-e-cola (EMV)
	QRCodeB64  string // base64 PNG (optional)
	Status     string // "ATIVA"|"CONCLUIDA"|... (Inter cob status)
	PayerCPF   string // present only once paid
	E2EID      string
}

type DictAccount struct {
	Key     string
	CPF     string // owner CPF of the PIX key
	Name    string
}

type TransferResult struct {
	E2EID  string
	Status string
}

type PixClient interface {
	CreateCharge(ctx context.Context, txid string, amount int64, payerHintCPF string) (*Charge, error)
	QueryCharge(ctx context.Context, txid string) (*Charge, error)
	DictLookup(ctx context.Context, pixKey string) (*DictAccount, error)
	Transfer(ctx context.Context, pixKey string, amount int64, idemKey string) (*TransferResult, error)
	Refund(ctx context.Context, e2eID string, amount int64, idemKey string) (*TransferResult, error)
}
```

- [ ] **Step 1: Write the file. Commit** — `feat: add PixClient interface and DTOs`

### Task 4.2: FakePixClient

**Files:**
- Create: `api/internal/pix/fake.go`
- Test: `api/internal/pix/fake_test.go`

**Interfaces:**
- Produces: `FakePixClient` with programmable behavior: `Charges map[string]*Charge`, `Dict map[string]*DictAccount`, hooks `TransferErr`, `RefundErr`, recorded calls. Implements `PixClient`.

- [ ] **Step 1: Implement the fake** so tests can stage a charge as CONCLUIDA with a given payer CPF, stage DICT results per key, and force transfer/refund failures. Record every call for assertions.

- [ ] **Step 2: Trivial test that it satisfies the interface + returns staged data. Commit** — `test: add fake PIX client`

### Task 4.3: Real Inter client (mTLS + OAuth2 client_credentials)

**Files:**
- Create: `api/internal/pix/inter.go`
- Test: `api/internal/pix/inter_test.go` (httptest server with a self-signed pair; assert request shaping + token caching, NOT a live Inter call)

**Interfaces:**
- Produces: `NewInterClient(cfg *config.Config) (*InterClient, error)` implementing `PixClient`.

- [ ] **Step 1: Implement the HTTP client** with an `*http.Client` whose `Transport.TLSClientConfig.Certificates` loads the mTLS cert/key from `cfg.InterCertPath/InterKeyPath`. Add an OAuth2 client_credentials token manager (POST `{InterBaseURL}/oauth/v2/token`, scopes for cob/pix, cache token until expiry).

- [ ] **Step 2: Map the five methods to Inter endpoints:**
  - `CreateCharge` → `PUT /pix/v2/cob/{txid}` (immediate charge, `valor.original`, `chave`=`InterPixKey`); read `pixCopiaECola`, `location`.
  - `QueryCharge` → `GET /pix/v2/cob/{txid}` — read `status`, `valor`, `pix[].pagador.cpf`, `pix[].endToEndId`.
  - `DictLookup` → the account/DICT lookup endpoint for a key's owner; map owner CPF.
  - `Transfer` → `POST /banking/v2/pix` (PIX payment out to `pixKey`), idempotency header.
  - `Refund` → `PUT /pix/v2/pix/{e2eID}/devolucao/{id}` (devolução), idempotency.
  > **Verify exact request/response fields and paths against Inter's current API reference before going live** — the shapes above are the documented v2 endpoints; field names must be confirmed. This is an external-contract verification step, not a code placeholder.

- [ ] **Step 3: Test with httptest** (own TLS): assert `CreateCharge` issues `PUT /pix/v2/cob/{txid}` with the right JSON and parses the response; assert the OAuth token is fetched once and reused. Commit — `feat: add real Inter PIX client (mTLS + client_credentials)`

---

## Phase 5 — Services (business logic)

### Task 5.1: KYC client (account internal API)

**Files:**
- Create: `api/internal/kycclient/kycclient.go`
- Test: `api/internal/kycclient/kycclient_test.go` (httptest)

**Interfaces:**
- Produces: `Client`, `New(cfg) *Client`; `Confirm(ctx, userID, cpf string) error`, `Get(ctx, userID string) (*KYC, error)` where `KYC{Level,CPF,LegalName,BirthDate string}`. Manages the wallet→account M2M client_credentials token (scope `internal:account:kyc`), cached until expiry.

- [ ] **Step 1: Implement token fetch** (POST `{CtechURL}/v1.0/token`, `grant_type=client_credentials`, `WalletClientID/Secret`, `scope=internal:account:kyc`), then `Confirm` (POST `/v1.0/internal/kyc/confirm`) and `Get` (GET `/v1.0/internal/kyc/:user_id`). Map 409 confirm mismatch to a returned error the service can translate.

- [ ] **Step 2: httptest test + commit** — `feat: add account KYC internal client`

### Task 5.2: WalletService skeleton + GetBalances

**Files:**
- Create: `api/internal/services/wallet.go`
- Test: `api/internal/services/wallet_test.go`

**Interfaces:**
- Consumes: `*repositories.WalletRepository`, `*lock.Locker`, `pix.PixClient`, `*kycclient.Client`, `cache.Backend`, `*config.Config`.
- Produces: `WalletService`, `NewWalletService(...) *WalletService`; `GetBalances(ctx, userID string) (real, sandbox *wallet.Wallet, err error)` (calls `EnsureWallets`).

- [ ] **Step 1: Constructor + `GetBalances`. Unit test** GetBalances with a repo stubbed via an interface (extract a `walletRepo` interface in the service package for the methods it uses, so services are unit-testable without DynamoDB). Commit — `feat: add wallet service skeleton and balance lookup`

### Task 5.3: InitiateDeposit

**Interfaces:**
- Produces: `InitiateDeposit(ctx, userID, kycLevel string, amount int64, idemKey string) (*wallet.PixDeposit, *pix.Charge, error)`.

- [ ] **Step 1: Failing unit test** (fake PIX + stub repo): kyc_level `""` → `problem.KYCNotVerified`; kyc `basic`/`verified` → creates a cob (fake returns charge), persists `pix_deposits` status `pending` with 15-min `ttl`, returns QR.
- [ ] **Step 2: Implement.** Generate txid (ULID hex, ≤35 chars per Inter), resolve the user's real wallet id, call `pix.CreateCharge`, `PutItem` the `pix_deposits` row. Gate `kycLevel != ""`.
- [ ] **Step 3: Run pass. Commit** — `feat: implement PIX deposit initiation`

### Task 5.4: ConfirmDeposit (webhook → re-query → credit/refund)

**Interfaces:**
- Produces: `ConfirmDeposit(ctx, txid string) error`.

- [ ] **Step 1: Failing unit tests** (fake PIX + stub repo + stub kycclient):
  - payer CPF == KYC CPF → `Credit` called with `idempotency_key=txid`, deposit → `confirmed`; if kyc was `basic`, `kycclient.Confirm` called.
  - payer CPF != KYC CPF → NO credit; deposit → `rejected_cpf_mismatch`; `pix.Refund` called with the e2eID.
  - charge not CONCLUIDA on re-query → no-op (idempotent, safe to be re-woken).
  - refund failure → deposit stays flagged + returns an error that surfaces an operational alarm (log at error).
- [ ] **Step 2: Implement.** NEVER trust webhook payload: load `pix_deposits` by txid, call `pix.QueryCharge(txid)`, branch on status + CPF match. Use the account KYC CPF via `kycclient.Get(userID)`. Credit via `repo.Credit` (idemKey=txid). Fetch the wallet-held lock is NOT needed here (idempotency guard + conditional update suffice; deposits are credits, no balance condition), but still take the wallet lock to serialize with a concurrent withdrawal on the same wallet — document the choice.
- [ ] **Step 3: Run pass. Commit** — `feat: implement PIX deposit confirmation with re-query and CPF gate`

### Task 5.5: Withdraw

**Interfaces:**
- Produces: `Withdraw(ctx, userID, kycLevel string, lastMFAAt int64, amount int64, pixKey, idemKey string) (*wallet.Withdrawal, error)`.

- [ ] **Step 1: Failing unit tests:**
  - lock contention (locker returns ok=false) → `problem.WalletBusy`.
  - DICT owner CPF != KYC CPF → `problem.WithdrawCPFMismatch`, nothing debited, lock released.
  - happy path: fee computed via `wallet.WithdrawalFee`, `Debit` called for `amount+fee` (or two entries: withdraw + fee — implement as one debit of `amount+fee` writing two ledger entries within the transaction), transfer called, withdrawal `completed`.
  - transfer call fails after debit → withdrawal `processing` persisted (reconciliation will resolve), debit NOT rolled back, returns the withdrawal in processing (202-style).
  - insufficient balance → `problem.InsufficientBalance` (from Debit condition).
- [ ] **Step 2: Implement.** Order: acquire lock → `kycclient.Get` for KYC CPF (or trust `kyc_level==verified` gate + DICT match) → `pix.DictLookup(pixKey)` compare CPF → compute fee → `repo.Debit` amount+fee atomically (extend Debit or add `DebitWithFee` writing `withdraw` + `fee` ledger entries + guard in one transaction) → persist `withdrawals` row `processing` → `pix.Transfer` → on success update row `completed` (append no ledger; the debit already happened) → release lock. (Step-up/kyc gates are enforced at the handler; the service re-checks `kycLevel==verified` defensively.)
- [ ] **Step 3: Run pass. Commit** — `feat: implement PIX withdrawal with DICT match, fee, and processing state`

### Task 5.6: PurchaseSandbox

**Interfaces:**
- Produces: `PurchaseSandbox(ctx, userID string, amount int64, idemKey string) (debit, credit *wallet.LedgerEntry, err error)`.

- [ ] **Step 1: Failing unit tests:** locks acquired in order real→sandbox (assert via a recording locker); insufficient real balance → `InsufficientBalance`, sandbox untouched (atomic); happy path → real debited + sandbox credited atomically; replay returns same entries.
- [ ] **Step 2: Implement** using `locker.AcquireOrdered(realID, sandboxID)` then `repo.Transfer(realID, sandboxID, amount, EntrySandboxPurchase, EntrySandboxCredit, ...)`.
- [ ] **Step 3: Run pass. Commit** — `feat: implement sandbox purchase (cross-wallet atomic debit/credit)`

### Task 5.7: CreditSandbox / DebitSandbox (M2M)

**Interfaces:**
- Produces: `CreditSandbox(ctx, userID string, amount int64, idemKey, reason string) (*wallet.LedgerEntry, error)`, `DebitSandbox(ctx, userID string, amount int64, idemKey, reason string) (*wallet.LedgerEntry, error)`.

- [ ] **Step 1: Failing unit tests:** credit adds to the sandbox wallet (EnsureWallets first); debit respects balance (insufficient → `InsufficientBalance`); both idempotent; both take the sandbox lock.
- [ ] **Step 2: Implement** — resolve sandbox wallet id, lock it, `repo.Credit`/`repo.Debit` with entry type `game_credit`/`game_debit`, `ref=reason`.
- [ ] **Step 3: Run pass. Commit** — `feat: implement sandbox M2M credit/debit`

---

## Phase 6 — HTTP routes & wiring

### Task 6.1: Router, DTOs, helpers, fx wiring

**Files:**
- Create: `api/internal/api/v1/router.go`, `api/internal/api/v1/helpers.go`, `api/internal/api/v1/dto.go`
- Modify: `api/internal/app/app.go`

**Interfaces:**
- Produces: `v1.Register(app *fiber.App, c cache.Backend, cfg *config.Config, svc *services.WalletService)`; `app.Module` wiring all providers.

- [ ] **Step 1: Copy `helpers.go`** from `ctech-dfe`, keeping only what wallet uses: `sendProblem`, `sendItem`, `sendPage`, `bindJSON[T]`, cursor encode/decode (`buildNextCursor`, `decodeCursor`, `prevCursorOf`, `cursorPayload`, `PaginatedResponse`), `intQuery`. Drop DFe-specific helpers (`resolveActor`, fiscal config, state-reg). Module path swap.

- [ ] **Step 2: Write `dto.go`** — request bodies with validate tags:

```go
type DepositRequest  struct{ Amount int64 `json:"amount" validate:"required,gt=0"` }
type WithdrawRequest  struct{ Amount int64 `json:"amount" validate:"required,gt=0"`; PixKey string `json:"pix_key" validate:"required"` }
type SandboxPurchaseRequest struct{ Amount int64 `json:"amount" validate:"required,gt=0"` }
type SandboxOpRequest struct{ UserID string `json:"user_id" validate:"required"`; Amount int64 `json:"amount" validate:"required,gt=0"`; IdempotencyKey string `json:"idempotency_key" validate:"required"`; Reason string `json:"reason"` }
```

- [ ] **Step 3: Write `router.go`** with `Register` building the verifier, the user route group (auth middleware), and the internal route group; wire handlers from Tasks 6.2/6.3. Constant `HeaderIdempotencyKey = "Idempotency-Key"`.

- [ ] **Step 4: Complete `app.go`** — `Module` provides `config.Load`, `newAWSClients`, `newDynamoDBClient`, `newCacheBackend` (copy from dfe), `newLocker`, `newPixClient` (real Inter when creds set, else fail in prod / fake never in prod), `newKYCClient`, `NewWalletRepository`, `NewWalletService`, `newFiberApp`, and `fx.Invoke(registerRoutes, startServer)`. Copy `newFiberApp`, `startServer`, `errorHandler` from dfe (drop CORS `OrgHeader`, WS, consumer).

- [ ] **Step 5: `go build ./...`, run server against dynamodb-local, `GET /health`. Commit** — `feat: wire wallet api routes and fx module`

### Task 6.2: User route handlers

**Files:**
- Create: `api/internal/api/v1/wallet.go`

- [ ] **Step 1: Implement handlers**, each parsing → calling ONE service method → `sendItem`/`sendPage`/`sendProblem`:
  - `GET /v1.0/wallet` (auth) → `GetBalances`.
  - `POST /v1.0/wallet/deposits` (auth, `RequireKYC(basic)`) → read `Idempotency-Key`, `InitiateDeposit`.
  - `POST /v1.0/wallet/withdrawals` (auth, `RequireKYC(verified)`, `RequireRecentMFA(StepUpMaxAge)`) → `Withdraw`.
  - `POST /v1.0/wallet/sandbox/purchase` (auth) → `PurchaseSandbox`.
  - `GET /v1.0/wallet/:type/ledger` (auth) → validate `type` ∈ {real,sandbox}, `Statement` with cursor.
- [ ] **Step 2: Wire into `router.go`. Build. Commit** — `feat: add wallet user route handlers`

### Task 6.3: Internal route handlers

**Files:**
- Create: `api/internal/api/v1/internal.go`

- [ ] **Step 1: Implement:**
  - `POST /v1.0/internal/pix/webhook` — auth = Inter webhook secret/mTLS (NOT the account JWT). Verify `INTER_WEBHOOK_SECRET` (constant-time compare) or mTLS client cert; parse the txid(s) from the payload; call `ConfirmDeposit(txid)` for each. Return 200 quickly; never credit from payload.
  - `POST /v1.0/internal/wallet/sandbox/credit` (`RequireScope(ScopeWalletCredit)`) → `CreditSandbox`.
  - `POST /v1.0/internal/wallet/sandbox/debit` (`RequireScope(ScopeWalletDebit)`) → `DebitSandbox`.
- [ ] **Step 2: Wire, build, commit** — `feat: add internal PIX webhook and sandbox M2M routes`

---

## Phase 7 — Withdrawal reconciliation job

### Task 7.1: Reconciliation entrypoint + logic

**Files:**
- Create: `api/cmd/reconcile/main.go`, `api/internal/services/reconcile.go`
- Test: `api/internal/services/reconcile_test.go`

**Interfaces:**
- Produces: `ReconcileWithdrawals(ctx) (resolved, reversed, alarmed int, err error)` on `WalletService` (or a dedicated `ReconcileService`).

- [ ] **Step 1: Failing unit tests** (fake PIX + stub repo): a `processing` withdrawal whose Inter transfer now shows completed → mark `completed`; one that Inter shows failed/never-sent → reverse the internal debit (credit back amount+fee, idempotency_key=`reverse#{withdrawalID}`) and mark `reversed`; a reverse whose credit-back fails → mark `refund_failed` + log error alarm.
- [ ] **Step 2: Implement.** Query `withdrawals` GSI `gsi_status` = `processing`; for each, `pix.Query... ` the transfer status; resolve. `cmd/reconcile/main.go` builds a minimal fx app (config + aws + repo + pix + service) and invokes once (suitable for an EventBridge-scheduled Lambda/cron).
- [ ] **Step 3: Run pass. Commit** — `feat: add withdrawal reconciliation job`

---

## Phase 8 — Integration tests & operational seeding

### Task 8.1: Integration harness

**Files:**
- Create: `api/tests/integration/setup_test.go`

- [ ] **Step 1: Copy the dfe harness shape** (`//go:build integration`, `TestMain` skips if `DYNAMODB_ENDPOINT` unset, static creds, `TABLE_PREFIX=test`). Define `createTables`/`dropTables` creating all five wallet tables with their GSIs (`gsi_user`, `gsi_idem`, `gsi_status`). Provide helpers to build a real `WalletRepository`, an in-memory `cache.Backend`, a `FakePixClient`, and a stub KYC client.
- [ ] **Step 2: Commit** — `test: add integration harness for wallet (dynamodb-local)`

### Task 8.2: Integration tests for the money paths

**Files:**
- Create: `api/tests/integration/{deposit_test.go,withdraw_test.go,sandbox_test.go,idempotency_test.go}`

- [ ] **Step 1: Write the tests** (from the spec §H):
  - deposit: webhook→re-query(fake CONCLUIDA, matching CPF)→credit; balance increases; ledger has one entry.
  - CPF mismatch: deposit rejected + `Refund` recorded on the fake; no credit.
  - withdraw: DICT match → debit amount+fee (two ledger entries) → transfer recorded → `completed`.
  - withdraw CPF mismatch → 403, nothing debited.
  - concurrent withdraw on same wallet → second gets `wallet-busy` (drive via two goroutines contending the lock).
  - sandbox purchase debits real + credits sandbox atomically; real-insufficient leaves sandbox untouched.
  - idempotency: same key replayed returns same result, ledger has ONE entry; same key + different payload → `idempotency-conflict`.
  - no-negative: debit beyond balance → `insufficient-balance`, balance unchanged.
  - sandbox M2M without scope → 403 (route-level test with a token lacking the scope).
- [ ] **Step 2: Run** `make test-integration`. Expected: all PASS. Commit — `test: add integration tests for deposit/withdraw/sandbox/idempotency`

### Task 8.3: Operational seeding notes (no wallet code)

**Files:**
- Create: `api/docs` note or extend `docs/specs` — a short `OPERATIONS.md` in the repo root documenting the two out-of-band steps.

- [ ] **Step 1: Document** (this is operational, run against ctech-account, not code in this repo):
  1. Seed the wallet's `internal:wallet:credit` / `internal:wallet:debit` scopes into the global catalog: add them to `ctech-account/internal/scopes/catalog.go` and run `cmd/seedscopes`.
  2. Seed the wallet's own M2M client (confidential, `first_party:true`, `allowed_scopes:["internal:account:kyc"]`) via direct DynamoDB put into `{env}_account_oauth_clients`.
  3. Seed each consumer app's (poker/dominó/billing) M2M client with only the sandbox scope subset it needs via `AllowedScopes`.
- [ ] **Step 2: Commit** — `docs: add wallet operational seeding guide`

---

## Self-Review

**Spec coverage** (design spec §A–I):
- §A data model → Task 2.1 (+ tables in File Structure; idempotency refined to a guard item, flagged in Architecture).
- §A domain Repository/Service → Tasks 2.3–2.6, 5.x.
- §B rules (no-negative, idempotency, ordered lock, sandbox-never-real) → Tasks 2.4/2.5, 3.1, 1.2/6.2.
- §C deposit flow → Tasks 5.3/5.4, 6.3 (webhook).
- §D withdrawal flow (lock, DICT, fee, processing, reconciliation) → Tasks 5.5, 3.1, 2.2, 7.1.
- §E sandbox (M2M credit/debit, purchase) → Tasks 5.6/5.7, 6.3.
- §F routes + error codes → Tasks 6.1–6.3, 0.1 (problem codes).
- §G scopes → Tasks 1.2, 8.3.
- §H tests → Tasks 2.2, 8.2.
- §I cross-project → Tasks 5.1, 8.3.
- Risks (webhook forgery, transfer-after-debit limbo, refund failure, regulatory) → Tasks 5.4/6.3, 5.5/7.1, 5.4/7.1, (regulatory = out of technical scope, noted in CLAUDE.md).

**Deviations flagged for the review gate:**
1. Idempotency uses a dedicated guard item (`IDEM#{key}`, `attribute_not_exists`) rather than a "unique GSI" — DynamoDB GSIs cannot enforce uniqueness; the GSI is retained for replay lookup.
2. Added a `withdrawals` table (not explicit in the spec's table list) to carry the `processing` state the spec's §D/Risks require for reconciliation.
3. Real Inter endpoint field shapes must be confirmed against Inter's live API reference (Task 4.3) — flagged as external-contract verification, not left as a code placeholder.

**Placeholder scan:** none — external-API verification note in 4.3 is explicit and scoped.
**Type consistency:** `Mutation`, `Credit/Debit/Transfer`, `WithdrawalFee`, `Claims`, `PixClient`, `Locker.Acquire/AcquireOrdered` signatures are consistent across Tasks 2.x/3.1/4.1/5.x.
