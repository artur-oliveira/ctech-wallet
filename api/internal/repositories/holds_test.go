package repositories

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

func TestHoldStatusTransitionBuildsConditionalUpdateAttributes(t *testing.T) {
	const (
		fromStatus = "held"
		toStatus   = "released"
		timestamp  = "2026-07-31T12:00:00Z"
	)

	names, values := holdStatusTransition(fromStatus, toStatus, timestamp)
	if names["#status"] != holdStatusAttribute {
		t.Fatalf("status attribute = %q, want %q", names["#status"], holdStatusAttribute)
	}

	assertStringAttributeValue(t, values, ":from", fromStatus)
	assertStringAttributeValue(t, values, ":to", toStatus)
	assertStringAttributeValue(t, values, ":now", timestamp)
}

func assertStringAttributeValue(t *testing.T, values map[string]types.AttributeValue, key, want string) {
	t.Helper()
	got, ok := values[key].(*types.AttributeValueMemberS)
	if !ok {
		t.Fatalf("%s has type %T, want string attribute", key, values[key])
	}
	if got.Value != want {
		t.Fatalf("%s = %q, want %q", key, got.Value, want)
	}
}
