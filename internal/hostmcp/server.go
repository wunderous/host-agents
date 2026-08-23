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
	providercontract "github.com/wunderous/host-agents/contracts/provider"
	capabilitycatalog "github.com/wunderous/host-agents/internal/catalog"
	"github.com/wunderous/host-agents/internal/console"
	"github.com/wunderous/host-agents/internal/cordis"
	provideradapter "github.com/wunderous/host-agents/internal/cordis/mcp"
	"github.com/wunderous/host-agents/internal/ops"
	"github.com/wunderous/host-agents/internal/resource"
	"github.com/wunderous/host-agents/internal/resourceid"
	"github.com/wunderous/host-agents/internal/state"
	"github.com/wunderous/host-agents/internal/tasks"
	"github.com/wunderous/host-agents/internal/tools"
	"github.com/wunderous/host-agents/internal/version"
)

// Server is the host agent MCP server.
type Server struct {
	mcpServer                  *mcp.Server
	ops                        *ops.HostOperationsService
	tasks                      *tasks.Registry
	console                    *console.Runtime
	providerID                 string
	standalone                 bool
	allowMutations             bool
	state                      *state.Store
	admission                  *resource.Coordinator
	mu                         sync.Mutex
	catalogMu                  sync.RWMutex
	planMu                     sync.Mutex
	planWG                     sync.WaitGroup
	closed                     bool
	planCancels                map[string]context.CancelFunc
	toolDefs                   []tools.ToolDefinition
	catalog                    tools.CapabilityCatalogSnapshot
	registry                   *capabilitycatalog.Registry
	dynamic                    map[string]CapabilityImplementation
	providerContext            *cordis.Context
	providerLifecycle          *cordis.ProviderLifecycleManager
	providerAdapters           map[string]*provideradapter.Adapter
	providerMu                 sync.RWMutex
	providerValidation         map[string]string
	providerCandidates         map[string]*provideradapter.Adapter
	providerCandidateManifests map[string]providercontract.InstallManifest
	providerPreviousAdapters   map[string]*provideradapter.Adapter
	providerPreviousValidation map[string]string
}

// CapabilityImplementation is a trusted, already-installed host adapter. It
// is deliberately a typed function boundary: registering metadata never loads
// code, accepts shell text, or creates an executable capability by itself.
type CapabilityImplementation func(context.Context, map[string]any) (*mcp.CallToolResult, error)

type Options struct {
	ProviderID             string
	Ops                    *ops.HostOperationsService
	Logger                 *slog.Logger
	Standalone             bool
	AllowMutations         bool
	StateDir               string
	Version                string
	Admission              *resource.Coordinator
	AllowedImplementations map[string]bool
	AuthorizedProviders    map[string]bool
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
	capabilityDefs := append([]tools.ToolDefinition(nil), catalog...)
	if opts.Standalone {
		capabilityDefs = append(capabilityDefs, tools.StandaloneToolDefinitions()...)
		if all, loadErr := tools.LoadAllToolDefinitions("all"); loadErr == nil {
			for _, def := range all {
				if tools.StandaloneToolNames[def.Name] {
					capabilityDefs = append(capabilityDefs, def)
				}
			}
		}
	}
	capabilityDefs = tools.CanonicalizeToolDefinitions(capabilityDefs)
	capabilityCatalog := tools.BuildCapabilityCatalog(providerID, capabilityDefs)
	knownResourceKinds := map[string]bool{
		"host": true, "vm": true, "container": true, "incus": true, "network": true,
		"storage": true, "image": true, "profile": true, "k3s": true, "cloudflared": true,
		"tunnel": true, "model": true, "language": true, "embedding": true, "database": true,
		"service": true, "operation": true, "plan": true,
	}
	for _, kind := range resourceid.KnownTypes() {
		knownResourceKinds[kind] = true
	}
	for _, descriptor := range capabilityCatalog.Tools {
		for _, kind := range descriptor.ResourceKinds {
			knownResourceKinds[kind] = true
		}
	}
	registry := capabilitycatalog.NewRegistry(capabilityCatalog, capabilitycatalog.Options{
		ProviderID: providerID, AuthorizedProviders: opts.AuthorizedProviders, KnownResourceKinds: knownResourceKinds, AllowedImplementations: opts.AllowedImplementations,
	})
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
		mcpServer:                  srv,
		ops:                        opts.Ops,
		tasks:                      tasks.NewRegistry(),
		console:                    console.NewRuntime(opts.Ops.NewVMInteractiveCommand),
		providerID:                 providerID,
		standalone:                 opts.Standalone,
		allowMutations:             opts.AllowMutations,
		toolDefs:                   catalog,
		admission:                  opts.Admission,
		catalog:                    capabilityCatalog,
		registry:                   registry,
		dynamic:                    make(map[string]CapabilityImplementation),
		providerContext:            cordis.NewContext(),
		providerLifecycle:          cordis.NewProviderLifecycleManager(cordis.DrainPolicy{}),
		providerAdapters:           make(map[string]*provideradapter.Adapter),
		providerValidation:         make(map[string]string),
		providerCandidates:         make(map[string]*provideradapter.Adapter),
		providerCandidateManifests: make(map[string]providercontract.InstallManifest),
		providerPreviousAdapters:   make(map[string]*provideradapter.Adapter),
		providerPreviousValidation: make(map[string]string),
		planCancels:                make(map[string]context.CancelFunc),
	}
	if opts.Standalone || strings.TrimSpace(opts.StateDir) != "" {
		store, err := state.Open(opts.StateDir)
		if err != nil {
			return nil, err
		}
		hs.state = store
		opts.Ops.SetResourceRegistry(store)
		if err := hs.restoreProviderGenerations(); err != nil {
			_ = store.Close()
			return nil, fmt.Errorf("restore provider generations: %w", err)
		}
	}
	if _, err := hs.providerContext.Plugin(resourceServicesPlugin{registry: opts.Ops.ResourceRegistry(), resolver: opts.Ops, tenantID: opts.Ops.TenantID()}); err != nil {
		return nil, fmt.Errorf("mount resource services: %w", err)
	}
	hs.registerTools()
	return hs, nil
}

