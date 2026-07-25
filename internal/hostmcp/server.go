package hostmcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wunderous/host-agents/internal/console"
	"github.com/wunderous/host-agents/internal/ops"
	"github.com/wunderous/host-agents/internal/state"
	"github.com/wunderous/host-agents/internal/tasks"
	"github.com/wunderous/host-agents/internal/tools"
	"github.com/wunderous/host-agents/internal/version"
)

// Server is the host agent MCP server.
type Server struct {
	mcpServer      *mcp.Server
	ops            *ops.HostOperationsService
	tasks          *tasks.Registry
	console        *console.Runtime
	providerID     string
	standalone     bool
	allowMutations bool
	state          *state.Store
	mu             sync.Mutex
	toolDefs       []tools.ToolDefinition
}

type Options struct {
	ProviderID     string
	Ops            *ops.HostOperationsService
	Logger         *slog.Logger
	Standalone     bool
	AllowMutations bool
	StateDir       string
	Version        string
}

func NewServer(opts Options) (*Server, error) {
	if opts.Ops == nil {
		return nil, fmt.Errorf("ops service is required")
	}
	providerID := opts.ProviderID
	if providerID == "" {
		providerID = opts.Ops.ReadProviderID()
	}
	if opts.Standalone {
		if err := tools.ValidateStandaloneToolContract(); err != nil {
			return nil, err
		}
	}
	catalog, err := tools.HostToolDefinitionsForProvider(providerID)
	if err != nil {
		return nil, err
	}
	capabilities := &mcp.ServerCapabilities{
		Tools:     &mcp.ToolCapabilities{ListChanged: true},
		Resources: &mcp.ResourceCapabilities{ListChanged: true},
	}
	capabilities.Experimental = map[string]any{
		"tasks": map[string]any{
			"list":   map[string]any{},
			"cancel": map[string]any{},
			"requests": map[string]any{
				"tools": map[string]any{"call": map[string]any{}},
			},
		},
	}
	serverVersion := opts.Version
	if serverVersion == "" {
		serverVersion = version.Version
	}
	srv := mcp.NewServer(&mcp.Implementation{Name: "host-agent", Version: serverVersion}, &mcp.ServerOptions{
		Capabilities: capabilities,
		Logger:       opts.Logger,
	})
	hs := &Server{
		mcpServer:      srv,
		ops:            opts.Ops,
		tasks:          tasks.NewRegistry(),
		console:        console.NewRuntime(),
		providerID:     providerID,
		standalone:     opts.Standalone,
		allowMutations: opts.AllowMutations,
		toolDefs:       catalog,
	}
	if opts.Standalone {
		store, err := state.Open(opts.StateDir)
		if err != nil {
			return nil, err
		}
		hs.state = store
	}
	hs.registerTools()
	return hs, nil
}

// Close releases standalone-owned resources. Platform mode is also safe to
// close, which keeps shutdown behavior consistent across profiles.
func (s *Server) Close() error {
	if s == nil || s.state == nil {
		return nil
	}
	err := s.state.Close()
	s.state = nil
	return err
}

func (s *Server) MCP() *mcp.Server {
	return s.mcpServer
}

func (s *Server) Tasks() *tasks.Registry {
	return s.tasks
}

func (s *Server) AbortAllConsoleStreams() {
	s.console.AbortAll()
}

func (s *Server) registerTools() {
	if s.standalone {
		s.registerStandaloneTools()
		return
	}
	allDefs, err := tools.LoadAllToolDefinitions("all")
	if err != nil {
		allDefs = s.toolDefs
	}
	internalDefs, ierr := tools.LoadCatalogExcludedDispatchToolDefinitions()
	if ierr == nil {
		allDefs = append(allDefs, internalDefs...)
	}
	registered := map[string]bool{}
	for _, def := range allDefs {
		if tools.IsOmittedToolName(def.Name) {
			continue
		}
		if registered[def.Name] {
			continue
		}
		registered[def.Name] = true
		s.addRegisteredTool(def)
	}
}

func (s *Server) registerStandaloneTools() {
	defs := tools.StandaloneToolDefinitions()
	all, err := tools.LoadAllToolDefinitions("all")
	if err == nil {
		for _, def := range all {
			if tools.StandaloneToolNames[def.Name] {
				defs = append(defs, def)
			}
		}
	}
	seen := map[string]bool{}
	for _, def := range defs {
		if seen[def.Name] {
			continue
		}
		seen[def.Name] = true
		s.addRegisteredTool(def)
	}
}

func (s *Server) addRegisteredTool(def tools.ToolDefinition) {
	tool := &mcp.Tool{
		Name:        def.Name,
		Description: def.Description,
	}
	if s.standalone {
		tool.Meta = tools.StandaloneToolMetadata(def.Name)
	}
	if def.InputSchema != nil {
		tool.InputSchema = def.InputSchema
	}
	if def.OutputSchema != nil {
		tool.OutputSchema = def.OutputSchema
	}
	name := def.Name
	s.mcpServer.AddTool(tool, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return s.handleToolCall(ctx, req, name)
	})
}

