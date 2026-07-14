# Implementation Plan — Inter OAuth Token: API-Owned, Passed Per-Call

**Status:** implementation
**Depends on:** `docs/specs/2026-07-13-inter-token-manager-design.md` (kept as written)

## Goal

`api` owns the Inter OAuth token lifecycle (proactive refresh + single-flight guard) and passes the current
bearer to **every** PIX op on the Lambda wire. `pix-gateway` is a pure transport: sets
`Authorization: Bearer <passed token>`, never reads SSM for the token, never fetches it itself except via the
new `GetToken` op (invoked by `api`, using pix-gateway's own client creds). mTLS cert/key stay in pix-gateway's
own SSM (read once per container). The Inter OAuth client secret is likewise
pix-gateway's own SSM param, but it is read **lazily** by the `GetToken` op and
cached for the container's lifetime — not at cold start — so a container that
never mints a token never pays the SSM `GetParameter` call.

## Key design choice

Thread the bearer through `context` from the pix-gateway handler into `do`/`doIdem` — **not** by adding a `token`
param to all 7 `PixClient` methods. Reason: the api `PixClient` interface is consumed by services + `fake.go`;
changing 7 signatures ripples across the wallet service. Context seeding keeps both `PixClient` interfaces
unchanged. The api side never sees the token except inside `LambdaPixClient.call`, which injects it.

## Phase 0 — Wire contract (both modules, in sync by hand)

`api/internal/pix/rpc_types.go`
- Add `OAuthToken string \`json:"oauth_token"\`` to `rpcRequest`.
- Add `opGetToken rpcOp = "GetToken"`.
- Add `rpcGetTokenResult struct { Token string \`json:"token"\`; ExpiresIn int \`json:"expires_in"\` }`.

`pix-gateway/internal/rpc/types.go`
- Add `OAuthToken string \`json:"oauth_token"\`` to `Request`.
- Add `OpGetToken Op = "GetToken"`.
- Add `GetTokenResult struct { Token string \`json:"token"\`; ExpiresIn int \`json:"expires_in"\` }`.
- Add `ErrUnauthorizedSentinel = "unauthorized"` (mirrors `ErrKeyNotFoundSentinel`).

## Phase 1 — pix-gateway: pure transport

`pix-gateway/internal/inter/inter.go`
- Drop `tokens *tokenManager` field; remove `tokenManager` struct + `get`.
- `NewInterClient`: keep mTLS http.Client; retain `clientID`/`clientSecret`/`scope`/`tokenURL` as struct fields.
- Add `inter.TokenResult{Token string; ExpiresIn int}` + `GetToken(ctx) (TokenResult, error)` to the
  `inter.PixClient` interface and implement on `InterClient`: POST `client_credentials` to `c.tokenURL` over
  `c.http` (mTLS), decode `access_token`/`expires_in`. No SSM write.
- `do`/`doIdem`: replace `c.tokens.get(ctx)` with `bearer := bearerFromContext(ctx)`; if empty → error.
  Set `Authorization: Bearer <bearer>`.
- `Ping`: drop `c.tokens.get`; validate `bearerFromContext(ctx) != ""`. No Inter call.

`pix-gateway/internal/inter/bearer.go` (new)
- `bearerCtxKey`, `WithBearer(ctx, token)`, `bearerFromContext(ctx)`, `IsUnauthorized(err)` (unwrap
  `*statusError`, code==401).

`pix-gateway/cmd/outbound/main.go`
- `handle`: at top, `ctx = inter.WithBearer(ctx, req.OAuthToken)`.
- Add `case rpc.OpGetToken:` → `h.pix.GetToken(ctx)` → `okResp(rpc.GetTokenResult{...})`.
- Centralize error mapping via `toResp(err)`: `inter.ErrKeyNotFound`→`key_not_found`,
  `inter.IsUnauthorized`→`unauthorized`, else plain.

## Phase 2 — api: InterTokenManager (token owner)

`api/internal/pix/intertoken.go` (new)
- `InterTokenManager{ invoker lambdaInvoker; locker *lock.Locker; mu sync.Mutex; token string; expiry time.Time }`
  (reuse unexported `awsLambdaInvoker` from `lambda_client.go`, same package).
- `Get(ctx, force bool) (string, error)`:
  1. cache hit (`!force && token!="" && now < expiry-30s`) → return (no I/O).
  2. else `locker.Acquire(ctx, tokenRefreshLockKey)` (const `inter:token:refresh` → key
     `wallet:inter:token:refresh`; reuses `lock.Locker`). Double-check cache after acquire.
  3. lock error / contention → best-effort: return cached token if present (stale-ok), else one direct `fetch`.
  4. under lock: `fetch` → set cache → return.
- `fetch(ctx)`: marshal `rpcRequest{Op: opGetToken}`; `invoker.invoke`; parse `rpcResponse`/`rpcGetTokenResult`;
  expiry = `now + ExpiresIn - 30s`.
- `prime` + 55-min background loop via `fx.Lifecycle.OnStart` (best-effort). Never logs token.

## Phase 3 — api: inject token + 401 retry in LambdaPixClient

`api/internal/pix/lambda_client.go`
- `LambdaPixClient` gains `tokenMgr *InterTokenManager`.
- `call`: before invoke, `req.OAuthToken, _ = c.tokenMgr.Get(ctx, false)`; marshal with `OAuthToken`.
- After invoke: if `resp.Error == errUnauthorizedSentinel` → `tok, err := c.tokenMgr.Get(ctx, true)` (force); on
  success re-invoke **once**.

## Phase 4 — fx wiring (api/internal/app/app.go)

- Add `newInterTokenManager(client *lambda.Client, cfg *config.Config, locker *lock.Locker) *InterTokenManager`
  before `newLambdaPixClient`.
- Change `newLambdaPixClient(client, cfg, tokenMgr)` to wire the manager.

## Phase 5 — logging / redaction safety

- api HTTP `logger.New` logs path/status/latency only — no request body, so `oauth_token` never reaches api HTTP
  logs. No other api code logs the Invoke payload.
- pix-gateway: handler must never log `rpc.Request`. Documented constraint; no behavioral change.

## Phase 6 — tests

| Test | File | Approach |
|------|------|----------|
| InterTokenManager | api/internal/pix/intertoken_test.go | mock lambdaInvoker + memStore locker: fresh→no invoke; stale→invoke under lock; prime + 55m loop |
| Concurrent single-flight | same | N goroutines Get → exactly 1 fetch |
| GetToken handler | pix-gateway/internal/inter/inter_test.go | httptest Inter token stub → returns {token, expires_in}; no SSM |
| Wire oauth_token | same | httptest Inter stub asserts Authorization: Bearer <passed>; bearer via ctx |
| 401 retry | api/internal/pix/lambda_client_test.go | mock invoker returns unauthorized then success → call retries once |
| transport no tokenManager | pix-gateway/internal/inter/inter_test.go | compile + InterClient no longer holds tokenManager |

## Phase 7 — cdk / cross-project

- No cdk change for the token. api role keeps `lambda:InvokeFunction` on pix-gateway ARN. pix-gateway role keeps
  `ssm:GetParameter` on mTLS cert/key params only.
- Reviewed: api ↔ pix-gateway ✓; ui (none) ✓; cdk (none) ✓; ctech-account (none) ✓.

## Verification

```
cd api && go build ./... && go test ./internal/pix/...
cd ../pix-gateway && go build ./... && go test ./internal/inter/... ./cmd/...
```

## Files touched

```
api/internal/pix/rpc_types.go          +oauth_token, opGetToken, rpcGetTokenResult
api/internal/pix/intertoken.go         NEW InterTokenManager
api/internal/pix/lambda_client.go      +tokenMgr field, inject + 401 retry
api/internal/app/app.go                +newInterTokenManager wiring
pix-gateway/internal/rpc/types.go      +oauth_token, OpGetToken, GetTokenResult, ErrUnauthorizedSentinel
pix-gateway/internal/inter/inter.go    -tokenManager, +GetToken, ctx bearer
pix-gateway/internal/inter/client.go   +GetToken to interface, +TokenResult
pix-gateway/internal/inter/bearer.go   NEW ctx helpers + IsUnauthorized
pix-gateway/cmd/outbound/main.go       seed ctx, OpGetToken case, toResp
+ 4 test files
```

## Implementation notes / deviations

- **Single-flight:** `InterTokenManager.Get` coalesces concurrent refreshes via an
  in-process `sync.Cond` (one fetch runs, the rest wait for it) plus the Valkey
  lock for the cross-replica guard. `force=true` (used on a 401) bypasses the
  fresh-cache fast path but waiters still share the one in-flight refresh, so a
  stampede still hits Inter once. A nil locker (reconcile, tests) skips the
  Valkey guard safely.
- **Bearer threading:** the bearer travels in the `Invoke` payload
  (`rpcRequest.OAuthToken`) and is seeded into the request `context` by the
  pix-gateway handler, then read in `do`/`doIdem`. This keeps both `PixClient`
  interfaces token-free — no 7-signature churn on the api side.
- **401 retry bug caught by test:** `json.Unmarshal` leaves fields absent from
  the JSON intact, so the retried response kept the first attempt's `Error`.
  `call` now resets `resp = rpcResponse{}` before re-unmarshalling.
- **Redaction:** api's HTTP `logger.New` logs path/status/latency only — the
  bearer never reaches api logs. pix-gateway must not log `rpc.Request`.

## Status

Implemented and green: `go build ./...`, `go vet ./...`, `go test ./...` pass on
both `api` and `pix-gateway`.

