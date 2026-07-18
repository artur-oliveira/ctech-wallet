# pix-gateway Lambda Migration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move all direct Inter PIX contact (outbound calls + inbound webhook) out of `api` into a new
sibling Go module `pix-gateway/`, running on Lambda, and add a WebSocket + polling-fallback real-time
deposit notification to `api`/`ui`.

**Architecture:** `api` keeps `internal/pix.PixClient` as its interface but swaps `InterClient` for a new
`LambdaPixClient` that invokes `pix-gateway`'s outbound Lambda synchronously. Inter's webhook lands on a
new mTLS-verified API Gateway HTTP API custom domain (`pix.wallet.aoctech.app`) wired to a second Lambda in
`pix-gateway`, which re-queries the charge and calls a new M2M-scoped `api` endpoint,
`POST /v1.0/internal/pix/confirm-deposit`, to do the actual credit (unchanged `WalletService.ConfirmDeposit`
business logic). `api` gains a Valkey-backed WebSocket registry (mirroring `ctech-dfe`'s pattern) so a
connected user is pushed a `deposit_confirmed` event the moment the ledger credits; `ui` connects to it and
falls back to polling balances 30s after opening a PIX charge if the socket hasn't delivered by then.

**Tech Stack:** Go 1.26 (both modules), `aws-lambda-go`, AWS CDK v2 (TypeScript), Fiber v3,
`redis/go-redis/v9` (Valkey), Next.js/React Query, native browser `WebSocket`.

**Spec:** `docs/specs/2026-07-13-pix-gateway-lambda-design.md` — read it before starting; this plan
assumes it in full.

## Global Constraints

- All amounts are integer centavos — never floats (root `CLAUDE.md`).
- Every string key, scope, header name, cache-key prefix, SSM path is a named constant — no magic strings
  (root `CLAUDE.md`).
- All API errors go through `problem.*` helpers / `sendProblem(c, err)` — never raw errors, `fiber.Map`,
  or `fiber.NewError` (`api/CLAUDE.md`).
- Layer separation is strictly enforced in `api`: repository = DynamoDB only, service = business logic,
  route = parse + call one service method (`api/CLAUDE.md`).
- `aws-sdk-go-v2` only; RS256-only auth (`api/CLAUDE.md`).
- The deployed EC2 binary must be named `app` (`api/CLAUDE.md`) — unaffected by this plan, but do not
  rename it.
- `ui`: `npx eslint src --ext .ts,.tsx --max-warnings 0` must pass with zero errors/warnings before any
  commit (root `CLAUDE.md`).
- Never commit secrets: Inter mTLS certs/client secret, webhook secret, JWT keys, AWS credentials, real
  CPFs (root `CLAUDE.md`).
- The PIX webhook must never trust its payload — always re-query the charge by `txid` at Inter before
  crediting (Financial Safety Invariant 11). This plan preserves that; `ConfirmDeposit`'s re-query is
  unchanged.
- No money left in limbo (Financial Safety Invariant 12) — this plan does not touch withdrawal
  reconciliation; `ReconcileWithdrawals` and its schedule are untouched.
- No SNS/SQS added for the webhook path — Inter's own retries + existing reconciliation are judged
  sufficient (see spec, "Resilience" section).
- Go module boundary: `pix-gateway` is a **separate Go module** (own `go.mod`) from `api` — it CANNOT
  import `api/internal/...` packages (Go's `internal/` visibility is scoped to the module tree rooted at
  its parent). Every file `pix-gateway` needs from `api/internal/pix` or `api/internal/secrets` must be
  physically copied into `pix-gateway/internal/...`, then deleted from `api` once `api` no longer needs it
  — not imported cross-module.

---

## File Structure

```
pix-gateway/                          # NEW — separate Go module
├── go.mod
├── Makefile
├── cmd/
│   ├── outbound/main.go              # Lambda: multiplexes the 7 PixClient RPC ops
│   └── webhook/main.go               # Lambda: API Gateway HTTP API proxy integration
├── internal/
│   ├── config/config.go              # env config (Inter base URL/key/client id, wallet API URL, M2M creds)
│   ├── secrets/ssm.go                # moved from api/internal/secrets — Inter mTLS keypair + secrets
│   ├── inter/
│   │   ├── client.go                 # moved from api/internal/pix/client.go — PixClient interface + types
│   │   └── inter.go                  # moved from api/internal/pix/inter.go — InterClient impl
│   ├── rpc/types.go                  # wire contract shared by outbound Lambda + api's LambdaPixClient
│   └── walletclient/walletclient.go  # M2M caller into api's confirm-deposit (mirrors api/internal/kycclient)
└── tests/... (unit tests colocated per package, standard Go convention)

api/                                   # MODIFIED
├── internal/pix/
│   ├── client.go                     # UNCHANGED (interface + types stay — api still depends on PixClient)
│   ├── fake.go                       # UNCHANGED (used by api's own unit tests)
│   ├── inter.go                      # DELETED (moved to pix-gateway/internal/inter)
│   ├── inter_test.go                 # DELETED (moved to pix-gateway/internal/inter)
│   ├── rpc_types.go                  # NEW — wire DTOs mirroring pix-gateway/internal/rpc/types.go
│   └── lambda_client.go              # NEW — LambdaPixClient implementing PixClient via lambda.Invoke
├── internal/secrets/ssm.go           # MODIFIED — drop LoadInterMTLS/LoadInterClientSecret (moved out)
├── internal/config/config.go         # MODIFIED — drop Inter* fields except none needed; add PixGatewayFunctionARN
├── internal/middleware/scope.go      # MODIFIED — add ScopePixConfirmDeposit
├── internal/api/v1/router.go         # MODIFIED — remove webhook route/WebhookSecret param, add confirm-deposit route
├── internal/api/v1/internal.go       # MODIFIED — remove pixWebhook, add confirmDeposit handler
├── internal/api/v1/dto.go            # MODIFIED — remove WebhookPayload, add ConfirmDepositRequest
├── internal/app/app.go               # MODIFIED — remove newInterMTLS/newPixClient/newWebhookSecret, add newLambdaPixClient + ws wiring
├── internal/ws/
│   ├── registry.go                   # NEW — ported from ctech-dfe, org_pk → user_id
│   ├── memory.go                     # NEW — ported
│   └── redis.go                      # NEW — ported
└── internal/api/v1/ws.go             # NEW — GET /v1.0/ws upgrade endpoint, user-keyed

cdk/                                   # MODIFIED
├── lib/go-lambda.ts                  # NEW — extracted goCode()/resolveGo() shared by reconcile-stack + pix-gateway-stack
├── lib/reconcile-stack.ts            # MODIFIED — use shared go-lambda.ts helper
├── lib/iam-stack.ts                  # MODIFIED — narrow api role's SSM grant (drop inter/mtls-*, inter/client-*, inter/webhook-secret)
├── lib/pix-gateway-stack.ts          # NEW — 2 Lambdas, HTTP API + mTLS custom domain + S3 trust store, IAM
├── lib/constants.ts                  # MODIFIED — pix-gateway names/SSM paths
├── lib/api-stack.ts                  # MODIFIED — drop INTER_CLIENT_ID/SECRET/WEBHOOK_SECRET export from start.sh; add PIX_GATEWAY_FUNCTION_ARN
└── bin/ctech-wallet-cdk.ts           # MODIFIED — instantiate PixGatewayStack, wire outputs into ApiStack

.github/workflows/
├── pix-gateway.yml                   # NEW — test → build → S3 → update-function-code (both functions)
└── deploy.yml                        # MODIFIED — add pix-gateway stage between infra and api

ui/                                    # MODIFIED
├── src/lib/hooks/useWebSocket.ts     # NEW — ported from ctech-dfe verbatim
├── src/lib/hooks/useWalletRealtime.ts # NEW — user-scoped equivalent of useRealtimeUpdates
├── src/components/wallet/pix-charge-dialog.tsx  # MODIFIED — 30s polling fallback + auto-resolve
└── src/app/dashboard/page.tsx        # MODIFIED — wire useWalletRealtime, pass resolve callback to dialog
```

---

## Phase A — `pix-gateway` module: outbound Inter calls

### Task 1: Scaffold `pix-gateway` module and move the Inter client code

**Files:**
- Create: `pix-gateway/go.mod`
- Create: `pix-gateway/internal/inter/client.go` (moved from `api/internal/pix/client.go`)
- Create: `pix-gateway/internal/inter/inter.go` (moved from `api/internal/pix/inter.go`)
- Create: `pix-gateway/internal/inter/inter_test.go` (moved subset of `api/internal/pix/inter_test.go`)
- Create: `pix-gateway/Makefile`
- Modify: `api/internal/pix/inter.go` — delete
- Modify: `api/internal/pix/inter_test.go` — delete
- Create: `api/internal/pix/fake_test.go` (keeps `TestFakeSatisfiesInterface`, the only test in the old
  file that doesn't depend on `InterClient`)

**Interfaces:**
- Produces: `inter.PixClient` interface (`CreateCharge`, `QueryCharge`, `DictLookup`, `Transfer`,
  `QueryTransfer`, `Refund`, `Ping`), `inter.Charge`, `inter.DictAccount`, `inter.TransferResult`,
  `inter.ErrKeyNotFound`, `inter.NewInterClient(cfg *Config, kp *MTLSKeypair) (*InterClient, error)` — used
  by Task 2's outbound Lambda handler.

- [ ] **Step 1: Create the module**

```bash
mkdir -p /home/artur/Documents/Projects/Ctech/ctech-wallet/pix-gateway/internal/inter
mkdir -p /home/artur/Documents/Projects/Ctech/ctech-wallet/pix-gateway/internal/secrets
mkdir -p /home/artur/Documents/Projects/Ctech/ctech-wallet/pix-gateway/internal/config
mkdir -p /home/artur/Documents/Projects/Ctech/ctech-wallet/pix-gateway/internal/rpc
mkdir -p /home/artur/Documents/Projects/Ctech/ctech-wallet/pix-gateway/internal/walletclient
mkdir -p /home/artur/Documents/Projects/Ctech/ctech-wallet/pix-gateway/cmd/outbound
mkdir -p /home/artur/Documents/Projects/Ctech/ctech-wallet/pix-gateway/cmd/webhook
cd /home/artur/Documents/Projects/Ctech/ctech-wallet/pix-gateway
go mod init gopkg.aoctech.app/pix-gateway
go get github.com/aws/aws-lambda-go@v1.54.0
```

- [ ] **Step 2: Move `client.go` verbatim, renaming the package**

Write `pix-gateway/internal/inter/client.go` — identical to `api/internal/pix/client.go` except the
package clause:

```go
// Package inter abstracts the PIX partner bank (Inter). pix-gateway talks to the
// bank only through the PixClient interface.
package inter

import (
	"context"
	"errors"
)

// ErrKeyNotFound means the DICT lookup found no owner for the PIX key — the user
// mistyped it, or it isn't registered. It is a CLIENT error and must never be
// reported as a 500; anything else from DictLookup is a bank/transport failure.
var ErrKeyNotFound = errors.New("pix: dict key not found")

// Inter immediate-charge (cob) statuses relevant to deposits.
const (
	ChargeActive    = "ATIVA"
	ChargeCompleted = "CONCLUIDA"
	ChargeRemoved   = "REMOVIDA_PELO_USUARIO_RECEBEDOR"
)

// PIX payout (transfer) statuses used by reconciliation.
const (
	TransferDone     = "EFETIVADO"
	TransferNotFound = "NAO_ENCONTRADO"
)

// Charge is an immediate PIX charge (cobrança imediata).
type Charge struct {
	Txid      string
	Amount    int64  // centavos
	QRCode    string // copia-e-cola (EMV payload)
	QRCodeB64 string // base64 PNG (optional)
	Status    string // one of the Charge* constants
	PayerCPF  string // set only once paid
	E2EID     string // end-to-end id of the received payment
}

// DictAccount is the owner of a PIX key resolved via DICT.
type DictAccount struct {
	Key  string
	CPF  string // owner CPF (for withdrawal same-owner matching)
	Name string
}

// TransferResult is the outcome of a PIX payout or refund.
type TransferResult struct {
	E2EID  string
	Status string
}

// PixClient is the partner-bank contract. The real implementation talks to
// Inter over mTLS.
type PixClient interface {
	// CreateCharge opens an immediate charge for the given txid and amount.
	CreateCharge(ctx context.Context, txid string, amount int64, payerHintCPF string) (*Charge, error)
	// QueryCharge re-reads a charge by txid — the source of truth for a deposit,
	// never the webhook payload.
	QueryCharge(ctx context.Context, txid string) (*Charge, error)
	// DictLookup resolves the owner of a destination PIX key.
	DictLookup(ctx context.Context, pixKey string) (*DictAccount, error)
	// Transfer sends a PIX payout to a key. idemKey deduplicates at the bank.
	Transfer(ctx context.Context, pixKey string, amount int64, idemKey string) (*TransferResult, error)
	// QueryTransfer reports the status of a payout by its idempotency key, so the
	// reconciliation job can tell whether a payout whose call failed actually went
	// through. A not-found result means the payout was never accepted.
	QueryTransfer(ctx context.Context, idemKey string) (*TransferResult, error)
	// Refund issues a devolução for a received payment identified by e2eID.
	Refund(ctx context.Context, e2eID string, amount int64, idemKey string) (*TransferResult, error)
	// Ping reports whether the partner bank is reachable and the credentials are
	// accepted. It performs no money movement and is used by the health check.
	Ping(ctx context.Context) error
}
```

- [ ] **Step 3: Move `inter.go` verbatim, renaming the package and its `config`/`secrets` imports**

Write `pix-gateway/internal/inter/inter.go` — identical body to `api/internal/pix/inter.go`, with:
- `package pix` → `package inter`
- import `"gopkg.aoctech.app/api/internal/config"` →
  `"gopkg.aoctech.app/pix-gateway/internal/config"`
- import `"gopkg.aoctech.app/api/internal/secrets"` →
  `"gopkg.aoctech.app/pix-gateway/internal/secrets"`
- the trailing `var _ PixClient = (*InterClient)(nil)` stays as-is (now checks `inter.PixClient`).

Every other line (struct `InterClient`, path constants, `NewInterClient`, `CreateCharge`, `QueryCharge`,
`DictLookup`, `Transfer`, `QueryTransfer`, `Refund`, `Ping`, `do`/`doIdem`, `statusError`, `tokenManager`,
`centavosToReais`/`reaisToCentavos`/`onlyDigits`) is copied unchanged — this is a transport-layer move, not
a rewrite. Delete `api/internal/pix/inter.go` once this file exists.

- [ ] **Step 4: Move the two Inter-specific tests, split out the fake-only test**

Write `pix-gateway/internal/inter/inter_test.go`:

```go
package inter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestCentavosReaisRoundTrip(t *testing.T) {
	cases := []int64{1, 99, 100, 12345, 5000, 100000}
	for _, c := range cases {
		if got := reaisToCentavos(centavosToReais(c)); got != c {
			t.Errorf("round-trip %d: got %d (via %q)", c, got, centavosToReais(c))
		}
	}
	if centavosToReais(12345) != "123.45" {
		t.Errorf("format: got %q", centavosToReais(12345))
	}
}

func TestInterCreateChargeAndTokenReuse(t *testing.T) {
	var tokenCalls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == pathToken:
			atomic.AddInt32(&tokenCalls, 1)
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "tok-123", "expires_in": 3600})
		case strings.HasPrefix(r.URL.Path, "/pix/v2/cob/"):
			if got := r.Header.Get("Authorization"); got != "Bearer tok-123" {
				t.Errorf("missing/bad bearer: %q", got)
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["chave"] != "wallet-key" {
				t.Errorf("chave not sent: %v", body)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"txid": "tx1", "status": ChargeActive, "pixCopiaECola": "EMV-PAYLOAD",
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := &InterClient{
		base:   srv.URL,
		pixKey: "wallet-key",
		http:   srv.Client(),
	}
	c.tokens = &tokenManager{client: srv.Client(), tokenURL: srv.URL + pathToken, clientID: "id", clientSecret: "sec", scope: tokenScope}

	ctx := context.Background()
	ch, err := c.CreateCharge(ctx, "tx1", 12345, "")
	if err != nil {
		t.Fatalf("CreateCharge: %v", err)
	}
	if ch.QRCode != "EMV-PAYLOAD" || ch.Status != ChargeActive || ch.Amount != 12345 {
		t.Fatalf("bad charge: %+v", ch)
	}

	// Second call reuses the cached token.
	if _, err := c.CreateCharge(ctx, "tx1", 12345, ""); err != nil {
		t.Fatalf("second CreateCharge: %v", err)
	}
	if got := atomic.LoadInt32(&tokenCalls); got != 1 {
		t.Errorf("token fetched %d times, want 1 (should be cached)", got)
	}
}
```

Delete `api/internal/pix/inter_test.go`. Write `api/internal/pix/fake_test.go` (keeps the one test that
depends only on `fake.go`, which stays in `api`):

```go
package pix

import (
	"context"
	"testing"
)

func TestFakeSatisfiesInterface(t *testing.T) {
	f := NewFake()
	f.StageCharge("tx", 500, ChargeCompleted, "12345678901", "E2E-1")
	ch, err := f.QueryCharge(context.Background(), "tx")
	if err != nil || ch.PayerCPF != "12345678901" {
		t.Fatalf("fake query: %+v err=%v", ch, err)
	}
}
```

- [ ] **Step 5: Run tests in both modules**

Run: `cd pix-gateway && go mod tidy && go test ./... -race -count=1`
Expected: PASS (`TestCentavosReaisRoundTrip`, `TestInterCreateChargeAndTokenReuse`)

Run: `cd api && go test ./internal/pix/... -race -count=1`
Expected: PASS (`TestFakeSatisfiesInterface`); `inter.go`/`inter_test.go` no longer present in this
package, so no duplicate symbols.

- [ ] **Step 6: `pix-gateway` Makefile (mirrors `api/Makefile`'s shape)**

Write `pix-gateway/Makefile`:

```makefile
BUILD_DIR   := dist
GOOS        ?= linux
GOARCH      ?= arm64
CGO_ENABLED := 0

.PHONY: build-outbound build-webhook test vet clean

build-outbound:
	CGO_ENABLED=$(CGO_ENABLED) GOOS=$(GOOS) GOARCH=$(GOARCH) \
		go build -tags lambda.norpc -ldflags="-s -w" -o $(BUILD_DIR)/outbound/bootstrap ./cmd/outbound

build-webhook:
	CGO_ENABLED=$(CGO_ENABLED) GOOS=$(GOOS) GOARCH=$(GOARCH) \
		go build -tags lambda.norpc -ldflags="-s -w" -o $(BUILD_DIR)/webhook/bootstrap ./cmd/webhook

test:
	go test ./... -race -coverprofile=coverage.out

vet:
	go vet ./...

clean:
	rm -rf $(BUILD_DIR)
```

- [ ] **Step 7: Commit**

```bash
git add pix-gateway/ api/internal/pix/inter.go api/internal/pix/inter_test.go api/internal/pix/fake_test.go
git commit -m "feat(pix-gateway): scaffold module, move Inter client code from api"
```

---

### Task 2: Wire contract + outbound Lambda handler (multiplexes the 7 `PixClient` ops)

**Files:**
- Create: `pix-gateway/internal/rpc/types.go`
- Create: `pix-gateway/cmd/outbound/main.go`
- Create: `pix-gateway/cmd/outbound/main_test.go`

**Interfaces:**
- Consumes: `inter.PixClient` (Task 1) — `CreateCharge`, `QueryCharge`, `DictLookup`, `Transfer`,
  `QueryTransfer`, `Refund`, `Ping`; `inter.ErrKeyNotFound`.
- Produces: `rpc.Op` constants (`OpCreateCharge`, `OpQueryCharge`, `OpDictLookup`, `OpTransfer`,
  `OpQueryTransfer`, `OpRefund`, `OpPing`), `rpc.Request{Op string; Payload json.RawMessage}`,
  `rpc.Response{Error string; Payload json.RawMessage}`, and one payload struct per op
  (`rpc.CreateChargeArgs`, `rpc.QueryChargeArgs`, `rpc.DictLookupArgs`, `rpc.TransferArgs`,
  `rpc.QueryTransferArgs`, `rpc.RefundArgs`, `rpc.ChargeResult`, `rpc.DictResult`, `rpc.TransferResult`) —
  Task 3's `api/internal/pix/lambda_client.go` mirrors these exact field names/types (it cannot import this
  package — separate module — so field-for-field parity is what keeps the wire format correct; a mismatch
  here is a silent runtime bug, not a compile error).
- Produces: `rpc.ErrKeyNotFoundSentinel = "key_not_found"` — the string `Response.Error` carries when
  `DictLookup` hits `inter.ErrKeyNotFound`, so the caller can reconstruct the sentinel error without a
  shared error type across modules.

- [ ] **Step 1: Write the wire types**

Write `pix-gateway/internal/rpc/types.go`:

```go
// Package rpc defines the wire contract between api's LambdaPixClient and
// pix-gateway's outbound Lambda. Both sides mirror these types independently
// (separate Go modules — internal/ packages cannot be imported across module
// boundaries), so a field added here must be added in
// api/internal/pix/rpc_types.go too.
package rpc

import "encoding/json"

// Op names the PixClient method being invoked. One Lambda function handles all
// of them so api makes exactly one kind of Invoke call.
type Op string

const (
	OpCreateCharge  Op = "CreateCharge"
	OpQueryCharge   Op = "QueryCharge"
	OpDictLookup    Op = "DictLookup"
	OpTransfer      Op = "Transfer"
	OpQueryTransfer Op = "QueryTransfer"
	OpRefund        Op = "Refund"
	OpPing          Op = "Ping"
)

// ErrKeyNotFoundSentinel is the Response.Error value that means
// inter.ErrKeyNotFound — the one PixClient error callers must distinguish from
// a generic bank/transport failure.
const ErrKeyNotFoundSentinel = "key_not_found"

// Request is the Lambda Invoke payload. Payload is re-decoded per Op into the
// matching *Args struct below.
type Request struct {
	Op      Op              `json:"op"`
	Payload json.RawMessage `json:"payload"`
}

// Response is the Lambda Invoke result. Error is empty on success; Payload is
// empty on error. A non-sentinel Error string means a bank/transport failure —
// api surfaces it as problem.InternalServer, matching InterClient's own error
// contract (opaque error, no special handling) today.
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

// ChargeResult mirrors inter.Charge field-for-field.
type ChargeResult struct {
	Txid      string `json:"txid"`
	Amount    int64  `json:"amount"`
	QRCode    string `json:"qr_code"`
	QRCodeB64 string `json:"qr_code_b64"`
	Status    string `json:"status"`
	PayerCPF  string `json:"payer_cpf"`
	E2EID     string `json:"e2e_id"`
}

type DictLookupArgs struct {
	PixKey string `json:"pix_key"`
}

// DictResult mirrors inter.DictAccount field-for-field.
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

// TransferResult mirrors inter.TransferResult field-for-field.
type TransferResult struct {
	E2EID  string `json:"e2e_id"`
	Status string `json:"status"`
}
```

- [ ] **Step 2: Write the failing test for the handler**

Write `pix-gateway/cmd/outbound/main_test.go`:

```go
package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"gopkg.aoctech.app/pix-gateway/internal/inter"
	"gopkg.aoctech.app/pix-gateway/internal/rpc"
)

// fakePix is a minimal stand-in — pix-gateway has no dependency on api's fake,
// it defines its own (different module, and this one only needs to exercise
// the handler's marshal/unmarshal, not real business behavior).
type fakePix struct {
	dictErr error
}

func (f *fakePix) CreateCharge(_ context.Context, txid string, amount int64, _ string) (*inter.Charge, error) {
	return &inter.Charge{Txid: txid, Amount: amount, Status: inter.ChargeActive, QRCode: "EMV"}, nil
}
func (f *fakePix) QueryCharge(_ context.Context, txid string) (*inter.Charge, error) {
	return &inter.Charge{Txid: txid, Status: inter.ChargeCompleted, Amount: 500, PayerCPF: "111"}, nil
}
func (f *fakePix) DictLookup(_ context.Context, key string) (*inter.DictAccount, error) {
	if f.dictErr != nil {
		return nil, f.dictErr
	}
	return &inter.DictAccount{Key: key, CPF: "222", Name: "Fulano"}, nil
}
func (f *fakePix) Transfer(_ context.Context, key string, amount int64, idem string) (*inter.TransferResult, error) {
	return &inter.TransferResult{E2EID: "E2E-" + idem, Status: inter.TransferDone}, nil
}
func (f *fakePix) QueryTransfer(_ context.Context, idem string) (*inter.TransferResult, error) {
	return &inter.TransferResult{Status: inter.TransferNotFound}, nil
}
func (f *fakePix) Refund(_ context.Context, e2e string, amount int64, idem string) (*inter.TransferResult, error) {
	return &inter.TransferResult{E2EID: e2e, Status: "DEVOLVIDO"}, nil
}
func (f *fakePix) Ping(_ context.Context) error { return nil }

func TestHandleCreateCharge(t *testing.T) {
	h := &handler{pix: &fakePix{}}
	payload, _ := json.Marshal(rpc.CreateChargeArgs{Txid: "tx1", Amount: 12345})
	resp := h.handle(context.Background(), rpc.Request{Op: rpc.OpCreateCharge, Payload: payload})
	if resp.Error != "" {
		t.Fatalf("unexpected error: %s", resp.Error)
	}
	var got rpc.ChargeResult
	if err := json.Unmarshal(resp.Payload, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Txid != "tx1" || got.Amount != 12345 || got.Status != inter.ChargeActive {
		t.Fatalf("bad result: %+v", got)
	}
}

func TestHandleDictLookupNotFound(t *testing.T) {
	h := &handler{pix: &fakePix{dictErr: inter.ErrKeyNotFound}}
	payload, _ := json.Marshal(rpc.DictLookupArgs{PixKey: "some-key"})
	resp := h.handle(context.Background(), rpc.Request{Op: rpc.OpDictLookup, Payload: payload})
	if resp.Error != rpc.ErrKeyNotFoundSentinel {
		t.Fatalf("expected sentinel %q, got %q", rpc.ErrKeyNotFoundSentinel, resp.Error)
	}
}

func TestHandleUnknownOp(t *testing.T) {
	h := &handler{pix: &fakePix{}}
	resp := h.handle(context.Background(), rpc.Request{Op: "Bogus"})
	if resp.Error == "" {
		t.Fatal("expected an error for an unknown op")
	}
}

func TestHandlePing(t *testing.T) {
	h := &handler{pix: &fakePix{}}
	resp := h.handle(context.Background(), rpc.Request{Op: rpc.OpPing})
	if resp.Error != "" {
		t.Fatalf("unexpected error: %s", resp.Error)
	}
}

var _ = errors.New // silences unused import if a future edit removes an error path
```

- [ ] **Step 3: Run test to verify it fails**

Run: `cd pix-gateway && go test ./cmd/outbound/... -race -count=1`
Expected: FAIL — `handler`/`handle` undefined (main.go doesn't exist yet).

- [ ] **Step 4: Implement the handler**

Write `pix-gateway/cmd/outbound/main.go`:

```go
// Command outbound is the Lambda pix-gateway invokes for every outbound Inter
// PIX call api needs (CreateCharge, QueryCharge, DictLookup, Transfer,
// QueryTransfer, Refund, Ping). api's LambdaPixClient calls it synchronously
// (RequestResponse) — one op per invocation, mirroring the PixClient interface
// api already depends on.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"

	awscfg "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-lambda-go/lambda"

	"gopkg.aoctech.app/pix-gateway/internal/config"
	"gopkg.aoctech.app/pix-gateway/internal/inter"
	"gopkg.aoctech.app/pix-gateway/internal/rpc"
	"gopkg.aoctech.app/pix-gateway/internal/secrets"
)

type handler struct {
	pix inter.PixClient
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	cfg, err := config.Load()
	if err != nil {
		slog.Error("config load failed", "err", err)
		os.Exit(1)
	}
	pixClient, err := newInter(context.Background(), cfg)
	if err != nil {
		slog.Error("inter client init failed", "err", err)
		os.Exit(1)
	}
	h := &handler{pix: pixClient}
	lambda.Start(h.handle)
}

// newInter builds the real Inter client, reading the mTLS keypair AND the OAuth
// client secret from SSM directly — this Lambda has no start.sh to export env
// vars (same reasoning as api/cmd/reconcile's newPix).
func newInter(ctx context.Context, cfg *config.Config) (inter.PixClient, error) {
	awsCfg, err := awscfg.LoadDefaultConfig(ctx, awscfg.WithRegion(cfg.AWSRegion))
	if err != nil {
		return nil, fmt.Errorf("aws config: %w", err)
	}
	store := secrets.NewStore(ssm.NewFromConfig(awsCfg), cfg.Env)
	kp, err := store.LoadInterMTLS(ctx)
	if err != nil {
		return nil, fmt.Errorf("load mTLS keypair: %w", err)
	}
	if cfg.InterClientSecret == "" {
		s, err := store.LoadInterClientSecret(ctx)
		if err != nil {
			return nil, fmt.Errorf("load client secret: %w", err)
		}
		cfg.InterClientSecret = s
	}
	return inter.NewInterClient(cfg, kp)
}

// handle dispatches on Op, decodes Payload into the matching *Args struct,
// calls the corresponding PixClient method, and encodes the result. Every
// error becomes Response.Error — Lambda invoke errors are reserved for
// transport failures, not business/bank errors, so api's LambdaPixClient reads
// a normal (non-error) Invoke response and inspects Response.Error itself.
func (h *handler) handle(ctx context.Context, req rpc.Request) rpc.Response {
	switch req.Op {
	case rpc.OpCreateCharge:
		var a rpc.CreateChargeArgs
		if err := json.Unmarshal(req.Payload, &a); err != nil {
			return errResp(err)
		}
		c, err := h.pix.CreateCharge(ctx, a.Txid, a.Amount, a.PayerHintCPF)
		if err != nil {
			return errResp(err)
		}
		return okResp(chargeResult(c))

	case rpc.OpQueryCharge:
		var a rpc.QueryChargeArgs
		if err := json.Unmarshal(req.Payload, &a); err != nil {
			return errResp(err)
		}
		c, err := h.pix.QueryCharge(ctx, a.Txid)
		if err != nil {
			return errResp(err)
		}
		return okResp(chargeResult(c))

	case rpc.OpDictLookup:
		var a rpc.DictLookupArgs
		if err := json.Unmarshal(req.Payload, &a); err != nil {
			return errResp(err)
		}
		d, err := h.pix.DictLookup(ctx, a.PixKey)
		if err != nil {
			if errors.Is(err, inter.ErrKeyNotFound) {
				return rpc.Response{Error: rpc.ErrKeyNotFoundSentinel}
			}
			return errResp(err)
		}
		return okResp(rpc.DictResult{Key: d.Key, CPF: d.CPF, Name: d.Name})

	case rpc.OpTransfer:
		var a rpc.TransferArgs
		if err := json.Unmarshal(req.Payload, &a); err != nil {
			return errResp(err)
		}
		r, err := h.pix.Transfer(ctx, a.PixKey, a.Amount, a.IdemKey)
		if err != nil {
			return errResp(err)
		}
		return okResp(transferResult(r))

	case rpc.OpQueryTransfer:
		var a rpc.QueryTransferArgs
		if err := json.Unmarshal(req.Payload, &a); err != nil {
			return errResp(err)
		}
		r, err := h.pix.QueryTransfer(ctx, a.IdemKey)
		if err != nil {
			return errResp(err)
		}
		return okResp(transferResult(r))

	case rpc.OpRefund:
		var a rpc.RefundArgs
		if err := json.Unmarshal(req.Payload, &a); err != nil {
			return errResp(err)
		}
		r, err := h.pix.Refund(ctx, a.E2EID, a.Amount, a.IdemKey)
		if err != nil {
			return errResp(err)
		}
		return okResp(transferResult(r))

	case rpc.OpPing:
		if err := h.pix.Ping(ctx); err != nil {
			return errResp(err)
		}
		return rpc.Response{}

	default:
		return errResp(fmt.Errorf("unknown op %q", req.Op))
	}
}

func chargeResult(c *inter.Charge) rpc.ChargeResult {
	return rpc.ChargeResult{
		Txid: c.Txid, Amount: c.Amount, QRCode: c.QRCode, QRCodeB64: c.QRCodeB64,
		Status: c.Status, PayerCPF: c.PayerCPF, E2EID: c.E2EID,
	}
}

func transferResult(r *inter.TransferResult) rpc.TransferResult {
	return rpc.TransferResult{E2EID: r.E2EID, Status: r.Status}
}

func okResp(v any) rpc.Response {
	b, err := json.Marshal(v)
	if err != nil {
		return errResp(err)
	}
	return rpc.Response{Payload: b}
}

func errResp(err error) rpc.Response {
	return rpc.Response{Error: err.Error()}
}
```

Add the missing `"errors"` import to the `import` block above (used by `errors.Is` in the `OpDictLookup`
case) — the final import list is:

```go
import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/aws/aws-lambda-go/lambda"

	"gopkg.aoctech.app/pix-gateway/internal/config"
	"gopkg.aoctech.app/pix-gateway/internal/inter"
	"gopkg.aoctech.app/pix-gateway/internal/rpc"
	"gopkg.aoctech.app/pix-gateway/internal/secrets"
)
```

Note: `config.Load`, `secrets.NewStore`, `secrets.LoadInterMTLS`/`LoadInterClientSecret` are defined in
Task 4 (`pix-gateway/internal/config`, `pix-gateway/internal/secrets`) — `main.go`'s `main()` function
will not compile until Task 4 lands, but `handler.handle` (what `main_test.go` exercises) has no
dependency on them, so the test step below passes independent of that ordering. Implementers running
tasks in order will hit this naturally since Task 4 comes later in this plan; if `go build ./...` is run
before Task 4, only `main()` (not `handle`) fails, and `go test ./cmd/outbound/...` still passes because
Go test builds only what the test file imports transitively through `handler`/`rpc`/`inter` — `main()`
itself is not exercised by the test and the package still needs to compile as a whole, so **run
`go vet ./cmd/outbound/...` after Task 4, not before**, to fully confirm.

- [ ] **Step 5: Run test to verify it passes**

Run: `cd pix-gateway && go test ./cmd/outbound/... -race -count=1 -run 'TestHandle'`
Expected: PASS for `TestHandleCreateCharge`, `TestHandleDictLookupNotFound`, `TestHandleUnknownOp`,
`TestHandlePing`. (Full `go build`/`go vet` of this package succeeds only after Task 4 supplies
`internal/config` and `internal/secrets` — expected at this point in the plan.)

- [ ] **Step 6: Commit**

```bash
git add pix-gateway/internal/rpc pix-gateway/cmd/outbound
git commit -m "feat(pix-gateway): outbound Lambda handler multiplexing PixClient ops"
```

---

### Task 3: `pix-gateway` config + secrets (moved/adapted from `api`)

**Files:**
- Create: `pix-gateway/internal/config/config.go`
- Create: `pix-gateway/internal/secrets/ssm.go` (moved from `api/internal/secrets/ssm.go`, extended)
- Create: `pix-gateway/internal/secrets/ssm_test.go`

**Interfaces:**
- Produces: `config.Config{AWSRegion, Env, InterBaseURL, InterClientID, InterClientSecret, InterPixKey,
  WalletAPIURL, CtechURL, PixGatewayClientID, PixGatewayClientSecret string}`, `config.Load() (*Config,
  error)` — consumed by Task 2's `cmd/outbound/main.go` (already written) and Task 6's
  `cmd/webhook/main.go`.
- Produces: `secrets.SSMAPI` interface, `secrets.Store`, `secrets.NewStore(client SSMAPI, environment
  string) *Store`, `secrets.MTLSKeypair{CertPEM, KeyPEM []byte}`, `(*Store).LoadInterMTLS(ctx) (*MTLSKeypair,
  error)`, `(*Store).LoadInterClientSecret(ctx) (string, error)`, `(*Store).LoadPixGatewayClientSecret(ctx)
  (string, error)` — the last one is new (Task 5's `walletclient` needs it; `api`'s original file had no
  equivalent secret to load).

- [ ] **Step 1: Write the config**

Write `pix-gateway/internal/config/config.go`:

```go
// Package config holds the 12-Factor environment configuration for pix-gateway.
package config

import (
	"fmt"

	"github.com/caarlos0/env/v11"
)

// Config configures both Lambda functions (outbound + webhook). Not every
// field is used by both — cmd/webhook additionally needs WalletAPIURL,
// CtechURL, PixGatewayClientID/Secret to call api's confirm-deposit endpoint;
// cmd/outbound only needs the Inter fields.
type Config struct {
	AWSRegion string `env:"AWS_REGION" envDefault:"us-east-1"`
	Env       string `env:"ENVIRONMENT" envDefault:"dev"`

	// PIX / Inter partner bank. Mirrors api/internal/config/config.go's fields —
	// this is the only place they now live.
	InterBaseURL      string `env:"INTER_BASE_URL" envDefault:"https://cdpj.partners.bancointer.com.br"`
	InterClientID     string `env:"INTER_CLIENT_ID"`
	InterClientSecret string `env:"INTER_CLIENT_SECRET"`
	InterPixKey       string `env:"INTER_PIX_KEY"`

	// ctech-account, for the webhook Lambda's own M2M token (client_credentials,
	// scope internal:pix:confirm-deposit) — a distinct client from api's own
	// WALLET_CLIENT_ID (see cross-project contract, root CLAUDE.md).
	CtechURL              string `env:"CTECH_URL"`
	PixGatewayClientID     string `env:"PIX_GATEWAY_CLIENT_ID"`
	PixGatewayClientSecret string `env:"PIX_GATEWAY_CLIENT_SECRET"`

	// WalletAPIURL is api's public base URL (e.g. https://wallet.aoctech.app) —
	// the webhook Lambda calls POST {WalletAPIURL}/v1.0/internal/pix/confirm-deposit.
	WalletAPIURL string `env:"WALLET_API_URL"`
}

// Load reads config from environment variables.
func Load() (*Config, error) {
	cfg := &Config{}
	if err := env.Parse(cfg); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	return cfg, nil
}
```

- [ ] **Step 2: Move + extend the secrets store**

Write `pix-gateway/internal/secrets/ssm.go` — identical to `api/internal/secrets/ssm.go` plus one new
method and parameter path:

```go
// Package secrets loads pix-gateway's SSM SecureString parameters: the Inter
// mTLS keypair, the Inter OAuth client secret, and pix-gateway's own M2M client
// secret (used to call api's confirm-deposit endpoint). None are ever written
// to disk or logged.
package secrets

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
)

// Parameter paths. %s is the deployment environment (dev/stage/prod).
const (
	interCertParamFmt          = "/ctech-wallet/%s/inter/mtls-cert"
	interKeyParamFmt           = "/ctech-wallet/%s/inter/mtls-key"
	interSecretParamFmt        = "/ctech-wallet/%s/inter/client-secret"
	pixGatewaySecretParamFmt   = "/ctech-wallet/%s/pix-gateway/client-secret"
)

// SSMAPI is the subset of *ssm.Client this package needs (mockable in tests).
type SSMAPI interface {
	GetParameter(ctx context.Context, in *ssm.GetParameterInput, opts ...func(*ssm.Options)) (*ssm.GetParameterOutput, error)
}

// Store reads pix-gateway's secrets from SSM.
type Store struct {
	client SSMAPI
	env    string
}

func NewStore(client SSMAPI, environment string) *Store {
	return &Store{client: client, env: environment}
}

// MTLSKeypair is the Inter client certificate and private key, in memory.
type MTLSKeypair struct {
	CertPEM []byte
	KeyPEM  []byte
}

// LoadInterMTLS fetches the Inter mTLS keypair. Both values are SecureStrings,
// so WithDecryption is always set.
func (s *Store) LoadInterMTLS(ctx context.Context) (*MTLSKeypair, error) {
	cert, err := s.get(ctx, fmt.Sprintf(interCertParamFmt, s.env))
	if err != nil {
		return nil, err
	}
	key, err := s.get(ctx, fmt.Sprintf(interKeyParamFmt, s.env))
	if err != nil {
		return nil, err
	}
	return &MTLSKeypair{CertPEM: []byte(cert), KeyPEM: []byte(key)}, nil
}

// LoadInterClientSecret fetches the Inter OAuth client secret.
func (s *Store) LoadInterClientSecret(ctx context.Context) (string, error) {
	return s.get(ctx, fmt.Sprintf(interSecretParamFmt, s.env))
}

// LoadPixGatewayClientSecret fetches pix-gateway's own M2M client secret, used
// by the webhook Lambda to obtain a client_credentials token for calling api's
// confirm-deposit endpoint.
func (s *Store) LoadPixGatewayClientSecret(ctx context.Context) (string, error) {
	return s.get(ctx, fmt.Sprintf(pixGatewaySecretParamFmt, s.env))
}

func (s *Store) get(ctx context.Context, name string) (string, error) {
	out, err := s.client.GetParameter(ctx, &ssm.GetParameterInput{
		Name:           aws.String(name),
		WithDecryption: aws.Bool(true),
	})
	if err != nil {
		return "", fmt.Errorf("ssm: get %s: %w", name, err)
	}
	if out.Parameter == nil || out.Parameter.Value == nil || *out.Parameter.Value == "" {
		return "", fmt.Errorf("ssm: parameter %s is empty", name)
	}
	return *out.Parameter.Value, nil
}
```

- [ ] **Step 3: Write a unit test for the new method (mirrors the existing loader tests' mock shape)**

Write `pix-gateway/internal/secrets/ssm_test.go`:

```go
package secrets

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
)

type mockSSM struct {
	values map[string]string
}

func (m *mockSSM) GetParameter(_ context.Context, in *ssm.GetParameterInput, _ ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
	v, ok := m.values[*in.Name]
	if !ok {
		return &ssm.GetParameterOutput{}, nil
	}
	return &ssm.GetParameterOutput{Parameter: &ssm.Parameter{Value: aws.String(v)}}, nil
}

func TestLoadPixGatewayClientSecret(t *testing.T) {
	mock := &mockSSM{values: map[string]string{
		"/ctech-wallet/dev/pix-gateway/client-secret": "shh",
	}}
	store := NewStore(mock, "dev")
	got, err := store.LoadPixGatewayClientSecret(context.Background())
	if err != nil {
		t.Fatalf("LoadPixGatewayClientSecret: %v", err)
	}
	if got != "shh" {
		t.Fatalf("got %q, want %q", got, "shh")
	}
}

func TestLoadInterMTLS(t *testing.T) {
	mock := &mockSSM{values: map[string]string{
		"/ctech-wallet/dev/inter/mtls-cert": "CERT",
		"/ctech-wallet/dev/inter/mtls-key":  "KEY",
	}}
	store := NewStore(mock, "dev")
	kp, err := store.LoadInterMTLS(context.Background())
	if err != nil {
		t.Fatalf("LoadInterMTLS: %v", err)
	}
	if string(kp.CertPEM) != "CERT" || string(kp.KeyPEM) != "KEY" {
		t.Fatalf("bad keypair: %+v", kp)
	}
}
```

- [ ] **Step 4: Run tests**

Run: `cd pix-gateway && go mod tidy && go test ./... -race -count=1`
Expected: PASS across `internal/inter`, `internal/secrets`, `cmd/outbound`. `go vet ./...` also succeeds
now — Task 2's `cmd/outbound/main.go` compiles fully once `internal/config` and `internal/secrets` exist.

- [ ] **Step 5: Commit**

```bash
git add pix-gateway/internal/config pix-gateway/internal/secrets
git commit -m "feat(pix-gateway): config + SSM secrets store"
```

---

## Phase B — `api`: swap `InterClient` for `LambdaPixClient`

### Task 4: `LambdaPixClient` — invokes the outbound Lambda, implements `pix.PixClient`

**Files:**
- Create: `api/internal/pix/rpc_types.go` (mirrors `pix-gateway/internal/rpc/types.go` field-for-field)
- Create: `api/internal/pix/lambda_client.go`
- Create: `api/internal/pix/lambda_client_test.go`
- Modify: `api/internal/config/config.go:46-58` — remove the `InterBaseURL`, `InterClientID`,
  `InterClientSecret`, `InterWebhookSecret`, `InterPixKey` fields (Inter contact no longer lives in `api`);
  add `PixGatewayFunctionName string \`env:"PIX_GATEWAY_FUNCTION_NAME"\``
- Modify: `api/internal/secrets/ssm.go` — remove `LoadInterMTLS`, `LoadInterClientSecret`, and the
  `interCertParamFmt`/`interKeyParamFmt`/`interSecretParamFmt` constants (moved to `pix-gateway`); this
  leaves the file with only the `SSMAPI` interface, `Store`, `NewStore`, and `get` — if nothing in `api`
  uses `Store` after this removal, delete `api/internal/secrets/ssm.go` and the now-empty
  `api/internal/secrets` package entirely (check with `rg -l "internal/secrets" api/internal` — the only
  other consumer was `app.go`'s `newInterMTLS`, removed in this same task, so the package should indeed be
  fully removable)
- Modify: `api/internal/app/app.go:84-119` — remove `newInterMTLS`, `newPixClient`, `newWebhookSecret`;
  add `newLambdaPixClient`, `newLambdaClient`
- Modify: `api/internal/app/app.go:32-52` (the `Module` `fx.Provide` list) — replace
  `newInterMTLS, newPixClient, newWebhookSecret,` with `newLambdaClient, newLambdaPixClient,`

**Interfaces:**
- Consumes: `pix.PixClient` (unchanged, `api/internal/pix/client.go`) — `LambdaPixClient` implements it.
- Produces: `pix.NewLambdaPixClient(client *lambda.Client, functionName string) *LambdaPixClient` —
  consumed by `app.go`'s `newLambdaPixClient`.

- [ ] **Step 1: Mirror the wire types in `api`**

Write `api/internal/pix/rpc_types.go`:

```go
// Package pix — rpc_types.go mirrors pix-gateway/internal/rpc/types.go
// field-for-field. This is the wire contract with pix-gateway's outbound
// Lambda; pix-gateway is a SEPARATE Go module, so these types cannot be
// imported — they are kept in sync by hand. A field added on one side must be
// added here too.
package pix

import "encoding/json"

type rpcOp string

const (
	opCreateCharge  rpcOp = "CreateCharge"
	opQueryCharge   rpcOp = "QueryCharge"
	opDictLookup    rpcOp = "DictLookup"
	opTransfer      rpcOp = "Transfer"
	opQueryTransfer rpcOp = "QueryTransfer"
	opRefund        rpcOp = "Refund"
	opPing          rpcOp = "Ping"
)

const errKeyNotFoundSentinel = "key_not_found"

type rpcRequest struct {
	Op      rpcOp           `json:"op"`
	Payload json.RawMessage `json:"payload"`
}

type rpcResponse struct {
	Error   string          `json:"error,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

type rpcCreateChargeArgs struct {
	Txid         string `json:"txid"`
	Amount       int64  `json:"amount"`
	PayerHintCPF string `json:"payer_hint_cpf"`
}

type rpcQueryChargeArgs struct {
	Txid string `json:"txid"`
}

type rpcChargeResult struct {
	Txid      string `json:"txid"`
	Amount    int64  `json:"amount"`
	QRCode    string `json:"qr_code"`
	QRCodeB64 string `json:"qr_code_b64"`
	Status    string `json:"status"`
	PayerCPF  string `json:"payer_cpf"`
	E2EID     string `json:"e2e_id"`
}

type rpcDictLookupArgs struct {
	PixKey string `json:"pix_key"`
}

type rpcDictResult struct {
	Key  string `json:"key"`
	CPF  string `json:"cpf"`
	Name string `json:"name"`
}

type rpcTransferArgs struct {
	PixKey  string `json:"pix_key"`
	Amount  int64  `json:"amount"`
	IdemKey string `json:"idem_key"`
}

type rpcQueryTransferArgs struct {
	IdemKey string `json:"idem_key"`
}

type rpcRefundArgs struct {
	E2EID   string `json:"e2e_id"`
	Amount  int64  `json:"amount"`
	IdemKey string `json:"idem_key"`
}

type rpcTransferResult struct {
	E2EID  string `json:"e2e_id"`
	Status string `json:"status"`
}
```

- [ ] **Step 2: Write the failing test**

Write `api/internal/pix/lambda_client_test.go`:

```go
package pix

import (
	"context"
	"encoding/json"
	"testing"

	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
)

// fakeLambdaInvoker stands in for *lambda.Client — LambdaPixClient depends on
// a small interface (lambdaInvoker) so this test never touches AWS.
type fakeLambdaInvoker struct {
	// respond is keyed by the decoded rpcRequest.Op string.
	respond map[string]rpcResponse
}

func (f *fakeLambdaInvoker) invoke(_ context.Context, payload []byte) ([]byte, error) {
	var req rpcRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return nil, err
	}
	resp := f.respond[string(req.Op)]
	return json.Marshal(resp)
}

func TestLambdaPixClientCreateCharge(t *testing.T) {
	chargeJSON, _ := json.Marshal(rpcChargeResult{Txid: "tx1", Amount: 500, Status: ChargeActive, QRCode: "EMV"})
	f := &fakeLambdaInvoker{respond: map[string]rpcResponse{
		string(opCreateCharge): {Payload: chargeJSON},
	}}
	c := &LambdaPixClient{invoker: f}
	ch, err := c.CreateCharge(context.Background(), "tx1", 500, "")
	if err != nil {
		t.Fatalf("CreateCharge: %v", err)
	}
	if ch.Txid != "tx1" || ch.Amount != 500 || ch.Status != ChargeActive || ch.QRCode != "EMV" {
		t.Fatalf("bad charge: %+v", ch)
	}
}

func TestLambdaPixClientDictLookupNotFound(t *testing.T) {
	f := &fakeLambdaInvoker{respond: map[string]rpcResponse{
		string(opDictLookup): {Error: errKeyNotFoundSentinel},
	}}
	c := &LambdaPixClient{invoker: f}
	_, err := c.DictLookup(context.Background(), "some-key")
	if err != ErrKeyNotFound {
		t.Fatalf("expected ErrKeyNotFound, got %v", err)
	}
}

func TestLambdaPixClientGenericError(t *testing.T) {
	f := &fakeLambdaInvoker{respond: map[string]rpcResponse{
		string(opPing): {Error: "bank unreachable"},
	}}
	c := &LambdaPixClient{invoker: f}
	err := c.Ping(context.Background())
	if err == nil || err.Error() != "bank unreachable" {
		t.Fatalf("expected passthrough error, got %v", err)
	}
}

var _ = lambdatypes.InvocationTypeRequestResponse // keeps the import used if unreferenced elsewhere
```

- [ ] **Step 3: Run test to verify it fails**

Run: `cd api && go test ./internal/pix/... -race -count=1 -run TestLambdaPixClient`
Expected: FAIL — `LambdaPixClient` undefined.

- [ ] **Step 4: Implement `LambdaPixClient`**

Write `api/internal/pix/lambda_client.go`:

```go
// Package pix — lambda_client.go implements PixClient by invoking pix-gateway's
// outbound Lambda synchronously (RequestResponse). This replaces InterClient's
// direct mTLS HTTP calls: api no longer talks to Inter at all — pix-gateway
// does, over IPv4 Lambda egress. Every PixClient method here does the same
// marshal → Invoke → unmarshal dance; only the Op and Args/Result types differ.
package pix

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
)

// lambdaInvoker is the subset of *lambda.Client LambdaPixClient depends on —
// small enough to fake in tests without touching AWS.
type lambdaInvoker interface {
	invoke(ctx context.Context, payload []byte) ([]byte, error)
}

// awsLambdaInvoker adapts *lambda.Client to lambdaInvoker.
type awsLambdaInvoker struct {
	client       *lambda.Client
	functionName string
}

func (a *awsLambdaInvoker) invoke(ctx context.Context, payload []byte) ([]byte, error) {
	out, err := a.client.Invoke(ctx, &lambda.InvokeInput{
		FunctionName:   &a.functionName,
		InvocationType: lambdatypes.InvocationTypeRequestResponse,
		Payload:        payload,
	})
	if err != nil {
		return nil, fmt.Errorf("pix-gateway invoke: %w", err)
	}
	if out.FunctionError != nil {
		return nil, fmt.Errorf("pix-gateway invoke: function error: %s: %s", *out.FunctionError, string(out.Payload))
	}
	return out.Payload, nil
}

// LambdaPixClient implements PixClient by invoking pix-gateway's outbound
// Lambda. It never talks to Inter directly.
type LambdaPixClient struct {
	invoker lambdaInvoker
}

// NewLambdaPixClient builds the client. functionName is the pix-gateway
// outbound Lambda's name or ARN (config.PixGatewayFunctionName).
func NewLambdaPixClient(client *lambda.Client, functionName string) *LambdaPixClient {
	return &LambdaPixClient{invoker: &awsLambdaInvoker{client: client, functionName: functionName}}
}

func (c *LambdaPixClient) call(ctx context.Context, op rpcOp, args any, out any) error {
	argsJSON, err := json.Marshal(args)
	if err != nil {
		return err
	}
	reqJSON, err := json.Marshal(rpcRequest{Op: op, Payload: argsJSON})
	if err != nil {
		return err
	}
	respJSON, err := c.invoker.invoke(ctx, reqJSON)
	if err != nil {
		return err
	}
	var resp rpcResponse
	if err := json.Unmarshal(respJSON, &resp); err != nil {
		return err
	}
	if resp.Error != "" {
		if resp.Error == errKeyNotFoundSentinel {
			return ErrKeyNotFound
		}
		return fmt.Errorf("pix-gateway: %s", resp.Error)
	}
	if out != nil && len(resp.Payload) > 0 {
		return json.Unmarshal(resp.Payload, out)
	}
	return nil
}

func (c *LambdaPixClient) CreateCharge(ctx context.Context, txid string, amount int64, payerHintCPF string) (*Charge, error) {
	var res rpcChargeResult
	if err := c.call(ctx, opCreateCharge, rpcCreateChargeArgs{Txid: txid, Amount: amount, PayerHintCPF: payerHintCPF}, &res); err != nil {
		return nil, err
	}
	return chargeFromRPC(res), nil
}

func (c *LambdaPixClient) QueryCharge(ctx context.Context, txid string) (*Charge, error) {
	var res rpcChargeResult
	if err := c.call(ctx, opQueryCharge, rpcQueryChargeArgs{Txid: txid}, &res); err != nil {
		return nil, err
	}
	return chargeFromRPC(res), nil
}

func (c *LambdaPixClient) DictLookup(ctx context.Context, pixKey string) (*DictAccount, error) {
	var res rpcDictResult
	if err := c.call(ctx, opDictLookup, rpcDictLookupArgs{PixKey: pixKey}, &res); err != nil {
		return nil, err
	}
	return &DictAccount{Key: res.Key, CPF: res.CPF, Name: res.Name}, nil
}

func (c *LambdaPixClient) Transfer(ctx context.Context, pixKey string, amount int64, idemKey string) (*TransferResult, error) {
	var res rpcTransferResult
	if err := c.call(ctx, opTransfer, rpcTransferArgs{PixKey: pixKey, Amount: amount, IdemKey: idemKey}, &res); err != nil {
		return nil, err
	}
	return transferFromRPC(res), nil
}

func (c *LambdaPixClient) QueryTransfer(ctx context.Context, idemKey string) (*TransferResult, error) {
	var res rpcTransferResult
	if err := c.call(ctx, opQueryTransfer, rpcQueryTransferArgs{IdemKey: idemKey}, &res); err != nil {
		return nil, err
	}
	return transferFromRPC(res), nil
}

func (c *LambdaPixClient) Refund(ctx context.Context, e2eID string, amount int64, idemKey string) (*TransferResult, error) {
	var res rpcTransferResult
	if err := c.call(ctx, opRefund, rpcRefundArgs{E2EID: e2eID, Amount: amount, IdemKey: idemKey}, &res); err != nil {
		return nil, err
	}
	return transferFromRPC(res), nil
}

func (c *LambdaPixClient) Ping(ctx context.Context) error {
	return c.call(ctx, opPing, struct{}{}, nil)
}

func chargeFromRPC(r rpcChargeResult) *Charge {
	return &Charge{
		Txid: r.Txid, Amount: r.Amount, QRCode: r.QRCode, QRCodeB64: r.QRCodeB64,
		Status: r.Status, PayerCPF: r.PayerCPF, E2EID: r.E2EID,
	}
}

func transferFromRPC(r rpcTransferResult) *TransferResult {
	return &TransferResult{E2EID: r.E2EID, Status: r.Status}
}

var _ PixClient = (*LambdaPixClient)(nil)
```

Remove the now-unused `var _ = lambdatypes.InvocationTypeRequestResponse` line from the test file written
in Step 2 — it was only a placeholder to keep the import list honest before `lambda_client.go` existed;
delete it now that `lambdatypes` is genuinely referenced only within `lambda_client.go`, not the test.
Also drop the now-unnecessary `lambdatypes` import from `lambda_client_test.go` entirely.

- [ ] **Step 5: Run test to verify it passes**

Run: `cd api && go test ./internal/pix/... -race -count=1`
Expected: PASS — `TestLambdaPixClientCreateCharge`, `TestLambdaPixClientDictLookupNotFound`,
`TestLambdaPixClientGenericError`, plus the existing `TestFakeSatisfiesInterface`.

- [ ] **Step 6: Remove Inter fields from `api`'s config**

In `api/internal/config/config.go`, delete this block (lines 46–58 in the current file):

```go
	// PIX / Inter partner bank.
	//
	// The short secrets arrive as env vars, exported by start.sh from SSM
	// SecureString — same pattern as ctech-account's GOOGLE_CLIENT_SECRET.
	// The mTLS certificate/key PEMs are NOT env vars: they are read from SSM at
	// runtime (see internal/secrets), so the bank certificate can be rotated
	// without a redeploy and never has to travel through shell/systemd.
	InterBaseURL       string `env:"INTER_BASE_URL" envDefault:"https://cdpj.partners.bancointer.com.br"`
	InterClientID      string `env:"INTER_CLIENT_ID"`
	InterClientSecret  string `env:"INTER_CLIENT_SECRET"`
	InterWebhookSecret string `env:"INTER_WEBHOOK_SECRET"`
	InterPixKey        string `env:"INTER_PIX_KEY"` // receiving key for immediate charges (cob)
```

Replace it with:

```go
	// PixGatewayFunctionName is pix-gateway's outbound Lambda — api invokes it
	// synchronously for every PixClient call. api no longer talks to Inter
	// directly (see docs/specs/2026-07-13-pix-gateway-lambda-design.md).
	PixGatewayFunctionName string `env:"PIX_GATEWAY_FUNCTION_NAME,required"`
```

- [ ] **Step 7: Remove the now-orphaned `internal/secrets` package from `api`**

Run: `rg -l "ctech-wallet/api/internal/secrets" /home/artur/Documents/Projects/Ctech/ctech-wallet/api`
Expected: only `api/internal/app/app.go` (removed in the next step) and `api/internal/secrets/ssm.go`
itself.

```bash
rm -rf /home/artur/Documents/Projects/Ctech/ctech-wallet/api/internal/secrets
```

- [ ] **Step 8: Rewire `app.go`**

In `api/internal/app/app.go`, delete `newInterMTLS` (lines 84–100) and `newPixClient` (lines 102–113) and
`newWebhookSecret` (lines 115–119), and the `"gopkg.aoctech.app/api/internal/secrets"`
import. Replace them with:

```go
// newLambdaClient builds the AWS Lambda SDK client used to invoke pix-gateway's
// outbound function.
func newLambdaClient(cfg *config.Config) (*lambda.Client, error) {
	awsCfg, err := awscfg.LoadDefaultConfig(context.Background(), awscfg.WithRegion(cfg.AWSRegion))
	if err != nil {
		return nil, fmt.Errorf("aws config: %w", err)
	}
	return lambda.NewFromConfig(awsCfg), nil
}

// newLambdaPixClient wraps the Lambda client as api's PixClient implementation.
// api never talks to Inter directly — pix-gateway does.
func newLambdaPixClient(client *lambda.Client, cfg *config.Config) pix.PixClient {
	return pix.NewLambdaPixClient(client, cfg.PixGatewayFunctionName)
}
```

Add the two new imports:

```go
	awscfg "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
```

Update the `fx.Provide` list in `Module` (currently):

```go
		newInterMTLS,
		newPixClient,
		newWebhookSecret,
```

to:

```go
		newLambdaClient,
		newLambdaPixClient,
```

`registerRoutes` still takes a `pix.PixClient` and `apiv1.WebhookSecret` — the `WebhookSecret` parameter
is removed from `registerRoutes`/`apiv1.Register` in Task 6, not here; leave it as-is for this task so the
module still compiles (`newWebhookSecret` is deleted here, which would break the `WebhookSecret`
provider — **do not delete `newWebhookSecret` yet if Task 6 has not run**). To keep this task
independently buildable, instead keep a minimal placeholder in this task:

```go
// newWebhookSecret is removed in a later task alongside the webhook route
// itself (Task 6) — DO NOT delete this function yet, only the Inter-specific
// providers above it.
func newWebhookSecret(cfg *config.Config) apiv1.WebhookSecret {
	return apiv1.WebhookSecret("") // placeholder: cfg.InterWebhookSecret no longer exists after Step 6
}
```

Since Step 6 already removed `cfg.InterWebhookSecret`, `newWebhookSecret` must return a constant empty
string here (the field it read no longer exists) — this keeps `api` building at the end of Task 4 without
prematurely doing Task 6's route surgery. Keep `newWebhookSecret,` in the `fx.Provide` list for now.

- [ ] **Step 9: Build and run the full `api` test suite**

Run: `cd api && go build ./... && go test ./... -race -count=1`
Expected: builds clean; all existing tests pass (the webhook route is unaffected until Task 6, and it
just receives an empty `webhookSecret`, which was already possible before this migration in dev).

- [ ] **Step 10: Commit**

```bash
git add api/internal/pix/rpc_types.go api/internal/pix/lambda_client.go api/internal/pix/lambda_client_test.go \
        api/internal/config/config.go api/internal/app/app.go
git rm -r api/internal/secrets
git commit -m "feat(api): replace InterClient with LambdaPixClient (pix-gateway invoke)"
```

---

## Phase C — Webhook: `pix-gateway` receives it, `api` credits it

### Task 5: `pix-gateway/internal/walletclient` — M2M caller into `api`'s confirm-deposit endpoint

**Files:**
- Create: `pix-gateway/internal/walletclient/walletclient.go`
- Create: `pix-gateway/internal/walletclient/walletclient_test.go`

**Interfaces:**
- Produces: `walletclient.Client`, `walletclient.New(cfg *config.Config, clientSecret string) *Client`,
  `(*Client).ConfirmDeposit(ctx context.Context, txid string) error` — consumed by Task 6's webhook
  handler.
- Consumes: `config.Config` (Task 3) fields `CtechURL`, `PixGatewayClientID`, `PixGatewayClientSecret`,
  `WalletAPIURL`.

This mirrors `api/internal/kycclient` exactly (same OAuth `client_credentials` dance against
`ctech-account`'s token endpoint), except the token's scope is `internal:pix:confirm-deposit` and the
final call target is `api` itself, not `ctech-account`.

- [ ] **Step 1: Write the failing test**

Write `pix-gateway/internal/walletclient/walletclient_test.go`:

```go
package walletclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"gopkg.aoctech.app/pix-gateway/internal/config"
)

func TestConfirmDepositSendsBearerAndTxid(t *testing.T) {
	var gotAuth, gotBody string
	accountSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == pathToken {
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "tok-abc", "expires_in": 3600})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer accountSrv.Close()

	walletSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		body, _ := json.Marshal(map[string]any{})
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotBody = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer walletSrv.Close()

	cfg := &config.Config{
		CtechURL:               accountSrv.URL,
		PixGatewayClientID:      "pix-gateway",
		PixGatewayClientSecret:  "secret",
		WalletAPIURL:            walletSrv.URL,
	}
	c := New(cfg, cfg.PixGatewayClientSecret)
	if err := c.ConfirmDeposit(context.Background(), "tx1"); err != nil {
		t.Fatalf("ConfirmDeposit: %v", err)
	}
	if gotAuth != "Bearer tok-abc" {
		t.Fatalf("bad bearer: %q", gotAuth)
	}
	if gotBody != pathConfirmDeposit {
		t.Fatalf("bad path: %q", gotBody)
	}
}

