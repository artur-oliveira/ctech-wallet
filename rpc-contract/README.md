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

## Asaas BaaS custody operations (`types.go:26-37`)

Added for the per-user custody subaccount migration
(`docs/plans/2026-07-30-asaas-baas-implementation-plan.md`). Same envelope as
the Inter ops above — `OAuthToken` carries the Asaas credentials for these too
and MUST NOT be duplicated into `Payload`.

| Op | Args | Result | Notes |
|----|------|--------|-------|
| `AsaasCreateAccount` | `AsaasCreateAccountArgs` (`:143`) | `AsaasAccountResult` (`:159`) | creates the user's subaccount |
| `AsaasUploadDocument` | `AsaasUploadDocumentArgs` (`:167`) | — | rejected if the pending document carries an `OnboardingURL` (must go through that flow instead) |
| `AsaasCreateStaticPixKey` | `AsaasCreateStaticPixKeyArgs{}` (`:172`) | `AsaasPixAddressKeyResult` (`:174`) | the subaccount's EVP key |
| `AsaasCreatePixQRCode` | `AsaasCreatePixQRCodeArgs` (`:179`) | `AsaasQRCodeResult` (`:188`) | deposit QR / verification-fee QR |
| `AsaasQueryPayment` | `AsaasQueryPaymentArgs` (`:195`) | `AsaasPaymentResult` (`:199`) | **source of truth** for a deposit, mirrors `QueryCharge` |
| `AsaasQueryCustomer` | `AsaasQueryCustomerArgs` (`:207`) | `AsaasCustomerResult` (`:220`) | |
| `AsaasRefundPayment` | `AsaasRefundPaymentArgs` (`:214`) | — | refunds the payment that received the money |
| `AsaasCreateTransfer` | `AsaasCreateTransferArgs` (`:226`) | `AsaasTransferResult` (`:234`) | PIX payout from a subaccount, mirrors `Transfer` |
| `AsaasQueryTransfer` | `AsaasQueryTransferArgs` (`:241`) | `AsaasTransferResult` | reconciliation, mirrors `QueryTransfer` |
| `AsaasQueryAccountBalance` | `AsaasQueryAccountBalanceArgs{}` (`:245`) | `AsaasBalanceResult` (`:247`) | |
| `AsaasQueryAccountStatus` | `AsaasQueryAccountStatusArgs{}` (`:254`) | `AsaasAccountStatusResult` (`:268`) | approved only when `General == "APPROVED"`; the other three fields say which step is outstanding and must never be combined into an approval decision of their own |
| `AsaasListPendingDocuments` | `AsaasListPendingDocumentsArgs{}` (`:279`) | `AsaasPendingDocumentsResult` (`:299`) | |

## Sentinels (`Response.Error`)

- `key_not_found` (`types.go:25`) ⇒ Inter `ErrKeyNotFound` — destination PIX key
  unregistered. `api` distinguishes this from a generic failure to refund
  immediately instead of leaving a withdrawal `processing`.
- `unauthorized` (`types.go:29`) ⇒ Inter rejected the bearer (401); `api`
  invalidates + force‑refreshes the token and retries once
  (`lambda_client.go:87`).
- `transfer_not_found` (`types.go:52`) ⇒ a provider query succeeded and proved
  no transfer exists for the supplied external reference — deliberately
  distinct from a query/transport error: only this result permits
  resubmission.

Any other non‑empty `Error` is an opaque bank/transport failure surfaced as
`problem.InternalServer`.

## Mirror / drift notes

- **No money constants here.** The sandbox conversion rate is **not** defined in
  `rpc-contract`. It is mirrored **api ↔ ui by hand** (see `api/ENDPOINTS.md`
  §5, §7/B18): `SANDBOX_CREDITS_PER_CENTAVO=10` lives in
  `api/internal/domain/wallet/model.go` and `ui/src/lib/utils/money.ts`. Keep
  them in sync manually. There are no fee constants on either side any more —
  see `docs/specs/2026-08-16-withdrawal-fee-removal.md`.
- **`DictLookupArgs`/`DictResult` (`types.go:94,99`) are vestigial.** There is
  **no `OpDictLookup`** in the `Op` enum and `PixClient` has no `DictLookup`
  method, so DICT same‑owner verification is not wired end‑to‑end (see B30/B36:
  `PixClient.DictAccount` in `api/internal/pix/client.go:64` is dead along this
  path). Documented divergence, not fixed here.

## Cross‑links

- Consumer: [`../api/README.md`](../api/README.md) (`internal/pix/lambda_client.go`)
- Producer: [`../pix-gateway/README.md`](../pix-gateway/README.md) (`cmd/outbound/main.go`)
