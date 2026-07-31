package repositories

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/wallet/api/internal/config"
)

func TestNewBasePrefixesTable(t *testing.T) {
	b := NewBase(nil, &config.Config{TablePrefix: "test"}, "wallets")
	if b.TableName != "test_wallets" {
		t.Fatalf("TableName = %q, want %q", b.TableName, "test_wallets")
	}
}

func TestDecodeItemsPreservesOrder(t *testing.T) {
	type record struct {
		ID string `dynamodbav:"id"`
	}
	items := []map[string]types.AttributeValue{
		{"id": &types.AttributeValueMemberS{Value: "first"}},
		{"id": &types.AttributeValueMemberS{Value: "second"}},
	}
	got, err := DecodeItems[record](items)
	if err != nil {
		t.Fatalf("DecodeItems() error = %v", err)
	}
	if len(got) != 2 || got[0].ID != "first" || got[1].ID != "second" {
		t.Fatalf("DecodeItems() = %#v", got)
	}
}

func TestDecodeItemsRejectsMalformedItemWithoutPartialResult(t *testing.T) {
	type record struct {
		Amount int64 `dynamodbav:"amount"`
	}
	items := []map[string]types.AttributeValue{
		{"amount": &types.AttributeValueMemberN{Value: "10"}},
		{"amount": &types.AttributeValueMemberN{Value: "not-a-number"}},
	}
	got, err := DecodeItems[record](items)
	if err == nil {
		t.Fatal("DecodeItems() error = nil, want malformed-number error")
	}
	if got != nil {
		t.Fatalf("DecodeItems() = %#v, want nil on error", got)
	}
}

func TestWithUpdatedAtDoesNotMutateCallerMap(t *testing.T) {
	updates := map[string]any{"status": "processing"}
	got := withUpdatedAt(updates, "2026-07-31T12:00:00Z")

	if _, exists := updates[attributeUpdatedAt]; exists {
		t.Fatal("withUpdatedAt() mutated caller map")
	}
	if got["status"] != "processing" || got[attributeUpdatedAt] != "2026-07-31T12:00:00Z" {
		t.Fatalf("withUpdatedAt() = %#v", got)
	}
}

func TestWithUpdatedAtUsesRepositoryTimestamp(t *testing.T) {
	updates := map[string]any{attributeUpdatedAt: "caller-value"}
	got := withUpdatedAt(updates, "repository-value")
	if got[attributeUpdatedAt] != "repository-value" {
		t.Fatalf("updated_at = %#v, want repository timestamp", got[attributeUpdatedAt])
	}
}
