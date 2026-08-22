package wallet

// ProductSKU is one purchasable fixed-price digital good, sold for real PIX
// money with no ledger effect (docs/specs/2026-08-12-product-purchase-skus.md).
// Price is fixed, server-side, never client- or M2M-caller-supplied — same
// "never trust a money-shaped number from outside this table" posture as
// SandboxSKU.
type ProductSKU struct {
	ID         string `json:"id"`
	PriceCents int64  `json:"price_cents"`
}

// First consumer: poker's 6 premium reactions
// (ctech-poker/docs/specs/2026-08-12-premium-reactions.md), 2 emoji + 4
// targeted objects — SKU ID is poker-chosen, price is wallet-owned.
var productSKUCatalog = map[string]ProductSKU{
	"poker_reaction_cold":   {ID: "poker_reaction_cold", PriceCents: 100},
	"poker_reaction_fire":   {ID: "poker_reaction_fire", PriceCents: 100},
	"poker_reaction_poop":   {ID: "poker_reaction_poop", PriceCents: 500},
	"poker_reaction_rofl":   {ID: "poker_reaction_rofl", PriceCents: 500},
	"poker_reaction_knife":  {ID: "poker_reaction_knife", PriceCents: 500},
	"poker_reaction_turtle": {ID: "poker_reaction_turtle", PriceCents: 500},
	"poker_deck_casino":     {ID: "poker_deck_casino", PriceCents: 200},
	"poker_deck_bicycle":    {ID: "poker_deck_bicycle", PriceCents: 200},
	"poker_deck_vintage":    {ID: "poker_deck_vintage", PriceCents: 200},
	"poker_deck_golden":     {ID: "poker_deck_golden", PriceCents: 500},
	"poker_deck_pink":       {ID: "poker_deck_pink", PriceCents: 500},
	"poker_deck_alt":        {ID: "poker_deck_alt", PriceCents: 500},
	"poker_felt_midnight":   {ID: "poker_felt_midnight", PriceCents: 1000},
	"poker_felt_burgundy":   {ID: "poker_felt_burgundy", PriceCents: 1000},
	"poker_felt_ocean":      {ID: "poker_felt_ocean", PriceCents: 1000},
}

func ListProductSKUs() []ProductSKU {
	skus := make([]ProductSKU, 0, len(productSKUCatalog))
	for _, s := range productSKUCatalog {
		skus = append(skus, s)
	}
	return skus
}

// ProductSKUByID looks up a SKU by its ID, or ok=false if unknown.
func ProductSKUByID(id string) (ProductSKU, bool) {
	sku, ok := productSKUCatalog[id]
	return sku, ok
}
