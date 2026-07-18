package v1

import (
	"context"
	"errors"
	"testing"

	"gopkg.aoctech.app/wallet/api/internal/cache"
	"gopkg.aoctech.app/wallet/api/internal/pix"

	"github.com/gofiber/fiber/v3"
)

const (
	testTime      = "2026-07-12T00:00:00Z"
	testCacheSize = 8
)

func TestAggregate(t *testing.T) {
	entry := func(status string) healthEntry {
		return healthEntry{Status: status}
	}

	tests := []struct {
		name       string
		checks     map[string]healthEntry
		wantStatus string
		wantCode   int
	}{
		{
			name:       "all pass",
			checks:     map[string]healthEntry{componentDynamoDB: entry(statusPass), componentCache: entry(statusPass)},
			wantStatus: statusPass,
			wantCode:   fiber.StatusOK,
		},
		{
			name:       "degraded dependency warns without leaving the load balancer",
			checks:     map[string]healthEntry{componentDynamoDB: entry(statusPass), componentCache: entry(statusWarn)},
			wantStatus: statusWarn,
			wantCode:   statusMultiStatus,
		},
		{
			name:       "dynamodb down fails the probe",
			checks:     map[string]healthEntry{componentDynamoDB: entry(statusFail), componentCache: entry(statusPass)},
			wantStatus: statusFail,
			wantCode:   fiber.StatusServiceUnavailable,
		},
		{
			name:       "fail outranks warn",
			checks:     map[string]healthEntry{componentDynamoDB: entry(statusFail), componentCache: entry(statusWarn)},
			wantStatus: statusFail,
			wantCode:   fiber.StatusServiceUnavailable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotStatus, gotCode := aggregate(tt.checks)
			if gotStatus != tt.wantStatus || gotCode != tt.wantCode {
				t.Fatalf("aggregate() = (%s, %d), want (%s, %d)", gotStatus, gotCode, tt.wantStatus, tt.wantCode)
			}
		})
	}
}

func TestCheckCache(t *testing.T) {
	got := checkCache(context.Background(), cache.NewMemoryBackend(testCacheSize), testTime)
	if got.Status != statusPass {
		t.Fatalf("healthy cache: status = %s, want %s", got.Status, statusPass)
	}

	got = checkCache(context.Background(), nil, testTime)
	if got.Status != statusWarn || got.ObservedValue != healthUnavailableV {
		t.Fatalf("missing cache: got (%s, %v), want (%s, %v)", got.Status, got.ObservedValue, statusWarn, float64(healthUnavailableV))
	}
}

// A bank outage must never fail the probe — deposits and withdrawals degrade,
// but balances and the ledger stay fully serveable.
func TestCheckPixDownWarnsOnly(t *testing.T) {
	fake := pix.NewFake()
	fake.PingErr = errors.New("inter unreachable")

	got := checkPix(context.Background(), fake, testTime)
	if got.Status != statusWarn {
		t.Fatalf("bank down: status = %s, want %s", got.Status, statusWarn)
	}

	fake.PingErr = nil
	if got := checkPix(context.Background(), fake, testTime); got.Status != statusPass {
		t.Fatalf("bank up: status = %s, want %s", got.Status, statusPass)
	}
}

func TestCheckJWKSMissingVerifier(t *testing.T) {
	got := checkJWKS(context.Background(), nil, testTime)
	if got.Status != statusWarn {
		t.Fatalf("no verifier: status = %s, want %s", got.Status, statusWarn)
	}
}