// Close releases standalone-owned resources. Platform mode is also safe to
// close, which keeps shutdown behavior consistent across profiles.
func (s *Server) Close() error {
	if s == nil {
		return nil
	}
	s.planMu.Lock()
	if s.closed {
		s.planMu.Unlock()
		s.planWG.Wait()
		return nil
	}
	s.closed = true
	for runID, cancel := range s.planCancels {
		cancel()
		delete(s.planCancels, runID)
	}
	s.planMu.Unlock()
	s.planWG.Wait()
	s.planMu.Lock()
	store := s.state
	s.state = nil
	s.planMu.Unlock()
	if s.providerContext != nil {
		_ = s.providerContext.Dispose(context.Background())
	}
	s.providerMu.Lock()
	adapters := s.providerAdapters
	s.providerAdapters = make(map[string]*provideradapter.Adapter)
	s.providerValidation = make(map[string]string)
	uniqueAdapters := make(map[*provideradapter.Adapter]struct{}, len(adapters)+len(s.providerCandidates))
	for _, adapter := range adapters {
		uniqueAdapters[adapter] = struct{}{}
	}
	for _, adapter := range s.providerCandidates {
		uniqueAdapters[adapter] = struct{}{}
	}
	s.providerCandidates = make(map[string]*provideradapter.Adapter)
	s.providerPreviousAdapters = make(map[string]*provideradapter.Adapter)
	s.providerPreviousValidation = make(map[string]string)
	s.providerMu.Unlock()
	for adapter := range uniqueAdapters {
		_ = adapter.Close()
	}
	if store == nil {
		return nil
	}
	return store.Close()
}

func (s *Server) MCP() *mcp.Server {
	return s.mcpServer
}

func (s *Server) Ops() *ops.HostOperationsService {
	return s.ops
}

// CatalogSnapshot returns the immutable capability snapshot used by MCP
// registration and plan revision checks. Dynamic registration replaces the
// server's current snapshot and invalidates older client revisions.
func (s *Server) CatalogSnapshot() tools.CapabilityCatalogSnapshot {
	s.catalogMu.RLock()
	defer s.catalogMu.RUnlock()
	return cloneCatalogSnapshot(s.catalog)
}

func cloneCatalogSnapshot(snapshot tools.CapabilityCatalogSnapshot) tools.CapabilityCatalogSnapshot {
	snapshot.Tools = append([]tools.CapabilityDescriptor(nil), snapshot.Tools...)
	return snapshot
}

