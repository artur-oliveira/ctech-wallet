package app

import (
	"testing"

	"go.uber.org/fx"
)

// The erasure providers must resolve in the root graph (the compile cannot
// see a missing fx dependency).
func TestModuleGraphResolves(t *testing.T) {
	if err := fx.ValidateApp(Module); err != nil {
		t.Fatal(err)
	}
}