func (s *Server) handleToolCall(ctx context.Context, req *mcp.CallToolRequest, name string) (*mcp.CallToolResult, error) {
	args := map[string]any{}
	if len(req.Params.Arguments) > 0 {
		if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
			return tools.ErrorResult(fmt.Errorf("invalid arguments: %w", err)), nil
		}
	}
	if s.standalone && tools.IsStandaloneMutation(name) && !s.allowMutations {
		return tools.ErrorResult(fmt.Errorf("standalone mutations are disabled; set OPUTE_STANDALONE_ALLOW_MUTATIONS=true")), nil
	}
	if s.standalone {
		switch name {
		case "list_operations":
			if s.state != nil {
				limit := intFromAny(args["limit"])
				operations, err := s.state.List(limit)
				if err != nil {
					return tools.ErrorResult(err), nil
				}
				return structuredResult(map[string]any{"operations": operations}, ""), nil
			}
			return structuredResult(map[string]any{"operations": s.tasks.List()}, ""), nil
		case "get_operation":
			id, _ := args["operationId"].(string)
			if s.state != nil {
				operation, found, err := s.state.Get(id)
				if err != nil {
					return tools.ErrorResult(err), nil
				}
				if found {
					return structuredResult(operation, ""), nil
				}
			}
			rec, ok := s.tasks.Get(id)
			if !ok {
				return tools.ErrorResult(fmt.Errorf("operation not found: %s", id)), nil
			}
			return structuredResult(s.tasks.ToGetTaskResult(rec), ""), nil
		case "cancel_operation":
			id, _ := args["operationId"].(string)
			if s.state != nil {
				_ = s.state.Cancel(id)
			}
			rec, ok := s.tasks.Cancel(id)
			if !ok || rec == nil {
				return tools.ErrorResult(fmt.Errorf("operation cannot be cancelled: %s", id)), nil
			}
			return structuredResult(s.tasks.ToGetTaskResult(rec), ""), nil
		}
	}
	if name == "stream_vm_console" {
		vmName, _ := args["vmName"].(string)
		opID, _ := args["operationId"].(string)
		return s.console.StreamVMConsole(vmName, opID)
	}
	if name == "send_console_input" {
		opID, _ := args["operationId"].(string)
		data, _ := args["data"].(string)
		return s.console.SendConsoleInput(opID, data)
	}
	if name == "resize_console" {
		opID, _ := args["operationId"].(string)
		width := intFromAny(args["width"])
		height := intFromAny(args["height"])
		return s.console.ResizeConsole(opID, width, height)
	}
	if tasks.TaskAwareTools[name] && (hasTaskAugmentation(req) || s.standalone) {
		return s.createAsyncTask(name, args)
	}
	onData := func(chunk string) {}
	return tools.DispatchTool(ctx, s.ops, name, args, onData)
}

func structuredResult(value any, text string) *mcp.CallToolResult {
	content := []mcp.Content{}
	if text != "" {
		content = append(content, &mcp.TextContent{Text: text})
	}
	return &mcp.CallToolResult{Content: content, StructuredContent: value}
}

func hasTaskAugmentation(req *mcp.CallToolRequest) bool {
	if req.Params == nil {
		return false
	}
	if req.Params.Meta != nil {
		if _, ok := req.Params.Meta["task"]; ok {
			return true
		}
	}
	return false
}

func (s *Server) createAsyncTask(name string, args map[string]any) (*mcp.CallToolResult, error) {
	desc := fmt.Sprintf("Executing %s...", name)
	if vm, ok := args["vmName"].(string); ok && vm != "" {
		desc = fmt.Sprintf("Running %s on '%s'...", name, vm)
	}
	taskCtx, cancel := context.WithCancel(context.Background())
	rec := s.tasks.CreateWithCancel(name, redactTaskArgs(args), time.Hour, desc, nil, cancel)
	if s.state != nil {
		_ = s.state.Create(rec.TaskID, name, desc)
	}
	go func(taskID string) {
		onData := func(chunk string) { s.tasks.AppendLog(taskID, chunk) }
		result, err := tools.DispatchTool(taskCtx, s.ops, name, args, onData)
		if err != nil {
			if s.state != nil {
				_ = s.state.Fail(taskID, err.Error())
			}
			s.tasks.Fail(taskID, err.Error())
			return
		}
		if result.IsError {
			message := "operation failed"
			for _, content := range result.Content {
				if text, ok := content.(*mcp.TextContent); ok && strings.TrimSpace(text.Text) != "" {
					message = text.Text
					break
				}
			}
			if s.state != nil {
				_ = s.state.Fail(taskID, message)
			}
			s.tasks.Fail(taskID, message)
			return
		}
		tr := tasks.ToolResult{StructuredContent: result.StructuredContent, IsError: result.IsError}
		for _, c := range result.Content {
			if tc, ok := c.(*mcp.TextContent); ok {
				tr.Content = append(tr.Content, map[string]any{"type": "text", "text": tc.Text})
			}
		}
		s.tasks.Complete(taskID, tr)
		if s.state != nil {
			_ = s.state.Complete(taskID, tr)
		}
	}(rec.TaskID)
	return &mcp.CallToolResult{
		Content:           []mcp.Content{&mcp.TextContent{Text: desc}},
		StructuredContent: map[string]any{"taskId": rec.TaskID, "status": rec.Status},
	}, nil
}