// RegisterCapability adds a typed capability bound to a trusted host
// implementation and publishes a new catalog revision. It is intended for a
// provider adapter during startup; callers must refresh clients after success.
func (s *Server) RegisterCapability(registration capabilitycatalog.Registration, implementation CapabilityImplementation) error {
	if s == nil || implementation == nil {
		return fmt.Errorf("server and capability implementation are required")
	}
	if s.registry == nil {
		return fmt.Errorf("capability registry is unavailable")
	}
	if err := s.registry.Register(registration); err != nil {
		return err
	}
	snapshot := s.registry.Snapshot()
	s.catalogMu.Lock()
	s.catalog = snapshot
	s.dynamic[registration.Descriptor.OperationID] = implementation
	s.catalogMu.Unlock()
	s.addRegisteredCapability(registration.Descriptor)
	return nil
}

// registerProviderServices publishes the operations declared by a validated
// provider manifest only after its candidate generation has passed setup and
// activation validation. The operation implementation remains a generic MCP
// call through the currently active provider adapter; no provider-specific
// symbols enter the Host Agent catalog.
func (s *Server) registerProviderServices(manifest providercontract.InstallManifest) error {
	if len(manifest.Services) == 0 {
		return nil
	}
	if err := s.registry.AuthorizeProvider(manifest.Provider.ID); err != nil {
		return err
	}
	implementation := "provider:" + manifest.Provider.ID
	for _, service := range manifest.Services {
		for _, operation := range service.Operations {
			descriptor := tools.CapabilityDescriptor{
				OperationID:       operation.ID,
				Name:              operation.ID,
				Description:       "Provider service " + service.ID + " operation " + operation.ID,
				InputSchema:       operation.InputSchema,
				OutputSchema:      operation.OutputSchema,
				Effect:            operation.Effect,
				Privilege:         operation.Effect,
				RequiresApproval:  operation.Effect != "read",
				Provider:          manifest.Provider.ID,
				Implementation:    implementation,
				ResourceKinds:     append([]string(nil), operation.ResourceKinds...),
				Idempotent:        operation.Idempotent,
				SupportsReadiness: operation.SupportsReadiness,
			}
			if err := s.registry.Upsert(capabilitycatalog.Registration{Descriptor: descriptor, ProviderID: manifest.Provider.ID, Implementation: implementation}); err != nil {
				return err
			}
			s.catalogMu.Lock()
			_, alreadyPublished := s.dynamic[operation.ID]
			s.catalog = s.registry.Snapshot()
			s.dynamic[operation.ID] = func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
				s.providerMu.RLock()
				adapter := s.providerAdapters[manifest.Provider.ID]
				s.providerMu.RUnlock()
				if adapter == nil {
					return tools.ErrorResult(fmt.Errorf("provider %q is not active", manifest.Provider.ID)), nil
				}
				return adapter.Call(ctx, operation.ID, args)
			}
			s.catalogMu.Unlock()
			if !alreadyPublished {
				s.addRegisteredCapability(descriptor)
			}
		}
	}
	return nil
}

// DispatchTool is the single execution boundary for MCP, task, and HWP
// callers. Keeping admission here prevents one transport from bypassing the
// host-wide policy when two agent instances share WSL/Incus resources.
func (s *Server) DispatchTool(ctx context.Context, name string, args map[string]any, onData func(string)) (*mcp.CallToolResult, error) {
	if s.requiresCanonicalURI(name) && strings.TrimSpace(stringValue(args["uri"])) == "" {
		return tools.ErrorResult(fmt.Errorf("%s requires canonical resource uri; legacy entity names are not accepted", name)), nil
	}
	if err := bindCanonicalResourceArguments(s.ops, args); err != nil {
		return tools.ErrorResult(err), nil
	}
	if s.admission == nil {
		return s.dispatchRegisteredOrBuiltIn(ctx, name, args, onData)
	}
	release, err := s.admission.Acquire(ctx, name)
	if err != nil {
		return tools.ErrorResult(err), nil
	}
	defer release()
	return s.dispatchRegisteredOrBuiltIn(ctx, name, args, onData)
}

func (s *Server) requiresCanonicalURI(name string) bool {
	s.catalogMu.RLock()
	defer s.catalogMu.RUnlock()
	for _, definition := range s.toolDefs {
		if definition.Name != name {
			continue
		}
		for _, field := range requiredSchemaFields(definition.InputSchema) {
			if field == "uri" {
				return true
			}
		}
		return false
	}
	return false
}

