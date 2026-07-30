package wallet

// SandboxSKU is one purchasable pack in the direct PIX→sandbox-credits sale
// (plan §9.1/§9.3). Price/credits are fixed, server-side, and never
// client-supplied — same "never trust the client with a money-shaped number"
// posture as every other amount in this codebase.
//
// The catalog below is a placeholder illustrating the shape (using the same
// SandboxCreditsPerCentavo rate as the ring-fence conversion, purely for a
// consistent example) — actual SKUs/pricing/promotional rates are a product
// decision outside this engineering scope; update this table, never the
// purchase flow itself, once real SKUs are decided.
type SandboxSKU struct {
	ID             string
	PriceCents     int64
	CreditsGranted int64
}

var sandboxSKUCatalog = map[string]SandboxSKU{
	"pack_100":  {ID: "pack_100", PriceCents: 100, CreditsGranted: 100 * SandboxCreditsPerCentavo},
	"pack_500":  {ID: "pack_500", PriceCents: 500, CreditsGranted: 500 * SandboxCreditsPerCentavo},
	"pack_1000": {ID: "pack_1000", PriceCents: 1000, CreditsGranted: 1000 * SandboxCreditsPerCentavo},
}

// SandboxSKUByID looks up a SKU by its ID, or ok=false if unknown.
func SandboxSKUByID(id string) (SandboxSKU, bool) {
	sku, ok := sandboxSKUCatalog[id]
	return sku, ok
}