// redactTaskArgs keeps task inspection useful without retaining credentials or
// arbitrary manifests in the in-memory task registry. The original arguments
// remain available only to the running goroutine.
func redactTaskArgs(args map[string]any) map[string]any {
	return redactTaskValue(args).(map[string]any)
}

func redactTaskValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, child := range typed {
			lower := strings.ToLower(key)
			if strings.Contains(lower, "token") || strings.Contains(lower, "password") || strings.Contains(lower, "secret") || lower == "manifest" || lower == "sql" {
				out[key] = "[redacted]"
				continue
			}
			out[key] = redactTaskValue(child)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, child := range typed {
			out[i] = redactTaskValue(child)
		}
		return out
	default:
		return value
	}
}

// HandleExtensionMethod serves tasks/* and custom resources/* when go-sdk lacks native task support.
func (s *Server) HandleExtensionMethod(method string, params json.RawMessage) (any, error) {
	switch method {
	case "tasks/list":
		items := make([]map[string]any, 0)
		for _, rec := range s.tasks.List() {
			items = append(items, s.tasks.ToGetTaskResult(rec))
		}
		return map[string]any{"tasks": items}, nil
	case "tasks/get":
		var p struct {
			TaskID string `json:"taskId"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		rec, ok := s.tasks.Get(p.TaskID)
		if !ok {
			return nil, fmt.Errorf("task not found: %s", p.TaskID)
		}
		return s.tasks.ToGetTaskResult(rec), nil
	case "tasks/cancel":
		var p struct {
			TaskID string `json:"taskId"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		rec, ok := s.tasks.Cancel(p.TaskID)
		if !ok || rec == nil {
			return nil, fmt.Errorf("cannot cancel task: %s", p.TaskID)
		}
		return s.tasks.ToGetTaskResult(rec), nil
	case "resources/list":
		return s.listTaskResources()
	case "resources/read":
		var p struct {
			URI string `json:"uri"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		return s.readTaskResource(p.URI)
	default:
		return nil, fmt.Errorf("unsupported extension method: %s", method)
	}
}

func (s *Server) listTaskResources() (map[string]any, error) {
	resources := make([]map[string]any, 0)
	for _, rec := range s.tasks.List() {
		resources = append(resources, map[string]any{
			"uri":         fmt.Sprintf("mcp://host/tasks/%s", rec.TaskID),
			"name":        fmt.Sprintf("Status for task %s", rec.TaskID[:8]),
			"description": rec.StatusMessage,
			"mimeType":    "application/json",
		})
		if len(rec.Logs) > 0 || rec.Status == tasks.StatusWorking {
			resources = append(resources, map[string]any{
				"uri":      fmt.Sprintf("mcp://host/tasks/%s/logs", rec.TaskID),
				"name":     fmt.Sprintf("Logs for task %s", rec.TaskID[:8]),
				"mimeType": "text/plain",
			})
		}
	}
	return map[string]any{"resources": resources}, nil
}

func (s *Server) readTaskResource(uri string) (map[string]any, error) {
	if strings.HasPrefix(uri, "mcp://host/tasks/") && strings.HasSuffix(uri, "/logs") {
		taskID := strings.TrimPrefix(uri, "mcp://host/tasks/")
		taskID = strings.TrimSuffix(taskID, "/logs")
		rec, ok := s.tasks.Get(taskID)
		if !ok {
			return nil, fmt.Errorf("task not found")
		}
		return map[string]any{
			"contents": []map[string]any{{
				"uri": uri, "mimeType": "text/plain", "text": strings.Join(rec.Logs, ""),
			}},
		}, nil
	}
	if strings.HasPrefix(uri, "mcp://host/tasks/") {
		taskID := strings.TrimPrefix(uri, "mcp://host/tasks/")
		rec, ok := s.tasks.Get(taskID)
		if !ok {
			return nil, fmt.Errorf("task not found")
		}
		b, _ := json.MarshalIndent(s.tasks.ToGetTaskResult(rec), "", "  ")
		return map[string]any{
			"contents": []map[string]any{{
				"uri": uri, "mimeType": "application/json", "text": string(b),
			}},
		}, nil
	}
	return nil, fmt.Errorf("invalid resource URI: %s", uri)
}

func intFromAny(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	default:
		return 0
	}
}