func requiredSchemaFields(schema map[string]any) []string {
	raw, ok := schema["required"].([]string)
	if ok {
		return raw
	}
	values, _ := schema["required"].([]any)
	fields := make([]string, 0, len(values))
	for _, value := range values {
		if field, ok := value.(string); ok {
			fields = append(fields, field)
		}
	}
	return fields
}

func stringValue(value any) string {
	valueString, _ := value.(string)
	return valueString
}

// bindCanonicalResourceArguments is the typed boundary between the public
// URI contract and legacy provider method signatures. The provider receives
// only coordinates obtained from the tenant-checked registry; callers cannot
// smuggle a guessed vmName through the MCP arguments.
func bindCanonicalResourceArguments(service *ops.HostOperationsService, args map[string]any) error {
	uri, ok := args["uri"].(string)
	if !ok || strings.TrimSpace(uri) == "" {
		return nil
	}
	coordinates, err := service.ResolveResource(uri, "")
	if err != nil {
		return err
	}
	for key, value := range coordinates.Values {
		switch typed := value.(type) {
		case string:
			if strings.TrimSpace(typed) != "" {
				args["__resolved_"+key] = typed
			}
		case bool, float64, int, int64:
			args["__resolved_"+key] = value
		}
	}
	if value, ok := coordinates.Values["providerInstanceName"].(string); ok && strings.TrimSpace(value) != "" {
		args["__resolvedVmName"] = value
	}
	return nil
}

func (s *Server) dispatchRegisteredOrBuiltIn(ctx context.Context, name string, args map[string]any, onData func(string)) (*mcp.CallToolResult, error) {
	s.catalogMu.RLock()
	implementation := s.dynamic[name]
	s.catalogMu.RUnlock()
	if implementation != nil {
		return implementation(ctx, args)
	}
	return tools.DispatchTool(ctx, s.ops, name, args, onData)
}

func (s *Server) Tasks() *tasks.Registry {
	return s.tasks
}

func (s *Server) AbortAllConsoleStreams() {
	s.console.AbortAll()
}

// OpenHostStream is the HWP stream boundary for host-owned interactive tools.
// It deliberately reuses the same console runtime as direct MCP calls so
// admission and PTY lifecycle cannot diverge by transport.
func (s *Server) OpenHostStream(operationID, action string, args map[string]any, onData func(string)) error {
	if action != "stream_vm_shell" && action != "stream_vm_console" {
		return fmt.Errorf("unsupported host stream action: %s", action)
	}
	vmName, _ := args["vmName"].(string)
	return s.console.OpenVMStream(vmName, operationID, onData)
}

// OpenHostStreamWithClose exposes the PTY close boundary to HWP.
func (s *Server) OpenHostStreamWithClose(operationID, action string, args map[string]any, onData, onClose func(string)) error {
	if action != "stream_vm_shell" && action != "stream_vm_console" {
		return fmt.Errorf("unsupported host stream action: %s", action)
	}
	vmName, _ := args["vmName"].(string)
	return s.console.OpenVMStreamWithClose(vmName, operationID, onData, onClose)
}

func (s *Server) SendHostStreamInput(operationID, data string) error {
	_, err := s.console.SendConsoleInput(operationID, data)
	return err
}

func (s *Server) ResizeHostStream(operationID string, width, height int) error {
	_, err := s.console.ResizeConsole(operationID, width, height)
	return err
}

func (s *Server) CloseHostStream(operationID string) {
	s.console.CloseStream(operationID)
}

func (s *Server) registerTools() {
	snapshot := s.CatalogSnapshot()
	if s.standalone {
		s.registerStandaloneTools(snapshot)
		return
	}
	allDefs, err := tools.LoadAllToolDefinitions("all")
	if err != nil {
		allDefs = s.toolDefs
	}
	allDefs = tools.CanonicalizeToolDefinitions(allDefs)
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
		s.addRegisteredTool(def, snapshot)
	}
}

func (s *Server) registerStandaloneTools(snapshot tools.CapabilityCatalogSnapshot) {
	defs := tools.StandaloneToolDefinitions()
	defs = tools.CanonicalizeToolDefinitions(defs)
	all, err := tools.LoadAllToolDefinitions("all")
	if err == nil {
		for _, def := range all {
			if tools.StandaloneToolNames[def.Name] {
				defs = append(defs, def)
			}
		}
		defs = tools.CanonicalizeToolDefinitions(defs)
	}
	seen := map[string]bool{}
	for _, def := range defs {
		if seen[def.Name] {
			continue
		}
		seen[def.Name] = true
		s.addRegisteredTool(def, snapshot)
	}
}

