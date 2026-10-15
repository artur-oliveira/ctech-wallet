# P5 idle check: is the old surface still called?

The API access log carries `route` (the route template) and `azp` (the calling client) on every line.
P5 deletes nothing until this query returns **zero rows** for seven days in production
(`docs/plans/2026-10-14-wallet-ledger-p5-cleanup.md` in ctech-billing, Task 1).

Start the week only after the deploy that added `route`/`azp` is live. Log group: `/ctech-wallet/prod/app`.

```
fields @timestamp, azp, route, status
| filter route like /\/internal\/wallet\/(charge|sandbox-purchases|product-purchases|game|pix\/confirm-)/
   or route like /\/wallet\/(game|gambling|sandbox)\//
| stats count() as calls, max(@timestamp) as last_call by route, azp
| sort calls desc
```

Deposits and withdrawals on the legacy rail leave no route of their own; with `BILLING_RAIL=on` they are idle
when the `pix-gateway` Lambda has had zero invocations for the same seven days (CloudWatch `Invocations`).

A row names the caller to fix first. Do not start Task 2 with a non-zero row.
