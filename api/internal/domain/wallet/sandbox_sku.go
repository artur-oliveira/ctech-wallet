package wallet

// SandboxSKU is one purchasable pack in the direct PIX→sandbox-credits sale
// (plan §9.1/§9.3). Price/credits are fixed, server-side, and never
// client-supplied — same "never trust the client with a money-shaped number"
// posture as every other amount in this codebase.
//
// The catalog below is a placeholder illustrating the shape (using the same
// SandboxCreditsPerCents rate as the ring-fence conversion, purely for a
// consistent example) — actual SKUs/pricing/promotional rates are a product
// decision outside this engineering scope; update this table, never the
// purchase flow itself, once real SKUs are decided.
type SandboxSKU struct {
	ID           string `json:"id"`
	PriceCents   int64  `json:"price_cents"`   // preço em centavos
	BaseCredits  int64  `json:"base_credits"`  // créditos sem bônus
	BonusPercent int64  `json:"bonus_percent"` // percentual de bônus
}

func (s SandboxSKU) TotalCredits() int64 {
	return s.BaseCredits + (s.BaseCredits * s.BonusPercent / 100)
}

var sandboxSKUCatalog = map[string]SandboxSKU{
	"pack_100": {
		ID:           "pack_100",
		PriceCents:   100,
		BaseCredits:  100 * SandboxCreditsPerCent,
		BonusPercent: 0,
	},
	"pack_500": {
		ID:           "pack_500",
		PriceCents:   500,
		BaseCredits:  500 * SandboxCreditsPerCent,
		BonusPercent: 10,
	},
	"pack_1000": {
		ID:           "pack_1000",
		PriceCents:   1000,
		BaseCredits:  1000 * SandboxCreditsPerCent,
		BonusPercent: 20,
	},
	"pack_2000": {
		ID:           "pack_2000",
		PriceCents:   2000,
		BaseCredits:  2000 * SandboxCreditsPerCent,
		BonusPercent: 35,
	},
	"pack_5000": {
		ID:           "pack_5000",
		PriceCents:   5000,
		BaseCredits:  5000 * SandboxCreditsPerCent,
		BonusPercent: 50,
	},
	"pack_10000": {
		ID:           "pack_10000",
		PriceCents:   10000,
		BaseCredits:  10000 * SandboxCreditsPerCent,
		BonusPercent: 100,
	},
}

func ListSKUs() []SandboxSKU {
	skus := make([]SandboxSKU, len(sandboxSKUCatalog))
	i := 0
	for _, s := range sandboxSKUCatalog {
		skus[i] = s
		i++
	}
	return skus
}

// SandboxSKUByID looks up a SKU by its ID, or ok=false if unknown.
func SandboxSKUByID(id string) (SandboxSKU, bool) {
	sku, ok := sandboxSKUCatalog[id]
	return sku, ok
}
