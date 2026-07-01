package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/opute-io/host-agents/internal/app"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if err := app.Run(context.Background(), logger); err != nil {
		logger.Error("fatal", "err", err)
		os.Exit(1)
	}
}
