package main

import (
	"log/slog"
	"os"
	_ "time/tzdata" // responsible-gambling windows need America/Sao_Paulo everywhere

	"go.uber.org/fx"
	"go.uber.org/fx/fxevent"
	"gopkg.aoctech.app/wallet/api/internal/app"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)
	// fx's default logger prints a console-formatted tree with blank lines in it.
	// Every other line this process emits is JSON, and a blank line is not a log
	// record at all — CloudWatch's PutLogEvents rejects a zero-length message and
	// fails the entire batch it arrived in, so one banner gap can stop a
	// service's log shipping. Route fx's own events through the same handler.
	fx.New(app.Module, fx.WithLogger(func() fxevent.Logger {
		return &fxevent.SlogLogger{Logger: logger}
	})).Run()
}