func (s *Server) addRegisteredTool(def tools.ToolDefinition, snapshot tools.CapabilityCatalogSnapshot) {
	tool := &mcp.Tool{
		Name:        def.Name,
		Description: def.Description,
		Meta:        tools.CapabilityMeta(def, snapshot),
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

func (s *Server) addRegisteredCapability(descriptor tools.CapabilityDescriptor) {
	tool := &mcp.Tool{
		Name:        descriptor.Name,
		Description: descriptor.Description,
		Meta: map[string]any{
			"catalogRevision": s.CatalogSnapshot().Revision,
			"capability":      descriptor,
		},
		InputSchema:  descriptor.InputSchema,
		OutputSchema: descriptor.OutputSchema,
	}
	name := descriptor.Name
	s.mcpServer.AddTool(tool, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return s.handleToolCall(ctx, req, name)
	})
}

func (s *Server) handleToolCall(ctx context.Context, req *mcp.CallToolRequest, name string) (*mcp.CallToolResult, error) {
	args := map[string]any{}
	if req.Params != nil && len(req.Params.Arguments) > 0 {
		if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
			return tools.ErrorResult(fmt.Errorf("invalid arguments: %w", err)), nil
		}
	}
	dynamicEffect := s.dynamicEffect(name)
	if s.standalone && (tools.IsStandaloneMutation(name) || (dynamicEffect != "" && dynamicEffect != "read")) && !s.allowMutations {
		return tools.ErrorResult(fmt.Errorf("standalone mutations are disabled; set OPUTE_STANDALONE_ALLOW_MUTATIONS=true")), nil
	}
	switch name {
	case "validate_host_plan":
		return s.handleValidateHostPlan(args)
	case "run_host_plan":
		return s.handleRunHostPlan(args)
	case "get_host_plan_run":
		return s.handleGetHostPlanRun(args)
	case "validate_runtime_recipe":
		return s.handleValidateRuntimeRecipe(args)
	case "run_runtime_recipe":
		return s.handleRunRuntimeRecipe(args)
	case "get_runtime_recipe_run":
		return s.handleGetRuntimeRecipeRun(args)
	case "validate_tunnel_recipe":
		return s.handleValidateTunnelRecipe(args)
	case "run_tunnel_recipe":
		return s.handleRunTunnelRecipe(args)
	case "get_tunnel_run":
		return s.handleGetTunnelRun(args)
	case "opute.provider.install":
		return s.handleProviderInstall(args)
	case "opute.provider.validate":
		return s.handleProviderValidate(args)
	case "opute.provider.status":
		return s.handleProviderStatus(args)
	case "opute.provider.reload":
		return s.handleProviderReload(args)
	case "opute.provider.teardown":
		return s.handleProviderTeardown(args)
	case "get_capability_catalog":
		return s.handleGetCapabilityCatalog()
	case "open_assistant_session":
		return s.handleOpenAssistantSession(args)
	}
	if name == "cancel_operation" {
		if id, _ := args["operationId"].(string); id != "" {
			if result, handled := s.cancelHostPlan(id); handled {
				return result, nil
			}
		}
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
	if name == "request_task_input" {
		if !hasTaskAugmentation(req) || !taskExtensionDeclared(req) {
			return tools.ErrorResult(fmt.Errorf("request_task_input requires the MCP Tasks extension")), nil
		}
		return s.createInputRequestTask(args)
	}
	if tasks.TaskAwareTools[name] && hasTaskAugmentation(req) && taskExtensionDeclared(req) {
		return s.createAsyncTask(name, args)
	}
	onData := func(chunk string) {}
	return s.DispatchTool(ctx, name, args, onData)
}

func (s *Server) createInputRequestTask(args map[string]any) (*mcp.CallToolResult, error) {
	prompt, _ := args["prompt"].(string)
	if strings.TrimSpace(prompt) == "" {
		return tools.ErrorResult(fmt.Errorf("request_task_input requires prompt")), nil
	}
	responseType := "string"
	if requested, ok := args["responseType"].(string); ok && requested != "" {
		responseType = requested
	}
	if responseType != "string" && responseType != "boolean" {
		return tools.ErrorResult(fmt.Errorf("request_task_input responseType must be string or boolean")), nil
	}
	_, cancel := context.WithCancel(context.Background())
	inputRequests := map[string]any{
		"response": map[string]any{
			"type":   responseType,
			"prompt": prompt,
		},
	}
	desc := "Waiting for operator input..."
	// Bind the task ID into the resume closure after creation without retaining
	// request arguments in the durable projection.
	var taskID string
	rec := s.tasks.CreateWithInput("request_task_input", redactTaskArgs(args), time.Hour, desc, nil, cancel, inputRequests, func(responses map[string]any) {
		result := tasks.ToolResult{StructuredContent: map[string]any{"response": responses["response"]}}
		s.tasks.Complete(taskID, result)
		if s.state != nil {
			_ = s.state.Complete(taskID, result)
		}
	})
	taskID = rec.TaskID
	if s.state != nil {
		_ = s.state.Create(rec.TaskID, "request_task_input", desc)
	}
	return &mcp.CallToolResult{
		Content:           []mcp.Content{&mcp.TextContent{Text: desc}},
		StructuredContent: s.tasks.ToCreateTaskResult(rec),
	}, nil
}

func (s *Server) dynamicEffect(name string) string {
	s.catalogMu.RLock()
	defer s.catalogMu.RUnlock()
	if _, ok := s.dynamic[name]; !ok {
		return ""
	}
	for _, descriptor := range s.catalog.Tools {
		if descriptor.Name == name {
			return descriptor.Effect
		}
	}
	return ""
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

func taskExtensionDeclared(req *mcp.CallToolRequest) bool {
	if req == nil || req.Params == nil || req.Params.Meta == nil {
		return false
	}
	capabilities, ok := req.Params.Meta["io.modelcontextprotocol/clientCapabilities"].(map[string]any)
	if !ok {
		capabilities, _ = req.Params.Meta["clientCapabilities"].(map[string]any)
	}
	extensions, ok := capabilities["extensions"].(map[string]any)
	if !ok {
		return false
	}
	_, ok = extensions["io.modelcontextprotocol/tasks"]
	return ok
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
		result, err := s.DispatchTool(taskCtx, name, args, onData)
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
		StructuredContent: s.tasks.ToCreateTaskResult(rec),
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
				out[key] = redactSensitiveTaskValue(child)
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

func redactSensitiveTaskValue(value any) any {
	switch typed := value.(type) {
	case []any:
		out := make([]any, len(typed))
		for i := range typed {
			out[i] = "[redacted]"
		}
		return out
	case []string:
		out := make([]string, len(typed))
		for i := range typed {
			out[i] = "[redacted]"
		}
		return out
	default:
		return "[redacted]"
	}
}

// HandleExtensionMethod serves Tasks and custom resources when go-sdk lacks native task support.
func (s *Server) HandleExtensionMethod(method string, params json.RawMessage) (any, error) {
	switch method {
	case "server/discover":
		return map[string]any{
			"resultType":        "complete",
			"supportedVersions": []string{"2026-07-28"},
			"capabilities": map[string]any{
				"tools":      map[string]any{},
				"resources":  map[string]any{"listChanged": true},
				"extensions": map[string]any{"io.modelcontextprotocol/tasks": map[string]any{}},
			},
			"_meta": map[string]any{"io.modelcontextprotocol/serverInfo": map[string]any{"name": "host-agent", "version": version.Version}},
		}, nil
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
		return map[string]any{"resultType": "complete"}, nil
	case "tasks/update":
		var p struct {
			TaskID         string         `json:"taskId"`
			InputResponses map[string]any `json:"inputResponses"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		if strings.TrimSpace(p.TaskID) == "" {
			return nil, fmt.Errorf("tasks/update requires taskId")
		}
		if p.InputResponses == nil {
			return nil, fmt.Errorf("tasks/update requires inputResponses")
		}
		if _, ok := s.tasks.Get(p.TaskID); !ok {
			return nil, fmt.Errorf("task not found: %s", p.TaskID)
		}
		if _, ok := s.tasks.Update(p.TaskID, p.InputResponses); !ok {
			return nil, fmt.Errorf("task cannot accept input: %s", p.TaskID)
		}
		return map[string]any{"resultType": "complete"}, nil
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
	return map[string]any{"resultType": "complete", "resources": resources}, nil
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
			"resultType": "complete",
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
			"resultType": "complete",
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
