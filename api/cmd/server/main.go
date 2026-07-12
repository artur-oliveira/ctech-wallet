package main

import (
	"log/slog"
	"os"

	"github.com/artur-oliveira/ctech-wallet/api/internal/app"
	"go.uber.org/fx"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	fx.New(app.Module).Run()
}
