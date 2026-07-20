# CLAUDE.md — pix-gateway (ctech-wallet-pix-gateway)

AWS Lambda pair — the **only** component allowed to talk to Banco Inter's
PIX/Banking APIs. The `api` service never opens an mTLS connection; it
invokes these Lambdas over `lambda:Invoke` with an Inter OAuth bearer
supplied per call.

> This gateway is the front door to **real money movement** at Inter. The 12
> Financial Safety Invariants in the repo root [`../CLAUDE.md`](../CLAUDE.md)
> still bind (the webhook is never the source of truth — Invariant #11 — and
> the CPF gate is anti-fraud only).

## Two functions

- **`cmd/outbound`** — performs the actual Inter calls (CreateCharge,
  QueryCharge, Transfer, QueryTransfer, Refund, Ping, GetToken). Dispatches
  on the `rpc-contract` `Op` enum (`cmd/outbound/main.go:89`); returns
  `Response{Error,Payload}` with sentinels `key_not_found`/`unauthorized`
  (`:199`); scrubs the bearer from logs (`scrubPayload`, `:218`).
- **`cmd/webhook`** — receives Inter's PIX callback, validates it, and asks
  `api` to re-derive + credit the deposit. Carries **no Inter credentials at
  all** (`cmd/webhook/main.go:1`).

## Wire contract

Both Lambdas speak `rpc-contract` ([`../rpc-contract/README.md`](../rpc-contract/README.md)):
`api` sends `Request{Op, OAuthToken, Payload}`, `pix-gateway` returns
`Response{Error, Payload}`. The Inter bearer travels in `OAuthToken` and
**must never be logged** (`cmd/outbound/main.go:68`, `scrubPayload:218`).

## Outbound — Inter client (`internal/inter`)

- **mTLS:** `tls.X509KeyPair` from the SSM-loaded keypair, `MinVersion:
  TLS12`, 20s timeout (`inter.go:67-92`).
- **OAuth2 client_credentials:** `POST {base}/oauth/v2/token` (`pathToken`,
  `inter.go:53`) with scope
  `cob.read cob.write pix.read pix.write pagamento-pix.read pagamento-pix.write`
  (`tokenScope`, `inter.go:58`). The **Inter bearer is NOT cached** — it is
  passed per call by `api` in `OAuthToken` and seeded into ctx
  (`internal/inter/bearer.go`). Only the **Inter OAuth client secret** is
  cached (`secret()` lazy-loads from SSM once, `inter.go:137`).
- **Endpoints (centralized constants, `inter.go:52-60`):**
  | Op | Inter path |
  |----|-----------|
  | CreateCharge | `PUT /pix/v2/cob/{txid}` (expiry 300s, `chave` = `INTER_PIX_KEY`) |
  | QueryCharge | `GET /pix/v2/cob/{txid}` |
  | Transfer (payout) | `POST /banking/v2/pix` (`x-id-idempotente` = idemKey) |
  | QueryTransfer | `GET /banking/v2/pix/{idemKey}` (any error ⇒ `NAO_ENCONTRADO`) |
  | Refund (devolução) | `PUT /pix/v2/pix/{e2eid}/devolucao/{idemKey}` |
- **QR code:** Inter returns only the EMV string; `qrPNG` generates the
  base64 PNG (`inter.go:172` + `internal/inter/qr.go`). A render miss is
  logged and left empty — the EMV text still reaches the client.
- **Money:** integer **centavos** internally; `centavosToReais` /
  `reaisToCentavos` convert to Inter's R$ decimal strings (`inter.go:386-408`).

## Webhook — deposit wake-up (`cmd/webhook`)

1. Inter POSTs to the mTLS-verified API Gateway custom domain
   `pix.wallet.aoctech.app`. The HTTP method is **not** checked in code —
   API Gateway routing enforces it (`cmd/webhook/main.go:1`).
2. **Auth:** constant-time compare of `?hmac=` query param against the SSM
   webhook secret (`subtle.ConstantTimeCompare`, `main.go:114`). Mismatch ⇒
   `401`. **B35:** the secret travels in the **URL query string** (Inter
   echoes it back on every callback; it is *not* a body signature —
   `internal/secrets/ssm.go`).
3. Parses `txid`(s) from the body (supports the `pix[]` list or a bare
   detail object, `main.go:123`).
4. For each txid, calls `api`'s `POST /v1.0/internal/pix/confirm-deposit`
   with scope `internal:wallet:confirm-deposit` and the **payer CPF/name**
   from the webhook body (`internal/walletclient/walletclient.go:23`,`:55`).
   `api` then re-queries Inter itself before crediting (Invariant #11).
5. Any `ConfirmDeposit` error ⇒ `500` so Inter **retries** the whole
   payload (idempotent per txid, `main.go:149`).

> **CPF anti-fraud:** Inter's charge re-query no longer returns the payer, so
> the webhook body is the **only** source of payer CPF/name. `api` persists
> it and uses it only for the CPF-match gate — never to authorize crediting.

## Secrets — SSM SecureString (`internal/secrets/ssm.go`)

All read with `WithDecryption: true`; none hit disk or logs.

| Parameter | Consumed by |
|-----------|-------------|
| `/ctech-wallet/{env}/inter/mtls-cert` | outbound (mTLS) |
| `/ctech-wallet/{env}/inter/mtls-key` | outbound (mTLS) |
| `/ctech-wallet/{env}/inter/client-secret` | outbound `GetToken` (lazy-cached) |
| `/ctech-wallet/{env}/pix-gateway/client-secret` | webhook (own M2M token) |
| `/ctech-wallet/{env}/inter/webhook-secret` | webhook (hmac compare) |

## Known divergences (documented, NOT fixed here)

| ID | Where | Status |
|----|-------|--------|
| **B30/B36** | `DictAccount` (`internal/inter/client.go:60`, `api/internal/pix/client.go:64`) and `DictLookupArgs`/`DictResult` (`rpc-contract/types.go:94,99`) exist, but there is **no `OpDictLookup`** in the `Op` enum and `PixClient` has no `DictLookup` method. DICT same-owner verification is therefore not wired end-to-end — `WithdrawCPFMismatch` same-owner matching is currently unimplemented. | Open. |
| **B35** | Webhook secret in `?hmac=` query string (`cmd/webhook/main.go:114`). Not a body signature. | Open. |
| — | `PayerHintCPF` (`rpc-contract/types.go:59`) accepted + forwarded by `api` (`lambda_client.go:122`) but **never sent in the Inter request body** — `CreateCharge` builds only `calendario/valor/chave` (`inter.go:155`). Dead in the production path. | Dead param. |
| — | `config.go:27` comment says scope `internal:pix:confirm-deposit` — **typo**; the real scope is `internal:wallet:confirm-deposit` (B2/B3 family). Code uses the correct string. | Comment fix only. |

## Build / deploy

```bash
make build   # builds both binaries (outbound + webhook) for the Lambda runtime
make test    # unit
```
Lambda runtime: `provided.al2023`; deployed by `../cdk/lib/pix-gateway-stack.ts`.

## Cross-links

- Consumer: [`../api/README.md`](../api/README.md) (`internal/pix/lambda_client.go`)
- Wire contract: [`../rpc-contract/README.md`](../rpc-contract/README.md)
- IAM / deploy: [`../cdk/README.md`](../cdk/README.md)
- Operations (SSM seeding, webhook registration): [`../OPERATIONS.md`](../OPERATIONS.md) §4
