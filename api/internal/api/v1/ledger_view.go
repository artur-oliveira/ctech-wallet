package v1

import (
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
)

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

// cursorMatchesWallet reports whether a decoded statement cursor belongs to
// walletID. The cursor is client-supplied, so a tampered one naming another
// wallet's partition must be rejected up front (400) instead of reaching
// DynamoDB, which would refuse it with a ValidationException (a 500 for us).
func cursorMatchesWallet(startKey map[string]types.AttributeValue, walletID string) bool {
	if len(startKey) == 0 {
		return true
	}
	pk, ok := startKey["pk"].(*types.AttributeValueMemberS)
	return ok && pk.Value == walletID
}
