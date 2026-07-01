package console

import (
	"fmt"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Runtime manages active VM console streams.
type Runtime struct {
	mu sync.Mutex
	// operationId -> cancel func
	streams map[string]func()
}

func NewRuntime() *Runtime {
	return &Runtime{streams: make(map[string]func())}
}

func (r *Runtime) AbortAll() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, cancel := range r.streams {
		cancel()
		delete(r.streams, id)
	}
}

func (r *Runtime) Register(operationID string, cancel func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.streams[operationID] = cancel
}

func (r *Runtime) Unregister(operationID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.streams, operationID)
}

// StreamVMConsole starts a console stream (stub until PTY wiring is complete).
func (r *Runtime) StreamVMConsole(vmName, operationID string) (*mcp.CallToolResult, error) {
	if vmName == "" {
		return nil, fmt.Errorf("vmName is required")
	}
	if operationID == "" {
		operationID = fmt.Sprintf("console-%s", vmName)
	}
	r.Register(operationID, func() {})
	payload := map[string]any{
		"vmName":      vmName,
		"status":      "started",
		"operationId": operationID,
	}
	return &mcp.CallToolResult{
		Content:           []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Console stream started for VM '%s'.", vmName)}},
		StructuredContent: payload,
	}, nil
}

func (r *Runtime) SendConsoleInput(operationID, data string) (*mcp.CallToolResult, error) {
	if operationID == "" {
		return nil, fmt.Errorf("operationId is required")
	}
	payload := map[string]any{"status": "sent", "operationId": operationID}
	return &mcp.CallToolResult{
		Content:           []mcp.Content{&mcp.TextContent{Text: "Input sent."}},
		StructuredContent: payload,
	}, nil
}

func (r *Runtime) ResizeConsole(operationID string, width, height int) (*mcp.CallToolResult, error) {
	if operationID == "" {
		return nil, fmt.Errorf("operationId is required")
	}
	payload := map[string]any{"status": "resized", "operationId": operationID}
	return &mcp.CallToolResult{
		Content:           []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Resized console to %dx%d.", width, height)}},
		StructuredContent: payload,
	}, nil
}
