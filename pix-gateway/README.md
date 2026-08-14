# pix-gateway

AWS Lambda pair that is the **only** component allowed to talk to Banco Inter's
PIX/Banking APIs. The `api` service never opens an mTLS connection — it invokes
these Lambdas over `lambda:Invoke` with an Inter OAuth bearer supplied per call.
Split into two functions:

- **`cmd/outbound`** — performs the actual Inter calls (CreateCharge, QueryCharge,
  Transfer, QueryTransfer, Refund, Ping, GetToken). Dispatches on the
  `rpc-contract` `Op` enum (`cmd/outbound/main.go:89`).
- **`cmd/webhook`** — receives Inter's PIX callback, validates it, and asks `api`
  to re‑derive + credit the deposit. Carries **no Inter credentials at all**
  (`cmd/webhook/main.go:1`).

> The API reads its Inter bearer from `pix-gateway`'s `GetToken` op and passes it
> on the wire (`OAuthToken` in `rpc-contract.Request`) — it is **never logged**
> and neither request nor response financial payloads are logged.

## Wire contract

Both Lambdas speak `rpc-contract` (`../rpc-contract/README.md`): `api` sends
`Request{Op, OAuthToken, Payload}`, `pix-gateway` returns `Response{Error,
Payload}` with sentinels `key_not_found`, `unauthorized`, and
`transfer_not_found`
(`cmd/outbound/main.go:199`).

## Outbound — Inter client (`internal/inter`)

- **mTLS:** `tls.X509KeyPair` from the SSM‑loaded keypair, `MinVersion:
  TLS12`, 20s timeout (`inter.go:67`, `:75`).
- **OAuth2 client_credentials:** `POST {base}/oauth/v2/token` (`pathToken`,
  `inter.go:53`) with scope
  `cob.read cob.write pix.read pix.write pagamento-pix.read pagamento-pix.write`
  (`tokenScope`, `inter.go:58`). The **Inter bearer is NOT cached** — it is
  passed per call by `api` in `OAuthToken` and seeded into ctx
  (`bearer.go:15`). Only the **Inter OAuth client secret** is cached
  (`secret()` lazy‑loads from SSM once, `inter.go:137`).
- **Endpoints (centralized constants, `inter.go:52`):**
  | Op | Inter path |
  |----|-----------|
  | CreateCharge | `PUT /pix/v2/cob/{txid}` (expiry 300s, `chave` = `INTER_PIX_KEY`) |
  | QueryCharge | `GET /pix/v2/cob/{txid}` |
  | Transfer (payout) | `POST /banking/v2/pix` (`x-id-idempotente` = idemKey) |
  | QueryTransfer | `GET /banking/v2/pix/{idemKey}` (any error ⇒ `NAO_ENCONTRADO` so reconciliation reverses) |
  | Refund (devolução) | `PUT /pix/v2/pix/{e2eid}/devolucao/{refundID}` |
- **Refund idempotency:** Inter requires the client-generated devolução ID to
  match `[a-zA-Z0-9]{1,35}`. `interRefundID` preserves already-compliant IDs
  (so historical successful refunds keep the same provider idempotency key)
  and deterministically maps service keys containing separators such as
  `sandbox_refund#...` to 35 lowercase hexadecimal SHA-256 characters.
- **QR code:** Inter returns only the EMV string; `qrPNG` generates the base64
  PNG (`inter.go:172`, `internal/inter/qr.go`). A render miss is logged and
  left empty — the EMV text still reaches the client.
- **Money:** integer **centavos** internally; `centavosToReais` /
  `reaisToCentavos` convert to Inter's R$ decimal strings (`inter.go:386`).

### Dead parameter / incomplete feature (divergences B30/B36)

- `CreateChargeArgs.PayerHintCPF` (`rpc-contract/types.go:59`) is accepted and
  forwarded by `api` (`lambda_client.go:122`) but **never sent in the Inter
  request body** — `CreateCharge` builds only `calendario/valor/chave`
  (`inter.go:155`). Dead in the production path.
- `DictAccount` (`inter/client.go:60`, `api/internal/pix/client.go:64`) and
  `DictLookupArgs`/`DictResult` (`rpc-contract/types.go:94`) exist, but there is
  **no `OpDictLookup`** in the `Op` enum and `PixClient` has no `DictLookup`
  method. DICT **same‑owner** verification is therefore not wired end‑to‑end —
  `WithdrawCPFMismatch` same‑owner matching is currently unimplemented.

