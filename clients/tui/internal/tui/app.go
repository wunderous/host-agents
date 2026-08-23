package tui

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/wunderous/host-agents/pkg/hostagentclient"
)

// Config is the small presentation boundary for the standalone TUI process.
// The process owns editing and rendering; all capability discovery,
// validation, authorization, and execution remain on Host Agent.
type Config struct {
	Endpoint string
	Token    string
	In       io.Reader
	Out      io.Writer
	NoPrompt bool
}

// Run starts the deterministic, LLM-independent terminal client. It uses the
// same catalog-driven Executor as library consumers and sends exactly one MCP
// call for each submitted command.
func Run(ctx context.Context, config Config) error {
	if config.In == nil {
		config.In = strings.NewReader("")
	}
	if config.Out == nil {
		config.Out = io.Discard
	}
	client, err := hostagentclient.Connect(ctx, config.Endpoint, config.Token)
	if err != nil {
		return err
	}
	defer client.Close()
	executor := NewExecutor(client)
	if err := executor.Refresh(ctx); err != nil {
		return err
	}
	fmt.Fprintln(config.Out, "opute-host-agent connected")
	fmt.Fprintf(config.Out, "catalog revision: %s\n", executor.Catalog.Revision)
	fmt.Fprintln(config.Out, "deterministic mode is ready")

	scanner := bufio.NewScanner(config.In)
	scanner.Buffer(make([]byte, 4096), 128*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if !config.NoPrompt {
			fmt.Fprint(config.Out, "❯ ")
		}
		if line == "/exit" || line == "exit" {
			fmt.Fprintln(config.Out, "bye")
			return nil
		}
		switch line {
		case "/context", "/help":
			fmt.Fprintf(config.Out, "catalog revision: %s\n", executor.Catalog.Revision)
			continue
		case "/tools":
			for _, descriptor := range executor.Catalog.Tools {
				fmt.Fprintln(config.Out, descriptor.Name)
			}
			continue
		}
		command, err := ParseCommand(line)
		if err != nil {
			fmt.Fprintf(config.Out, "error: %s\n", err)
			continue
		}
		arguments := make(map[string]any, len(command.Arguments))
		for name, value := range command.Arguments {
			arguments[name] = value.Typed
		}
		arguments, err = resolveEntityReferences(ctx, executor, arguments)
		if err != nil {
			fmt.Fprintf(config.Out, "error: %s\n", err)
			continue
		}
		receipt, err := executor.ExecuteDraft(ctx, CommandDraft{
			Operation:       command.Operation,
			Arguments:       arguments,
			CatalogRevision: executor.Catalog.Revision,
		})
		if err != nil {
			fmt.Fprintf(config.Out, "error: %s\n", err)
			continue
		}
		encoded, _ := json.Marshal(receipt.Result.StructuredContent)
		fmt.Fprintln(config.Out, string(encoded))
	}
	return scanner.Err()
}

func resolveEntityReferences(ctx context.Context, executor *Executor, arguments map[string]any) (map[string]any, error) {
	resolved := cloneMap(arguments)
	for name, value := range arguments {
		token, ok := value.(string)
		if !ok || !strings.HasPrefix(token, "@") {
			continue
		}
		entities, err := executor.ListVMs(ctx)
		if err != nil {
			return nil, fmt.Errorf("resolve %s: %w", name, err)
		}
		query := strings.TrimPrefix(token, "@")
		query = strings.TrimPrefix(query, "vm:")
		var match *Entity
		for index := range entities {
			if entities[index].Name == query {
				candidate := entities[index]
				match = &candidate
				break
			}
		}
		if match == nil {
			return nil, fmt.Errorf("no authorized entity matches %q", token)
		}
		resolved[name] = match.Name
	}
	return resolved, nil
}