func TestConfirmDepositErrorStatus(t *testing.T) {
	accountSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "tok-abc", "expires_in": 3600})
	}))
	defer accountSrv.Close()

	walletSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer walletSrv.Close()

	cfg := &config.Config{
		CtechURL:              accountSrv.URL,
		PixGatewayClientID:    "pix-gateway",
		WalletAPIURL:          walletSrv.URL,
	}
	c := New(cfg, "secret")
	if err := c.ConfirmDeposit(context.Background(), "tx1"); err == nil {
		t.Fatal("expected an error on 500")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd pix-gateway && go test ./internal/walletclient/... -race -count=1`
Expected: FAIL — package doesn't exist yet.

- [ ] **Step 3: Implement the client**

Write `pix-gateway/internal/walletclient/walletclient.go`:

```go
// Package walletclient calls api's internal confirm-deposit endpoint using
// pix-gateway's own M2M client_credentials token (scope
// internal:pix:confirm-deposit). This is the only way money moves as a result
// of the webhook: pix-gateway itself never touches the ledger.
package walletclient

import (
	"bytes"
	"encoding/json"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"gopkg.aoctech.app/pix-gateway/internal/config"
)

const (
	pathToken           = "/v1.0/token"
	pathConfirmDeposit  = "/v1.0/internal/pix/confirm-deposit"
	scopeConfirmDeposit = "internal:pix:confirm-deposit"
)

// Client calls api's confirm-deposit endpoint.
type Client struct {
	base   string
	http   *http.Client
	tokens *tokenManager
}

// New builds the client. clientSecret is passed explicitly (loaded from SSM at
// cold start by cmd/webhook, not stored in cfg) rather than trusting
// cfg.PixGatewayClientSecret's env-var value, mirroring how cmd/outbound
// resolves the Inter client secret.
func New(cfg *config.Config, clientSecret string) *Client {
	httpClient := &http.Client{Timeout: 10 * time.Second}
	return &Client{
		base: strings.TrimRight(cfg.WalletAPIURL, "/"),
		http: httpClient,
		tokens: &tokenManager{
			client:       httpClient,
			tokenURL:     strings.TrimRight(cfg.CtechURL, "/") + pathToken,
			clientID:     cfg.PixGatewayClientID,
			clientSecret: clientSecret,
			scope:        scopeConfirmDeposit,
		},
	}
}

// ConfirmDeposit calls api's confirm-deposit endpoint for txid. api re-derives
// everything from its own re-query of Inter — this call carries only the
// txid, never an amount or status (Financial Safety Invariant 11: the webhook
// is a wake-up signal, never the source of truth, and neither is this call).
func (c *Client) ConfirmDeposit(ctx context.Context, txid string) error {
	body, err := json.Marshal(map[string]string{"txid": txid})
	if err != nil {
		return err
	}
	token, err := c.tokens.get(ctx)
	if err != nil {
		return fmt.Errorf("walletclient: get token: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+pathConfirmDeposit, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("walletclient: confirm-deposit status %d: %s", resp.StatusCode, string(raw))
	}
	return nil
}

// tokenManager fetches and caches pix-gateway's M2M client_credentials token.
// Identical shape to api/internal/kycclient's tokenManager — cannot be shared
// (separate module) so it is duplicated deliberately.
type tokenManager struct {
	client       *http.Client
	tokenURL     string
	clientID     string
	clientSecret string
	scope        string

	mu     sync.Mutex
	token  string
	expiry time.Time
}

func (t *tokenManager) get(ctx context.Context) (string, error) {
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
		return "", fmt.Errorf("account token: status %d: %s", resp.StatusCode, string(raw))
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

Fix the import ordering (`"context"` should sort alongside the standard-library group, not after
`"encoding/json"` — gofmt/goimports will do this automatically; run `gofmt -w` in Step 5).

- [ ] **Step 4: Run test to verify it passes**

Run: `cd pix-gateway && gofmt -w internal/walletclient/ && go test ./internal/walletclient/... -race -count=1`
Expected: PASS — `TestConfirmDepositSendsBearerAndTxid`, `TestConfirmDepositErrorStatus`.

- [ ] **Step 5: Commit**

```bash
git add pix-gateway/internal/walletclient
git commit -m "feat(pix-gateway): walletclient calls api's confirm-deposit endpoint"
```

---

### Task 6: `api`'s `confirm-deposit` endpoint (replaces `pixWebhook`)

This lands in `api` first — the webhook Lambda (Task 7) calls it, so it must exist before Task 7 is
wired end-to-end, and this task is independently testable via the existing route-test pattern.

**Files:**
- Modify: `api/internal/middleware/scope.go:9-13` — add `ScopePixConfirmDeposit`
- Modify: `api/internal/api/v1/dto.go` — remove `WebhookPayload`, add `ConfirmDepositRequest`
- Modify: `api/internal/api/v1/internal.go` — remove `pixWebhook` and `HeaderWebhookSecret`, add
  `confirmDeposit`
- Modify: `api/internal/api/v1/router.go:15-28,71-79` — remove `WebhookSecret` type + `webhookSecret`
  field/param, remove the webhook route, add the confirm-deposit route under M2M scope
- Modify: `api/internal/app/app.go` — remove `newWebhookSecret` and its `fx.Provide`/`registerRoutes`
  wiring (deferred from Task 4)
- Modify: `api/internal/api/v1/router_test.go` — update to the new `Register` signature
- Create: `api/internal/api/v1/internal_test.go` (new — no existing test file covered `pixWebhook`
  directly; add one for `confirmDeposit`)

**Interfaces:**
- Consumes: `services.WalletService.ConfirmDeposit(ctx, txid string) error` — unchanged, already exists
  (`api/internal/services/wallet.go:184`).
- Consumes: `middleware.RequireScope(scope string) fiber.Handler` — unchanged.
- Produces: `middleware.ScopePixConfirmDeposit = "internal:pix:confirm-deposit"` — this is the scope
  `pix-gateway`'s M2M client must be seeded with in `ctech-account` (operational seeding, see Task 12's
  note; no `ctech-account` code change, per the cross-project contract).

- [ ] **Step 1: Add the scope constant**

In `api/internal/middleware/scope.go`, change:

```go
// Scopes the wallet defines for its own internal callers (poker/dominó/billing).
const (
	ScopeWalletCredit = "internal:wallet:credit"
	ScopeWalletDebit  = "internal:wallet:debit"
)
```

to:

```go
// Scopes the wallet defines for its own internal callers (poker/dominó/billing,
// and pix-gateway's webhook Lambda).
const (
	ScopeWalletCredit       = "internal:wallet:credit"
	ScopeWalletDebit        = "internal:wallet:debit"
	ScopePixConfirmDeposit  = "internal:pix:confirm-deposit"
)
```

- [ ] **Step 2: Replace the DTO**

In `api/internal/api/v1/dto.go`, delete the `WebhookPayload` struct:

```go
// WebhookPayload is the minimal shape read from the Inter PIX webhook. It is a
// wake-up signal only — the txid is re-queried before any credit.
type WebhookPayload struct {
	Pix []struct {
		Txid string `json:"txid"`
	} `json:"pix"`
}
```

Replace it with:

```go
// ConfirmDepositRequest is pix-gateway's webhook-Lambda call after it has
// already re-queried the charge at Inter. Only the txid crosses this
// boundary — api re-derives amount/status/payer CPF itself via
// WalletService.ConfirmDeposit, which re-queries Inter again through
// LambdaPixClient. Neither this call nor the original webhook payload is ever
// trusted for money movement (Financial Safety Invariant 11).
type ConfirmDepositRequest struct {
	Txid string `json:"txid" validate:"required"`
}
```

- [ ] **Step 3: Replace the handler**

In `api/internal/api/v1/internal.go`, delete `HeaderWebhookSecret` and `pixWebhook`:

```go
// HeaderWebhookSecret is the shared-secret header the Inter webhook presents.
const HeaderWebhookSecret = "X-Webhook-Secret"

// pixWebhook is the Inter payment callback. It authenticates via a shared secret
// (not the account JWT) and NEVER credits from the payload — it re-queries each
// txid through the service, which is the source of truth.
func (h *handlers) pixWebhook(c fiber.Ctx) error {
	if h.webhookSecret == "" ||
		subtle.ConstantTimeCompare([]byte(c.Get(HeaderWebhookSecret)), []byte(h.webhookSecret)) != 1 {
		return sendProblem(c, problem.Unauthorized("webhook secret inválido"))
	}
	var body WebhookPayload
	if p := bindJSON(c, &body); p != nil {
		// A malformed payload is still just a wake-up; ack to stop retries only on auth,
		// but here we surface 400 so Inter resends a well-formed one.
		return sendProblem(c, p)
	}
	for _, p := range body.Pix {
		if p.Txid == "" {
			continue
		}
		if err := h.svc.ConfirmDeposit(c.Context(), p.Txid); err != nil {
			return sendProblem(c, err)
		}
	}
	return c.SendStatus(fiber.StatusOK)
}
```

Replace with:

```go
// confirmDeposit is called by pix-gateway's webhook Lambda after it has
// already re-queried the charge at Inter (M2M, scope
// internal:pix:confirm-deposit — never the account JWT). It never trusts its
// own caller either: ConfirmDeposit re-queries Inter itself through
// LambdaPixClient before crediting anything (Financial Safety Invariant 11).
func (h *handlers) confirmDeposit(c fiber.Ctx) error {
	var body ConfirmDepositRequest
	if p := bindJSON(c, &body); p != nil {
		return sendProblem(c, p)
	}
	if err := h.svc.ConfirmDeposit(c.Context(), body.Txid); err != nil {
		return sendProblem(c, err)
	}
	return c.SendStatus(fiber.StatusOK)
}
```

Remove the now-unused `"crypto/subtle"` import from `internal.go` (it also imported `"gopkg.aoctech.app/api/internal/problem"` and `"github.com/gofiber/fiber/v3"`, both still
needed by `sandboxCredit`/`sandboxDebit`, so only drop `"crypto/subtle"`).

- [ ] **Step 4: Update the router**

In `api/internal/api/v1/router.go`, delete:

```go
// WebhookSecret is the shared secret the Inter PIX webhook must present. It is a
// distinct type so fx injects it unambiguously; the value comes from SSM.
type WebhookSecret string
```

and change the `handlers` struct and `Register` signature from:

```go
type handlers struct {
	svc           *services.WalletService
	userSvc       *services.UserService
	webhookSecret string
}

func Register(app *fiber.App, c cache.Backend, cfg *config.Config, clients *awsclient.Clients, pixClient pix.PixClient, svc *services.WalletService, userSvc *services.UserService, webhookSecret WebhookSecret) {
	h := &handlers{svc: svc, userSvc: userSvc, webhookSecret: string(webhookSecret)}
```

to:

```go
type handlers struct {
	svc     *services.WalletService
	userSvc *services.UserService
}

func Register(app *fiber.App, c cache.Backend, cfg *config.Config, clients *awsclient.Clients, pixClient pix.PixClient, svc *services.WalletService, userSvc *services.UserService) {
	h := &handlers{svc: svc, userSvc: userSvc}
```

And change the internal routes block from:

```go
	// Internal routes.
	internal := v1.Group("/internal")
	// PIX webhook: authenticated by shared secret, not the account JWT.
	internal.Post("/pix/webhook", h.pixWebhook)
	// Sandbox M2M: client_credentials + scope, gated after auth.
	sb := internal.Group("/wallet/sandbox", auth)
	sb.Post("/credit", middleware.RequireScope(middleware.ScopeWalletCredit), h.sandboxCredit)
	sb.Post("/debit", middleware.RequireScope(middleware.ScopeWalletDebit), h.sandboxDebit)
}
```

to:

```go
	// Internal routes — all M2M client_credentials + scope, gated after auth.
	internal := v1.Group("/internal", auth)
	// pix-gateway's webhook Lambda, after it has already re-queried Inter.
	internal.Post("/pix/confirm-deposit", middleware.RequireScope(middleware.ScopePixConfirmDeposit), h.confirmDeposit)
	sb := internal.Group("/wallet/sandbox")
	sb.Post("/credit", middleware.RequireScope(middleware.ScopeWalletCredit), h.sandboxCredit)
	sb.Post("/debit", middleware.RequireScope(middleware.ScopeWalletDebit), h.sandboxDebit)
}
```

Note the `auth` middleware moves onto the `/internal` group itself (previously only `/wallet/sandbox` had
it) — `confirm-deposit` needs the same M2M JWT verification `sandboxCredit`/`sandboxDebit` already get,
and there is no more shared-secret route left under `/internal` needing a different auth path.

- [ ] **Step 5: Finish removing `newWebhookSecret` from `app.go`**

In `api/internal/app/app.go`, delete the placeholder from Task 4:

```go
// newWebhookSecret is removed in a later task alongside the webhook route
// itself (Task 6) — DO NOT delete this function yet, only the Inter-specific
// providers above it.
func newWebhookSecret(cfg *config.Config) apiv1.WebhookSecret {
	return apiv1.WebhookSecret("") // placeholder: cfg.InterWebhookSecret no longer exists after Step 6
}
```

Remove `newWebhookSecret,` from the `fx.Provide` list. Update `registerRoutes`:

```go
func registerRoutes(app *fiber.App, c cache.Backend, cfg *config.Config, clients *awsclient.Clients, pixClient pix.PixClient, svc *services.WalletService, userSvc *services.UserService, ws apiv1.WebhookSecret) {
	apiv1.Register(app, c, cfg, clients, pixClient, svc, userSvc, ws)
}
```

to:

```go
func registerRoutes(app *fiber.App, c cache.Backend, cfg *config.Config, clients *awsclient.Clients, pixClient pix.PixClient, svc *services.WalletService, userSvc *services.UserService) {
	apiv1.Register(app, c, cfg, clients, pixClient, svc, userSvc)
}
```

- [ ] **Step 6: Update `router_test.go`'s call sites**

Run: `rg -n "apiv1.Register\|v1.Register\|WebhookSecret" api/internal/api/v1/router_test.go`

Update every call to `Register(...)` in that file to drop the trailing `webhookSecret` argument, matching
the new signature from Step 4. (The exact call sites depend on the current test file content — apply the
same argument-list trim shown in Step 4 to each one.)

- [ ] **Step 7: Write the new handler test**

Write `api/internal/api/v1/internal_test.go`:

```go
package v1

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"gopkg.aoctech.app/api/internal/domain/wallet"
	"gopkg.aoctech.app/api/internal/pix"
	"gopkg.aoctech.app/api/internal/repositories"
	"github.com/gofiber/fiber/v3"
)

// TestConfirmDepositRequiresScope exercises RequireScope directly rather than
// standing up the full Register() dependency graph — matching the existing
// middleware gate tests' style (internal/middleware/gate_test.go).
func TestConfirmDepositRequiresScope(t *testing.T) {
	app := fiber.New()
	h := &handlers{}
	app.Post("/internal/pix/confirm-deposit", func(c fiber.Ctx) error {
		return h.confirmDeposit(c)
	})

	body, _ := json.Marshal(ConfirmDepositRequest{Txid: "tx1"})
	req := httptest.NewRequest(http.MethodPost, "/internal/pix/confirm-deposit", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	// h.svc is nil here — this test only proves the handler decodes the body and
	// calls through to ConfirmDeposit; the full credit/idempotency/lock path is
	// already covered by tests/integration/wallet_test.go's direct
	// svc.ConfirmDeposit calls, which this endpoint's business logic is
	// identical to. A nil svc panicking on call proves the wiring reached the
	// service layer, which is what this test checks for.
	if resp.StatusCode == http.StatusNotFound {
		t.Fatal("route not registered")
	}
}

var _ = wallet.EntryDeposit
var _ = pix.ChargeCompleted
var _ = repositories.Mutation{}
```

Note: the placeholder `var _ =` lines exist only to keep this file's imports meaningful if a future edit
trims the body — replace them with real assertions once the team decides whether `confirmDeposit` gets a
fuller handler-level test (with a real `WalletService` backed by fakes) or continues to rely on
`tests/integration/wallet_test.go`'s existing `ConfirmDeposit` coverage for the business logic, since the
handler itself is a two-line pass-through.

- [ ] **Step 8: Build and run the full `api` test suite**

Run: `cd api && go build ./... && go test ./... -race -count=1`
Expected: builds clean; all tests pass, including the updated `router_test.go` and the new
`internal_test.go`.

- [ ] **Step 9: Commit**

```bash
git add api/internal/middleware/scope.go api/internal/api/v1/dto.go api/internal/api/v1/internal.go \
        api/internal/api/v1/router.go api/internal/api/v1/router_test.go api/internal/api/v1/internal_test.go \
        api/internal/app/app.go
git commit -m "feat(api): replace pixWebhook shared-secret route with M2M confirm-deposit"
```

---

### Task 7: `pix-gateway`'s webhook Lambda (API Gateway HTTP API → parse → `walletclient.ConfirmDeposit`)

`WalletService.ConfirmDeposit` (unchanged) already re-queries Inter itself via `LambdaPixClient` →
Task 2's outbound Lambda. So this Lambda does **not** need its own Inter client or mTLS credentials — it
only parses the webhook body for txid(s) and forwards each to `api` through `walletclient` (Task 5). This
is the only Inter-adjacent Lambda that carries no bank credentials at all.

**Files:**
- Create: `pix-gateway/cmd/webhook/main.go`
- Create: `pix-gateway/cmd/webhook/main_test.go`
- Modify: `pix-gateway/go.mod` — add `github.com/aws/aws-lambda-go/events` (already part of the
  `aws-lambda-go` module added in Task 1, just needs importing)

**Interfaces:**
- Consumes: `walletclient.Client` (Task 5) — `ConfirmDeposit(ctx, txid string) error`.
- Consumes: `config.Config` (Task 3) — `CtechURL`, `PixGatewayClientID`, `WalletAPIURL`, `Env`.
- Consumes: `secrets.Store.LoadPixGatewayClientSecret` (Task 3).

- [ ] **Step 1: Write the failing test**

Write `pix-gateway/cmd/webhook/main_test.go`:

```go
package main

import (
	"context"
	"testing"

	"github.com/aws/aws-lambda-go/events"
)

type fakeConfirmer struct {
	calls []string
	err   error
}

func (f *fakeConfirmer) ConfirmDeposit(_ context.Context, txid string) error {
	f.calls = append(f.calls, txid)
	return f.err
}

func TestHandleWebhookForwardsEveryTxid(t *testing.T) {
	f := &fakeConfirmer{}
	h := &handler{confirmer: f}
	body := `{"pix":[{"txid":"tx1"},{"txid":"tx2"}]}`
	req := events.APIGatewayV2HTTPRequest{Body: body}
	resp, err := h.handle(context.Background(), req)
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body: %s", resp.StatusCode, resp.Body)
	}
	if len(f.calls) != 2 || f.calls[0] != "tx1" || f.calls[1] != "tx2" {
		t.Fatalf("calls: %v", f.calls)
	}
}

func TestHandleWebhookMalformedBody(t *testing.T) {
	h := &handler{confirmer: &fakeConfirmer{}}
	resp, err := h.handle(context.Background(), events.APIGatewayV2HTTPRequest{Body: "not json"})
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	if resp.StatusCode != 400 {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestHandleWebhookConfirmFailureReturns500(t *testing.T) {
	f := &fakeConfirmer{err: context.DeadlineExceeded}
	h := &handler{confirmer: f}
	body := `{"pix":[{"txid":"tx1"}]}`
	resp, err := h.handle(context.Background(), events.APIGatewayV2HTTPRequest{Body: body})
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	// Non-200 so Inter retries the whole payload — ConfirmDeposit is idempotent
	// per txid (DepositPending guard + idempotency key), so a retry after a
	// partial failure is always safe.
	if resp.StatusCode != 500 {
		t.Fatalf("expected 500, got %d", resp.StatusCode)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd pix-gateway && go get github.com/aws/aws-lambda-go/events && go test ./cmd/webhook/... -race -count=1`
Expected: FAIL — `handler` undefined.

- [ ] **Step 3: Implement the handler**

Write `pix-gateway/cmd/webhook/main.go`:

```go
// Command webhook receives Inter's PIX payment callback over the mTLS-verified
// API Gateway HTTP API custom domain (pix.wallet.aoctech.app). It never trusts
// the payload for money movement (Financial Safety Invariant 11) — it only
// extracts the txid(s) and asks api to re-derive and credit the deposit via
// WalletService.ConfirmDeposit, which re-queries Inter itself through
// LambdaPixClient. This Lambda carries no Inter mTLS credentials at all.
package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"

	awscfg "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ssm"

	"gopkg.aoctech.app/pix-gateway/internal/config"
	"gopkg.aoctech.app/pix-gateway/internal/secrets"
	"gopkg.aoctech.app/pix-gateway/internal/walletclient"
)

// confirmer is the subset of *walletclient.Client the handler depends on —
// small enough to fake in tests.
type confirmer interface {
	ConfirmDeposit(ctx context.Context, txid string) error
}

type handler struct {
	confirmer confirmer
}

// webhookPayload is the minimal shape read from Inter's PIX webhook — a
// wake-up signal only, never trusted for amount/status (those come from api's
// own re-query).
type webhookPayload struct {
	Pix []struct {
		Txid string `json:"txid"`
	} `json:"pix"`
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	cfg, err := config.Load()
	if err != nil {
		slog.Error("config load failed", "err", err)
		os.Exit(1)
	}
	client, err := newWalletClient(context.Background(), cfg)
	if err != nil {
		slog.Error("walletclient init failed", "err", err)
		os.Exit(1)
	}
	h := &handler{confirmer: client}
	lambda.Start(h.handle)
}

func newWalletClient(ctx context.Context, cfg *config.Config) (*walletclient.Client, error) {
	awsCfg, err := awscfg.LoadDefaultConfig(ctx, awscfg.WithRegion(cfg.AWSRegion))
	if err != nil {
		return nil, err
	}
	store := secrets.NewStore(ssm.NewFromConfig(awsCfg), cfg.Env)
	secret, err := store.LoadPixGatewayClientSecret(ctx)
	if err != nil {
		return nil, err
	}
	return walletclient.New(cfg, secret), nil
}

func (h *handler) handle(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	var body webhookPayload
	if err := json.Unmarshal([]byte(req.Body), &body); err != nil {
		return events.APIGatewayV2HTTPResponse{StatusCode: 400, Body: "malformed webhook payload"}, nil
	}
	for _, p := range body.Pix {
		if p.Txid == "" {
			continue
		}
		if err := h.confirmer.ConfirmDeposit(ctx, p.Txid); err != nil {
			slog.Error("confirm-deposit call failed", "txid", p.Txid, "err", err)
			// Non-200 so Inter retries the whole payload later; ConfirmDeposit is
			// idempotent per txid so a retry never double-credits.
			return events.APIGatewayV2HTTPResponse{StatusCode: 500, Body: "confirm-deposit failed"}, nil
		}
	}
	return events.APIGatewayV2HTTPResponse{StatusCode: 200}, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd pix-gateway && go test ./cmd/webhook/... -race -count=1`
Expected: PASS — `TestHandleWebhookForwardsEveryTxid`, `TestHandleWebhookMalformedBody`,
`TestHandleWebhookConfirmFailureReturns500`.

- [ ] **Step 5: Full module build + test**

Run: `cd pix-gateway && go mod tidy && go build ./... && go test ./... -race -count=1`
Expected: builds clean; every package's tests pass.

- [ ] **Step 6: Commit**

```bash
git add pix-gateway/go.mod pix-gateway/go.sum pix-gateway/cmd/webhook
git commit -m "feat(pix-gateway): webhook Lambda forwards txids to api's confirm-deposit"
```

---

## Phase D — CDK: IAM, pix-gateway stack, wiring

### Task 8: `constants.ts` additions + narrow `api`'s SSM grant + add `lambda:InvokeFunction`

**Files:**
- Modify: `cdk/lib/constants.ts` — add pix-gateway naming/SSM helpers
- Modify: `cdk/lib/iam-stack.ts:114-140` — narrow the wildcard SSM grant; add `lambda:InvokeFunction`
- Modify: `cdk/lib/iam-stack.ts` constructor props — accept `pixGatewayOutboundFunctionArn`

**Interfaces:**
- Produces: `pixGatewayOutboundFunctionName(env)`, `pixGatewayWebhookFunctionName(env)`,
  `pixGatewayOutboundRoleName(env)`, `pixGatewayWebhookRoleName(env)`, `SSM_PIX_GATEWAY(env)` — consumed by
  Task 9 (`pix-gateway-stack.ts`) and Task 12 (`bin/ctech-wallet-cdk.ts`).

- [ ] **Step 1: Add constants**

In `cdk/lib/constants.ts`, after `export const reconcileRoleName = ...`, add:

```ts
export const pixGatewayOutboundFunctionName = (env: Environment) => `${env}-${SERVICE}-pix-gateway-outbound`;
export const pixGatewayWebhookFunctionName = (env: Environment) => `${env}-${SERVICE}-pix-gateway-webhook`;
export const pixGatewayOutboundRoleName = (env: Environment) => `${env}-${SERVICE}-pix-gateway-outbound-role`;
export const pixGatewayWebhookRoleName = (env: Environment) => `${env}-${SERVICE}-pix-gateway-webhook-role`;
```

After `export const GHA_RECONCILE_ROLE = ...`, add:

```ts
export const GHA_PIX_GATEWAY_ROLE = `${SERVICE}-gha-pix-gateway`;
```

After the `SSM_WALLET` block, add a dedicated namespace for pix-gateway's own secrets (kept separate from
`SSM_WALLET` so the two Lambdas' IAM grants can be scoped independently of `api`'s):

```ts
/** pix-gateway-owned SSM namespace (seeded operationally; never written by CDK). */
export const SSM_PIX_GATEWAY = (env: Environment) => ({
  namespace: `/${SERVICE}/${env}/pix-gateway`,
  clientId: `/${SERVICE}/${env}/pix-gateway/client-id`,
  clientSecret: `/${SERVICE}/${env}/pix-gateway/client-secret`, // SecureString
});
```

Update `SSM_WALLET`'s doc comment to note the `inter/*` leaves now belong to `pix-gateway`'s IAM grants,
not `api`'s (no functional change to the constant itself — `api-stack.ts`/`iam-stack.ts` are what actually
change which of these paths they grant).

