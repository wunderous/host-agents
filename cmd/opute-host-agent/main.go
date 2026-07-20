package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/wunderous/host-agents/internal/app"
	"github.com/wunderous/host-agents/internal/config"
	"github.com/wunderous/host-agents/internal/version"
)

func main() {
	mode := flag.String("mode", "", "agent mode: platform or standalone")
	transport := flag.String("transport", "", "transport: http (Streamable HTTP; stdio is not supported)")
	showVersion := flag.Bool("version", false, "print the agent version and exit")
	check := flag.Bool("check", false, "validate configuration and state access, then exit")
	envFile := flag.String("env-file", "", "load KEY=VALUE settings from a file")
	var envOverrides envFlags
	flag.Var(&envOverrides, "env", "set a KEY=VALUE environment override; repeatable")
	flag.Parse()
	if *showVersion {
		fmt.Println(version.Version)
		return
	}
	resolvedEnvFile := strings.TrimSpace(*envFile)
	if resolvedEnvFile == "" {
		resolvedEnvFile = strings.TrimSpace(os.Getenv("OPUTE_HOST_AGENT_ENV_FILE"))
	}
	if resolvedEnvFile != "" {
		if err := config.LoadEnvFile(resolvedEnvFile); err != nil {
			logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
			logger.Error("failed to load env file", "err", err)
			os.Exit(2)
		}
	}
	for _, assignment := range envOverrides {
		key, value, ok := strings.Cut(assignment, "=")
		if !ok || strings.TrimSpace(key) == "" {
			println("--env requires KEY=VALUE")
			os.Exit(2)
		}
		if err := os.Setenv(strings.TrimSpace(key), value); err != nil {
			println("failed to set --env override:", err.Error())
			os.Exit(2)
		}
	}
	if *mode != "" {
		_ = os.Setenv("OPUTE_AGENT_MODE", *mode)
	}
	if rawTransport := strings.TrimSpace(*transport); rawTransport != "" && !strings.EqualFold(rawTransport, "http") {
		fmt.Fprintf(os.Stderr, "invalid --transport %q: only Streamable HTTP (http) is supported\n", rawTransport)
		os.Exit(2)
	}
	if *check {
		if err := app.Check(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println("configuration ok")
		return
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if err := app.Run(context.Background(), logger); err != nil {
		logger.Error("fatal", "err", err)
		os.Exit(1)
	}
}

type envFlags []string

func (e *envFlags) String() string {
	return strings.Join(*e, ",")
}

func (e *envFlags) Set(value string) error {
	*e = append(*e, value)
	return nil
}
