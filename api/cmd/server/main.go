package main

import (
	"log/slog"
	"os"
	_ "time/tzdata" // responsible-gambling windows need America/Sao_Paulo everywhere

	"go.uber.org/fx"
	"gopkg.aoctech.app/wallet/api/internal/app"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	fx.New(app.Module).Run()
}
