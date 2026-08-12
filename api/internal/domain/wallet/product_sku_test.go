package wallet

import "testing"

func TestListProductSKUsIncludesEveryCatalogEntry(t *testing.T) {
	skus := ListProductSKUs()
	if len(skus) != len(productSKUCatalog) {
		t.Fatalf("got %d skus, want %d", len(skus), len(productSKUCatalog))
	}
	for _, s := range skus {
		if s.PriceCents <= 0 {
			t.Fatalf("sku %q has non-positive price %d", s.ID, s.PriceCents)
		}
	}
}

func TestProductSKUByIDUnknown(t *testing.T) {
	if _, ok := ProductSKUByID("does-not-exist"); ok {
		t.Fatal("expected ok=false for unknown sku")
	}
}
