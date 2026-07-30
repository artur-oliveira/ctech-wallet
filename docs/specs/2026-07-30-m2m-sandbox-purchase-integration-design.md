# M2M sandbox-purchase integration (e.g. ctech-poker)

Lets an M2M client (`client_credentials`, scope `internal:wallet:sandbox-purchase`) open, poll, and refund a
direct PIX→sandbox-credits sale on a user's behalf — same underlying flow as the user-facing
`/wallet/sandbox/purchases` routes (`docs/specs/2026-07-12-three-wallet-topology-design.md` §9.1/§9.3), just
caller-initiated: the client shows its own UI and QR code instead of redirecting the user into the wallet's UI.

## Endpoints (`internal/api/v1/m2m_sandbox_purchase.go`, `router.go`)

All under `/v1.0/internal/wallet/sandbox-purchase`, gated by `middleware.RequireScope(ScopeWalletSandboxPurchase)`.

| Route | Body | Notes |
|-------|------|-------|
| `POST /` | `{user_id, sku, idempotency_key}` | Returns `purchase_id`, PIX QR (`pix_copia_e_cola`/`qr_code_base64`), status |
| `GET /:id` | — | Status poll — the client's mandated confirm-before-credit path |
| `POST /:id/refund` | `{user_id, idempotency_key}` | Mirrors the user-facing refund |

## Ownership & idempotency isolation

`SandboxPurchase.RequestingClient` stores the caller's `AZP`. The purchase ID is namespaced by client
(`"sbxp#"+client+"#"+userID+"#"+idemKey` vs. the user-direct `"sbxp#"+userID+"#"+idemKey`) so two different M2M
clients — or an M2M client and the user-direct route — can never collide on the same `(userID, idemKey)` pair.
`GetSandboxPurchase`/`RefundSandboxPurchase` reject a purchase whose `RequestingClient` doesn't match the caller
with `404` (never `403`) — a purchase belonging to a different client does not exist as far as the caller is
concerned.

## Notify-back: how the wallet finds the caller's webhook URL

Flow: `Poker → Wallet (open purchase) → PIX charge created → user pays → Inter → wallet's inbound PIX
webhook → re-query Inter (Invariant #11) → credit sandbox wallet → wallet notifies Poker's webhook`.

The caller's URL is **never a per-request parameter** — a client-supplied callback URL is an SSRF vector (an M2M
token could point the wallet's outbound call at an arbitrary host), the same reasoning that keeps the PIX
withdrawal destination pinned to the caller's own KYC CPF rather than a request field. Instead it is a
**per-client registration**, looked up by `AZP` at confirm/refund time — the same "admin sets it directly, no API
write path" posture as the `wallets` table's `fee_bps`/`min_deposit` overrides.

Registration lives in one SSM SecureString JSON blob:

```
/ctech-wallet/{env}/m2m-clients  →  {"poker": {"WebhookURL": "https://...", "HMACSecret": "..."}}
```

Loaded once at process startup (`internal/secrets/ssm.go` `LoadM2MClients`, wired in `app.go`/`cmd/reconcile`) into
`WalletService.m2mClients map[string]M2MClient`. An unset parameter is a valid "no M2M client registered yet"
state, not a startup failure — `LoadM2MClients` is the one `secrets.Store` loader that tolerates
`ParameterNotFound`.

## Delivery

`dispatchM2MWebhook` (`internal/services/m2m_webhook.go`) POSTs a JSON body (purchase id, user id, sku, status,
amounts) to the registered URL, signed `X-Wallet-Signature: sha256=hex(HMAC-SHA256(body, client's secret))` — JSON
+ HMAC, not protobuf: no other wallet integration uses protobuf, and it would add a codegen dependency for the
receiver with no benefit over a scheme every other integration already uses.

**Receiver contract (mirrors Invariant #11):** the callback body is a wake-up signal only. The receiver MUST
`GET /internal/wallet/sandbox-purchase/:id` to confirm the purchase before crediting its own currency — never
credit off the callback body directly, exactly as the wallet itself never trusts Inter's/Asaas's webhook body for
money movement.

Dispatch is synchronous (inline, ≤5s timeout) at the end of `ConfirmSandboxPurchase`/`RefundSandboxPurchase` and
never fails the caller's request — the ledger mutation already committed. Outcome is recorded on the purchase row
itself (`SandboxPurchase.WebhookStatus`: `""` → `delivered`/`failed`).

## Retry

Failed deliveries are retried by the reconcile job (`cmd/reconcile`), not a dedicated queue — reusing the existing
sweep shape (`SweepPendingSandboxPurchases`'s sibling). `GSISandboxPurchaseWebhookStatus` on
`wallet_sandbox_purchases` (`webhook_status = "failed"`) backs `WalletService.RetryFailedM2MWebhooks`, run every
reconcile cycle alongside the other sweeps. A webhook failure never raises the operational alarm
`cmd/reconcile` exits non-zero on (Invariant #12 is about money in limbo — a lost notification is not; the
receiver's own poll path covers it either way).

## New scope

`ScopeWalletSandboxPurchase = "internal:wallet:sandbox-purchase"` (`internal/middleware/scope.go`) — deliberately
its own scope, not reused from `ScopeWalletCredit`: this creates a PIX charge and a purchase record, not a bare
ledger credit.
