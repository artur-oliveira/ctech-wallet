# rpc-contract

Small Go module (`gopkg.aoctech.app/wallet/rpc-contract`) holding the **money constants shared by `api` (Go) and `ui`
(TypeScript)**. It is wired into `api` with `replace gopkg.aoctech.app/wallet/rpc-contract => ../rpc-contract`.

## Money constants (`money.json`, `money.go`)

`money.json` is the canonical source; `money.go` keeps compile-time Go constants for the same values.

| JSON key | Go const | Value | Meaning |
|----------|----------|-------|---------|
| `sandbox_credits_per_centavo` | `SandboxCreditsPerCent` | 100 | Sandbox credits per centavo (R$ 1,00 = 10000 credits); display and Poker pack pricing only |
| `max_amount_cents` | `MaxAmountCents` | 100000000 | Ceiling on any single inbound amount (R$ 1.000.000,00); used by `api` as `MaxInboundAmount` |

Drift is caught by tests on both sides: `money_test.go` (Go consts vs JSON) and
`ui/src/lib/utils/money-contract.test.mjs` (UI constants in `ui/src/lib/utils/money-constants.ts` vs JSON). There are no fee
constants anywhere (`docs/specs/2026-08-16-withdrawal-fee-removal.md`).

## `types.go` is a leftover

`types.go` still declares the old Pix gateway Lambda RPC envelope (`Op`, `Request`, `Response`, `CreateChargeArgs`,
`TransferArgs`, ...). The gateway, its Lambdas and `api`'s `LambdaPixClient` no longer exist (Pix goes through `ctech-billing`),
and nothing imports those types: `api` imports only the money constants. Treat the file as dead code to be deleted in a
code change; do not build on it.

## Cross-links

- Consumer: `api/internal/domain/wallet/model.go` (`MaxInboundAmount`, `SandboxCreditsPerCent`)
- UI mirror: [`../ui/README.md`](../ui/README.md)
