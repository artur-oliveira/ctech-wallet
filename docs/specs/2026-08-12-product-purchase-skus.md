# Generic digital-product purchase (PIX) — design

Date: 2026-08-12

## Goal

Let an M2M caller (first consumer: ctech-poker, selling premium table-reaction cosmetics) sell an
arbitrary fixed-price digital good for real PIX money, without wallet ever crediting anything
internally. This generalizes the existing sandbox-purchase flow
(`docs/specs/2026-07-30-m2m-sandbox-purchase-integration-design.md`) by dropping the one part that's
specific to selling sandbox credits: `ConfirmSandboxPurchase`'s ledger `Credit` call. A skin, an
avatar frame, a premium reaction — none of them are sandbox currency, so none of them should touch
`WalletRepository`, a wallet lock, or the ledger at all. This is a product sale (CDC), same
enforcement posture as the sandbox-purchase sale: no KYC gate, charged directly to CTech's pooled
account, never a wallet-to-wallet transfer, never Asaas custody.

## Non-goals

- No refund-eligibility logic here. "Was this ever used?" is the caller's domain fact (poker knows
  whether a reaction was ever sent), not wallet's. Wallet trusts the M2M caller's refund request the
  same way it trusts `game-cashout` — the caller already validated its own domain rule before
  calling. Unlike `RefundSandboxPurchase`, there is no `AnyDebitSince` usage check on this path.
- No catalog UI or admin write path. SKUs are a fixed Go table, same as `sandboxSKUCatalog` — edit
  the table, ship a deploy, to add/reprice a product.

## Domain model (`internal/domain/wallet`)

```go
// ProductSKU is one purchasable fixed-price digital good. Price is fixed,
// server-side, never client- or M2M-caller-supplied — same "never trust a
// money-shaped number from outside this table" posture as SandboxSKU.
type ProductSKU struct {
ID         string `json:"id"`
PriceCents int64  `json:"price_cents"`
}

var productSKUCatalog = map[string]ProductSKU{
// First consumer: poker's 6 premium reactions (see
// ctech-poker/docs/specs/2026-08-12-premium-reactions.md), 2 emoji + 4
// targeted objects — SKU ID is poker-chosen, price is wallet-owned.
"poker_reaction_cold":   {ID: "poker_reaction_cold", PriceCents: 100},
"poker_reaction_fire":   {ID: "poker_reaction_fire", PriceCents: 100},
"poker_reaction_poop":   {ID: "poker_reaction_poop", PriceCents: 500},
"poker_reaction_rofl":   {ID: "poker_reaction_rofl", PriceCents: 500},
"poker_reaction_knife":  {ID: "poker_reaction_knife", PriceCents: 500},
"poker_reaction_turtle": {ID: "poker_reaction_turtle", PriceCents: 500},
}

func ListProductSKUs() []ProductSKU
func ProductSKUByID(id string) (ProductSKU, bool)
```

```go
// ProductPurchase mirrors SandboxPurchase's shape minus everything about
// credits: no CreditSK, no CreditsGranted, no EnsureSandboxWallet, no ledger
// entry type. Statuses: pending → confirmed | refunded | expired. There is no
// refund_pending stage — see "Refund" below, the refund call has nothing to
// resume except the PIX provider call itself, which is idempotent on E2EID.
type ProductPurchase struct {
PurchaseID       string
UserID           string
SKU              string
AmountExpected   int64
Status           string // pending | confirmed | refunded | expired
RequestingClient string // caller's AZP, e.g. "poker"
RequestHash      string
E2EID            string
WebhookStatus    string
CreatedAt        string
UpdatedAt        string
TTL              int64
}
```

New table `wallet_product_purchases`, pk `purchase_id` — same shape as `wallet_sandbox_purchases`
(`repositories.NewBase(db, cfg, wallet.TableProductPurchases)`), own repository
`ProductPurchaseRepository` (`PutIfAbsent`, `Get`, `TransitionStatus`, `ListPendingOlderThan`).

## Service (`internal/services/product_purchase.go`)

Mirrors `sandbox_purchase.go` with the credit step removed:

- `PurchaseProductDirect(ctx, userID, sku, idemKey, requestingClient) (*ProductPurchase, *pix.Charge, error)`
  — same deterministic txid derivation as `sandboxPurchaseTxID` (rename to a shared
  `productTxID`/`directSaleTxID` helper both flows call, prefix `"prdp"` so pix-gateway's webhook
  dispatch can route it distinctly from `"sbxp"`), same `PutIfAbsent` idempotent-reservation-before-
  charge pattern, same idempotency-conflict-on-mismatch replay handling.
