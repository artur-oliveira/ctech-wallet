# Cutover: deposits and withdrawals through Billing

Spec: `ctech-billing/docs/specs/2026-10-14-wallet-ledger-billing-rail.md`. Plan: `ctech-billing/docs/plans/2026-10-14-wallet-ledger-p2-wallet-on-billing.md`.

`BILLING_RAIL` (`off` | `on`) chooses what moves the money. **Off is the Inter Pix gateway, unchanged.** On sends deposits and withdrawals to Billing, which owns the Inter connection. Sandbox and product purchases stay on the gateway until P4/P5.

## How the pieces fit

- Deposit: the wallet makes a Billing customer for the user (or reuses theirs), issues a one-off invoice whose reference is the deposit id, takes the Pix copia-e-cola from the invoice's checkout link, and shows it. When it is paid Billing sends a signed `invoice.paid` to `POST /v1.0/internal/billing/events`; the wallet only uses it as a wake-up, re-reads the invoice and credits when **both** Billing's payer verdict is `match` **and** the wallet's own check against the KYC CPF passes, and the paid amount equals the deposit's. Otherwise the payer is refunded (credit note by Pix). No payer recorded yet means the deposit is held pending with an alarm, never credited and never refunded; the pending-deposit sweep keeps asking.
- Withdrawal: debit first, then a Billing payout to the CPF on the user's KYC record. Billing answers `processing`; only its later `done` completes the withdrawal. The reconcile Lambda asks Billing by the withdrawal's reference. A timeout or an error never reverses anything; a payout Billing has never heard of is believed only after 10 minutes.

## Checklist

1. **Billing is ready (P1).** The tenant-zero Inter connection is live and healthy (`ctech-billing/docs/runbooks/tenant-zero-inter-rail.md`), and Billing is on a version with `owner_key` on one-off invoices and `GET /v1.0/customers/by-user/:user_id`.
2. **The wallet's OAuth client for Billing** (in `{env}_account_oauth_clients`, like §2 of OPERATIONS.md): confidential, `first_party: true`, `allowed_scopes` exactly `billing:customers:read billing:customers:write billing:invoices:read billing:invoices:write internal:billing:payouts-read internal:billing:payouts-write`. Store `client_id` and `client_secret` in SSM `/ctech-wallet/{env}/billing-client-id` and `/ctech-wallet/{env}/billing-client-secret` (SecureString).
3. **Tell Billing who it is** (Billing PR, `api/tenants/ctech.json`): a credential `{"client_id": "<the client id>", "owner_key": "wallet", "description": "CTech Wallet"}` and a webhook endpoint (it receives every event of invoices carrying owner key `wallet`; the wallet answers 200 and ignores all but `invoice.paid`) `{"id": "whe_wallet", "url": "https://wallet.internal.aoctech.app/v1.0/internal/billing/events", "owner_key": "wallet", "secret_env": "WEBHOOK_SECRET_WALLET"}`; add `WEBHOOK_SECRET_WALLET` to `api/cmd/seed/run.sh` from SSM `/ctech-wallet/{env}/billing-event-secret`. Generate that secret (`openssl rand -hex 32`) and store it as SecureString first, then run the seed (`api/cmd/seed/run.sh prod`).
4. **Remaining SSM:** `/ctech-wallet/{env}/billing-url` (String, `https://billing-api.aoctech.app`).
5. **Deploy with the rail off** (the default): nothing changes. The new route is not mounted and no Billing call is made, including at startup.
6. **Non-prod (when a stage exists):** set `BILLING_RAIL` to `on` for that environment in `cdk/lib/constants.ts`, deploy, deposit R$ 1,00, confirm the credit, withdraw R$ 1,00, confirm the withdrawal `completed`.
7. **Production — ask the owner first (live money).** Set `prod: 'on'`, deploy. The owner deposits R$ 1,00 and withdraws it. Watch the pending-deposit sweep and reconcile for a week.

## Rollback

Set `BILLING_RAIL` back to `off` and deploy. Do it only when no deposit or withdrawal created on Billing is still in flight, or the old rail would look for them at Inter and not find them:

Check the wallet's `status` index on the deposits table (`pending`, `refund_pending`, `refund_failed`) and on the withdrawals table (`processing`): all must be empty. A deposit already paid at Billing and still pending when the flag goes off is credited only after turning it back on.

## Known limits

- Billing exposes one payer per invoice, so a second person paying the same QR is not seen as an excess payment by the wallet. Billing records it on its side.
- Billing does not expose the bank's end-to-end id; the invoice id stands for the payment in the wallet's records of Billing deposits.
