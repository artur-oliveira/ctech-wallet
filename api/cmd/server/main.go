package main

import (
	"log/slog"
	"os"

	"go.uber.org/fx"
	"gopkg.aoctech.app/wallet/api/internal/app"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	fx.New(app.Module).Run()
}
