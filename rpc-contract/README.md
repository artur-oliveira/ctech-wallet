# rpc-contract

Not a Go module any more: just `money.json`, the **money constants shared by `api` (Go) and `ui` (TypeScript)**.
`api` mirrors them as compile-time constants in `api/internal/domain/wallet/model.go`
(`MaxInboundAmount`, `SandboxCreditsPerCent`); the old Go module, its `replace` directive and the leftover Pix
gateway RPC types (`types.go`) were removed.

## Money constants (`money.json`)

| JSON key | Go const | Value | Meaning |
|----------|----------|-------|---------|
| `sandbox_credits_per_centavo` | `SandboxCreditsPerCent` | 100 | Sandbox credits per centavo (R$ 1,00 = 10000 credits); display and Poker pack pricing only |
| `max_amount_cents` | `MaxInboundAmount` | 100000000 | Ceiling on any single inbound amount (R$ 1.000.000,00) |

Drift is caught by tests on both sides: `TestMoneyConstantsMatchJSON` (`api/internal/domain/wallet/model_test.go`, Go
consts vs JSON) and `ui/src/lib/utils/money-contract.test.mjs` (UI constants in `ui/src/lib/utils/money-constants.ts` vs
JSON). There are no fee constants anywhere (`docs/specs/2026-08-16-withdrawal-fee-removal.md`).

## Cross-links

- Consumer: `api/internal/domain/wallet/model.go`
- UI mirror: [`../ui/README.md`](../ui/README.md)
