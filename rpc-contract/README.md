# rpc-contract

Shared Go wire contract between `api`'s `LambdaPixClient` and `pix-gateway`'s
**outbound** Lambda. Both modules import this package instead of hand‑mirroring
it (`types.go:1`), so there is a **single source of truth** for the PIX RPC
shape. `api` invokes the Lambda synchronously (`InvocationType: RequestResponse`)
with a fresh Inter bearer; `pix-gateway` performs the actual Inter mTLS/OAuth2
call and returns the result.

- **Module:** `gopkg.aoctech.app/wallet/rpc-contract` (`go.mod`).
- **Wired in:** `api/go.mod:24` and `pix-gateway/go.mod` via
  `replace … => ../rpc-contract`.
- **No TS counterpart** — this is Go‑only; the UI never speaks this contract.

## Envelope

```go
type Request  struct { Op Op `json:"op"`; OAuthToken string `json:"oauth_token"`; Payload json.RawMessage `json:"payload"` } // types.go:34
type Response struct { Error string `json:"error,omitempty"`; Payload json.RawMessage `json:"payload,omitempty"` }            // types.go:51
```

`OAuthToken` is supplied by `api`'s `InterTokenManager` on every call and
**must never be logged** (`types.go:31`). `Payload` is re‑decoded per `Op` into
the matching `*Args`.

## Operations (`Op`, `types.go:12`)

| Op | Args | Result | Notes |
|----|------|--------|-------|
| `CreateCharge` | `CreateChargeArgs{Txid, Amount, PayerHintCPF}` (`:56`) | `ChargeResult` (`:67`) | opens Inter immediate charge (cob) |
| `QueryCharge` | `QueryChargeArgs{Txid}` (`:62`) | `ChargeResult` | **source of truth** for a deposit |
| `Transfer` | `TransferArgs{PixKey, Amount, IdemKey}` (`:105`) | `TransferResult` (`:122`) | PIX payout |
| `QueryTransfer` | `QueryTransferArgs{IdemKey}` (`:111`) | `TransferResult` | reconciliation |
| `Refund` | `RefundArgs{E2EID, Amount, IdemKey}` (`:115`) | `TransferResult` | devolução |
| `Ping` | — | — | reachability, no money movement |
| `GetToken` | — | `GetTokenResult{Token, ExpiresIn}` (`:42`) | Inter OAuth2 bearer |

## Sentinels (`Response.Error`)

- `key_not_found` (`types.go:25`) ⇒ Inter `ErrKeyNotFound` — destination PIX key
  unregistered. `api` distinguishes this from a generic failure to refund
  immediately instead of leaving a withdrawal `processing`.
- `unauthorized` (`types.go:29`) ⇒ Inter rejected the bearer (401); `api`
  invalidates + force‑refreshes the token and retries once
  (`lambda_client.go:87`).

Any other non‑empty `Error` is an opaque bank/transport failure surfaced as
`problem.InternalServer`.

## Mirror / drift notes

- **No money constants here.** Fees and the sandbox conversion rate are
  **not** defined in `rpc-contract`. They are mirrored **api ↔ ui by hand**
  (see `api/ENDPOINTS.md` §5, §7/B18): `FEE_ABSOLUTE_MIN=100` and
  `SANDBOX_CREDITS_PER_CENTAVO=10` live in `api/internal/domain/wallet` and
  `ui/src/lib/utils/{fee,money}.ts`. Keep them in sync manually.
- **`DictLookupArgs`/`DictResult` (`types.go:94,99`) are vestigial.** There is
  **no `OpDictLookup`** in the `Op` enum and `PixClient` has no `DictLookup`
  method, so DICT same‑owner verification is not wired end‑to‑end (see B30/B36:
  `PixClient.DictAccount` in `api/internal/pix/client.go:64` is dead along this
  path). Documented divergence, not fixed here.

## Cross‑links

- Consumer: [`../api/README.md`](../api/README.md) (`internal/pix/lambda_client.go`)
- Producer: [`../pix-gateway/README.md`](../pix-gateway/README.md) (`cmd/outbound/main.go`)
