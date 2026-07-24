package rpccontract

// Money constants shared between api (Go) and ui (TypeScript) — B18. The
// canonical values live in money.json; these consts exist so Go callers keep
// compile-time constants, and money_test.go fails the build if they ever
// drift from the JSON. The ui reads money.json directly in its own sync test
// (ui/src/lib/utils/money-contract.test.mjs).
const (
	// Withdrawal fee defaults (design spec §D); per-wallet DynamoDB fields
	// override them.
	DefaultFeeBps = 200  // 2.00% in basis points
	DefaultFeeMin = 100  // R$ 1,00 in centavos
	DefaultFeeMax = 1000 // R$ 10,00 in centavos
	// AbsoluteFeeMin is a hard floor no per-wallet override may go below —
	// it covers the PIX transfer cost.
	AbsoluteFeeMin = 100 // R$ 1,00 in centavos

	// SandboxCreditsPerCentavo is the fixed real→sandbox conversion rate.
	SandboxCreditsPerCentavo = 10

	// MaxAmountCents caps any single inbound amount (R$ 1.000.000,00).
	MaxAmountCents = 100_000_000
)
