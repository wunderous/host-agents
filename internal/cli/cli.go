package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wunderous/host-agents/internal/app"
	"github.com/wunderous/host-agents/internal/config"
	"github.com/wunderous/host-agents/internal/version"
	"github.com/wunderous/host-agents/pkg/hostagentclient"
)

// Run executes the server-only command surface. The deterministic TUI is a
// separately released Bun application and is never launched or discovered by
// this binary.
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
	case "recipe":
		return runRecipe(ctx, commandArgs, stdout, stderr)
	case "provider":
		return runProvider(ctx, commandArgs, stdout, stderr)
	case "help":
		printUsage(stdout)
		return nil
	default:
		return fmt.Errorf("unknown command %q; use standalone, serve, recipe, provider, or help", command)
	}
}

func splitCommand(args []string) (string, []string) {
	if len(args) == 0 {
		return "serve", nil
	}
	first := strings.TrimSpace(args[0])
	if first == "standalone" || first == "serve" || first == "recipe" || first == "provider" || first == "help" {
		return first, args[1:]
	}
	// Flags retain the server's historical implicit command behavior. In
	// particular, --url/--token are no longer silently routed to a client;
	// serve rejects them as unknown flags so the breaking surface is explicit.
	if strings.HasPrefix(first, "-") {
		return "serve", args
	}
	return first, args[1:]
}

func runRecipe(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("recipe requires validate, apply, or status")
	}
	subcommand := args[0]
	fs := flag.NewFlagSet("recipe "+subcommand, flag.ContinueOnError)
	fs.SetOutput(stderr)
	source := fs.String("source", "", "recipe path or pinned remote source")
	revision := fs.String("revision", "", "immutable source revision")
	sha256Hash := fs.String("sha256", "", "expected raw recipe sha256, as hex or sha256:<hex>")
	kind := fs.String("kind", "runtime", "recipe family: runtime or tunnel")
	runID := fs.String("run-id", "", "durable recipe run ID")
	resume := fs.Bool("resume", false, "resume a persisted recipe run")
	wait := fs.Bool("wait", true, "wait for recipe apply to reach a terminal state")
	activate := fs.Bool("activate", false, "after successful validation, make this runtime active for its declared capability")
	var inputFlags stringFlags
	fs.Var(&inputFlags, "input", "recipe input as key=value; repeatable")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if subcommand != "validate" && subcommand != "apply" && subcommand != "status" {
		return fmt.Errorf("unknown recipe command %q; use validate, apply, or status", subcommand)
	}
	if subcommand == "apply" && !*wait {
		return fmt.Errorf("recipe apply always waits for a terminal result; use the MCP operation directly for asynchronous execution")
	}
	if *kind != "runtime" && *kind != "tunnel" {
		return fmt.Errorf("recipe kind must be runtime or tunnel")
	}
	arguments := map[string]any{}
	toolName := ""
	validateTool := "validate_runtime_recipe"
	runTool := "run_runtime_recipe"
	getTool := "get_runtime_recipe_run"
	if *kind == "tunnel" {
		validateTool, runTool, getTool = "validate_tunnel_recipe", "run_tunnel_recipe", "get_tunnel_run"
	}
	switch subcommand {
	case "validate":
		toolName = validateTool
	case "apply":
		toolName = runTool
	case "status":
		toolName = getTool
	}
	if subcommand == "status" {
		if strings.TrimSpace(*runID) == "" {
			return fmt.Errorf("recipe status requires --run-id")
		}
		arguments["runId"] = strings.TrimSpace(*runID)
	} else {
		if strings.TrimSpace(*source) == "" {
			return fmt.Errorf("recipe %s requires --source", subcommand)
		}
		arguments["source"] = strings.TrimSpace(*source)
		if strings.TrimSpace(*revision) != "" {
			arguments["revision"] = strings.TrimSpace(*revision)
		}
		if strings.TrimSpace(*sha256Hash) != "" {
			arguments["sha256"] = strings.TrimSpace(*sha256Hash)
		}
		if len(inputFlags) > 0 {
			inputs, err := parseRecipeInputs(inputFlags)
			if err != nil {
				return err
			}
			arguments["inputs"] = inputs
		}
		if subcommand == "apply" {
			if *resume {
				arguments["resume"] = true
			}
			if *activate {
				arguments["activate"] = true
			}
		}
	}
	logger := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
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
	client, err := hostagentclient.ConnectWithTransport(ctx, clientTransport)
	if err != nil {
		return err
	}
	defer client.Close()
	result, err := client.Call(ctx, toolName, arguments)
	if err != nil {
		return err
	}
	if result == nil {
		return fmt.Errorf("recipe operation returned no result")
	}
	if result.IsError {
		return fmt.Errorf("recipe operation failed: %s", cliResultText(result))
	}
	if subcommand == "apply" && *wait {
		started, ok := cliStructuredObject(result.StructuredContent)
		if !ok {
			return fmt.Errorf("recipe apply returned no structured run identity")
		}
		runID, _ := started["runId"].(string)
		if strings.TrimSpace(runID) == "" {
			return fmt.Errorf("recipe apply returned no run ID")
		}
		result, err = waitForRecipeRun(ctx, client, runID, getTool)
		if err != nil {
			return err
		}
		if result.IsError {
			return fmt.Errorf("recipe operation failed: %s", cliResultText(result))
		}
		status, _ := cliStructuredObject(result.StructuredContent)
		if value, _ := status["status"].(string); value != "completed" {
			if message, _ := status["error"].(string); message != "" {
				return fmt.Errorf("recipe operation ended with status %q: %s", value, message)
			}
			return fmt.Errorf("recipe operation ended with status %q", value)
		}
	}
	encoded, err := json.MarshalIndent(result.StructuredContent, "", "  ")
	if err != nil {
		return fmt.Errorf("encode recipe result: %w", err)
	}
	_, err = fmt.Fprintln(stdout, string(encoded))
	return err
}

