# Withdrawal-fee removal

**Date:** 2026-08-16  
**Status:** approved

CTech never charges a withdrawal fee. A withdrawal debits exactly its `amount`,
creates one `withdraw` ledger entry, and has no CTech fee ledger entry,
configuration, sweep, or feature flag.

This supersedes prior wallet documents that proposed `fee_bps`, `fee_min`,
`fee_max`, or `WithdrawalFee`. A payment-provider tariff is a provider cost,
not CTech revenue, and must remain separate from this rule.
Historical DynamoDB fee attributes and ledger rows remain immutable history;
new code neither reads nor writes them.
