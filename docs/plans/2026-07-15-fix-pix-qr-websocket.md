# Fix: PIX QR code not shown + WebSocket `/v1.0/ws` fails

**Date:** 2026-07-15
**Status:** implemented

## Context

Two production bugs reported against `ctech-wallet`:

1. **No QR image on deposit.** `pix-gateway` opens an Inter PIX cobrança and gets back only the EMV copy-paste string (`pixCopiaECola`). Inter does **not** return a QR image. `Charge.QRCodeB64` is therefore always `""`. The frontend `pix-charge-dialog.tsx:115` renders the QR `<img>` only when `qr_code_base64` is truthy, so no scannable QR ever appears (the EMV text still shows at line 124).
2. **WebSocket upgrade fails.** Browser `wss://wallet.aoctech.app/v1.0/ws` errors. Server log shows `GET /v1.0/ws → 500` with `websocket: the client is not using the websocket protocol: 'upgrade' token not found in 'Connection' header`. The request carries a `request-id`, so it reached the Go app through nginx — nginx stripped the `Upgrade`/`Connection: Upgrade` headers.

## Root causes (verified by reading code)

- **P1:** `pix-gateway/internal/inter/inter.go:168` — `QRCode: resp.PixCopiaECola` populated, `QRCodeB64` never set. Frontend consumes `qr_code_base64` (`api/internal/api/v1/wallet.go:47` ← `charge.QRCodeB64`) for the `<img>`.
- **P2:** `cdk/lib/api-stack.ts:267-288` `location /` sets `proxy_set_header Connection "";` and has no `proxy_set_header Upgrade $http_upgrade;`. `api/internal/api/v1/ws.go:77` uses `fws.FastHTTPUpgrader`; without the upgrade headers it rejects the request. Route is mounted at `/v1.0/ws` (`router.go:28,34` + `ws.go:82`).

## Changes

### P1 — generate QR image in pix-gateway

- **`pix-gateway/internal/inter/qr.go` (new):** helper `qrPNG(text string) (string, error)` → `qrcode.Encode(text, qrcode.Medium, 256)` → `base64.StdEncoding.EncodeToString`. Dep `github.com/skip2/go-qrcode` (now direct in go.mod).
- **`pix-gateway/internal/inter/inter.go` (`CreateCharge`):** build the `Charge`, and when `QRCode` (the EMV string) is non-empty, set `QRCodeB64 = qrPNG(QRCode)`; on render error log a warning and leave it empty (never fail the charge for a QR rendering miss).
- **`pix-gateway/internal/inter/inter_test.go`:** `TestDoSetsBearer` now also asserts `QRCodeB64 != ""` and that it base64-decodes to a PNG (`\x89PNG`).
- **Optional (recommended): `Ping` reachability (`inter.go:270`):** keep the bearer-presence check, then TCP-dial `c.base` host on `:443` (or parsed port) with a 5s timeout; return an error on failure. `TestPingValidatesBearer` still passes (dials the localhost httptest server).

### P2 — nginx WebSocket forwarding

- **`cdk/lib/api-stack.ts`:** add the canonical `map $http_upgrade $connection_upgrade` in the `http {}` block, and a dedicated `location = /v1.0/ws` (before `location /`) that forwards `Upgrade` / `Connection: $connection_upgrade`, sets long `proxy_read_timeout`/`proxy_send_timeout` (3600s) and `proxy_buffering off`. `location /` keeps its keepalive `Connection ""` behavior unchanged.

## Verification

- **P1:** `cd pix-gateway && go test ./internal/inter/...` and `go build ./...`.
- **P2:** `cd cdk && npx cdk synth`; requires `cdk deploy` (infra change). After deploy, `wscat -c wss://wallet.aoctech.app/v1.0/ws` should upgrade (101) and, after the JWT frame, return `{"type":"connected",...}`. Browser deposit dialog then shows the QR image.
- **Cross-project:** `api` unchanged (already forwards `qr_code_base64`); `ui` unchanged (existing `<img>` consumes the now-populated field).

## Out of scope

- Client-side QR rendering — not needed; the backend now populates the field the UI already renders.
- CloudFront config — forwards WebSocket by default; verify post-deploy, change only if the upgrade still fails at the CF layer.
