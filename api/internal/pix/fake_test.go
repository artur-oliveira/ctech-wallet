package pix

import (
	"context"
	"testing"
)

func TestFakeSatisfiesInterface(t *testing.T) {
	f := NewFake()
	f.StageCharge("tx", 500, ChargeCompleted, "12345678901", "E2E-1")
	ch, err := f.QueryCharge(context.Background(), "tx")
	if err != nil || ch.PayerCPF != "12345678901" {
		t.Fatalf("fake query: %+v err=%v", ch, err)
	}
}
