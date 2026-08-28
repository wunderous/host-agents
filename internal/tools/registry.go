package tools

import (
	"context"
	"fmt"
	"sort"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/wunderous/host-agents/internal/hostagent"
)

// ToolHandler executes one host capability. Every dispatch entry is one of
// these, registered under exactly one name.
type ToolHandler func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error)

// toolHandlers is the dispatch table. It replaces a 1,067-line switch whose
// only machine-readable form was its own source text (plan §2.4, W2).
var toolHandlers = map[string]ToolHandler{}

// register binds one handler to one tool name.
//
// The duplicate panic is the partition guarantee: once the eight domain
// packages each register their own names, a tool claimed by two domains fails
// at init rather than resolving to whichever file the linker saw last. That is
// what made partitioning the old internal/ops safe (W2, M3).
func register(name string, handler ToolHandler) {
	if _, exists := toolHandlers[name]; exists {
		panic(fmt.Sprintf("dispatch: tool %q registered twice", name))
	}
	if handler == nil {
		panic(fmt.Sprintf("dispatch: tool %q registered with a nil handler", name))
	}
	toolHandlers[name] = handler
}

// LookupTool returns the handler registered for name, if any.
func LookupTool(name string) (ToolHandler, bool) {
	handler, ok := toolHandlers[name]
	return handler, ok
}

// RegisteredToolNames returns every registered dispatch name, sorted.
func RegisteredToolNames() []string {
	names := make([]string, 0, len(toolHandlers))
	for name := range toolHandlers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
