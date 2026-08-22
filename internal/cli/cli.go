package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wunderous/host-agents/internal/app"
	"github.com/wunderous/host-agents/internal/config"
	"github.com/wunderous/host-agents/internal/tui"
	"github.com/wunderous/host-agents/internal/version"
)

// Run executes the single-binary command surface.
//
//   - no subcommand: standalone host runtime plus TUI over in-memory MCP
//   - serve: MCP server only, normally over Streamable HTTP
//   - tui: TUI attached to an existing MCP server
//
// The no-subcommand default is deliberately local and Platform-independent.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if stdout == nil {
		stdout = os.Stdout
	}
	if stderr == nil {
		stderr = os.Stderr
	}
	if len(args) == 1 && (args[0] == "--version" || args[0] == "-version") {
		fmt.Fprintln(stdout, version.Version)
		return nil
	}

	command, commandArgs := splitCommand(args)
	switch command {
	case "standalone":
		return runStandalone(ctx, commandArgs, stdout, stderr)
	case "serve":
		return runServer(ctx, commandArgs, stdout, stderr)
	case "tui":
		return runTUI(ctx, commandArgs, stdout, stderr)
	case "help":
		printUsage(stdout)
		return nil
	default:
		return fmt.Errorf("unknown command %q; use standalone, serve, or tui", command)
	}
}

func splitCommand(args []string) (string, []string) {
	if len(args) == 0 {
		return "standalone", nil
	}
	first := strings.TrimSpace(args[0])
	if first == "standalone" || first == "serve" || first == "tui" || first == "help" {
		return first, args[1:]
	}
	// An explicit endpoint means the caller wants the attached TUI mode.
	// Preserve the old server-only flags as an implicit `serve` command so
	// existing service units and launchers continue to work after the rename.
	for _, arg := range args {
		if arg == "--mode" || strings.HasPrefix(arg, "--mode=") || arg == "--transport" || strings.HasPrefix(arg, "--transport=") || arg == "--check" || arg == "--env-file" || strings.HasPrefix(arg, "--env-file=") || arg == "--env" || strings.HasPrefix(arg, "--env=") {
			return "serve", args
		}
	}
	for _, arg := range args {
		if arg == "--url" || strings.HasPrefix(arg, "--url=") || arg == "--token" || strings.HasPrefix(arg, "--token=") {
			return "tui", args
		}
	}
	return "standalone", args
}

func runStandalone(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	setenv("OPUTE_AGENT_MODE", "standalone")
	config, err := parseTUIFlags("standalone", args, stderr)
	if err != nil {
		return err
	}
	logger := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	runtime, err := app.NewRuntime(logger)
	if err != nil {
		return err
	}
	defer runtime.Close()

	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := runtime.Host().MCP().Connect(ctx, serverTransport, nil)
	if err != nil {
		return fmt.Errorf("connect in-process MCP server: %w", err)
	}
	defer serverSession.Close()

	config.Transport = clientTransport
	config.Out = stdout
	config.Err = stderr
	if err := tui.Run(ctx, config); err != nil {
		return err
	}
	if err := serverSession.Wait(); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("wait for in-process MCP server: %w", err)
	}
	return nil
}