func runProvider(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("provider requires install, validate, status, or reload")
	}
	subcommand := args[0]
	fs := flag.NewFlagSet("provider "+subcommand, flag.ContinueOnError)
	fs.SetOutput(stderr)
	source := fs.String("source", "", "trusted local provider descriptor path")
	endpoint := fs.String("endpoint", "", "provider MCP endpoint override")
	token := fs.String("token", "", "provider bearer token")
	mode := fs.String("mode", "", "provider recipe mode, such as managed or external")
	providerID := fs.String("provider", "", "connected provider ID")
	operation := fs.String("operation", "", "provider validation operation")
	activate := fs.Bool("activate", false, "after successful validation, make this provider active")
	var inputFlags stringFlags
	fs.Var(&inputFlags, "input", "provider input as key=value; repeatable")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if subcommand != "install" && subcommand != "validate" && subcommand != "status" && subcommand != "reload" {
		return fmt.Errorf("unknown provider command %q; use install, validate, status, or reload", subcommand)
	}
	arguments := map[string]any{}
	toolName := "opute.provider." + subcommand
	if *source != "" {
		arguments["source"] = *source
	}
	if *endpoint != "" {
		arguments["endpoint"] = *endpoint
	}
	if *token != "" {
		arguments["token"] = *token
	}
	if *mode != "" {
		arguments["mode"] = *mode
	}
	if *activate {
		arguments["activate"] = true
	}
	if len(inputFlags) > 0 {
		inputs, err := parseRecipeInputs(inputFlags)
		if err != nil {
			return err
		}
		arguments["inputs"] = inputs
	}
	if *providerID != "" {
		arguments["provider"] = *providerID
	}
	if *operation != "" {
		arguments["operation"] = *operation
	}
	if (subcommand == "install" || subcommand == "reload") && *source == "" {
		return fmt.Errorf("provider %s requires --source", subcommand)
	}
	if subcommand == "validate" || subcommand == "status" {
		if *providerID == "" {
			return fmt.Errorf("provider %s requires --provider", subcommand)
		}
	}
	logger := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
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
	client, err := hostagentclient.ConnectWithTransport(ctx, clientTransport)
	if err != nil {
		return err
	}
	defer client.Close()
	result, err := client.Call(ctx, toolName, arguments)
	if err != nil {
		return err
	}
	if result == nil {
		return fmt.Errorf("provider operation returned no result")
	}
	if result.IsError {
		return fmt.Errorf("provider operation failed: %s", cliResultText(result))
	}
	if subcommand == "install" || subcommand == "reload" {
		if started, ok := cliStructuredObject(result.StructuredContent); ok {
			if runID, _ := started["runId"].(string); strings.TrimSpace(runID) != "" {
				statusTool := "get_runtime_recipe_run"
				if *mode == "tunnel" {
					statusTool = "get_tunnel_run"
				}
				result, err = waitForRecipeRun(ctx, client, runID, statusTool)
				if err != nil {
					return err
				}
				if result.IsError {
					return fmt.Errorf("provider operation failed: %s", cliResultText(result))
				}
				status, _ := cliStructuredObject(result.StructuredContent)
				if value, _ := status["status"].(string); value != "completed" {
					message, _ := status["error"].(string)
					if message != "" {
						return fmt.Errorf("provider operation ended with status %q: %s", value, message)
					}
					return fmt.Errorf("provider operation ended with status %q", value)
				}
			}
		}
	}
	encoded, err := json.MarshalIndent(result.StructuredContent, "", "  ")
	if err != nil {
		return fmt.Errorf("encode provider result: %w", err)
	}
	_, err = fmt.Fprintln(stdout, string(encoded))
	return err
}

