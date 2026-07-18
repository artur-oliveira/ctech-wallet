package repositories

import (
	"testing"

	"gopkg.aoctech.app/api/internal/config"
)

func TestNewBasePrefixesTable(t *testing.T) {
	b := NewBase(nil, &config.Config{TablePrefix: "test"}, "wallets")
	if b.TableName != "test_wallets" {
		t.Fatalf("TableName = %q, want %q", b.TableName, "test_wallets")
	}
}

func TestBuildUpdateExprSetAndRemove(t *testing.T) {
	expr, names, values, err := buildUpdateExpr(map[string]any{
		"balance": int64(500),
		"cleared": nil,
	})
	if err != nil {
		t.Fatalf("buildUpdateExpr: %v", err)
	}
	// SET clause for balance, REMOVE clause for the nil field.
	if _, ok := values[":balance"]; !ok {
		t.Errorf("expected :balance value, got %v", values)
	}
	if names["#cleared"] != "cleared" || names["#balance"] != "balance" {
		t.Errorf("expr names wrong: %v", names)
	}
	if expr == "" {
		t.Errorf("empty update expression")
	}
}
