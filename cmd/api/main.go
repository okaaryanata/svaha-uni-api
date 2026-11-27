package main

import (
	"log/slog"
	"os"

	"github.com/okaaryanata/svaha-uni-api/internal/app"
)

func main() {
	if err := app.Run(); err != nil {
		slog.Error("API stopped", "error", err)
		os.Exit(1)
	}
}