## Webhook — deposit wake‑up (`cmd/webhook`)

1. Inter POSTs to the mTLS‑verified API Gateway custom domain
   `pix.wallet.aoctech.app`. The HTTP method is **not** checked in code — API
   Gateway routing enforces it (`cmd/webhook/main.go:1`).
2. **Auth:** constant‑time compare of `?hmac=` query param against the SSM
   webhook secret (`subtle.ConstantTimeCompare`, `main.go:114`). A mismatch ⇒
   `401`. **See B35:** the secret travels in the **URL query string** (Inter
   echoes it back on every callback; it is *not* a body signature —
   `secrets/ssm.go:71`).
3. Parses `txid`(s) from the body (supports the `pix[]` list or a bare detail
   object, `main.go:123`).
4. Dispatches by reserved txid prefix using the same narrow
   `internal:wallet:confirm-deposit` scope: `sbxp` →
   `/confirm-sandbox-purchase`, `prdp` → `/confirm-product-purchase`, and every
   other txid → `/confirm-deposit`. Only ordinary deposits forward the
   **payer CPF/name** from the webhook body. Every API handler re-queries Inter
   before changing durable state (Invariant #11).
5. Any confirmation error ⇒ `500` so Inter **retries** the whole payload. All
   three confirmation flows are idempotent per txid.

> **CPF anti‑fraud:** Inter's charge re‑query no longer returns the payer, so
> the webhook body is the **only** source of payer CPF/name. api persists it and
> uses it only for the CPF‑match gate — never to authorize crediting.

## Secrets — SSM SecureString (`internal/secrets/ssm.go:16`)

Asaas account credentials travel only in the Lambda request's redacted
`oauth_token` transport field. They are never duplicated into the logged JSON
payload. Request/response logs redact credentials, documents, CPF fields, and
encoded QR images; raw Inter/webhook bodies are not logged.

All read with `WithDecryption: true`; none hit disk or logs.

| Parameter | Consumed by |
|-----------|-------------|
| `/ctech-wallet/{env}/inter/mtls-cert` | outbound (mTLS) |
| `/ctech-wallet/{env}/inter/mtls-key` | outbound (mTLS) |
| `/ctech-wallet/{env}/inter/client-secret` | outbound `GetToken` (lazy‑cached) |
| `/ctech-wallet/{env}/pix-gateway/client-secret` | webhook (own M2M token) |
| `/ctech-wallet/{env}/inter/webhook-secret` | webhook (hmac compare) |

## Config (`internal/config/config.go`)

`INTER_BASE_URL` (default `https://cdpj.partners.bancointer.com.br`),
`INTER_CLIENT_ID`, `INTER_CLIENT_SECRET`, `INTER_PIX_KEY`, `CTECH_URL`,
`PIX_GATEWAY_CLIENT_ID`, `PIX_GATEWAY_CLIENT_SECRET`, `WALLET_API_URL`,
`AWS_REGION`, `ENVIRONMENT`.

## Deploy / build

```bash
make build        # builds both binaries (outbound + webhook) for the Lambda runtime
make test         # unit
```

Lambda runtime: provided.al2023; deployed by `cdk/lib/pix-gateway-stack.ts`.

## Cross‑links

- Consumer: [`../api/README.md`](../api/README.md) (`internal/pix/lambda_client.go`)
- Wire contract: [`../rpc-contract/README.md`](../rpc-contract/README.md)
- IAM / deploy: [`../cdk/README.md`](../cdk/README.md)
- Operations (SSM seeding, webhook registration): [`../OPERATIONS.md`](../OPERATIONS.md) §4

## Known divergences

| ID | Where | Status |
|----|-------|--------|
| B30/B36 | `DictAccount` / DICT same‑owner verify dead (no `OpDictLookup`, no `PixClient.DictLookup`). `WithdrawCPFMismatch` same‑owner check unimplemented. | Open — documented. |
| B35 | Webhook secret in `?hmac=` query string (`cmd/webhook/main.go:114`). Not a body signature. | Open — documented. |
| — | `PayerHintCPF` accepted but never sent to Inter (`inter.go:155`). | Dead param. |