func runServer(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	mode := fs.String("mode", "", "agent profile: standalone or platform")
	transport := fs.String("transport", "", "MCP transport: http")
	envFile := fs.String("env-file", "", "load KEY=VALUE settings from a file")
	check := fs.Bool("check", false, "validate configuration and state access, then exit")
	var envOverrides envFlags
	fs.Var(&envOverrides, "env", "set a KEY=VALUE environment override; repeatable")
	if err := fs.Parse(args); err != nil {
		return err
	}
	resolvedEnvFile := strings.TrimSpace(*envFile)
	if resolvedEnvFile == "" {
		resolvedEnvFile = strings.TrimSpace(os.Getenv("OPUTE_HOST_AGENT_ENV_FILE"))
	}
	if resolvedEnvFile != "" {
		if err := config.LoadEnvFile(resolvedEnvFile); err != nil {
			return fmt.Errorf("load env file: %w", err)
		}
	}
	for _, assignment := range envOverrides {
		key, value, ok := strings.Cut(assignment, "=")
		if !ok || strings.TrimSpace(key) == "" {
			return fmt.Errorf("--env requires KEY=VALUE")
		}
		if err := os.Setenv(strings.TrimSpace(key), value); err != nil {
			return fmt.Errorf("set %s: %w", strings.TrimSpace(key), err)
		}
	}
	resolvedMode := strings.TrimSpace(*mode)
	if resolvedMode == "" {
		resolvedMode = strings.TrimSpace(os.Getenv("OPUTE_AGENT_MODE"))
	}
	if resolvedMode == "" {
		resolvedMode = "standalone"
	}
	resolvedTransport := strings.TrimSpace(*transport)
	if resolvedTransport == "" {
		resolvedTransport = strings.TrimSpace(os.Getenv("OPUTE_TRANSPORT"))
	}
	if resolvedTransport == "" {
		resolvedTransport = "http"
	}
	if !strings.EqualFold(resolvedTransport, "http") {
		return fmt.Errorf("invalid --transport %q: only Streamable HTTP (http) is supported", resolvedTransport)
	}
	setenv("OPUTE_AGENT_MODE", resolvedMode)
	setenv("OPUTE_TRANSPORT", resolvedTransport)
	if *check {
		if err := app.Check(); err != nil {
			return err
		}
		fmt.Fprintln(stdout, "configuration ok")
		return nil
	}
	return app.Run(ctx, slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))
}

func runTUI(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	config, err := parseTUIFlags("tui", args, stderr)
	if err != nil {
		return err
	}
	config.Out = stdout
	config.Err = stderr
	return tui.Run(ctx, config)
}

func parseTUIFlags(name string, args []string, stderr io.Writer) (tui.Config, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	endpoint := fs.String("url", "", "Host Agent MCP Streamable HTTP endpoint (tui mode only)")
	token := fs.String("token", "", "optional bearer token")
	planDir := fs.String("plan-dir", "", "local plan resume directory")
	autoApprove := fs.Bool("auto-approve", false, "approve mutations without interactive confirmation")
	noPrompt := fs.Bool("no-prompt", false, "do not print the prompt")
	if err := fs.Parse(args); err != nil {
		return tui.Config{}, err
	}
	if name == "standalone" && strings.TrimSpace(*endpoint) != "" {
		return tui.Config{}, fmt.Errorf("standalone mode uses its in-process MCP server; use `tui --url ...` for an attached client")
	}
	if strings.TrimSpace(*endpoint) == "" {
		*endpoint = os.Getenv("OPUTE_HOST_AGENT_URL")
	}
	if strings.TrimSpace(*token) == "" {
		*token = os.Getenv("MCP_AUTH_TOKEN")
	}
	return tui.Config{
		Endpoint:    *endpoint,
		Token:       *token,
		PlanDir:     *planDir,
		AutoApprove: *autoApprove,
		NoPrompt:    *noPrompt,
	}, nil
}

func printUsage(out io.Writer) {
	fmt.Fprintln(out, "Usage: opute-host-agent [standalone|serve|tui] [flags]")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "  opute-host-agent                   standalone MCP server + TUI in one process")
	fmt.Fprintln(out, "  opute-host-agent serve             MCP server only (HTTP)")
	fmt.Fprintln(out, "  opute-host-agent tui --url URL     TUI attached to an existing MCP server")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Standalone mode never requires Opute Platform.")
}

func setenv(key, value string) {
	if strings.TrimSpace(value) != "" {
		_ = os.Setenv(key, value)
	}
}

type envFlags []string

func (e *envFlags) String() string { return strings.Join(*e, ",") }

func (e *envFlags) Set(value string) error {
	*e = append(*e, value)
	return nil
}
