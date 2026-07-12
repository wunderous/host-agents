package main

import (
	"context"
	"flag"
	"log/slog"
	"os"

	"github.com/opute-io/host-agents/internal/app"
)

func main() {
	mode := flag.String("mode", "", "agent mode: platform or standalone")
	transport := flag.String("transport", "", "transport: http or stdio")
	flag.Parse()
	if *mode != "" {
		_ = os.Setenv("OPUTE_AGENT_MODE", *mode)
	}
	if *transport != "" {
		_ = os.Setenv("OPUTE_TRANSPORT", *transport)
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if err := app.Run(context.Background(), logger); err != nil {
		logger.Error("fatal", "err", err)
		os.Exit(1)
	}
}