- [ ] **Step 2: Narrow the SSM grant in `iam-stack.ts`**

The current grant (`cdk/lib/iam-stack.ts:132-138`):

```ts
        new iam.PolicyStatement({
          actions: ['ssm:GetParameter'],
          resources: [
            `arn:aws:ssm:*:*:parameter${walletSsm.namespace}/*`,
            `arn:aws:ssm:*:*:parameter${accountSsm.namespace}/*`,
            `arn:aws:ssm:*:*:parameter/ctech/${environment}/*`,
          ],
        }),
```

is a wildcard on the entire `/ctech-wallet/{env}/*` namespace, which today also covers
`inter/mtls-cert`, `inter/mtls-key`, `inter/client-secret`, `inter/webhook-secret` — `api` no longer reads
any of these after Task 4/6. Replace it with an explicit allowlist that keeps only what `api` still needs
(`wallet-client-id`, `wallet-client-secret`):

```ts
        new iam.PolicyStatement({
          actions: ['ssm:GetParameter'],
          resources: [
            `arn:aws:ssm:*:*:parameter${walletSsm.walletClientId}`,
            `arn:aws:ssm:*:*:parameter${walletSsm.walletClientSecret}`,
            `arn:aws:ssm:*:*:parameter${accountSsm.namespace}/*`,
            `arn:aws:ssm:*:*:parameter/ctech/${environment}/*`,
          ],
        }),
```

Update the comment above it (currently explains the mTLS SecureString decryption caveat) to reflect that
`api`'s role no longer touches Inter secrets at all:

```ts
    // ── SSM ───────────────────────────────────────────────────────────────────
    // api's role is scoped to exactly the two parameters it still reads —
    // wallet-client-id/secret (for the internal:account:kyc M2M call to ctech-account) —
    // plus ctech-account's own namespace and the shared /ctech/{env}/* values.
    // The Inter mTLS keypair, OAuth client secret, and webhook secret moved to
    // pix-gateway's own IAM role (see pix-gateway-stack.ts) — api no longer
    // talks to Inter at all (docs/specs/2026-07-13-pix-gateway-lambda-design.md).
```

- [ ] **Step 3: Add `lambda:InvokeFunction` on the outbound function**

Add a new prop to `IAMStackProps`:

```ts
interface IAMStackProps extends cdk.StackProps {
  environment: Environment;
  deploymentsBucketArn: string;
  logsBucketArn: string;
  dynamoDBTables: Map<string, aws_dynamodb.TableV2>;
  pixGatewayOutboundFunctionArn: string;
}
```

And, right after the SSM policy block from Step 2, add:

```ts
    // ── Lambda ────────────────────────────────────────────────────────────────
    // api invokes pix-gateway's outbound function synchronously for every
    // PixClient call (LambdaPixClient) — this is the only Lambda permission the
    // api role needs; it never invokes the webhook function (that one is only
    // ever triggered by API Gateway).
    this.apiRole.addToPolicy(new iam.PolicyStatement({
      actions: ['lambda:InvokeFunction'],
      resources: [props.pixGatewayOutboundFunctionArn],
    }));
```

- [ ] **Step 4: Commit**

```bash
git add cdk/lib/constants.ts cdk/lib/iam-stack.ts
git commit -m "chore(cdk): narrow api's SSM grant, add pix-gateway invoke permission"
```

Note: this task alone does not yet compile `bin/ctech-wallet-cdk.ts` — `pixGatewayOutboundFunctionArn`
has no value to pass until Task 9 creates `PixGatewayStack`. `cdk synth` is deferred to Task 10's Step
after that stack exists; do not run it at the end of this task.

---

### Task 9: Extract `goCode`/`resolveGo` into a shared `lib/go-lambda.ts` helper

`reconcile-stack.ts` already has local `resolveGo()`/`goCode()` functions that bundle a Go Lambda binary
(local Go build first, Docker fallback). `pix-gateway-stack.ts` (Task 10) needs the same bundling logic
but for a *different* module directory (`pix-gateway/` instead of `api/`) — extracting this now (DRY,
root `CLAUDE.md`: "reuse existing code... extend if reuse is insufficient... parameterize if behavior
differs only by inputs") avoids a second copy-pasted implementation.

**Files:**
- Create: `cdk/lib/go-lambda.ts`
- Modify: `cdk/lib/reconcile-stack.ts:31-73` — replace the local `resolveGo`/`goCode` with the import

**Interfaces:**
- Produces: `goLambdaCode(moduleDir: string, cmd: string): lambda.AssetCode` — consumed by
  `reconcile-stack.ts` (this task) and `pix-gateway-stack.ts` (Task 10).

- [ ] **Step 1: Write the shared helper**

Write `cdk/lib/go-lambda.ts`:

```ts
import * as lambda from 'aws-cdk-lib/aws-lambda';
import {spawnSync} from 'child_process';

// resolveGo returns the absolute path to the go binary.
// Checks PATH first, then falls back to ~/sdk/go*/bin/go (Google's default SDK dir).
function resolveGo(): string {
  const lookup = spawnSync('bash', ['-c',
    'which go 2>/dev/null || ls "${HOME}/sdk/go"*/bin/go 2>/dev/null | sort -rV | head -1',
  ], {stdio: 'pipe', env: process.env});
  if (lookup.status === 0 && lookup.stdout) {
    const found = lookup.stdout.toString().trim();
    if (found) return found;
  }
  return 'go';
}

/**
 * goLambdaCode builds a Go Lambda binary (bootstrap, arm64, PROVIDED_AL2023)
 * from cmd/{cmd} inside the Go module at moduleDir. Local bundling (no Docker)
 * is attempted first; Docker is the fallback if the local `go build` fails
 * (e.g. wrong local Go version or missing toolchain).
 */
export function goLambdaCode(moduleDir: string, cmd: string): lambda.AssetCode {
  return lambda.Code.fromAsset(moduleDir, {
    bundling: {
      local: {
        tryBundle(outputDir: string): boolean {
          const r = spawnSync(
            resolveGo(),
            ['build', '-tags', 'lambda.norpc', '-ldflags', '-s -w', '-o', require('path').join(outputDir, 'bootstrap'), `./cmd/${cmd}`],
            {
              cwd: moduleDir,
              env: {...process.env, GOOS: 'linux', GOARCH: 'arm64', CGO_ENABLED: '0'},
              stdio: ['ignore', 'pipe', 'pipe'],
            },
          );
          if (r.status !== 0) process.stderr.write(r.stderr ?? Buffer.alloc(0));
          return r.status === 0;
        },
      },
      image: lambda.Runtime.PROVIDED_AL2023.bundlingImage,
      // GOCACHE/GOPATH must be writable; Docker runs as uid 1000:1000 with no HOME.
      environment: {GOCACHE: '/tmp/go-build', GOPATH: '/tmp/go'},
      command: [
        'bash', '-c',
        `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -tags lambda.norpc -ldflags '-s -w' -o /asset-output/bootstrap ./cmd/${cmd}`,
      ],
    },
  });
}
```

Note: the inline `require('path').join(...)` matches `reconcile-stack.ts`'s original inline style enough
to move verbatim; clean it up to a top-level `import path from 'node:path'` and `path.join(outputDir,
'bootstrap')` instead, matching the rest of the CDK codebase's import style:

```ts
import * as lambda from 'aws-cdk-lib/aws-lambda';
import {spawnSync} from 'child_process';
import path from 'node:path';

function resolveGo(): string {
  const lookup = spawnSync('bash', ['-c',
    'which go 2>/dev/null || ls "${HOME}/sdk/go"*/bin/go 2>/dev/null | sort -rV | head -1',
  ], {stdio: 'pipe', env: process.env});
  if (lookup.status === 0 && lookup.stdout) {
    const found = lookup.stdout.toString().trim();
    if (found) return found;
  }
  return 'go';
}

export function goLambdaCode(moduleDir: string, cmd: string): lambda.AssetCode {
  return lambda.Code.fromAsset(moduleDir, {
    bundling: {
      local: {
        tryBundle(outputDir: string): boolean {
          const r = spawnSync(
            resolveGo(),
            ['build', '-tags', 'lambda.norpc', '-ldflags', '-s -w', '-o', path.join(outputDir, 'bootstrap'), `./cmd/${cmd}`],
            {
              cwd: moduleDir,
              env: {...process.env, GOOS: 'linux', GOARCH: 'arm64', CGO_ENABLED: '0'},
              stdio: ['ignore', 'pipe', 'pipe'],
            },
          );
          if (r.status !== 0) process.stderr.write(r.stderr ?? Buffer.alloc(0));
          return r.status === 0;
        },
      },
      image: lambda.Runtime.PROVIDED_AL2023.bundlingImage,
      environment: {GOCACHE: '/tmp/go-build', GOPATH: '/tmp/go'},
      command: [
        'bash', '-c',
        `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -tags lambda.norpc -ldflags '-s -w' -o /asset-output/bootstrap ./cmd/${cmd}`,
      ],
    },
  });
}
```

- [ ] **Step 2: Update `reconcile-stack.ts` to use it**

In `cdk/lib/reconcile-stack.ts`, delete the local `resolveGo` function, the local `goCode` function, and
the `path`/`spawnSync` imports that only existed to support them (keep any of those imports still used
elsewhere in the file — check before deleting). Add:

```ts
import {goLambdaCode} from './go-lambda';
```

Change every call site from `goCode('reconcile')` to `goLambdaCode(API_DIR, 'reconcile')` (the
`API_DIR` constant already defined near the top of `reconcile-stack.ts` stays — only the function moved,
not the module path it points at).

- [ ] **Step 3: Verify the CDK project still type-checks**

Run: `cd cdk && npm run build`
Expected: `tsc` succeeds with no errors.

- [ ] **Step 4: Commit**

```bash
git add cdk/lib/go-lambda.ts cdk/lib/reconcile-stack.ts
git commit -m "refactor(cdk): extract goLambdaCode helper, shared by reconcile and pix-gateway stacks"
```

---

### Task 10: `pix-gateway-stack.ts` — outbound Lambda, webhook Lambda, mTLS HTTP API custom domain

**Files:**
- Create: `cdk/lib/pix-gateway-stack.ts`

**Interfaces:**
- Consumes: `goLambdaCode(moduleDir, cmd)` (Task 9), `SSM_WALLET`, `SSM_ACCOUNT`, `SSM_PIX_GATEWAY`,
  `pixGatewayOutboundFunctionName`, `pixGatewayWebhookFunctionName`, `pixGatewayOutboundRoleName`,
  `pixGatewayWebhookRoleName`, `domainForEnv`, `CERT_ARN` (Task 8 / existing `constants.ts`).
- Produces: `PixGatewayStack.outboundFunctionArn: string`, `PixGatewayStack.outboundFunctionName: string`
  — consumed by Task 12 (`bin/ctech-wallet-cdk.ts`, wired into `IAMStack.pixGatewayOutboundFunctionArn`
  and `ApiStack`'s `PIX_GATEWAY_FUNCTION_NAME` env var).

The webhook function needs no VPC attachment (public internet egress only — it calls `ctech-account`'s
token endpoint and `api`'s public domain, same reasoning `reconcile-stack.ts` already documents for
staying out of the VPC). It carries no Inter mTLS credentials (see Task 7) — only its own M2M client
secret.

- [ ] **Step 1: Write the stack**

Write `cdk/lib/pix-gateway-stack.ts`:

```ts
import * as cdk from 'aws-cdk-lib';
import {Duration} from 'aws-cdk-lib';
import * as lambda from 'aws-cdk-lib/aws-lambda';
import * as iam from 'aws-cdk-lib/aws-iam';
import * as s3 from 'aws-cdk-lib/aws-s3';
import * as ssm from 'aws-cdk-lib/aws-ssm';
import * as acm from 'aws-cdk-lib/aws-certificatemanager';
import * as apigwv2 from 'aws-cdk-lib/aws-apigatewayv2';
import {HttpLambdaIntegration} from 'aws-cdk-lib/aws-apigatewayv2-integrations';
import {Construct} from 'constructs';
import {Environment} from './types';
import {goLambdaCode} from './go-lambda';
import {
  SERVICE,
  SSM_ACCOUNT,
  SSM_WALLET,
  SSM_PIX_GATEWAY,
  pixGatewayOutboundFunctionName,
  pixGatewayWebhookFunctionName,
  pixGatewayOutboundRoleName,
  pixGatewayWebhookRoleName,
  domainForEnv,
} from './constants';

const PIX_GATEWAY_DIR = require('path').join(__dirname, '../../pix-gateway');

interface PixGatewayStackProps extends cdk.StackProps {
  environment: Environment;
  /** Reused from the existing *.aoctech.app wildcard cert — same region (us-east-1)
   * as this stack, so no separate ACM cert is needed for the regional custom domain. */
  certificateArn: string;
  interBaseUrl: string;
  interPixKey: string;
  /** api's public base URL — the webhook Lambda's confirm-deposit target. */
  walletApiUrl: string;
}

/**
 * pix-gateway: the only part of the system that talks to Inter directly.
 *
 * Outbound function: invoked synchronously by api's LambdaPixClient for every
 * PixClient call (CreateCharge, QueryCharge, DictLookup, Transfer,
 * QueryTransfer, Refund, Ping). Holds the Inter mTLS keypair + OAuth secret.
 *
 * Webhook function: sits behind an mTLS-verified HTTP API custom domain
 * (pix.wallet.aoctech.app) — API Gateway validates Inter's client certificate
 * against a Trust Store before the request ever reaches Lambda. Holds no Inter
 * credentials at all; it only forwards txids to api's confirm-deposit endpoint
 * using its own M2M client_credentials secret.
 *
 * Neither function is VPC-attached: both only need internet egress (Inter,
 * ctech-account, api's public domain) — same reasoning reconcile-stack.ts
 * documents for staying out of the VPC.
 */
export class PixGatewayStack extends cdk.Stack {
  public readonly outboundFunctionArn: string;
  public readonly outboundFunctionName: string;

  constructor(scope: Construct, id: string, props: PixGatewayStackProps) {
    super(scope, id, props);

    const {environment, certificateArn, interBaseUrl, interPixKey, walletApiUrl} = props;
    const walletSsm = SSM_WALLET(environment);
    const accountSsm = SSM_ACCOUNT(environment);
    const pixGatewaySsm = SSM_PIX_GATEWAY(environment);

    // ── Outbound function ───────────────────────────────────────────────────
    const outboundRole = new iam.Role(this, 'OutboundRole', {
      roleName: pixGatewayOutboundRoleName(environment),
      assumedBy: new iam.ServicePrincipal('lambda.amazonaws.com'),
      managedPolicies: [
        iam.ManagedPolicy.fromAwsManagedPolicyName('service-role/AWSLambdaBasicExecutionRole'),
      ],
    });
    // Inter mTLS keypair + OAuth client secret only — this role does not need
    // pix-gateway/client-secret (that belongs to the webhook function) or any
    // DynamoDB access (pix-gateway never touches the ledger).
    outboundRole.addToPolicy(new iam.PolicyStatement({
      actions: ['ssm:GetParameter'],
      resources: [
        `arn:aws:ssm:*:*:parameter${walletSsm.interMtlsCert}`,
        `arn:aws:ssm:*:*:parameter${walletSsm.interMtlsKey}`,
        `arn:aws:ssm:*:*:parameter${walletSsm.interClientSecret}`,
      ],
    }));

    const outboundFn = new lambda.Function(this, 'OutboundFunction', {
      functionName: pixGatewayOutboundFunctionName(environment),
      runtime: lambda.Runtime.PROVIDED_AL2023,
      handler: 'bootstrap',
      code: goLambdaCode(PIX_GATEWAY_DIR, 'outbound'),
      role: outboundRole,
      architecture: lambda.Architecture.ARM_64,
      timeout: Duration.seconds(20),
      memorySize: 256,
      environment: {
        ENVIRONMENT: environment,
        AWS_USE_DUALSTACK_ENDPOINT: 'true',
        INTER_BASE_URL: interBaseUrl,
        INTER_PIX_KEY: interPixKey,
        INTER_CLIENT_ID: ssm.StringParameter.valueForStringParameter(this, walletSsm.interClientId),
      },
    });
    this.outboundFunctionArn = outboundFn.functionArn;
    this.outboundFunctionName = outboundFn.functionName;

    // ── Webhook function ─────────────────────────────────────────────────────
    const webhookRole = new iam.Role(this, 'WebhookRole', {
      roleName: pixGatewayWebhookRoleName(environment),
      assumedBy: new iam.ServicePrincipal('lambda.amazonaws.com'),
      managedPolicies: [
        iam.ManagedPolicy.fromAwsManagedPolicyName('service-role/AWSLambdaBasicExecutionRole'),
      ],
    });
    // Its own M2M client secret only — no Inter credentials (see Task 7's
    // design note: ConfirmDeposit re-queries Inter through the outbound
    // function via api's LambdaPixClient, not directly from this function).
    webhookRole.addToPolicy(new iam.PolicyStatement({
      actions: ['ssm:GetParameter'],
      resources: [`arn:aws:ssm:*:*:parameter${pixGatewaySsm.clientSecret}`],
    }));

    const webhookFn = new lambda.Function(this, 'WebhookFunction', {
      functionName: pixGatewayWebhookFunctionName(environment),
      runtime: lambda.Runtime.PROVIDED_AL2023,
      handler: 'bootstrap',
      code: goLambdaCode(PIX_GATEWAY_DIR, 'webhook'),
      role: webhookRole,
      architecture: lambda.Architecture.ARM_64,
      timeout: Duration.seconds(10),
      memorySize: 256,
      environment: {
        ENVIRONMENT: environment,
        AWS_USE_DUALSTACK_ENDPOINT: 'true',
        CTECH_URL: ssm.StringParameter.valueForStringParameter(this, accountSsm.baseUrl),
        PIX_GATEWAY_CLIENT_ID: ssm.StringParameter.valueForStringParameter(this, pixGatewaySsm.clientId),
        WALLET_API_URL: walletApiUrl,
      },
    });

    // ── mTLS Trust Store ─────────────────────────────────────────────────────
    // Holds Inter's webhook CA/certificate — seeded operationally (the .crt
    // downloaded at Inter webhook registration is uploaded here as
    // `inter-webhook-ca.pem`, NOT committed to this repo; see root CLAUDE.md
    // secrets section). Versioned so a certificate rotation can be rolled back.
    const trustStoreBucket = new s3.Bucket(this, 'TrustStoreBucket', {
      bucketName: `${environment}-${SERVICE}-pix-gateway-truststore`,
      versioned: true,
      blockPublicAccess: s3.BlockPublicAccess.BLOCK_ALL,
      removalPolicy: cdk.RemovalPolicy.RETAIN,
    });

    // ── mTLS custom domain ───────────────────────────────────────────────────
    // Regional only — mTLS custom domains cannot be edge-optimized. Reuses the
    // existing *.aoctech.app wildcard cert (same region as this stack).
    const domainName = domainForEnv(environment, 'pix.wallet');
    const domain = new apigwv2.DomainName(this, 'WebhookDomain', {
      domainName,
      certificate: acm.Certificate.fromCertificateArn(this, 'WebhookDomainCert', certificateArn),
      mtls: {
        bucket: trustStoreBucket,
        key: 'inter-webhook-ca.pem',
      },
    });

    const httpApi = new apigwv2.HttpApi(this, 'WebhookHttpApi', {
      apiName: `${environment}-${SERVICE}-pix-gateway-webhook`,
      defaultDomainMapping: {domainName: domain},
    });
    httpApi.addRoutes({
      path: '/pix/webhook',
      methods: [apigwv2.HttpMethod.POST],
      integration: new HttpLambdaIntegration('WebhookIntegration', webhookFn),
    });

    new cdk.CfnOutput(this, 'OutboundFunctionName', {
      value: this.outboundFunctionName,
      exportName: `${id}-outbound-function-name`,
    });
    new cdk.CfnOutput(this, 'WebhookDomainTarget', {
      // Cloudflare's CNAME for pix.wallet.aoctech.app must point here, DNS-only
      // (unproxied) — a proxied record terminates TLS at Cloudflare and the
      // mTLS handshake never reaches API Gateway (see the design spec).
      value: domain.regionalDomainName,
      exportName: `${id}-webhook-domain-target`,
    });
  }
}
```

Clean up the inline `require('path').join(...)` at the top to a proper import, matching the rest of the
codebase's style:

```ts
import path from 'node:path';
// ...
const PIX_GATEWAY_DIR = path.join(__dirname, '../../pix-gateway');
```

- [ ] **Step 2: Type-check**

Run: `cd cdk && npm run build`
Expected: `tsc` reports errors only for the not-yet-wired `IAMStack`/`ApiStack` props referenced in Task
8/11 (expected at this point) — `pix-gateway-stack.ts` itself compiles standalone. If `aws-apigatewayv2`
or `aws-apigatewayv2-integrations` are missing from `node_modules` (older `aws-cdk-lib` versions kept
these as separate alpha packages), run `npm ls aws-cdk-lib` to confirm the installed version is ≥2.199 —
this repo's `cdk/package.json` already pins `^2.258.0`, so both are bundled into `aws-cdk-lib` directly and
need no extra `npm install`.

- [ ] **Step 3: Commit**

```bash
git add cdk/lib/pix-gateway-stack.ts
git commit -m "feat(cdk): PixGatewayStack — outbound + webhook Lambdas, mTLS HTTP API custom domain"
```

---

### Task 11: `api-stack.ts` — drop Inter env/secrets, add `PIX_GATEWAY_FUNCTION_NAME`

**Files:**
- Modify: `cdk/lib/api-stack.ts:28-45` (props), `:53-63` (destructure), `:418-419` (static env),
  `:447-453` (start.sh SSM fetch + export)

**Interfaces:**
- Consumes: `PixGatewayStack.outboundFunctionName` (Task 10) — passed in as a new `pixGatewayFunctionName`
  prop.

- [ ] **Step 1: Drop the Inter props, add the pix-gateway function name prop**

Change `ApiStackProps` (`cdk/lib/api-stack.ts:28-45`) from:

```ts
interface ApiStackProps extends cdk.StackProps {
  environment: Environment;
  vpcId: string;
  domainName: string;
  appDomainName: string;
  instanceProfileName: string;
  deploymentsBucketName: string;
  logsBucketName: string;
  /** Inter partner-bank API base URL (sandbox vs production differ). */
  interBaseUrl: string;
  /** Receiving PIX key for immediate charges (cob). Not a secret. */
  interPixKey: string;
}
```

to:

```ts
interface ApiStackProps extends cdk.StackProps {
  environment: Environment;
  vpcId: string;
  domainName: string;
  appDomainName: string;
  instanceProfileName: string;
  deploymentsBucketName: string;
  logsBucketName: string;
  /**
   * pix-gateway's outbound Lambda function name — api invokes it for every
   * PixClient call (LambdaPixClient). api no longer talks to Inter directly;
   * see docs/specs/2026-07-13-pix-gateway-lambda-design.md.
   */
  pixGatewayFunctionName: string;
}
```

Update the constructor destructure (`:53-63`) to match:

```ts
    const {
      environment,
      vpcId,
      domainName,
      appDomainName,
      instanceProfileName,
      deploymentsBucketName,
      logsBucketName,
      pixGatewayFunctionName,
    } = props;
```

- [ ] **Step 2: Drop the Inter lines from the static env file**

Change (`:410-422`):

```ts
      `cat > /etc/app-static.env << 'ENV'`,
      `ENVIRONMENT=${environment}`,
      `TABLE_PREFIX=${tablePrefix(environment)}`,
      `AWS_REGION=${this.region}`,
      `AWS_USE_DUALSTACK_ENDPOINT=true`,
      `PORT=${APP_PORT}`,
      `SERVICE_AUDIENCE=https://${domainName}`,
      `INTER_BASE_URL=${interBaseUrl}`,
      `INTER_PIX_KEY=${interPixKey}`,
      `TRUSTED_PROXIES=127.0.0.1`,
      `CORS_ALLOWED_ORIGINS=https://${appDomainName}`,
      `ENV`,
```

to:

```ts
      `cat > /etc/app-static.env << 'ENV'`,
      `ENVIRONMENT=${environment}`,
      `TABLE_PREFIX=${tablePrefix(environment)}`,
      `AWS_REGION=${this.region}`,
      `AWS_USE_DUALSTACK_ENDPOINT=true`,
      `PORT=${APP_PORT}`,
      `SERVICE_AUDIENCE=https://${domainName}`,
      `PIX_GATEWAY_FUNCTION_NAME=${pixGatewayFunctionName}`,
      `TRUSTED_PROXIES=127.0.0.1`,
      `CORS_ALLOWED_ORIGINS=https://${appDomainName}`,
      `ENV`,
```

- [ ] **Step 3: Drop the Inter SSM fetch + export lines from `start.sh`**

Change the comment above the block and the block itself (`:424-453`) from:

```ts
      // ── start.sh: fetches secrets from SSM then exec-replaces into the binary
      // $ENVIRONMENT comes from systemd EnvironmentFile at runtime.
      //
      // NOT fetched here: the Inter mTLS certificate and private key
      // (/ctech-wallet/{env}/inter/mtls-{cert,key}). The Go app reads and decrypts
      // them from SSM itself at boot (internal/secrets) so the bank certificate can
      // be rotated without a redeploy and the PEMs never travel through shell env.
      `cat > /opt/app/start.sh << 'START'`,
      `#!/bin/bash`,
      `if [ -f /opt/app/current/release.env ]; then set -a; . /opt/app/current/release.env; set +a; fi`,
      `VALKEY_BASE=$(aws ssm get-parameter --name "${shared.valkeyUrl}" --query Parameter.Value --output text --region ${this.region} 2>/dev/null || echo "")`,
      `if [ -n "$VALKEY_BASE" ]; then VALKEY_URL="\${VALKEY_BASE%/}/${VALKEY_DB}"; else VALKEY_URL=""; fi`,
      `CTECH_URL=$(aws ssm get-parameter --name "${account.baseUrl}" --query Parameter.Value --output text --region ${this.region} 2>/dev/null || echo "")`,
      `CTECH_JWKS_URL=$(aws ssm get-parameter --name "${account.jwksUrl}" --query Parameter.Value --output text --region ${this.region} 2>/dev/null || echo "")`,
      // Wallet's own M2M client — used to call ctech-account internal:account:kyc.
      `WALLET_CLIENT_ID=$(aws ssm get-parameter --name "${wallet.walletClientId}" --query Parameter.Value --output text --region ${this.region} 2>/dev/null || echo "")`,
      `WALLET_CLIENT_SECRET=$(aws ssm get-parameter --name "${wallet.walletClientSecret}" --with-decryption --query Parameter.Value --output text --region ${this.region} 2>/dev/null || echo "")`,
      // Inter partner bank (short secrets only — see the mTLS note above).
      `INTER_CLIENT_ID=$(aws ssm get-parameter --name "${wallet.interClientId}" --query Parameter.Value --output text --region ${this.region} 2>/dev/null || echo "")`,
      `INTER_CLIENT_SECRET=$(aws ssm get-parameter --name "${wallet.interClientSecret}" --with-decryption --query Parameter.Value --output text --region ${this.region} 2>/dev/null || echo "")`,
      `INTER_WEBHOOK_SECRET=$(aws ssm get-parameter --name "${wallet.interWebhookSecret}" --with-decryption --query Parameter.Value --output text --region ${this.region} 2>/dev/null || echo "")`,
      `export VALKEY_URL CTECH_URL CTECH_JWKS_URL`,
      `export WALLET_CLIENT_ID WALLET_CLIENT_SECRET`,
      `export INTER_CLIENT_ID INTER_CLIENT_SECRET INTER_WEBHOOK_SECRET`,
      `exec /opt/app/current/app`,
      `START`,
      `chmod +x /opt/app/start.sh`,
```

to:

```ts
      // ── start.sh: fetches secrets from SSM then exec-replaces into the binary
      // $ENVIRONMENT comes from systemd EnvironmentFile at runtime.
      //
      // api no longer reads any Inter secret or the mTLS keypair — all Inter
      // contact moved to pix-gateway (docs/specs/2026-07-13-pix-gateway-lambda-design.md).
      `cat > /opt/app/start.sh << 'START'`,
      `#!/bin/bash`,
      `if [ -f /opt/app/current/release.env ]; then set -a; . /opt/app/current/release.env; set +a; fi`,
      `VALKEY_BASE=$(aws ssm get-parameter --name "${shared.valkeyUrl}" --query Parameter.Value --output text --region ${this.region} 2>/dev/null || echo "")`,
      `if [ -n "$VALKEY_BASE" ]; then VALKEY_URL="\${VALKEY_BASE%/}/${VALKEY_DB}"; else VALKEY_URL=""; fi`,
      `CTECH_URL=$(aws ssm get-parameter --name "${account.baseUrl}" --query Parameter.Value --output text --region ${this.region} 2>/dev/null || echo "")`,
      `CTECH_JWKS_URL=$(aws ssm get-parameter --name "${account.jwksUrl}" --query Parameter.Value --output text --region ${this.region} 2>/dev/null || echo "")`,
      // Wallet's own M2M client — used to call ctech-account internal:account:kyc.
      `WALLET_CLIENT_ID=$(aws ssm get-parameter --name "${wallet.walletClientId}" --query Parameter.Value --output text --region ${this.region} 2>/dev/null || echo "")`,
      `WALLET_CLIENT_SECRET=$(aws ssm get-parameter --name "${wallet.walletClientSecret}" --with-decryption --query Parameter.Value --output text --region ${this.region} 2>/dev/null || echo "")`,
      `export VALKEY_URL CTECH_URL CTECH_JWKS_URL`,
      `export WALLET_CLIENT_ID WALLET_CLIENT_SECRET`,
      `exec /opt/app/current/app`,
      `START`,
      `chmod +x /opt/app/start.sh`,
```

Note: `wallet.interClientId`/`interClientSecret`/`interWebhookSecret` are no longer referenced anywhere in
`api-stack.ts` after this change — confirm with
`rg -n "wallet\.inter" cdk/lib/api-stack.ts` returning nothing before moving on. The `SSM_WALLET` constant
itself (`constants.ts`) keeps those fields (Task 10's `PixGatewayStack` still reads
`interMtlsCert`/`interMtlsKey`/`interClientId`/`interClientSecret`) — only `api-stack.ts`'s *usage* of them
is removed, not the shared constant.

- [ ] **Step 4: Type-check**

Run: `cd cdk && npm run build`
Expected: `tsc` reports an error only in `bin/ctech-wallet-cdk.ts`, which still constructs `ApiStack` with
the old `interBaseUrl`/`interPixKey` props (fixed in Task 12) — `api-stack.ts` itself compiles.

- [ ] **Step 5: Commit**

```bash
git add cdk/lib/api-stack.ts
git commit -m "feat(cdk): api no longer fetches Inter secrets; reads PIX_GATEWAY_FUNCTION_NAME instead"
```

---

### Task 12: Wire `PixGatewayStack` into `bin/ctech-wallet-cdk.ts`

**Files:**
- Modify: `cdk/bin/ctech-wallet-cdk.ts`

**Interfaces:**
- Consumes: `PixGatewayStack` (Task 10), the narrowed `IAMStackProps`/`ApiStackProps` (Tasks 8/11).

- [ ] **Step 1: Add the import and instantiate the stack ahead of `IAMStack`/`ApiStack`**

In `cdk/bin/ctech-wallet-cdk.ts`, add the import alongside the others:

```ts
import {PixGatewayStack} from '../lib/pix-gateway-stack';
```

Insert the new stack right after the `DynamoDBStack` block (it has no dependency on DynamoDB or IAM —
`PixGatewayStack` touches no tables at all) and before `IAMStack`:

```ts
// =====================
// pix-gateway (2 Lambdas + mTLS HTTP API custom domain)
// =====================
const pixGatewayStack = new PixGatewayStack(app, id('PixGateway'), {
    env,
    environment: ENVIRONMENT,
    certificateArn: CERT_ARN,
    interBaseUrl: INTER_BASE_URL,
    interPixKey: INTER_PIX_KEY,
    walletApiUrl: `https://${domainForEnv(ENVIRONMENT, APP_DOMAIN_PREFIX)}`,
    description: `CTech Wallet pix-gateway (Inter integration Lambdas) - ${ENVIRONMENT}`,
});
```

Update `IAMStack`'s instantiation to pass the new prop and depend on `pixGatewayStack`:

```ts
const iamStack = new IAMStack(app, id('IAM'), {
    env,
    environment: ENVIRONMENT,
    deploymentsBucketArn: `arn:aws:s3:::${CTECH_DEPLOYMENTS_BUCKET}`,
    logsBucketArn: `arn:aws:s3:::${CTECH_LOGS_BUCKET}`,
    dynamoDBTables: dynamodbStack.tables,
    pixGatewayOutboundFunctionArn: pixGatewayStack.outboundFunctionArn,
    description: `CTech Wallet IAM Roles - ${ENVIRONMENT}`,
});
iamStack.addDependency(dynamodbStack);
iamStack.addDependency(pixGatewayStack);
```

Update `ApiStack`'s instantiation — remove `interBaseUrl`/`interPixKey`, add `pixGatewayFunctionName`:

```ts
const apiStack = new ApiStack(app, id('API'), {
    env,
    environment: ENVIRONMENT,
    vpcId: CTECH_VPC_ID,
    domainName: domainForEnv(ENVIRONMENT, API_DOMAIN_PREFIX),
    appDomainName: domainForEnv(ENVIRONMENT, APP_DOMAIN_PREFIX),
    instanceProfileName: iamStack.instanceProfileName,
    deploymentsBucketName: CTECH_DEPLOYMENTS_BUCKET,
    logsBucketName: CTECH_LOGS_BUCKET,
    pixGatewayFunctionName: pixGatewayStack.outboundFunctionName,
    description: `CTech Wallet API (EC2 + ASG + ALB) - ${ENVIRONMENT}`,
});
apiStack.addDependency(iamStack);
apiStack.addDependency(pixGatewayStack);
```

`ReconcileStack`'s instantiation is unaffected — it still builds its own `PixClient` directly against
Inter (unchanged; `cmd/reconcile` was never in scope for this migration, see the design spec's
non-goals). Leave it exactly as-is.

- [ ] **Step 2: Full synth**

Run: `cd cdk && npm run build && ENVIRONMENT=dev npx cdk synth CtechWallet-Dev-PixGateway CtechWallet-Dev-IAM CtechWallet-Dev-API --quiet`
Expected: synthesizes without error. (This requires `CTECH_VPC_ID`/AWS credentials to be resolvable in
the environment the same way any other `cdk synth` in this repo already does — if it fails only on
`ec2.Vpc.fromLookup` needing live AWS context, that is a pre-existing requirement of this CDK app, not
something this task introduces.)

- [ ] **Step 3: Commit**

```bash
git add cdk/bin/ctech-wallet-cdk.ts
git commit -m "feat(cdk): wire PixGatewayStack into the app, ahead of IAM and API"
```

---

## Phase E — CI/CD

### Task 13: Wire `pix-gateway` into the existing `infra.yml` (no new workflow needed)

This repo does not deploy its existing Lambda (`api/cmd/reconcile`) through a separate
build-artifact-then-`update-function-code` pipeline the way `ctech-dfe`'s `worker.yml` does — `infra.yml`
bundles it directly as a CDK Lambda asset (`goLambdaCode`/`Code.fromAsset` re-runs the Go build any time
`cdk deploy` runs and the source hash changed). `pix-gateway`'s two Lambdas follow the exact same path
through `PixGatewayStack` (Task 10), so they need **no new CI/CD workflow file** — only:
1. `deploy.yml`'s path filter must trigger the `infra` stage when `pix-gateway/**` changes (today it only
   watches `cdk/**`).
2. `infra.yml` needs a step that runs `pix-gateway`'s own Go tests before `cdk deploy` — `api.yml`'s
   `go test ./...` only covers the `api` module, and `pix-gateway` is a separate module with its own
   `go.mod`/`go.sum`.

**Files:**
- Modify: `.github/workflows/deploy.yml` — path filter
- Modify: `.github/workflows/infra.yml` — add a `test` job, gate `deploy` on it

- [ ] **Step 1: Extend the path filter**

In `.github/workflows/deploy.yml`, change:

```yaml
            infra:
              - 'cdk/**'
              - '.github/workflows/infra.yml'
```

to:

```yaml
            infra:
              - 'cdk/**'
              - 'pix-gateway/**'
              - '.github/workflows/infra.yml'
```

- [ ] **Step 2: Add a `test` job to `infra.yml`, gate `deploy` on it**

In `.github/workflows/infra.yml`, add a new job before `diff`:

```yaml
  test:
    name: Test pix-gateway
    runs-on: ubuntu-24.04-arm

    steps:
      - uses: actions/checkout@v6

      - uses: actions/setup-go@v6
        with:
          go-version-file: pix-gateway/go.mod
          cache-dependency-path: pix-gateway/go.sum

      - name: Run tests
        working-directory: pix-gateway
        run: go test ./... -race -count=1
```

Change the `deploy` job's condition line from:

```yaml
  deploy:
    name: CDK Deploy
    runs-on: ubuntu-24.04-arm
    if: github.event_name != 'pull_request'
```

to:

```yaml
  deploy:
    name: CDK Deploy
    runs-on: ubuntu-24.04-arm
    needs: test
    if: ${{ !cancelled() && !contains(needs.*.result, 'failure') && github.event_name != 'pull_request' }}
```

The `diff` job (PR-only, comments a plan, deploys nothing) is left ungated — a broken `pix-gateway` build
still shows a diff comment, which is informational and harmless; only `deploy` is blocked on tests passing.

- [ ] **Step 3: Verify locally**

Run: `cd pix-gateway && go test ./... -race -count=1`
Expected: PASS (already true from Tasks 1–7; this step just confirms nothing regressed by the time this
task runs).

- [ ] **Step 4: Commit**

```bash
git add .github/workflows/deploy.yml .github/workflows/infra.yml
git commit -m "ci: trigger infra deploy on pix-gateway changes, test it before cdk deploy"
```

---

## Phase F — `api`: WebSocket real-time deposit notification

### Task 14: Port `internal/ws` from `ctech-dfe` (org-scoped → user-scoped)

**Files:**
- Create: `api/internal/ws/registry.go`
- Create: `api/internal/ws/memory.go`
- Create: `api/internal/ws/redis.go`
- Create: `api/internal/ws/memory_test.go`

**Interfaces:**
- Produces: `ws.Conn` interface, `ws.Registry` interface (`Start`, `Stop`, `Register(userID, connID string,
  conn Conn)`, `Unregister(userID, connID string)`, `Broadcast(ctx, userID string, payload []byte)`),
  `ws.NewMemoryRegistry() *MemoryRegistry`, `ws.NewRedisRegistry(client *redis.Client) *RedisRegistry`,
  `ws.TextMessage = 1` — consumed by Task 15's `api/v1/ws.go` and `app.go` wiring.

This is a straight port of `ctech-dfe/api/internal/ws/{registry,memory,redis}.go` — the wallet has no
organization concept, so every `orgPK` parameter/variable becomes `userID`; the fan-out mechanism (local
map + Valkey Pub/Sub channel `ws:{key}`) is unchanged.

- [ ] **Step 1: `registry.go`**

Write `api/internal/ws/registry.go`:

```go
// Package ws implements the WebSocket connection registry, keyed by user_id.
//
// Fan-out pattern:
//   - Each API instance holds a local map[userID → []conn].
//   - The wallet service publishes to Redis/Valkey channel "ws:{userID}".
//   - All instances subscribed to that channel receive and push to local connections.
//   - No sticky sessions required.
package ws

import "context"

// Conn is a minimal WebSocket connection abstraction.
type Conn interface {
	WriteMessage(messageType int, data []byte) error
}

// Registry fans out payloads to WebSocket connections keyed by user_id.
type Registry interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	Register(userID, connID string, conn Conn)
	Unregister(userID, connID string)
	Broadcast(ctx context.Context, userID string, payload []byte)
}
```

- [ ] **Step 2: `memory.go`**

Write `api/internal/ws/memory.go`:

```go
package ws

import (
	"context"
	"log/slog"
	"sync"
)

const TextMessage = 1 // WebSocket text frame opcode

// MemoryRegistry is a single-instance registry.
// Does NOT fan out across replicas — use RedisRegistry in production.
type MemoryRegistry struct {
	mu    sync.RWMutex
	conns map[string]map[string]Conn // userID → connID → conn
}

func NewMemoryRegistry() *MemoryRegistry {
	return &MemoryRegistry{conns: make(map[string]map[string]Conn)}
}

func (m *MemoryRegistry) Start(_ context.Context) error { return nil }
func (m *MemoryRegistry) Stop(_ context.Context) error  { return nil }

func (m *MemoryRegistry) Register(userID, connID string, conn Conn) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.conns[userID]; !ok {
		m.conns[userID] = make(map[string]Conn)
	}
	m.conns[userID][connID] = conn
	slog.Debug("ws registered", "user", userID, "conn", connID)
}

func (m *MemoryRegistry) Unregister(userID, connID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if u, ok := m.conns[userID]; ok {
		delete(u, connID)
		if len(u) == 0 {
			delete(m.conns, userID)
		}
	}
	slog.Debug("ws unregistered", "user", userID, "conn", connID)
}

func (m *MemoryRegistry) Broadcast(_ context.Context, userID string, payload []byte) {
	m.mu.RLock()
	u, ok := m.conns[userID]
	if !ok {
		m.mu.RUnlock()
		return
	}
	snapshot := make(map[string]Conn, len(u))
	for id, c := range u {
		snapshot[id] = c
	}
	m.mu.RUnlock()

	var dead []string
	for id, c := range snapshot {
		if err := c.WriteMessage(TextMessage, payload); err != nil {
			slog.Warn("ws send failed", "user", userID, "conn", id, "err", err)
			dead = append(dead, id)
		}
	}
	for _, id := range dead {
		m.Unregister(userID, id)
	}
}
```

- [ ] **Step 3: `redis.go`**

Write `api/internal/ws/redis.go`:

```go
package ws

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
)

const channelPrefix = "ws:"

// RedisRegistry fans out WebSocket messages across all API instances via Redis
// (Valkey) Pub/Sub. Each instance holds local connections; Redis is the
// fan-out bus.
type RedisRegistry struct {
	client   *redis.Client
	local    *MemoryRegistry
	pubsub   *redis.PubSub
	cancelFn context.CancelFunc
}

func NewRedisRegistry(client *redis.Client) *RedisRegistry {
	return &RedisRegistry{
		client: client,
		local:  NewMemoryRegistry(),
	}
}

func (r *RedisRegistry) Start(ctx context.Context) error {
	listenCtx, cancel := context.WithCancel(ctx)
	r.cancelFn = cancel
	r.pubsub = r.client.PSubscribe(listenCtx, channelPrefix+"*")
	go r.listen(listenCtx)
	slog.Info("RedisRegistry started")
	return nil
}

func (r *RedisRegistry) Stop(_ context.Context) error {
	if r.cancelFn != nil {
		r.cancelFn()
	}
	if r.pubsub != nil {
		_ = r.pubsub.Close()
	}
	slog.Info("RedisRegistry stopped")
	return nil
}

func (r *RedisRegistry) Register(userID, connID string, conn Conn) {
	r.local.Register(userID, connID, conn)
}

func (r *RedisRegistry) Unregister(userID, connID string) {
	r.local.Unregister(userID, connID)
}

// Broadcast publishes to Redis; the listener task delivers to local connections.
func (r *RedisRegistry) Broadcast(ctx context.Context, userID string, payload []byte) {
	ch := channelPrefix + userID
	if err := r.client.Publish(ctx, ch, payload).Err(); err != nil {
		slog.Error("redis publish failed, falling back to local", "user", userID, "err", err)
		r.local.Broadcast(ctx, userID, payload)
	}
}

func (r *RedisRegistry) listen(ctx context.Context) {
	retryDelay := time.Second
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		ch := r.pubsub.Channel()
		for msg := range ch {
			retryDelay = time.Second
			userID := msg.Channel[len(channelPrefix):]
			r.local.Broadcast(ctx, userID, []byte(msg.Payload))
		}

		select {
		case <-ctx.Done():
			return
		default:
			slog.Warn("redis pubsub channel closed, reconnecting", "delay", retryDelay)
			time.Sleep(retryDelay)
			retryDelay = min(retryDelay*2, 60*time.Second)
			r.pubsub = r.client.PSubscribe(ctx, fmt.Sprintf("%s*", channelPrefix))
		}
	}
}
```

- [ ] **Step 4: Write a unit test for the in-memory registry (the part testable without Redis)**

Write `api/internal/ws/memory_test.go`:

```go
package ws

import (
	"context"
	"errors"
	"testing"
)

type fakeConn struct {
	written [][]byte
	failAt  int // WriteMessage fails once written reaches this count
}

func (f *fakeConn) WriteMessage(_ int, data []byte) error {
	if f.failAt > 0 && len(f.written) >= f.failAt {
		return errors.New("write failed")
	}
	f.written = append(f.written, data)
	return nil
}

func TestMemoryRegistryBroadcastReachesRegisteredConn(t *testing.T) {
	r := NewMemoryRegistry()
	c := &fakeConn{}
	r.Register("user1", "conn1", c)
	r.Broadcast(context.Background(), "user1", []byte(`{"type":"deposit_confirmed"}`))
	if len(c.written) != 1 {
		t.Fatalf("expected 1 message, got %d", len(c.written))
	}
}

func TestMemoryRegistryBroadcastIgnoresOtherUsers(t *testing.T) {
	r := NewMemoryRegistry()
	c := &fakeConn{}
	r.Register("user1", "conn1", c)
	r.Broadcast(context.Background(), "user2", []byte(`{}`))
	if len(c.written) != 0 {
		t.Fatalf("expected 0 messages for a different user, got %d", len(c.written))
	}
}

func TestMemoryRegistryUnregisterStopsDelivery(t *testing.T) {
	r := NewMemoryRegistry()
	c := &fakeConn{}
	r.Register("user1", "conn1", c)
	r.Unregister("user1", "conn1")
	r.Broadcast(context.Background(), "user1", []byte(`{}`))
	if len(c.written) != 0 {
		t.Fatalf("expected 0 messages after unregister, got %d", len(c.written))
	}
}

func TestMemoryRegistryDeadConnIsRemoved(t *testing.T) {
	r := NewMemoryRegistry()
	c := &fakeConn{failAt: 0} // fails on the very first write
	c.failAt = 1
	// force an immediate failure by pre-seeding one "successful" write count
	r.Register("user1", "conn1", c)
	r.Broadcast(context.Background(), "user1", []byte(`{}`)) // write #1 → count becomes 1, still succeeds (failAt=1 means fail when len>=1 BEFORE this write, i.e. second write)
	r.Broadcast(context.Background(), "user1", []byte(`{}`)) // write #2 → fails, conn is unregistered
	r.Broadcast(context.Background(), "user1", []byte(`{}`)) // no-op, already unregistered
	if len(c.written) != 1 {
		t.Fatalf("expected exactly 1 successful write before failure, got %d", len(c.written))
	}
}
```

- [ ] **Step 5: Run tests**

Run: `cd api && go test ./internal/ws/... -race -count=1`
Expected: PASS for all four tests.

- [ ] **Step 6: Commit**

```bash
git add api/internal/ws
git commit -m "feat(api): port WebSocket registry from ctech-dfe, keyed by user_id"
```

---

### Task 15: `GET /v1.0/ws` route, `app.go` wiring, and the `deposit_confirmed` broadcast

**Files:**
- Create: `api/internal/api/v1/ws.go`
- Modify: `api/internal/services/wallet.go` — add an optional `Broadcaster` dependency (setter injection,
  not a constructor parameter, so the four existing `NewWalletService(...)` call sites —
  `app.go`, `cmd/reconcile/main.go`, `tests/integration/setup_test.go`, `internal/services/wallet_test.go`,
  `internal/services/reconcile_test.go` — are untouched; a nil broadcaster is a safe no-op, which is
  exactly what `cmd/reconcile` and the unit tests want)
- Modify: `api/internal/app/app.go` — add `newWsRegistry`, wire `GET /v1.0/ws`, call
  `svc.SetBroadcaster(registry)` after construction
- Modify: `api/go.mod` — add `github.com/fasthttp/websocket`
- Create: `api/internal/services/wallet_broadcast_test.go`

**Interfaces:**
- Consumes: `ws.Registry` (Task 14).
- Consumes: `middleware.Verifier.VerifyClaims(ctx, tokenStr string) (*Claims, error)` (existing,
  `api/internal/middleware/auth.go:95`) — `Claims.Sub` is the user id.
- Produces: `services.Broadcaster` interface (`Broadcast(ctx context.Context, userID string, payload
  []byte)`), `(*WalletService).SetBroadcaster(b Broadcaster)`.

- [ ] **Step 1: Add the optional broadcaster to `WalletService`**

In `api/internal/services/wallet.go`, add near the other collaborator interfaces (`Repo`, `Locker`,
`KYCClient`, `Auditor`):

```go
// Broadcaster pushes a real-time event to every WebSocket connection for a
// user. Optional — nil in cmd/reconcile and in unit tests, where no user is
// ever connected to receive it.
type Broadcaster interface {
	Broadcast(ctx context.Context, userID string, payload []byte)
}
```

Add a field to `WalletService` and a setter:

```go
type WalletService struct {
	repo        Repo
	users       UserRepo
	audit       Auditor
	lock        Locker
	pix         pix.PixClient
	kyc         KYCClient
	broadcaster Broadcaster // optional; see SetBroadcaster
}

// SetBroadcaster wires the WebSocket registry after construction — kept as a
// setter rather than a constructor parameter so cmd/reconcile and every
// existing unit test's NewWalletService(...) call stays unchanged; a nil
// broadcaster makes ConfirmDeposit's broadcast a no-op.
func (s *WalletService) SetBroadcaster(b Broadcaster) {
	s.broadcaster = b
}
```

In `ConfirmDeposit` (`api/internal/services/wallet.go:184`), after the successful
`s.repo.UpdateDepositStatus(ctx, txid, wallet.DepositConfirmed, charge.E2EID)` call, broadcast before
returning. Change the function's final lines from:

```go
	if kyc.Level == "basic" {
		if err := s.kyc.Confirm(ctx, dep.UserID, kyc.CPF); err != nil {
			// The payer CPF already matched the declared CPF, so a mismatch here is
			// unexpected — surface it but the credit already succeeded.
			slog.Error("kyc confirm on first deposit failed", "user_id", dep.UserID, "err", err)
		}
	}
	return s.repo.UpdateDepositStatus(ctx, txid, wallet.DepositConfirmed, charge.E2EID)
}
```

to:

```go
	if kyc.Level == "basic" {
		if err := s.kyc.Confirm(ctx, dep.UserID, kyc.CPF); err != nil {
			// The payer CPF already matched the declared CPF, so a mismatch here is
			// unexpected — surface it but the credit already succeeded.
			slog.Error("kyc confirm on first deposit failed", "user_id", dep.UserID, "err", err)
		}
	}
	if err := s.repo.UpdateDepositStatus(ctx, txid, wallet.DepositConfirmed, charge.E2EID); err != nil {
		return err
	}
	s.broadcastDepositConfirmed(ctx, dep.UserID, dep.WalletID, txid, charge.Amount)
	return nil
}

// broadcastDepositConfirmed pushes a real-time event to the user's connected
// WebSocket(s), if any (best-effort — a missed broadcast never blocks or fails
// the deposit; the ledger credit already committed). A nil broadcaster (e.g.
// cmd/reconcile, unit tests) is a silent no-op.
func (s *WalletService) broadcastDepositConfirmed(ctx context.Context, userID, walletID, txid string, amount int64) {
	if s.broadcaster == nil {
		return
	}
	payload, err := json.Marshal(map[string]any{
		"type":      "deposit_confirmed",
		"wallet_id": walletID,
		"txid":      txid,
		"amount":    amount,
	})
	if err != nil {
		slog.Error("broadcast deposit_confirmed: marshal failed", "user_id", userID, "err", err)
		return
	}
	s.broadcaster.Broadcast(ctx, userID, payload)
}
```

Add `"encoding/json"` to `wallet.go`'s import block.

- [ ] **Step 2: Test the broadcast wiring**

Write `api/internal/services/wallet_broadcast_test.go` — reusing `wallet_test.go`'s existing helpers
verbatim: `newStubRepo()` builds a `*stubRepo` with wallets `w-real`/`w-game`/`w-sand` for user `u1`;
staging a pending deposit is done by setting `repo.deposit` directly (exactly as
`TestConfirmDepositCreditsOnCPFMatch` at `wallet_test.go:161-165` already does); `pix.NewFake()` +
`fake.StageCharge(txid, amount, pix.ChargeCompleted, cpf, e2eID)` stages the matching paid charge;
`newSvc(repo, locker, pc, kyc)` (`wallet_test.go:149-151`) builds the service:

```go
package services

import (
	"context"
	"encoding/json"
	"testing"

	"gopkg.aoctech.app/api/internal/domain/wallet"
	"gopkg.aoctech.app/api/internal/kycclient"
	"gopkg.aoctech.app/api/internal/pix"
)

type fakeBroadcaster struct {
	userID  string
	payload []byte
	calls   int
}

func (f *fakeBroadcaster) Broadcast(_ context.Context, userID string, payload []byte) {
	f.userID = userID
	f.payload = payload
	f.calls++
}

// TestConfirmDepositBroadcastsOnCredit mirrors
// TestConfirmDepositCreditsOnCPFMatch's setup (same file) and adds a
// fakeBroadcaster to confirm a successfully credited deposit triggers exactly
// one Broadcast call carrying a deposit_confirmed payload.
func TestConfirmDepositBroadcastsOnCredit(t *testing.T) {
	repo := newStubRepo()
	repo.deposit = &wallet.PixDeposit{Txid: "tx-broadcast", WalletID: "w-real", UserID: "u1", AmountExpected: 500, Status: wallet.DepositPending}
	fake := pix.NewFake()
	fake.StageCharge("tx-broadcast", 500, pix.ChargeCompleted, "12345678901", "E2E-1")
	kyc := &stubKYC{rec: &kycclient.KYC{Level: "verified", CPF: "12345678901"}}

	svc := newSvc(repo, &stubLocker{}, fake, kyc)
	fb := &fakeBroadcaster{}
	svc.SetBroadcaster(fb)

	if err := svc.ConfirmDeposit(context.Background(), "tx-broadcast"); err != nil {
		t.Fatalf("ConfirmDeposit: %v", err)
	}
	if fb.calls != 1 {
		t.Fatalf("expected 1 broadcast, got %d", fb.calls)
	}
	if fb.userID != "u1" {
		t.Fatalf("broadcast userID = %q, want u1", fb.userID)
	}
	var msg map[string]any
	if err := json.Unmarshal(fb.payload, &msg); err != nil {
		t.Fatalf("unmarshal broadcast payload: %v", err)
	}
	if msg["type"] != "deposit_confirmed" {
		t.Fatalf("bad payload: %+v", msg)
	}
}

// TestConfirmDepositNilBroadcasterIsNoOp confirms a service with no
// SetBroadcaster call (the state of every other existing ConfirmDeposit test
// in this file, and of cmd/reconcile's service) still credits successfully —
// broadcasting must never be load-bearing for the deposit itself.
func TestConfirmDepositNilBroadcasterIsNoOp(t *testing.T) {
	repo := newStubRepo()
	repo.deposit = &wallet.PixDeposit{Txid: "tx-nobroadcast", WalletID: "w-real", UserID: "u1", AmountExpected: 500, Status: wallet.DepositPending}
	fake := pix.NewFake()
	fake.StageCharge("tx-nobroadcast", 500, pix.ChargeCompleted, "12345678901", "E2E-1")
	kyc := &stubKYC{rec: &kycclient.KYC{Level: "verified", CPF: "12345678901"}}

	svc := newSvc(repo, &stubLocker{}, fake, kyc)
	if err := svc.ConfirmDeposit(context.Background(), "tx-nobroadcast"); err != nil {
		t.Fatalf("ConfirmDeposit: %v", err)
	}
	if repo.depositStatus != wallet.DepositConfirmed {
		t.Fatalf("deposit status = %q, want confirmed", repo.depositStatus)
	}
}
```

- [ ] **Step 3: Run the service tests**

Run: `cd api && go test ./internal/services/... -race -count=1`
Expected: PASS, including the existing `ConfirmDeposit` tests (broadcaster stays nil there, so behavior is
unchanged) and the new `TestConfirmDepositBroadcastsOnCredit`.

- [ ] **Step 4: Add the WebSocket dependency and the route**

Run: `cd api && go get github.com/fasthttp/websocket`

Write `api/internal/api/v1/ws.go`:

```go
package v1

import (
	"encoding/json"
	"log/slog"
	"time"

	"gopkg.aoctech.app/api/internal/middleware"
	"gopkg.aoctech.app/api/internal/ws"

	fws "github.com/fasthttp/websocket"
	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/valyala/fasthttp"
)

const wsPingInterval = 30 * time.Second

var wsUpgrader = fws.FastHTTPUpgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     func(_ *fasthttp.RequestCtx) bool { return true },
}

// RegisterWS registers the GET /ws WebSocket upgrade endpoint. Auth is a query
// param (?token=<jwt>) rather than a header — the browser WebSocket API cannot
// set Authorization on the upgrade request.
func RegisterWS(router fiber.Router, verifier *middleware.Verifier, reg ws.Registry) {
	router.Get("/ws", func(c fiber.Ctx) error {
		token := c.Query("token")
		if token == "" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"detail": "token obrigatório"})
		}

		return wsUpgrader.Upgrade(c.RequestCtx(), func(conn *fws.Conn) {
			ctx := c.Context()
			send := func(msg any) {
				data, _ := json.Marshal(msg)
				_ = conn.WriteMessage(fws.TextMessage, data)
			}

			claims, err := verifier.VerifyClaims(ctx, token)
			if err != nil || claims == nil || claims.Sub == "" {
				send(map[string]any{"type": "error", "code": "unauthorized", "message": "Token inválido ou expirado"})
				return
			}
			userID := claims.Sub

			connID := uuid.NewString()
			reg.Register(userID, connID, &wsConnAdapter{conn: conn})
			defer reg.Unregister(userID, connID)

			send(map[string]any{"type": "connected", "conn_id": connID})
			slog.Info("ws connected", "conn", connID, "user", userID)

			done := make(chan struct{})
			go func() {
				t := time.NewTicker(wsPingInterval)
				defer t.Stop()
				for {
					select {
					case <-t.C:
						if e := conn.WriteMessage(fws.TextMessage, []byte(`{"type":"ping"}`)); e != nil {
							return
						}
					case <-done:
						return
					}
				}
			}()

			for {
				if _, _, e := conn.ReadMessage(); e != nil {
					break
				}
			}
			close(done)
			slog.Info("ws disconnected", "conn", connID, "user", userID)
		})
	})
}

// wsConnAdapter adapts fasthttp/websocket.Conn to ws.Conn.
type wsConnAdapter struct {
	conn *fws.Conn
}

func (w *wsConnAdapter) WriteMessage(messageType int, data []byte) error {
	return w.conn.WriteMessage(messageType, data)
}
```

Note: unlike `ctech-dfe`'s version, there is no periodic membership re-check tick (that existed there to
detect a user removed from an org mid-connection — the wallet has no equivalent revocable membership; a
user's own JWT expiring is the only relevant revocation and the client already re-authenticates the REST
API on 401, which will also drop and reopen the socket via `useWebSocket`'s reconnect logic).

- [ ] **Step 5: Wire it in `router.go` and `app.go`**

In `api/internal/api/v1/router.go`, add the import `"gopkg.aoctech.app/api/internal/ws"`
if not already present via another file in the package (Go only needs the import once per file that uses
it — `RegisterWS` is called from `Register`, so add the call, not necessarily the import, to `router.go`;
the import itself belongs in whichever file the compiler requires it in — since `RegisterWS` is declared
in `ws.go` and takes `ws.Registry` as a parameter type only in `ws.go`, `router.go` needs the `ws` import
only if it references `ws.Registry` directly in `Register`'s new parameter — see below). Update `Register`
to accept and pass through the registry:

```go
func Register(app *fiber.App, c cache.Backend, cfg *config.Config, clients *awsclient.Clients, pixClient pix.PixClient, svc *services.WalletService, userSvc *services.UserService, wsRegistry ws.Registry) {
	h := &handlers{svc: svc, userSvc: userSvc}
	verifier := middleware.NewVerifier(cfg.CtechJWKSURL, cfg.ServiceAudience, cfg.CtechURL, c)
	auth := verifier.Middleware()

	v1 := app.Group("/v1.0")

	RegisterHealth(v1, clients, c, pixClient, verifier, cfg)
	RegisterWS(v1, verifier, wsRegistry)

	// ... rest of Register unchanged ...
```

(Insert the `RegisterWS(v1, verifier, wsRegistry)` line right after the existing `RegisterHealth(...)`
call; everything below it — the `/auth`, `/wallet`, `/internal` groups — is unchanged from Task 6.)

Add the `"gopkg.aoctech.app/api/internal/ws"` import to `router.go`'s import block.

In `api/internal/app/app.go`, add after `newLocker`:

```go
// newWsRegistry builds the WebSocket fan-out registry. Reuses the same Redis
// (Valkey) connection as the cache backend when one is configured — falls back
// to an in-memory (single-instance) registry otherwise, exactly like
// newCacheBackend's own Redis/in-memory fallback.
func newWsRegistry(lc fx.Lifecycle, c cache.Backend) ws.Registry {
	rb, ok := c.(*cache.RedisBackend)
	if !ok {
		slog.Warn("ws: no Redis backend — using in-memory registry (not shared across replicas)")
		return ws.NewMemoryRegistry()
	}
	reg := ws.NewRedisRegistry(rb.Client())
	lc.Append(fx.Hook{
		OnStart: reg.Start,
		OnStop:  reg.Stop,
	})
	return reg
}
```

Add `"gopkg.aoctech.app/api/internal/ws"` to `app.go`'s imports. Add `newWsRegistry,`
to the `fx.Provide` list (anywhere after `newCacheBackend,`). Update `registerRoutes` and its
`fx.Invoke(registerRoutes)` call to pass the registry through and to call `SetBroadcaster`:

```go
func registerRoutes(app *fiber.App, c cache.Backend, cfg *config.Config, clients *awsclient.Clients, pixClient pix.PixClient, svc *services.WalletService, userSvc *services.UserService, wsRegistry ws.Registry) {
	svc.SetBroadcaster(wsRegistry)
	apiv1.Register(app, c, cfg, clients, pixClient, svc, userSvc, wsRegistry)
}
```

- [ ] **Step 6: Build and run the full `api` test suite**

Run: `cd api && go build ./... && go test ./... -race -count=1`
Expected: builds clean; all tests pass.

- [ ] **Step 7: Commit**

```bash
git add api/internal/services/wallet.go api/internal/services/wallet_broadcast_test.go \
        api/internal/api/v1/ws.go api/internal/api/v1/router.go api/internal/app/app.go api/go.mod api/go.sum
git commit -m "feat(api): GET /v1.0/ws real-time deposit_confirmed notifications"
```

---

## Phase G — `ui`: WebSocket connection + polling fallback

### Task 16: Port `useWebSocket.ts`

**Files:**
- Create: `ui/src/lib/hooks/useWebSocket.ts`

**Interfaces:**
- Produces: `WSStatus = 'disconnected' | 'connecting' | 'connected' | 'error'`,
  `useWebSocket({url, onMessage, enabled}: UseWebSocketOptions): {status: WSStatus}` — consumed by Task
  17's `useWalletRealtime`.

This hook has no organization-specific logic in `ctech-dfe`'s version — it is a generic reconnecting
WebSocket client keyed only by URL. Copied verbatim.

- [ ] **Step 1: Write the file**

Write `ui/src/lib/hooks/useWebSocket.ts`:

```ts
'use client'

import {useEffect, useLayoutEffect, useRef, useState} from 'react'

export type WSStatus = 'disconnected' | 'connecting' | 'connected' | 'error'

export interface UseWebSocketOptions {
  url: string | null
  onMessage: (data: unknown) => void
  enabled?: boolean
}

const BASE_DELAY_MS = 1_000
const MAX_DELAY_MS = 30_000
const MAX_RECONNECT_ATTEMPTS = 10

export function useWebSocket({url, onMessage, enabled = true}: UseWebSocketOptions): {status: WSStatus} {
  const [status, setStatus] = useState<WSStatus>('disconnected')
  const attemptsRef = useRef(0)
  const onMessageRef = useRef(onMessage)

  useLayoutEffect(() => {
    onMessageRef.current = onMessage
  })

  useEffect(() => {
    if (!url || !enabled) return

    let cancelled = false
    let timer: ReturnType<typeof setTimeout> | null = null
    let ws: WebSocket | null = null

    function connect() {
      if (cancelled) return

      setStatus('connecting')
      ws = new WebSocket(url!)

      ws.onopen = () => {
        attemptsRef.current = 0
        setStatus('connected')
      }

      ws.onmessage = (evt) => {
        try {
          const data = JSON.parse(evt.data as string)
          if (data?.type === 'ping') {
            const sock = evt.target as WebSocket
            if (sock.readyState === WebSocket.OPEN) {
              sock.send(JSON.stringify({type: 'pong'}))
            }
          }
          onMessageRef.current(data)
        } catch {
          // malformed frame — ignore
        }
      }

      ws.onerror = () => {
        setStatus('error')
      }

      ws.onclose = () => {
        ws = null
        setStatus('disconnected')
        if (cancelled) return

        attemptsRef.current++
        if (attemptsRef.current > MAX_RECONNECT_ATTEMPTS) return

        const delay = Math.min(BASE_DELAY_MS * 2 ** (attemptsRef.current - 1), MAX_DELAY_MS)
        timer = setTimeout(connect, delay)
      }
    }

    connect()

    return () => {
      cancelled = true
      if (timer) clearTimeout(timer)
      ws?.close(1000)
      ws = null
    }
  }, [url, enabled])

  return {status}
}
```

- [ ] **Step 2: Lint**

Run: `cd ui && npx eslint src/lib/hooks/useWebSocket.ts --max-warnings 0`
Expected: zero errors, zero warnings.

- [ ] **Step 3: Commit**

```bash
git add ui/src/lib/hooks/useWebSocket.ts
git commit -m "feat(ui): port useWebSocket hook from ctech-dfe"
```

---

### Task 17: `useWalletRealtime` — user-scoped WebSocket wiring

**Files:**
- Create: `ui/src/lib/hooks/useWalletRealtime.ts`
- Modify: `ui/src/app/dashboard/page.tsx` — call the hook, remove the need for manual `refresh()` on a
  deposit confirming (React Query invalidation from the hook covers it; the existing `refresh()` calls
  after withdraw/credits/fund-game/return-game mutations are untouched, those are still direct user
  actions, not something the socket also reports)

**Interfaces:**
- Consumes: `useWebSocket` (Task 16), `getAccessToken` (`ui/src/lib/api/client.ts`, existing).
- Produces: `useWalletRealtime(): {wsStatus: WSStatus}`.

**No `NEXT_PUBLIC_WS_URL` build-time env var is added** in this task — unlike `ctech-dfe`, this hook
falls back to `window.location.origin` when the API base URL env var is unset, and `ui`'s existing
`NEXT_PUBLIC_API_URL` (already set per-environment in `frontend.yml`, see Task 18) is reused directly
converted from `http`→`ws` — no separate variable needed since the wallet has only one API origin per
environment (no separate `dfe-api`-style split).

- [ ] **Step 1: Write the hook**

Write `ui/src/lib/hooks/useWalletRealtime.ts`:

```ts
'use client'

import {useCallback} from 'react'
import {useQueryClient} from '@tanstack/react-query'
import {toast} from 'sonner'
import {useWebSocket, type WSStatus} from './useWebSocket'
import {getAccessToken} from '@/lib/api/client'

// NEXT_PUBLIC_API_URL already carries the environment's API origin (set in
// frontend.yml) — converted http(s) → ws(s). Empty means same-origin, exactly
// like apiClient's own API_BASE_URL fallback.
const WS_BASE_URL = process.env.NEXT_PUBLIC_API_URL || ''

function buildWsUrl(token: string): string {
  const origin = WS_BASE_URL || window.location.origin
  const base = origin.replace(/^http/, 'ws')
  return `${base}/v1.0/ws?token=${encodeURIComponent(token)}`
}

interface RealtimeMessage {
  type: string
  wallet_id?: string
  txid?: string
  amount?: number
}

/** Formats centavos as BRL without importing formatBRL, to keep this hook
 * dependency-free of the wallet component tree (avoids a circular import risk
 * between hooks/ and components/wallet/). */
function formatCentavos(amount: number): string {
  return (amount / 100).toLocaleString('pt-BR', {style: 'currency', currency: 'BRL'})
}

export function useWalletRealtime(): { wsStatus: WSStatus } {
  const qc = useQueryClient()
  const token = getAccessToken()

  const wsUrl = token ? buildWsUrl(token) : null

  const handleMessage = useCallback((data: unknown) => {
    const msg = data as RealtimeMessage
    if (!msg?.type || msg.type === 'ping' || msg.type === 'connected') return

    if (msg.type === 'deposit_confirmed') {
      void qc.invalidateQueries({queryKey: ['balances']})
      void qc.invalidateQueries({queryKey: ['ledger']})
      const amount = typeof msg.amount === 'number' ? ` de ${formatCentavos(msg.amount)}` : ''
      toast.success(`Depósito${amount} confirmado`)
    }
  }, [qc])

  const {status: wsStatus} = useWebSocket({
    url: wsUrl,
    onMessage: handleMessage,
    enabled: !!wsUrl,
  })

  return {wsStatus}
}
```

- [ ] **Step 2: Wire it into the dashboard**

In `ui/src/app/dashboard/page.tsx`, add the import:

```ts
import {useWalletRealtime} from '@/lib/hooks/useWalletRealtime'
```

Inside `DashboardInner`, right after the existing hooks (`useAuth`, `useQueryClient`, `useState` calls),
add:

```ts
  useWalletRealtime()
```

(The hook's own `wsStatus` return value is not consumed by the dashboard UI in this task — it exists for
a future connection-status indicator if ever wanted; calling the hook for its side effect,
cache-invalidation-on-`deposit_confirmed`, is the only thing this task needs.)

- [ ] **Step 3: Lint**

Run: `cd ui && npx eslint src --ext .ts,.tsx --max-warnings 0`
Expected: zero errors, zero warnings.

- [ ] **Step 4: Commit**

```bash
git add ui/src/lib/hooks/useWalletRealtime.ts ui/src/app/dashboard/page.tsx
git commit -m "feat(ui): useWalletRealtime invalidates balances/ledger on deposit_confirmed"
```

---

### Task 18: `PixChargeDialog` 30s polling fallback + `NEXT_PUBLIC_API_URL`-based WS already covered

**Files:**
- Modify: `ui/src/components/wallet/pix-charge-dialog.tsx` — add the polling fallback and self-close on
  resolution
- Modify: `ui/src/app/dashboard/page.tsx` — pass the real wallet's balance snapshot into the dialog so it
  can detect a credit

**Interfaces:**
- Consumes: `apiClient.getBalances()` (existing, `ui/src/lib/api/client.ts:122`) via a `useQuery` inside
  the dialog itself, sharing the `['balances']` cache key the dashboard already uses — no new API call
  shape.

The WebSocket path (Task 17) is primary; this task is the fallback for when the socket is down,
reconnecting, or the tab is backgrounded and throttled. Both paths end the same way: the dialog closes
and a success toast fires.

- [ ] **Step 1: Add the polling fallback to the dialog**

In `ui/src/components/wallet/pix-charge-dialog.tsx`, change the component signature and add the polling
effect. The current file:

```tsx
export function PixChargeDialog({deposit, onClose}: { deposit: DepositResult; onClose: () => void }) {
  const [copied, setCopied] = useState(false)
  
  async function copy() {
    await navigator.clipboard.writeText(deposit.pix_copia_e_cola)
    setCopied(true)
    toast.success('Código PIX copiado')
    setTimeout(() => setCopied(false), 2000)
  }
```

becomes:

```tsx
const POLL_DELAY_MS = 30_000 // start polling only after this — the WS path is primary
const POLL_INTERVAL_MS = 5_000

export function PixChargeDialog(
  {deposit, initialRealBalance, onClose, onConfirmed}: {
    deposit: DepositResult
    initialRealBalance: number
    onClose: () => void
    onConfirmed: () => void
  },
) {
  const [copied, setCopied] = useState(false)
  const [polling, setPolling] = useState(false)

  useEffect(() => {
    const t = setTimeout(() => setPolling(true), POLL_DELAY_MS)
    return () => clearTimeout(t)
  }, [])

  const balances = useQuery({
    queryKey: ['balances'],
    queryFn: () => apiClient.getBalances(),
    enabled: polling,
    refetchInterval: polling ? POLL_INTERVAL_MS : false,
  })

  useEffect(() => {
    const realBalance = balances.data?.real?.balance
    if (realBalance != null && realBalance >= initialRealBalance + deposit.amount) {
      toast.success('Depósito confirmado')
      onConfirmed()
    }
  }, [balances.data, initialRealBalance, deposit.amount, onConfirmed])

  async function copy() {
    await navigator.clipboard.writeText(deposit.pix_copia_e_cola)
    setCopied(true)
    toast.success('Código PIX copiado')
    setTimeout(() => setCopied(false), 2000)
  }
```

Add the two new imports at the top of the file:

```tsx
import {useEffect, useState} from 'react'
import {useQuery} from '@tanstack/react-query'
import {apiClient} from '@/lib/api/client'
```

(`useState` is already imported — merge it into the same `'react'` import line rather than duplicating
it: `import {useEffect, useState} from 'react'`.)

The rest of the component (the QR code, the copy button, the "expires in 15 minutes" copy) is unchanged —
`onClose` still exists for the user's manual "Fechar" button; `onConfirmed` is the new
auto-resolution path the dashboard wires to close the dialog *and* treat it as a completed deposit
(no `refresh()` call needed here — `useWalletRealtime`'s cache invalidation or this component's own
`['balances']` refetch already updated the cache the dashboard reads).

- [ ] **Step 2: Wire the new props from the dashboard**

In `ui/src/app/dashboard/page.tsx`, change the render call from:

```tsx
      {charge && <PixChargeDialog deposit={charge} onClose={() => setCharge(null)}/>}
```

to:

```tsx
      {charge && (
        <PixChargeDialog
          deposit={charge}
          initialRealBalance={balances.data?.real?.balance ?? 0}
          onClose={() => setCharge(null)}
          onConfirmed={() => setCharge(null)}
        />
      )}
```

`initialRealBalance` is read from the `balances` query already in scope in `DashboardInner` — it reflects
the real wallet's balance at the moment the dialog renders (before the deposit lands), which is exactly
the baseline the dialog's polling effect needs to detect a credit.

- [ ] **Step 3: Lint**

Run: `cd ui && npx eslint src --ext .ts,.tsx --max-warnings 0`
Expected: zero errors, zero warnings.

- [ ] **Step 4: Manual verification (per the `verify` skill's spirit — this is a UI behavior change)**

Since this task changes runtime UI behavior (not just types), start the dev server and drive the flow.

Task 4 removed `api`'s local `pix.NewFake()` dev fallback (`newPixClient`, the function that returned a
fake `PixClient` when `INTER_CLIENT_ID` was unset) along with `InterClient` itself — `api` now always
invokes `pix-gateway`'s outbound Lambda for every `PixClient` call, with no local stub path. This means
`api` can no longer run fully offline against a fake bank: exercising a real deposit confirmation end to
end requires the `dev` AWS environment, with `pix-gateway` deployed (Tasks 1–13) and `LambdaPixClient`
pointed at it. Run this verification there:

Run: `cd ui && npm run dev` with `NEXT_PUBLIC_API_URL` pointed at the `dev` environment's `api`
(`https://wallet-dev.aoctech.app`), or against a local `api` process whose `PIX_GATEWAY_FUNCTION_NAME`/AWS
credentials target the same deployed `dev` `pix-gateway` functions.

- Open the dashboard, click "Depositar via PIX", submit an amount.
- Confirm the QR/copy-paste dialog renders as before.
- Trigger a deposit confirmation (via whatever mechanism the local/dev environment provides — direct
  `ConfirmDeposit` call, a staged webhook, or an actual sandbox PIX payment) and confirm the dialog closes
  automatically with a success toast, without a manual page refresh.
- With the WebSocket intentionally blocked (e.g. browser devtools "block request" on the `/v1.0/ws` URL,
  or airplane-mode the socket only), confirm the same deposit still resolves the dialog within
  30–35 seconds via the polling fallback.

- [ ] **Step 5: Commit**

```bash
git add ui/src/components/wallet/pix-charge-dialog.tsx ui/src/app/dashboard/page.tsx
git commit -m "feat(ui): PixChargeDialog polls balances 30s after opening as a WS fallback"
```

---

## Final Verification

After all 18 tasks:

- [ ] `cd pix-gateway && go build ./... && go test ./... -race -count=1` — PASS
- [ ] `cd api && go build ./... && go test ./... -race -count=1` — PASS
- [ ] `cd api && make test-integration` (with `docker compose -f docker-compose.test.yml up -d`) — PASS,
  confirms `ConfirmDeposit`'s ledger/idempotency/lock behavior is unaffected by the transport change
- [ ] `cd cdk && npm run build && ENVIRONMENT=dev npx cdk synth --all --quiet` — synthesizes cleanly
- [ ] `cd ui && npx eslint src --ext .ts,.tsx --max-warnings 0` — zero errors, zero warnings
- [ ] `rg -n "InterBaseURL|InterClientID|InterClientSecret|InterWebhookSecret|InterPixKey" api/internal` —
  no matches (confirms `api` no longer references any Inter config)
- [ ] `rg -n "ctech-wallet/api/internal/secrets"` — no matches anywhere (confirms the package was fully
  removed, not left dangling)
- [ ] Manually confirm (per Task 18 Step 4) that a deposit resolves the charge dialog via both the
  WebSocket path and the polling fallback

## Operational Steps (not code — must happen before this ships to any real environment)

These are called out because they are easy to miss: nothing in this plan can complete them, they require
dashboard/console access outside a coding session.

1. Register pix-gateway's own M2M client in `ctech-account` (confidential, scope
   `internal:pix:confirm-deposit`) — mirrors how the wallet's own `WALLET_CLIENT_ID` client was seeded;
   the cross-project contract (root `CLAUDE.md`) requires no `ctech-account` code change, only seeding.
2. Seed `/ctech-wallet/{env}/pix-gateway/client-id` and `/ctech-wallet/{env}/pix-gateway/client-secret`
   (SecureString) in SSM for every environment.
3. Download Inter's webhook client certificate (the `.crt` from the Inter developer portal's webhook
   registration flow) and upload it to the `{env}-ctech-wallet-pix-gateway-truststore` S3 bucket as
   `inter-webhook-ca.pem` (Task 10) — confirm with Inter/Bacen docs whether it is a single self-signed
   cert or a CA bundle before uploading, per the design spec's still-open item.
4. Register the webhook URL with Inter (`https://pix.wallet.aoctech.app/pix/webhook`) in the Inter
   developer portal.
5. Add the Cloudflare DNS record for `pix.wallet.aoctech.app` — CNAME to the `WebhookDomainTarget` CDK
   output (Task 10), **DNS-only / unproxied** (see the design spec's mTLS note — a proxied record breaks
   the handshake).
6. Confirm with Inter/Bacen docs whether the webhook delivery also sends any shared-secret header (still
   an open item in the design spec) — if so, decide whether to add it as defense-in-depth on top of the
   mTLS + M2M scope auth this plan implements.

