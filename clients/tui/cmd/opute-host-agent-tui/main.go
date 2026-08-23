package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/wunderous/host-agents/clients/tui/internal/tui"
	"github.com/wunderous/host-agents/pkg/hostagentclient"
)

func main() {
	endpoint := flag.String("url", "http://127.0.0.1:3014/mcp", "Host Agent MCP endpoint")
	token := flag.String("token", "", "Host Agent bearer token")
	call := flag.String("call", "", "optional operation to call")
	argsJSON := flag.String("args", "{}", "JSON arguments for --call")
	interactive := flag.Bool("interactive", true, "run the deterministic terminal client when --call is omitted")
	noPrompt := flag.Bool("no-prompt", false, "do not render an input prompt")
	flag.Bool("auto-approve", false, "compatibility flag; Host Agent still enforces approval")
	flag.String("plan-dir", "", "compatibility flag for durable plan clients")
	flag.Parse()
	if *call == "" && *interactive {
		if err := tui.Run(context.Background(), tui.Config{Endpoint: *endpoint, Token: *token, In: os.Stdin, Out: os.Stdout, NoPrompt: *noPrompt}); err != nil {
			fail(err)
		}
		return
	}

	client, err := hostagentclient.Connect(context.Background(), *endpoint, *token)
	if err != nil {
		fail(err)
	}
	defer client.Close()
	executor := tui.NewExecutor(client)
	if *call == "" {
		if err := executor.Refresh(context.Background()); err != nil {
			fail(err)
		}
		printJSON(executor.Catalog)
		return
	}
	var arguments map[string]any
	if err := json.Unmarshal([]byte(*argsJSON), &arguments); err != nil {
		fail(fmt.Errorf("decode --args: %w", err))
	}
	result, err := client.Call(context.Background(), *call, arguments)
	if err != nil {
		fail(err)
	}
	if result != nil && result.IsError {
		fail(fmt.Errorf("operation %q failed", *call))
	}
	if result == nil {
		fail(fmt.Errorf("operation %q returned no result", *call))
	}
	printJSON(result.StructuredContent)
}

func printJSON(value any) {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		fail(err)
	}
	fmt.Println(string(encoded))
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