func waitForRecipeRun(ctx context.Context, client *hostagentclient.Client, runID, statusTool string) (*mcp.CallToolResult, error) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		result, err := client.Call(ctx, statusTool, map[string]any{"runId": runID})
		if err != nil {
			return nil, err
		}
		object, ok := cliStructuredObject(result.StructuredContent)
		if !ok {
			return nil, fmt.Errorf("recipe status returned no structured result")
		}
		status, _ := object["status"].(string)
		switch status {
		case "completed", "failed", "cancelled", "unknown":
			return result, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

func cliStructuredObject(value any) (map[string]any, bool) {
	if object, ok := value.(map[string]any); ok {
		return object, true
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, false
	}
	var object map[string]any
	if err := json.Unmarshal(encoded, &object); err != nil || object == nil {
		return nil, false
	}
	return object, true
}

func cliResultText(result *mcp.CallToolResult) string {
	if result == nil {
		return ""
	}
	var parts []string
	for _, content := range result.Content {
		if textContent, ok := content.(*mcp.TextContent); ok && strings.TrimSpace(textContent.Text) != "" {
			parts = append(parts, textContent.Text)
		}
	}
	return strings.Join(parts, "; ")
}

type stringFlags []string

func (f *stringFlags) String() string { return strings.Join(*f, ",") }

func (f *stringFlags) Set(value string) error {
	*f = append(*f, value)
	return nil
}

func parseRecipeInputs(values []string) (map[string]any, error) {
	inputs := make(map[string]any, len(values))
	for _, value := range values {
		key, raw, ok := strings.Cut(value, "=")
		if !ok || strings.TrimSpace(key) == "" {
			return nil, fmt.Errorf("--input requires key=value")
		}
		key = strings.TrimSpace(key)
		if strings.HasPrefix(raw, "@env:") {
			name := strings.TrimSpace(strings.TrimPrefix(raw, "@env:"))
			if name == "" || os.Getenv(name) == "" {
				return nil, fmt.Errorf("environment input %q is missing", name)
			}
			inputs[key] = os.Getenv(name)
			continue
		}
		var decoded any
		if err := json.Unmarshal([]byte(raw), &decoded); err == nil {
			inputs[key] = decoded
		} else {
			inputs[key] = raw
		}
	}
	return inputs, nil
}

func runStandalone(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	return runServer(ctx, append([]string{"--mode=standalone"}, args...), stdout, stderr)
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

func printUsage(out io.Writer) {
	fmt.Fprintln(out, "Usage: opute-host-agent [standalone|serve|recipe|provider|help] [flags]")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "  opute-host-agent                   server-only standalone MCP profile (HTTP)")
	fmt.Fprintln(out, "  opute-host-agent serve             MCP server only (HTTP)")
	fmt.Fprintln(out, "  opute-host-agent recipe validate --source ./recipe.yaml")
	fmt.Fprintln(out, "  opute-host-agent recipe apply --source ./recipe.yaml --activate --input model=qwen3.5:2b")
	fmt.Fprintln(out, "  opute-host-agent recipe status --run-id RUN_ID")
	fmt.Fprintln(out, "  opute-host-agent provider install --source ./plugin.yaml --activate")
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
