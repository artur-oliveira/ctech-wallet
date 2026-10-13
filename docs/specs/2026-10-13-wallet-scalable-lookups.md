# Wallet: lookups that stay cheap when the group grows

- **Status:** Proposed (2026-10-13). Motivated by the account-deletion participant plan (R21), written for a closed group of friends and asked to hold for thousands.
- **Scope:** how the wallet finds "everything of one user" and "everything waiting for a sweep" without reading other users' rows.

## 1. The problem

Three access patterns read by **status**, through `gsi_status` (deposits, withdrawals) and `gsi_hold_status` (holds), and then filter in memory:

| Caller | Pattern | Cost today |
|---|---|---|
| Sweeps (`SweepPendingDeposits`, `ListProcessingWithdrawals`, `ScanStaleHolds`) | all rows of one status | right shape: it is a work queue, as long as the status is non-terminal |
| `ListOpenHoldsForWallet` (conservation check) | rows of one status, then `wallet_id` filter | reads **every user's** open holds, and applies `limit` before the filter, so a user's hold can fall past the page and be missed |
| Account deletion (eligibility, purge) | all rows of one user, any status | the first draft read every row of every status |

The first two are fine for a few friends. The third and the hold lookup grow with the whole table, not with the user.

There is a second, quieter limit: every status transition writes the index entry under the **status value** as partition key. Terminal values (`confirmed`, `completed`, `refunded`) collect every row ever, on one index partition key. A single key sustains about 1,000 writes/s and 3,000 reads/s. Thousands of friends are far below that; it matters only around 10^5 active users.

## 2. Decision

1. **`gsi_user` on `wallet_pix_deposits`, `wallet_withdrawals` and `wallet_holds`** (hash `user_id`, sort `created_at`). "Everything of one user" becomes a key-condition Query whose cost is that user's rows. Used by the erasure blockers and purge, by `ListOpenHoldsForWallet` (query the user's partition, filter `held`, no cross-user read, no truncation bug), and later by a deposit/withdrawal history screen. DynamoDB indexes existing items on creation (`user_id` and `created_at` are already attributes), so no backfill code. **Do now**, with the erasure plan (Task 2, Task 10).
2. **Fix `ListOpenHoldsForWallet`** to use `gsi_user` and filter by wallet and `held`. It is a correctness fix, independent of scale. **Do now.**
3. **Sparse work-queue indexes for sweeps** (`open_status` present only while a row is non-terminal; removed on the terminal transition) replace `gsi_status` as the sweeps' source. Partitions stay tiny (only rows that still need work), terminal rows stop writing to a shared key, and the sweeps read exactly what they need. **Do not do now.** Trigger: any status partition passes a few hundred thousand rows, or CloudWatch shows throttling on a `gsi_status` partition.

## 3. Migration for item 3 (when its trigger fires)

1. Add the sparse attribute and a new index `gsi_open` (hash `open_status`, sort `created_at`). Writers set it on create (`pending`, `processing`, `refund_pending`, `refund_failed`) and remove it on every terminal transition, in the same conditional write that changes `status`.
2. One-off job (operator, explicit credentials, no `Scan` in the service role): set `open_status` on existing non-terminal rows. The set is small by construction.
3. Switch the sweep readers to `gsi_open`. Keep `gsi_status` for one release.
4. Drop `gsi_status` once nothing reads it.

Every step is reversible until step 4.

## 4. What does not change

- The ledger, balances, locks and idempotency (invariants 1 to 5). Indexes are read paths only.
- The retention table (`pk = SUB#{sub}`) is already keyed by user.
- Daily counters live on the user's item.

## 5. Cost

Item 1 adds three indexes: storage and one extra write per deposit, withdrawal and hold. Each row is small and the write rate is the user's money movements, so the cost is negligible next to the rows themselves. Item 3, when it comes, lowers the write cost of terminal transitions.
