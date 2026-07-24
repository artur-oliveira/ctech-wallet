# AGENTS.md — pix-gateway (ctech-wallet-pix-gateway)

Context for AI agents working on the PIX gateway. Identical intent to
[`CLAUDE.md`](CLAUDE.md); this is the agent-facing summary.

The **only** component allowed to talk to Banco Inter's PIX/Banking APIs.
`api` never opens an mTLS connection — it invokes these Lambdas over
`lambda:Invoke` with an Inter OAuth bearer per call. This gateway fronts
**real money movement** at Inter; the 12 Financial Safety Invariants in the
repo root [`../CLAUDE.md`](../CLAUDE.md) still bind (webhook is never the
source of truth — Invariant #11 — and the CPF gate is anti-fraud only).

## Two functions

- **`cmd/outbound`** — actual Inter calls. Dispatches on `rpc-contract` `Op`
  (`cmd/outbound/main.go:89`); returns `Response{Error,Payload}` with
  sentinels `key_not_found`/`unauthorized` (`:199`); scrubs the bearer from
  logs (`scrubPayload`, `:218`).
- **`cmd/webhook`** — receives Inter's PIX callback, validates it, asks `api`
  to re-derive + credit. Carries **no Inter credentials at all**
  (`cmd/webhook/main.go:1`).

## Rules (MUST follow)

- **Never** log the Inter bearer — it travels in `OAuthToken`
  (`cmd/outbound/main.go:68`, `scrubPayload:218`).
- **mTLS + OAuth2 client_credentials** only (`internal/inter/inter.go:67-92`,
  `tokenScope:58`, `pathToken:53`). Min TLS 1.2, 20s timeout.
- Endpoint paths/JSON field names follow Inter's v2 API — **confirm each
  shape against Inter's current reference + sandbox before enabling real
  money** (`inter.go:27-30`).
- **Money is integer centavos** internally; convert to Inter R$ strings via
  `centavosToReais`/`reaisToCentavos` (`inter.go:386-408`).
- Secrets are SSM SecureString only (`internal/secrets/ssm.go`) — none hit
  disk or logs. Inter mTLS/OAuth/webhook secrets live **here**, never in `api`.

## Webhook (Invariant #11 — never source of truth)

1. Inter POSTs to mTLS APIGW `pix.wallet.aoctech.app` (method enforced by
   APIGW, not code).
2. **B35:** auth is `?hmac=` query param constant-time-compared vs SSM
   secret (`cmd/webhook/main.go:114`) — **not** a body signature.
3. For each txid, calls `api` `POST /v1.0/internal/pix/confirm-deposit`
   (scope `internal:wallet:confirm-deposit`) with the payer CPF/name from
   the webhook body; `api` re-queries Inter before crediting.

## Known divergences (documented, NOT fixed here)

- **B30/B36** — `DictAccount` (`internal/inter/client.go:60`,
  `api/internal/pix/client.go:64`) + `DictLookupArgs`/`DictResult`
  (`rpc-contract/types.go:94,99`) exist, but **no `OpDictLookup`** and no
  `PixClient.DictLookup`. DICT same-owner verification is not wired
  end-to-end; `WithdrawCPFMismatch` same-owner matching is unimplemented.
- **B35** — webhook secret in `?hmac=` query string. Not a body signature.
- — `PayerHintCPF` accepted + forwarded by `api` but **never sent** to
  Inter (`CreateCharge` builds only `calendario/valor/chave`, `inter.go:155`).
  Dead in the production path.
- — `config.go:27` comment says `internal:pix:confirm-deposit` (**typo**;
  real scope `internal:wallet:confirm-deposit`). Code uses the correct string.

## Cross-links

- Consumer: [`../api/README.md`](../api/README.md) (`internal/pix/lambda_client.go`)
- Wire contract: [`../rpc-contract/README.md`](../rpc-contract/README.md)
- IAM / deploy: [`../cdk/README.md`](../cdk/README.md)
- Operations: [`../OPERATIONS.md`](../OPERATIONS.md) §4
