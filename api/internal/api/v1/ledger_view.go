package v1

import "gopkg.aoctech.app/wallet/api/internal/domain/wallet"

// ledgerEntryView is the statement row sent to clients: the immutable ledger
// entry plus the balance the wallet held before it. balance_before is derived,
// never stored, and is display metadata only: the authoritative balance lives in
// wallets.balance (Invariant #2). Embedding keeps the JSON keys of
// wallet.LedgerEntry, whose SK and IdempotencyKey are tagged `json:"-"`, so the
// internal attributes never reach the client.
type ledgerEntryView struct {
	wallet.LedgerEntry
	BalanceBefore int64 `json:"balance_before"`
}

func newLedgerEntryView(e wallet.LedgerEntry) ledgerEntryView {
	return ledgerEntryView{LedgerEntry: e, BalanceBefore: e.BalanceAfter - e.Amount}
}
