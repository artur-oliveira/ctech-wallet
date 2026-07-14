# Inter OAuth Token Manager — API-Owned, Passed Per-Call

**Status:** proposed (revised)
**Date:** 2026-07-13
**Depends on:** `docs/specs/2026-07-13-pix-gateway-lambda-design.md`
**Blocks:** nothing new.

---

## Revision note

The earlier draft of this doc put the token in an SSM `SecureString` shared
store that `pix-gateway` read on every PIX call (with an in-memory fast cache).
Under this model `pix-gateway` would make an SSM `GetParameter` + KMS `Decrypt`
call on every outbound invocation — a per-call AWS bill (SSM + KMS API
charges scale with Lambda invocation count) and a hard dependency on SSM being
available for *every* PIX op.

**Change:** the API owns the token lifecycle (proactive refresh + single-flight
guard) and passes the current bearer to `pix-gateway` **on the wire** with each
PIX op. `pix-gateway` becomes a pure transport: it sets `Authorization: Bearer
<passed token>` and never reads SSM or fetches tokens itself. No SSM token
store, no `PutParameter`, no DynamoDB lock table. This removes the per-call SSM
cost entirely and deletes the `tokenManager` from `pix-gateway`.

The IPv4 egress boundary from the pix-gateway migration is **unchanged**: the
API still never talks to Inter directly. The actual Inter token fetch happens
inside `pix-gateway` via a new `GetToken` op that `api` *invokes* — `api`
orchestrates refresh, `pix-gateway` performs the only direct Inter contact.

---

## Problem

Banco Inter throttles OAuth `client_credentials` issuance at **5 requests per
minute**. Two failure modes if uncoordinated:

1. **Cold-start stampede** — every cold `pix-gateway` invocation fetching its
   own token on first use, under scale-out/deploy/spike more than 5 land in a
   minute → `429` → every PIX call behind it fails.
2. **Per-call SSM cost** — `pix-gateway` reading a shared token from SSM on
   every call bills SSM+KMS per Lambda invocation (thousands/day) and makes
   PIX ops depend on SSM uptime.

The token is valid ~1h, so steady state needs ~1 fetch/hour. The fix is to make
issuance single-flight and owned by one place (`api`), and to stop reading SSM
per call.

## Goal

- `api` owns the token lifecycle: proactive refresh ~5 min before expiry,
  guarded by a single-flight lock so Inter is hit at most a few times/hour.
- `api` passes the current bearer to **every** PIX op on the Lambda wire.
- `pix-gateway` receives the token and passes it *as-is* to Inter — no SSM, no
  local fetch, no `tokenManager`.

## Design decisions (confirmed)

1. **Placement.** `api` owns lifecycle (refresh scheduling + Valkey
   single-flight guard). The Inter *fetch* still runs in `pix-gateway` (via a
   `GetToken` op), because `api` cannot reach Inter's IPv4-only endpoint. `api`
   only invokes the Lambda — the networking boundary stays intact.
2. **Credentials.** `pix-gateway` uses its **own** `InterClientID` /
   `InterClientSecret` (in `pix-gateway/internal/config/config.go`) for the
   `GetToken` fetch. `api` never holds the Inter client secret.
3. **Transport of the token.** The bearer travels in the Lambda `Invoke`
   payload (`oauth_token` field on the shared request envelope), never at rest.
   `api` keeps it in memory only; `pix-gateway` holds it for the duration of
   the single `Invoke` and discards it.
4. **Refresh cadence.** Proactive refresh every **55 minutes** (token ~1h;
   refreshes 5 min early). On `401` from Inter on a PIX op, `api` force-refreshes
   and retries the op once.
5. **No SSM token store.** The `/ctech-wallet/{env}/inter/oauth-token` SSM
   parameter from the prior draft is dropped. `pix-gateway` still reads its mTLS
   cert/key from SSM (needed for mTLS) — only the *bearer token* leaves SSM.

## Current state this builds on

