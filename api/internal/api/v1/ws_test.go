package v1

import "testing"

func TestIsClientPing(t *testing.T) {
	if !isClientPing([]byte(`{"type":"ping"}`)) {
		t.Error("expected {\"type\":\"ping\"} to be detected as a client ping")
	}
	if isClientPing([]byte(`{"type":"pong"}`)) {
		t.Error("expected {\"type\":\"pong\"} not to be detected as a client ping")
	}
	if isClientPing([]byte(`not json`)) {
		t.Error("expected malformed input not to be detected as a client ping")
	}
}
