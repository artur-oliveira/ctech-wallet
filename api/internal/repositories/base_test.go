package repositories

import (
	"testing"

	"gopkg.aoctech.app/wallet/api/internal/config"
)

func TestNewBasePrefixesTable(t *testing.T) {
	b := NewBase(nil, &config.Config{TablePrefix: "test"}, "wallets")
	if b.TableName != "test_wallets" {
		t.Fatalf("TableName = %q, want %q", b.TableName, "test_wallets")
	}
}