- `pix-gateway/internal/inter/inter.go` — `InterClient` + in-memory
  `tokenManager` (line 266). After this change `tokenManager` is **deleted**;
  `do`/`doIdem` take the token as a parameter.
- `pix-gateway/internal/config/config.go` — carries `InterClientID`,
  `InterClientSecret`, `InterBaseURL`, `InterPixKey`.
- `api/internal/pix/rpc_types.go` + `pix-gateway/internal/rpc/types.go` — the
  Lambda wire contract. Needs: the `oauth_token` field on the shared request
  envelope, and one new op `GetToken`.
- `api` already uses Valkey (Redis) for `lock` and `ws` — the refresh
  single-flight lock reuses it.

## Components

### 1. Wire contract change (both sides)

Add `oauth_token` to the shared request envelope so **every** op carries it:

```go
// api/internal/pix/rpc_types.go  — rpcRequest
type rpcRequest struct {
Op         rpcOp           `json:"op"`
OAuthToken string          `json:"oauth_token"` // api-injected; never logged
Payload    json.RawMessage `json:"payload"`
}

// pix-gateway/internal/rpc/types.go — Request
type Request struct {
Op         Op              `json:"op"`
OAuthToken string          `json:"oauth_token"`
Payload    json.RawMessage `json:"payload"`
}
```

Add one new op (status-only result, returns the token):

```go
// api
opGetToken rpcOp = "GetToken"
// pix-gateway
OpGetToken Op = "GetToken"
```

