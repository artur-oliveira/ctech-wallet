package rpccontract

// Money constants shared between api (Go) and ui (TypeScript) — B18. The
// canonical values live in money.json; these consts exist so Go callers keep
// compile-time constants, and money_test.go fails the build if they ever
// drift from the JSON. The ui reads money.json directly in its own sync test
// (ui/src/lib/utils/money-contract.test.mjs).
const (
	// SandboxCreditsPerCent is the fixed real→sandbox conversion rate.
	SandboxCreditsPerCent = 100

	// MaxAmountCents caps any single inbound amount (R$ 1.000.000,00).
	MaxAmountCents = 100_000_000
)