- `ConfirmProductPurchase(ctx, txid, sweep bool) error` — re-queries the PIX charge (never trusts a
  webhook body — Invariant #11), validates `charge.Amount == p.AmountExpected` (ALARM log on
  mismatch, same as sandbox), then **just** `TransitionStatus(pending → confirmed)` and dispatches the
  M2M webhook. No wallet lock, no `Credit`, no ledger entry — this is the entire generalization.
- `RefundProductPurchase(ctx, userID, purchaseID, idemKey, requestingClient) (*ProductPurchase, error)`
  — ownership check identical to `RefundSandboxPurchase` (wrong caller/user → 404, never 403). No
  usage check (see Non-goals). Calls `s.pix.Refund(ctx, p.E2EID, p.AmountExpected, "product_refund#"+purchaseID)`,
  then `TransitionStatus(confirmed → refunded)`. Replay-safe: already-refunded returns the row
  unchanged.
- `GetProductPurchase(ctx, purchaseID, requestingClient) (*ProductPurchase, error)` — same
  ownership-scoped read as `GetSandboxPurchase`.
- `SweepPendingProductPurchases` / no refund-pending sweep needed (refund has no pending stage to
  resume — a failed provider call simply leaves status `confirmed` and the caller's refund request
  is safe to retry as-is, `s.pix.Refund` is idempotent on `E2EID`+idempotency key).

## Endpoints (`internal/api/v1/m2m_product_purchase.go`, `router.go`)

All under `/v1.0/internal/wallet/product-purchase`, gated by
`middleware.RequireScope(ScopeWalletProductPurchase)`.

| Route              | Body                              | Notes                                                         |
|--------------------|-----------------------------------|---------------------------------------------------------------|
| `GET /skus`        | —                                 | `wallet.ListProductSKUs()`                                    |
| `POST /`           | `{user_id, sku, idempotency_key}` | Returns `purchase_id`, PIX QR, status, `expires_at`           |
| `GET /:id`         | —                                 | Status poll — caller's mandated confirm-before-unlocking path |
| `POST /:id/refund` | `{user_id, idempotency_key}`      | No usage check — see Non-goals                                |

## Notify-back

Reuses the existing per-client webhook registration (`/ctech-wallet/{env}/m2m-clients` SSM blob,
`WalletService.m2mClients`) and delivery/retry machinery (`markM2MWebhook`, `HeaderM2MWebhookSignature`)
unchanged. `m2mWebhookPayload` (`internal/services/m2m_webhook.go`) gains a `Kind string
\`json:"kind,omitempty"\`` field — today's dispatch sets nothing (implicitly "sandbox" to every
existing receiver); the sandbox path is updated to set `Kind: "sandbox"` explicitly and a new
`dispatchM2MWebhookProduct` sets `Kind: "product"` (its own payload, no `CreditsGranted`, since a
product purchase never has one). A receiver registered for both flows (poker will be) can route the
callback on `Kind` without inspecting the SKU namespace.

## New scope

`ScopeWalletProductPurchase = "internal:wallet:product-purchase"` (`internal/middleware/scope.go`) —
deliberately its own scope, not reused from `ScopeWalletSandboxPurchase`: this charges money and
confirms a sale with **no ledger effect whatsoever**, a materially different blast radius than a
scope that can mint sandbox credits.

## Error handling

- Wallet unreachable on create → existing breaker/retry wrapper, 503 to caller.
- Amount mismatch on confirm → ALARM log, purchase stays `pending` for manual reconciliation (same
  posture as the sandbox flow — this is real money, never silently resolved).
- Refund of an already-refunded purchase → idempotent 200 with the row unchanged.
- Refund of a not-yet-confirmed purchase → `409 Conflict`.

## Testing

- `services/product_purchase_test.go` — mirrors `sandbox_purchase_test.go` minus every
  credit/ledger/wallet-lock assertion: purchase→confirm→refund happy path, idempotent replay on
  create, amount-mismatch-on-confirm stays pending, refund-without-confirm rejected, cross-client
  ownership returns 404 not 403.
- `repositories/product_purchases_test.go` — mirrors `sandbox_purchases_test.go`'s `PutIfAbsent`/
  `TransitionStatus` conditional-write tests.
- `api/v1/m2m_product_purchase_test.go` — mirrors `m2m_sandbox_purchase_test.go`'s scope-gating and
  ownership-404 tests.

## Manual provisioning (outside this change)

- Grant `internal:wallet:product-purchase` to poker's M2M client in the OAuth catalog
  (`ctech-account/api/internal/scopes/catalog.go`) — a config/data change in a sibling repo, not code
  here.