`GetToken` request: none. Response payload: `{"token":"...","expires_in":3600}`.
`api` trusts `expires_in` (recomputes its own expiry as `now + expires_in - 30s`,
so clock skew can't persist a bad value).

### 2. `pix-gateway` — pure transport

- **`InterClient` drops `tokenManager`.** `do(ctx, method, path, body, out, token string)` sets
  `Authorization: Bearer <token>` and performs the call. `doIdem` gains a `token`
  param too (used for the payout idempotency header). `NewInterClient` no longer
  builds a `tokenManager`.
- **All 7 `PixClient` methods** take the token from the RPC `Request.OAuthToken`
  and forward it to `do`/`doIdem`. No SSM token read anywhere.
- **`Ping`** validates the passed token is non-empty (it no longer exercises a
  fetch); if a liveness probe is still wanted, pix-gateway can do a cheap
  `GET /pix/v2/cob` with the passed token — but the token itself is supplied by
  `api`.
- **New op `GetToken`** handler: fetch from Inter using pix-gateway's own creds
  (`/oauth/v2/token`, `client_credentials`, the existing `tokenScope`), return
  `{token, expires_in}`. This is the *only* place in `pix-gateway` that talks to
  Inter's token endpoint. No SSM write.

### 3. `api` — token owner (lifecycle)

**New `InterTokenManager`** (`api/internal/pix/intertoken.go`), an fx provider,
holding `*lambda.Client` + the pix-gateway function name (reuses the existing
Lambda invoke plumbing).

- **In-memory cache:** `token string`, `expiry time.Time`.
- **`Get(ctx) (string, error)`:**
    1. If cached token fresh (`expiry - now > 30s`) → return it (no I/O).
    2. Else acquire Valkey lock `inter:token:refresh` (reuse `lock` package; short
       TTL ~60s, retry/backoff). Under the lock: invoke pix-gateway `GetToken`,
       cache token + expiry, release lock, return. If the lock can't be acquired,
       best-effort invoke `GetToken` directly (don't block PIX traffic on lock
       unavailability — a stale-but-usable token still works).
- **Startup (`fx.Lifecycle.OnStart`):** best-effort `Get` to prime the cache so
  first traffic never does a synchronous fetch.
- **Background loop (`fx.Lifecycle.OnStart` goroutine):** every 55 min, call
  `Get` to force a proactive refresh. Long-running goroutine at process scope
  (api is a server) — distinct from the rule against goroutines *inside request
  handlers*.
- **Never logs the token.** Any request-logging middleware must redact
  `oauth_token`.

**`LambdaPixClient` injection.** Before each `Invoke`, the client calls
`tokenMgr.Get(ctx)` and sets `req.OAuthToken`. On a `401`/`statusError` whose
code is 401, it force-refreshes (`Get` with a forced flag) and retries the op
**once**.

## Data flow

```
[api InterTokenManager, every 55m, Redis-locked]
        │  lambda.Invoke(GetToken)
        ▼
[pix-gateway GetToken handler]
        │  GET Inter /oauth/v2/token  (≤1 call per refresh; api holds the value)
        ▼
   Inter ──returns bearer──▶ api caches in memory

[any PIX op, e.g. CreateCharge]
   api: tok = tokenMgr.Get(ctx)
        │  lambda.Invoke(op, payload, oauth_token=tok)   (no SSM, no KMS)
        ▼
   pix-gateway: do(..., tok)  →  Authorization: Bearer <tok>  →  Inter PIX API
```

**Cost math:** Inter is hit only by the single Redis-locked api refresher (~1 /
55 min). `pix-gateway` makes **zero** SSM/KMS calls for tokens — the bearer
rides the existing `Invoke` payload. No DynamoDB lock table. Far below 5/min and
no per-call AWS bill.

## Error handling

- **`GetToken` fails (Inter `429`/throttle):** api's Valkey-locked refresh backs
  off and retries inside the lock; concurrent callers wait on the lock, then read
  the cache once it lands. At most one caller hits Inter.
- **Token stale on a PIX op (Inter `401`):** `LambdaPixClient` force-refreshes
  and retries the op once. If the retry also `401`s, surface the existing opaque
  PixClient error (no change to error contract).
- **Valkey lock unavailable:** skip this refresh cycle; next PIX op with a stale
  token attempts a best-effort `GetToken` without the lock. Never block PIX
  traffic on lock failure.
- **Expiry skew:** `api` recomputes `expiry` from `expires_in` at fetch time
  (`now + expires_in - 30s`), so a skewed `expires_in` can't persist a bad value.

## Testing

| Change                           | Required tests                                                                                                                                    |
|----------------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------|
| `InterTokenManager`              | Unit — fresh cache short-circuits; stale → invokes `GetToken` under Valkey lock (mock lambda + mock redis); startup primes; background loop fires |
| `GetToken` handler               | Unit — returns token from Inter (mock Inter), no SSM write                                                                                        |
| Wire `oauth_token`               | Unit — every op payload carries it; pix-gateway `do` sets `Authorization: Bearer <tok>` (httptest Inter stub)                                     |
| `401` retry                      | Unit — stale token → 401 → forced refresh → retry succeeds once                                                                                   |
| Concurrent refresh single-flight | Integration — N concurrent refreshes → exactly 1 Inter token fetch                                                                                |
| pix-gateway transport            | Unit — `InterClient` no longer holds a `tokenManager`; methods forward the passed token                                                           |

## Financial Safety Invariants preserved

- `api` **never** calls Inter directly — the IPv4-egress boundary is untouched;
  `api` only invokes the Lambda (`GetToken` and the 7 PIX ops).
- The bearer is a credential: it travels only in the in-memory cache and the
  `Invoke` payload, never at rest, never logged. Same secrecy bar as the
  `inter/client-secret` param, with a smaller attack surface (no SSM copy).
- No money movement is added or changed — this is purely token plumbing around
  existing PIX calls.

## Cross-project impact

- **api ↔ pix-gateway:** new `GetToken` op; `oauth_token` field on the shared
  request envelope (all existing ops). No new network path; `lambda:InvokeFunction`
  already granted.
- **cdk:**
    - **Remove** from the prior draft: SSM `PutParameter`/`GetParameter` + KMS
      grants for a token param, and the `ctech-wallet-{env}-inter-token-lock`
      DynamoDB table. None of these are needed now.
    - `api` role keeps `lambda:InvokeFunction` on the pix-gateway ARN.
    - `pix-gateway` role keeps `ssm:GetParameter` on its **mTLS cert/key** params
      (those stay in SSM); it needs no token SSM access.
- **ctech-account:** no code change.

## Operational steps (not code)

1. Confirm Inter's `ExpiresIn` (assumed ~3600s) against the live token response;
   tune the 55-min cadence / 30s floor if it differs.
2. No SSM seed for the token — it is never persisted. `api` primes it on
   startup via `GetToken`.
3. Verify request-logging redaction of `oauth_token` before traffic.

## Non-goals

- No change to the 7 `PixClient` method *semantics* or their business callers.
- No change to withdrawal reconciliation or the webhook path.
- No SSM/DynamoDB token store.

---

## Appendix — Inter API operation verification

Each `pix-gateway` operation was checked against the saved Inter references
(`tmp/inter/{auth,banking,pix}.html`). Base host is
`https://cdpj.partners.bancointer.com.br` (prod) /
`https://cdpj-sandbox.partners.uatinter.co` (sandbox); PIX paths hang off
`/pix/v2`, Banking off `/banking/v2`.

| Operation     | HTTP | Path (code)                           | Documented?                 | Evidence                                                                                                     |
|---------------|------|---------------------------------------|-----------------------------|--------------------------------------------------------------------------------------------------------------|
| OAuth token   | POST | `/oauth/v2/token`                     | ✅                           | auth.html: `…/oauth/v2 /token`                                                                               |
| CreateCharge  | PUT  | `/pix/v2/cob/{txid}`                  | ✅                           | pix.html: `PUT Criar cobrança imediata com txid`; path `/cob/{txid}` (×22)                                   |
| QueryCharge   | GET  | `/pix/v2/cob/{txid}`                  | ✅                           | pix.html: `GET Consultar cobrança imediata`                                                                  |
| DictLookup    | GET  | `/banking/v2/pix/dict/{pixKey}`       | ⚠️ **not in provided docs** | no `/dict` endpoint in banking.html or pix.html; "DICT" appears only as the charge `chave` field description |
| Transfer      | POST | `/banking/v2/pix`                     | ✅                           | banking.html: `POST Incluir Pagamento Pix`; path `/pix`                                                      |
| QueryTransfer | GET  | `/banking/v2/pix/{codigoSolicitacao}` | ✅                           | banking.html: `GET Consultar Pagamento Pix`; path `/pix/{codigoSolicitacao}` (×5)                            |
| Refund        | PUT  | `/pix/v2/pix/{e2eid}/devolucao/{id}`  | ✅                           | pix.html: `PUT Solicitar devolução` / `GET Consultar devolução`; path `/devolucao/{` (×13), `{e2eid}` (×20)  |
| Ping          | —    | (token only)                          | n/a                         | exercises the OAuth token                                                                                    |

**Open flags (verify before real money):**

1. **`DictLookup` endpoint missing from the saved docs.** The code targets
   `GET /banking/v2/pix/dict/{pixKey}` returning `titular.cpfCnpj` /
   `titular.nome`. Neither `banking.html` nor `pix.html` documents a `/dict`
   endpoint — DICT is mentioned only as the `chave` field of a cobrança. Confirm
   the exact DICT consultation path/response against Inter's live API (or a
   DICT-specific reference); this op backs the `withdraw-cpf-mismatch` invariant,
   so it must be correct.
2. **`pix.pagamento` scope not found in the docs.** `tokenScope` requests
   `cob.read cob.write pix.read pix.write banking pix.pagamento`. All scopes
   except `pix.pagamento` appear across the docs (`banking` appears in all
   three). Inter's real payout scope is `pix.pagamento`, but the saved references
   don't list it — confirm the exact scope string registered for the app, since a
   wrong scope yields `403 AcessoNegado` on payouts.
3. **Response field names still unverified.** `codigoSolicitacao`, `EndToEndId`,
   `pixCopiaECola`, `cpfCnpj` appear in the docs and match the code's JSON tags,
   but the full request/response shapes (especially `CreateCharge` body and
   `QueryCharge` `pix[].pagador.cpf`) should be confirmed against the sandbox
   before enabling real money — as the code's own comment warns.
