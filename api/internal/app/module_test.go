package app

import (
	"context"
	"testing"

	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
)

// The erasure providers must resolve in the root graph (the compile cannot
// see a missing fx dependency).
func TestModuleGraphResolves(t *testing.T) {
	if err := fx.ValidateApp(Module); err != nil {
		t.Fatal(err)
	}
}

// A background worker started on the supervisor gets a cancellable context and
// the lifecycle stop cancels it and waits for it to return.
func TestSupervisorStopsWorkersAtShutdown(t *testing.T) {
	lc := fxtest.NewLifecycle(t)
	sup := newSupervisor(lc)
	started, stopped := make(chan struct{}), make(chan struct{})
	if err := sup.Run(context.Background(), "w", func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		close(stopped)
		return ctx.Err()
	}); err != nil {
		t.Fatal(err)
	}
	<-started
	lc.RequireStart().RequireStop()
	select {
	case <-stopped:
	default:
		t.Fatal("worker still running after the lifecycle stopped")
	}
}
