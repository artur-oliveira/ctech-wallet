# Merging the `game` wallet into `real` (P3)

After this, a user has one money balance, `real`. Holds, cash-outs and balance reads for games act on it; the ring-fence routes (`POST /wallet/game/deposit`, `POST /wallet/game/withdraw`, `POST /wallet/sandbox/purchase`) answer `410 route-retired`. Sandbox credits are bought only with the direct Pix sale (`/wallet/sandbox/purchases`), as before. Poker is unchanged: its balance read (`game_balance`) now carries the real balance, and its eligibility read no longer needs a game wallet.

## Before you start

1. **Poker real money is off.** `REAL_MONEY_ENABLED=false` in the target environment (it also needs a legal sign-off ref to be turned on). With the flag on and real money in play, personal limits are not enforced: the metered edge, `real → game`, is gone until Poker owns games (P4).
2. Deploy this code with `GAME_WALLET_MERGED=false` (constant in `cdk/lib/constants.ts`). Nothing changes yet.

## Run it

All from `api/`, with the environment's AWS profile.

```
export TABLE_PREFIX=prod ENVIRONMENT=prod AWS_PROFILE=ctech
go run ./cmd/merge-game --dry-run --poker-real-money-off     # report only
```

The report counts users with a game wallet, how many hold a balance, how many have an open hold, and the totals. Expect the open-hold count to be 0. A user with an open hold is skipped (never merged): wait for the table to close and run again.

Production on 2026-10-15 had one game wallet (the owner's, balance 0) and no holds.

```
go run ./cmd/merge-game --poker-real-money-off              # merge
```

The command prints a summary (`Merged`, `Noop`, `SkippedOpenHold`, `Failed`, and the totals before and after). It stops at once, exit 1, if a transfer's two ledger entries ever disagree. Re-running is safe: the idempotency key is `game-merge#<user_id>`, so a user already merged moves nothing the second time. A `Failed` user (for example a deposit landing mid-merge, which the version check refuses) is taken by the next run.

Take an on-demand DynamoDB backup of `<env>_wallets` and `<env>_wallet_ledger_entries` before the live run.

## Turn it on

Only when a run reports `SkippedOpenHold: 0` and `Failed: 0`: set `GAME_WALLET_MERGED` to `true` for the environment in `cdk/lib/constants.ts` and deploy. Check `GET /v1.0/wallet`: `real` is the old sum, `game` is absent, `activated` is still true for activated users.

## Roll back

1. Set `GAME_WALLET_MERGED` back to `false` and deploy.
2. `go run ./cmd/merge-game --reverse`: for each retired game wallet it moves back what the merge credited, as counter-entries (`game_unmerge_debit` / `game_unmerge_credit`), and reopens the wallet. A user whose `real` no longer covers it (they spent it) is reported as `skipped_spent` and never forced.

A user rolled back cannot be re-merged by the same command: the merge key `game-merge#<user_id>` already exists. That needs a one-off key change by a person.

## What this costs

No new infrastructure. The merge reads the wallets table once (a filtered scan, acceptable at this size) and writes a pair of ledger entries per user with a balance.
