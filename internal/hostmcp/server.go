package hostmcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	providercontract "github.com/wunderous/host-agents/contracts/provider"
	hostcapability "github.com/wunderous/host-agents/internal/capability"
	capabilitycatalog "github.com/wunderous/host-agents/internal/catalog"
	"github.com/wunderous/host-agents/internal/console"
	"github.com/wunderous/host-agents/internal/cordis"
	provideradapter "github.com/wunderous/host-agents/internal/cordis/mcp"
	"github.com/wunderous/host-agents/internal/hostagent"
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
	logger                     *slog.Logger
	agent                      *hostagent.Service
	tasks                      *tasks.Registry
	console                    *console.Runtime
	providerID                 string
	standalone                 bool
	allowMutations             bool
	state                      *state.Store
	admission                  resource.HostResourceService
	mu                         sync.Mutex
	catalogMu                  sync.RWMutex
	planMu                     sync.Mutex
	planWG                     sync.WaitGroup
	closed                     bool
	planCancels                map[string]context.CancelFunc
	toolDefs                   []tools.ToolDefinition
	catalog                    tools.CapabilityCatalogSnapshot
	registry                   *capabilitycatalog.Registry
	capabilities               map[string]hostcapability.Capability
	providerContext            *cordis.Context
	providerLifecycle          *cordis.ProviderLifecycleManager
	providerMu                 sync.RWMutex
	providerValidation         map[string]string
	providerCandidates         map[string]*provideradapter.Adapter
	providerCandidateManifests map[string]providercontract.InstallManifest
	providerManifests          map[string]providercontract.InstallManifest
	registeredToolNames        map[string]bool
}

func (s *Server) logProviderRestoreSkip(record state.ProviderGenerationRecord, stage string, err error) {
	logger := slog.Default()
	if s != nil && s.logger != nil {
		logger = s.logger
	}
	logger.Warn("provider generation restore skipped", "provider_id", record.ProviderID, "generation_id", record.GenerationID, "stage", stage, "error", err)
}

// CapabilityImplementation is a trusted, already-installed host adapter. It
// is deliberately a typed function boundary: registering metadata never loads
// code, accepts shell text, or creates an executable capability by itself.
type CapabilityImplementation func(context.Context, map[string]any) (*mcp.CallToolResult, error)

type Options struct {
	ProviderID             string
	Ops                    *hostagent.Service
	Logger                 *slog.Logger
	Standalone             bool
	AllowMutations         bool
	StateDir               string
	Version                string
	Admission              resource.HostResourceService
	AllowedImplementations map[string]bool
	AuthorizedProviders    map[string]bool
}

func NewServer(opts Options) (*Server, error) {
	if opts.Ops == nil {
		return nil, fmt.Errorf("host agent service is required")
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
	knownResourceKinds := make(map[string]bool)
	for _, kind := range resourceid.KnownTypes() {
		knownResourceKinds[kind] = true
	}
	registry := capabilitycatalog.NewRegistry(capabilityCatalog, capabilitycatalog.Options{
		ProviderID: providerID, AuthorizedProviders: opts.AuthorizedProviders, KnownResourceKinds: knownResourceKinds, AllowedImplementations: opts.AllowedImplementations,
	})
	if err := registry.ValidateBase(); err != nil {
		return nil, fmt.Errorf("validate host capability catalog: %w", err)
	}
	capabilities := &mcp.ServerCapabilities{
		Tools:     &mcp.ToolCapabilities{ListChanged: true},
		Resources: &mcp.ResourceCapabilities{ListChanged: true},
	}
	serverVersion := opts.Version
	if serverVersion == "" {
		serverVersion = version.Version
	}
	srv := mcp.NewServer(&mcp.Implementation{Name: "host-agent", Version: serverVersion}, &mcp.ServerOptions{
		Capabilities: capabilities,
		Logger:       opts.Logger,
	})
	resourceService := opts.Admission
	if resourceService == nil {
		resourceService = opts.Ops.ResourceService()
	}
	hs := &Server{
		mcpServer:                  srv,
		logger:                     opts.Logger,
		agent:                      opts.Ops,
		tasks:                      tasks.NewRegistry(),
		console:                    console.NewRuntime(opts.Ops.Incus().NewVMInteractiveCommand),
		providerID:                 providerID,
		standalone:                 opts.Standalone,
		allowMutations:             opts.AllowMutations,
		toolDefs:                   catalog,
		admission:                  resourceService,
		catalog:                    capabilityCatalog,
		registry:                   registry,
		capabilities:               make(map[string]hostcapability.Capability),
		providerContext:            cordis.NewContext(),
		providerLifecycle:          cordis.NewProviderLifecycleManager(cordis.DrainPolicy{}),
		providerValidation:         make(map[string]string),
		providerCandidates:         make(map[string]*provideradapter.Adapter),
		providerCandidateManifests: make(map[string]providercontract.InstallManifest),
		providerManifests:          make(map[string]providercontract.InstallManifest),
		registeredToolNames:        make(map[string]bool),
		planCancels:                make(map[string]context.CancelFunc),
	}
	// The lifecycle vocabulary is declared before anything can restore or
	// mount a provider, so every emission and every listener registration is
	// checked against a known event from the first transition onward.
	if err := defineProviderEvents(hs.providerContext); err != nil {
		return nil, err
	}
	if opts.Standalone || strings.TrimSpace(opts.StateDir) != "" {
		store, err := state.Open(opts.StateDir)
		if err != nil {
			return nil, err
		}
		hs.state = store
		opts.Ops.SetResourceRegistry(store)
		if err := hs.restoreTasks(); err != nil {
			_ = store.Close()
			return nil, fmt.Errorf("restore MCP tasks: %w", err)
		}
		if err := hs.restoreProviderGenerations(); err != nil {
			_ = store.Close()
			return nil, fmt.Errorf("restore provider generations: %w", err)
		}
	}
	if _, err := hs.providerContext.Plugin(resourceServicesPlugin{registry: opts.Ops.ResourceRegistry(), resolver: opts.Ops, tenantID: opts.Ops.TenantID(), service: resourceService}); err != nil {
		return nil, fmt.Errorf("mount resource services: %w", err)
	}
	hs.registerTools()
	opts.Ops.Kubernetes().SetKubernetesProviderExecutor(&kubernetesProviderExecutor{server: hs})
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
	// Disposing the context closes every mounted generation's adapter through
	// the fiber that owns it, in reverse mount order.
	if s.providerContext != nil {
		_ = s.providerContext.Dispose(context.Background())
	}
	s.providerMu.Lock()
	s.providerValidation = make(map[string]string)
	// Candidates are not mounted until activation, so they have no fiber yet
	// and are closed here.
	uniqueAdapters := make(map[*provideradapter.Adapter]struct{}, len(s.providerCandidates))
	for _, adapter := range s.providerCandidates {
		uniqueAdapters[adapter] = struct{}{}
	}
	s.providerCandidates = make(map[string]*provideradapter.Adapter)
	s.providerManifests = make(map[string]providercontract.InstallManifest)
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

func (s *Server) Ops() *hostagent.Service {
	return s.agent
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
	return tools.CloneCapabilityCatalogSnapshot(snapshot)
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
	if registration.Descriptor.Version == 0 {
		registration.Descriptor.Version = 1
	}
	capabilityValue := hostcapability.NewLegacyAdapter(registration.Descriptor, func(ctx context.Context, args hostcapability.RawArguments, _ tools.ExecutionBinding, sink hostcapability.ExecutionSink) (*mcp.CallToolResult, error) {
		return implementation(ctx, args)
	})
	registration.Capability = capabilityValue
	if err := s.registry.RegisterRegistration(registration); err != nil {
		return err
	}
	s.publishCapability(registration.Descriptor, capabilityValue)
	return nil
}

// RegisterCapabilityModule is the native registration path. The capability
// owns invocation and result validation; the server only binds it to the
// authorized catalog and lifecycle.
func (s *Server) RegisterCapabilityModule(value hostcapability.Capability, providerID, implementation string) error {
	if s == nil || value == nil {
		return fmt.Errorf("server and capability are required")
	}
	if err := s.registry.RegisterCapability(value, providerID, implementation); err != nil {
		return err
	}
	descriptor := value.Definition()
	s.publishCapability(descriptor, value)
	return nil
}

func (s *Server) publishCapability(descriptor tools.CapabilityDescriptor, value hostcapability.Capability) {
	s.catalogMu.Lock()
	snapshot := s.registry.Snapshot()
	s.catalog = snapshot
	s.capabilities[descriptor.OperationID] = value
	s.catalogMu.Unlock()
	s.refreshMCPTools()
}

// retireProviderCapabilities removes a provider's overlay from the registry
// and unpublishes its MCP tools so tools/list cannot advertise operations
// whose dispatch would fail closed.
func (s *Server) retireProviderCapabilities(providerID, generationID string) {
	if s == nil || s.registry == nil {
		return
	}
	s.catalogMu.Lock()
	retired := make([]string, 0)
	for _, descriptor := range append([]tools.CapabilityDescriptor(nil), s.catalog.Tools...) {
		if descriptor.Provider != providerID || (generationID != "" && descriptor.GenerationID != generationID) {
			continue
		}
		if err := s.registry.Unregister(descriptor.OperationID); err == nil {
			delete(s.capabilities, descriptor.OperationID)
			retired = append(retired, descriptor.Name)
		}
	}
	s.catalog = s.registry.Snapshot()
	s.catalogMu.Unlock()
	if len(retired) > 0 {
		s.refreshMCPTools()
	}
	s.providerMu.Lock()
	delete(s.providerManifests, providerID)
	s.providerMu.Unlock()
}

// registerProviderServices publishes the operations declared by a validated
// provider manifest only after its candidate generation has passed setup and
// activation validation. The operation implementation remains a generic MCP
// call through the currently active provider adapter; no provider-specific
// symbols enter the Host Agent catalog.
func (s *Server) registerProviderServices(manifest providercontract.InstallManifest) error {
	generationID := ""
	if generation, ok := s.providerLifecycle.Active(manifest.Provider.ID); ok {
		generationID = generation.ID
	}
	return s.registerProviderServicesForGeneration(manifest, generationID)
}

func (s *Server) registerProviderServicesForGeneration(manifest providercontract.InstallManifest, generationID string) error {
	if err := s.registry.AuthorizeProvider(manifest.Provider.ID); err != nil {
		return err
	}
	previousCatalog := s.registry.Snapshot()
	implementation := "provider:" + manifest.Provider.ID
	providerCapabilities := make([]hostcapability.Capability, 0)
	providerDescriptors := make([]tools.CapabilityDescriptor, 0)
	for _, service := range manifest.Services {
		for _, operation := range service.Operations {
			version := operation.Version
			if version == 0 {
				// Keep the in-process registration path compatible with older
				// manifests while new manifests are required to declare it.
				version = 1
			}
			description := strings.TrimSpace(operation.Description)
			if description == "" {
				description = "Provider service " + service.ID + " operation " + operation.ID
			}
			descriptor := tools.CapabilityDescriptor{
				OperationID:       operation.ID,
				Version:           version,
				Name:              operation.ID,
				Description:       description,
				InputSchema:       operation.InputSchema,
				OutputSchema:      operation.OutputSchema,
				OutputType:        operation.OutputType,
				ResultTypes:       operation.ResultTypes,
				Effect:            operation.Effect,
				Privilege:         operation.Effect,
				RequiresApproval:  operation.Effect != "read",
				Provider:          manifest.Provider.ID,
				Implementation:    implementation,
				GenerationID:      generationID,
				ResourceKinds:     append([]string(nil), operation.ResourceKinds...),
				RequiredFields:    requiredSchemaFields(operation.InputSchema),
				ValidationSchema:  operation.ValidationSchema,
				ObservationSchema: firstNonEmpty(operation.ObservationSchema, hostcapability.ObservationSchemaVersion),
				Requires:          providerBindings(operation.Requires),
				Produces:          providerBindings(operation.Produces),
				Idempotent:        operation.Idempotent,
				SupportsReadiness: operation.SupportsReadiness,
			}
			if operation.ResourceCost != nil {
				descriptor.ResourceCost = &tools.ResourceCost{
					CPUCores: operation.ResourceCost.CPUCores, MemoryBytes: operation.ResourceCost.MemoryBytes,
					DiskBytes: operation.ResourceCost.DiskBytes, Tasks: operation.ResourceCost.Tasks,
					Class: operation.ResourceCost.Class,
				}
			}
			serviceID := service.ID
			providerCapability := hostcapability.NewProviderAdapter(descriptor, func(ctx context.Context, args hostcapability.RawArguments, _ tools.ExecutionBinding, _ hostcapability.ExecutionSink) (*mcp.CallToolResult, error) {
				// Every provider operation is affine to the generation that
				// published it. The session is captured first and the mounted
				// service is resolved through it, so a call can never check one
				// generation and then execute against a newer adapter (C-08).
				session, err := s.providerLifecycle.OpenSession(manifest.Provider.ID)
				if err != nil || session.GenerationID() != descriptor.GenerationID {
					if err == nil {
						session.Close()
					}
					return tools.ErrorResult(fmt.Errorf("provider generation %q is no longer active", descriptor.GenerationID)), nil
				}
				defer session.Close()
				value, ok := s.providerServiceValueFor(manifest.Provider.ID, serviceID, session.GenerationID())
				if !ok || value.adapter == nil {
					return tools.ErrorResult(fmt.Errorf("provider generation %q is not connected", descriptor.GenerationID)), nil
				}
				return value.adapter.CallSynchronousOnly(ctx, operation.ID, args)
			})
			providerCapabilities = append(providerCapabilities, providerCapability)
			providerDescriptors = append(providerDescriptors, descriptor)
		}
	}
	if generationID != "" {
		if err := s.registry.ReplaceGeneration(generationID, providerCapabilities); err != nil {
			return err
		}
	} else {
		if err := s.registry.ReplaceProvider(manifest.Provider.ID, providerCapabilities); err != nil {
			return err
		}
	}
	currentCatalog := s.registry.Snapshot()
	s.catalogMu.Lock()
	for _, descriptor := range previousCatalog.Tools {
		if descriptor.Provider == manifest.Provider.ID {
			delete(s.capabilities, descriptor.OperationID)
		}
	}
	s.catalog = currentCatalog
	for index, value := range providerCapabilities {
		s.capabilities[providerDescriptors[index].OperationID] = value
	}
	s.catalogMu.Unlock()

	// Rebuild the complete MCP surface so replacement and retirement publish a
	// single catalog revision with generated edges on every tool.
	s.refreshMCPTools()
	s.providerMu.Lock()
	s.providerManifests[manifest.Provider.ID] = manifest
	s.providerMu.Unlock()
	return nil
}

func providerCapabilityNames(snapshot tools.CapabilityCatalogSnapshot, providerID string) []string {
	seen := make(map[string]bool)
	names := make([]string, 0)
	for _, descriptor := range snapshot.Tools {
		if descriptor.Provider != providerID || seen[descriptor.Name] {
			continue
		}
		seen[descriptor.Name] = true
		names = append(names, descriptor.Name)
	}
	sort.Strings(names)
	return names
}

func providerBindings(bindings []providercontract.ResourceBinding) []tools.ResourceBinding {
	if len(bindings) == 0 {
		return nil
	}
	converted := make([]tools.ResourceBinding, 0, len(bindings))
	for _, binding := range bindings {
		converted = append(converted, tools.ResourceBinding{
			Argument: binding.Argument, ResourceType: binding.ResourceType,
			SourcePath: binding.SourcePath, SelectorID: binding.SelectorID, Required: binding.Required,
		})
	}
	return converted
}

// DispatchTool is the single execution boundary for MCP, task, and internal
// host-worker callers. Keeping admission here prevents one transport from bypassing the
// host-wide policy when two agent instances share WSL/Incus resources.
func (s *Server) DispatchTool(ctx context.Context, name string, args map[string]any, onData func(string)) (*mcp.CallToolResult, error) {
	rawArgs := cloneArguments(args)
	// MCP HTTP calls enter through handleToolCall, which intercepts lifecycle
	// tools before the generic capability adapter. Internal host-worker calls
	// enter here directly, so use one lifecycle adapter for both transports.
	// The adapter admits the lifecycle operation before its provider callback or
	// durable plan is started; plan nodes acquire their own inherited/typed
	// reservation at the generic dispatch boundary.
	if result, err, handled := s.dispatchLifecycleTool(ctx, name, rawArgs); handled {
		return result, err
	}
	binding, err := resolveExecutionBinding(s, name, rawArgs)
	if err != nil {
		return tools.ErrorResult(err), nil
	}
	if s.admission == nil {
		return s.dispatchRegisteredOrBuiltIn(ctx, name, rawArgs, binding, onData)
	}
	reservation, err := s.admitInvocation(ctx, name, rawArgs, binding)
	if err != nil {
		return tools.ErrorResult(err), nil
	}
	if reservation != nil {
		defer func() { _ = s.admission.Release(reservation) }()
		binding.ReservationID = reservation.ID
		binding.ResourcePolicyRevision = s.admission.Snapshot().PolicyRevision
		ctx = resource.WithReservation(ctx, reservation)
	}
	return s.dispatchRegisteredOrBuiltIn(ctx, name, rawArgs, binding, onData)
}

// dispatchLifecycleTool handles the small set of transport-owned operations
// that are not ordinary capability registrations. They still have a typed
// catalog descriptor and must cross the same resource-admission boundary as a
// registered capability. The lifecycle routing table is explicit operation
// ownership, not a name-based resource classifier.
func (s *Server) dispatchLifecycleTool(ctx context.Context, name string, args map[string]any) (*mcp.CallToolResult, error, bool) {
	if !isLifecycleTool(name) {
		return nil, nil, false
	}
	binding := tools.ExecutionBinding{
		SchemaVersion: tools.ExecutionBindingSchemaVersion,
		Admission:     "tenant-resource-registry",
		Authorization: "admitted",
	}
	if s.admission != nil {
		reservation, err := s.admitInvocation(ctx, name, args, binding)
		if err != nil {
			return tools.ErrorResult(err), nil, true
		}
		if reservation != nil {
			defer func() { _ = s.admission.Release(reservation) }()
			ctx = resource.WithReservation(ctx, reservation)
			binding.ReservationID = reservation.ID
			binding.ResourcePolicyRevision = s.admission.Snapshot().PolicyRevision
		}
	}
	result, err := s.invokeLifecycleTool(ctx, name, args)
	return result, err, true
}

func isLifecycleTool(name string) bool {
	switch name {
	case "validate_host_plan", "run_host_plan", "get_host_plan_run",
		"validate_runtime_recipe", "run_runtime_recipe", "get_runtime_recipe_run",
		"validate_tunnel_recipe", "run_tunnel_recipe", "get_tunnel_run",
		"opute.provider.install", "opute.provider.validate", "opute.provider.status",
		"opute.provider.reload", "opute.provider.teardown",
		"get_capability_catalog", "open_assistant_session":
		return true
	default:
		return false
	}
}

func (s *Server) invokeLifecycleTool(ctx context.Context, name string, args map[string]any) (*mcp.CallToolResult, error) {
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
		return s.handleProviderInstallContext(ctx, args)
	case "opute.provider.validate":
		return s.handleProviderValidateContext(ctx, args)
	case "opute.provider.status":
		return s.handleProviderStatus(args)
	case "opute.provider.reload":
		return s.handleProviderReloadContext(ctx, args)
	case "opute.provider.teardown":
		return s.handleProviderTeardownContext(ctx, args)
	case "get_capability_catalog":
		return s.handleGetCapabilityCatalog()
	case "open_assistant_session":
		return s.handleOpenAssistantSession(args)
	default:
		return nil, fmt.Errorf("unknown lifecycle tool %q", name)
	}
}

func (s *Server) admitInvocation(ctx context.Context, name string, args map[string]any, binding tools.ExecutionBinding) (*resource.Reservation, error) {
	class, declared := tools.RegisteredAdmissionClass(name)
	if !declared {
		// Transport-owned lifecycle definitions carry their cost in the typed
		// catalog descriptor. Do not infer a workload class from a name: an
		// unregistered mutating operation must fail closed below.
		class = resource.ClassControl
	}
	cost := resource.DefaultCostForClass(class)
	descriptor, found := s.capabilityDescriptor(name)
	if found && descriptor.ResourceCost != nil {
		declaredClass := class
		if descriptor.ResourceCost.Class != "" {
			parsed, ok := admissionClass(descriptor.ResourceCost.Class)
			if !ok {
				return nil, fmt.Errorf("resource_declaration_invalid: capability %q declares unknown resource class %q", name, descriptor.ResourceCost.Class)
			}
			declaredClass = parsed
		}
		cost = resource.AdmissionRequest{
			Class: declaredClass, CPUCores: descriptor.ResourceCost.CPUCores,
			MemoryBytes: descriptor.ResourceCost.MemoryBytes, DiskBytes: descriptor.ResourceCost.DiskBytes,
			Tasks: descriptor.ResourceCost.Tasks,
		}
	} else if found && s.isProviderCapability(name) && descriptor.Effect != string(tools.EffectRead) {
		return nil, fmt.Errorf("resource_declaration_required: provider workload capability %q must declare typed resourceCost metadata", name)
	} else if found && s.isProviderCapability(name) {
		// Read-only provider calls have no declared host workload. They do not
		// take a permit, which preserves nested provider callbacks while still
		// requiring typed cost metadata for every mutating provider operation.
		cost = resource.DefaultCostForClass(resource.ClassControl)
	} else if found && descriptor.Effect != string(tools.EffectRead) && !declared {
		return nil, fmt.Errorf("resource_declaration_required: capability %q must declare typed resourceCost metadata", name)
	} else if !found && !declared {
		return nil, fmt.Errorf("resource_declaration_required: operation %q is not registered with a typed resource cost", name)
	}
	cost.Operation = name
	cost.AgentID = s.agent.AgentID()
	cost.OperationID = stringArgument(args, "operationId")
	cost.TaskID = stringArgument(args, "taskId")
	if operationID, taskID := resource.OperationIdentityFromContext(ctx); cost.OperationID == "" && cost.TaskID == "" {
		cost.OperationID, cost.TaskID = operationID, taskID
	}
	cost.ResourceURI = firstBoundURI(binding)
	cost.GenerationID = binding.GenerationID
	if parent, ok := resource.ReservationFromContext(ctx); ok {
		cost.ParentReservationID = parent.ID
	}
	return s.admission.Admit(ctx, cost)
}

func (s *Server) capabilityDescriptor(name string) (tools.CapabilityDescriptor, bool) {
	s.catalogMu.RLock()
	defer s.catalogMu.RUnlock()
	for _, descriptor := range s.catalog.Tools {
		if descriptor.Name == name {
			return descriptor, true
		}
	}
	return tools.CapabilityDescriptor{}, false
}

func admissionClass(value string) (resource.Class, bool) {
	switch resource.Class(strings.TrimSpace(value)) {
	case resource.ClassControl, resource.ClassNormal, resource.ClassHeavy:
		return resource.Class(value), true
	default:
		return "", false
	}
}

func stringArgument(args map[string]any, key string) string {
	value, _ := args[key].(string)
	return strings.TrimSpace(value)
}

func firstBoundURI(binding tools.ExecutionBinding) string {
	for _, bound := range binding.Resources {
		if strings.TrimSpace(bound.URI) != "" {
			return bound.URI
		}
	}
	return ""
}

func (s *Server) isProviderCapability(name string) bool {
	s.catalogMu.RLock()
	capabilityValue := s.capabilities[name]
	s.catalogMu.RUnlock()
	if capabilityValue == nil {
		return false
	}
	definition := capabilityValue.Definition()
	return strings.HasPrefix(strings.TrimSpace(definition.Implementation), "provider:")
}

func cloneArguments(args map[string]any) map[string]any {
	if args == nil {
		return map[string]any{}
	}
	clone := make(map[string]any, len(args))
	for key, value := range args {
		clone[key] = value
	}
	return clone
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

// resolveExecutionBinding is the typed admission boundary between the public
// URI contract and capability execution. Canonical URIs are resolved through
// the tenant-checked registry and the provider-native coordinates are carried
// in the returned binding; the raw argument map is never rewritten, and
// callers cannot smuggle a guessed provider instance name through arguments.
func resolveExecutionBinding(server *Server, name string, args map[string]any) (tools.ExecutionBinding, error) {
	binding := tools.ExecutionBinding{
		SchemaVersion: tools.ExecutionBindingSchemaVersion,
		Admission:     "tenant-resource-registry",
		Authorization: "admitted",
	}
	if server == nil || server.agent == nil {
		return binding, nil
	}
	// Declared descriptor bindings are authoritative for their argument. There
	// is intentionally no generic `uri` fallback: a URI-shaped field is not a
	// resource contract until the capability declares its binding.
	bindingByArgument := make(map[string][]tools.ResourceBinding)
	var descriptor tools.CapabilityDescriptor
	server.catalogMu.RLock()
	for _, candidate := range server.catalog.Tools {
		if candidate.Name == name {
			descriptor = candidate
			for _, require := range candidate.Requires {
				if argument := strings.TrimSpace(require.Argument); argument != "" {
					bindingByArgument[argument] = append(bindingByArgument[argument], require)
				}
			}
			break
		}
	}
	binding.CatalogRevision = server.catalog.Revision
	binding.GenerationID = descriptor.GenerationID
	server.catalogMu.RUnlock()
	binding.TenantID = server.agent.TenantID()
	arguments := make([]string, 0, len(bindingByArgument))
	for argument := range bindingByArgument {
		arguments = append(arguments, argument)
	}
	sort.Strings(arguments)
	for _, argument := range arguments {
		resourceBindings := bindingByArgument[argument]
		value, present := argumentValue(args, argument)
		uri, ok := value.(string)
		if !present || !ok || strings.TrimSpace(uri) == "" {
			required := false
			for _, resourceBinding := range resourceBindings {
				required = required || resourceBinding.Required
			}
			if required {
				if argument == "uri" {
					return tools.ExecutionBinding{}, tools.NewCapabilityError("admission", "resource_binding", fmt.Errorf("%s requires canonical resource uri", name))
				}
				return tools.ExecutionBinding{}, tools.NewCapabilityError("admission", "resource_binding", fmt.Errorf("%s requires canonical resource argument %q", name, argument))
			}
			continue
		}
		var coordinates hostagent.Coordinates
		var lastErr error
		resolved := false
		for _, resourceBinding := range resourceBindings {
			candidate, err := server.agent.ResolveResource(uri, resourceBinding.ResourceType)
			if err == nil {
				coordinates = candidate
				resolved = true
				break
			}
			lastErr = err
		}
		if !resolved {
			return tools.ExecutionBinding{}, tools.NewCapabilityError("admission", "resource_binding", fmt.Errorf("resolve %s argument %q: %w", name, argument, lastErr))
		}
		binding.Resources = append(binding.Resources, tools.BoundResource{
			Argument: argument, URI: coordinates.URI.String(), ResourceType: coordinates.ResourceType,
			TenantID: coordinates.TenantID, ResourceID: coordinates.ResourceID, Coordinates: coordinates.Values,
		})
	}
	return binding, nil
}

func argumentValue(args map[string]any, path string) (any, bool) {
	if strings.TrimSpace(path) == "" {
		return nil, false
	}
	var current any = args
	for _, segment := range strings.Split(path, ".") {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = object[segment]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

func (s *Server) dispatchRegisteredOrBuiltIn(ctx context.Context, name string, args map[string]any, binding tools.ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
	s.catalogMu.RLock()
	capabilityValue := s.capabilities[name]
	s.catalogMu.RUnlock()
	if capabilityValue == nil {
		return nil, fmt.Errorf("capability %q is not registered", name)
	}
	binding = s.completeProducedResourceBinding(capabilityValue.Definition(), args, binding)
	result, err := capabilityValue.Invoke(ctx, hostcapability.RawArguments(args), binding, onData)
	if err != nil {
		observation := s.normalizeCapabilityObservation(capabilityValue, hostcapability.CapabilityObservation{Status: "invoke_error"}, binding.CatalogRevision)
		s.recordCapabilityInvocation(capabilityValue, args, binding, nil, observation, err)
		return tools.ErrorResult(err), nil
	}
	if result != nil && !result.IsError {
		// Materialize declared resource identity before adapter validation. Provider
		// adapters may return an empty structured result while the execution
		// binding still carries the canonical resource selected at admission.
		result.StructuredContent = materializeBoundResourceOutputs(capabilityValue.Definition(), result.StructuredContent, binding)
	}
	observation, validationErr := capabilityValue.ValidateResult(ctx, result)
	if validationErr != nil {
		observation := s.normalizeCapabilityObservation(capabilityValue, hostcapability.CapabilityObservation{Status: "invalid_result"}, binding.CatalogRevision)
		typedErr := tools.NewCapabilityError("capability", "invalid_result", fmt.Errorf("capability %q returned invalid result: %w", name, validationErr))
		errorResult := tools.ErrorResult(typedErr)
		s.recordCapabilityInvocation(capabilityValue, args, binding, errorResult, observation, typedErr)
		return errorResult, nil
	}
	if result != nil && !result.IsError {
		if err := validateProducedResources(capabilityValue.Definition(), result.StructuredContent, s.agent.TenantID()); err != nil {
			observation := s.normalizeCapabilityObservation(capabilityValue, hostcapability.CapabilityObservation{Status: "invalid_result"}, binding.CatalogRevision)
			typedErr := tools.NewCapabilityError("capability", "invalid_resource_output", err)
			errorResult := tools.ErrorResult(typedErr)
			s.recordCapabilityInvocation(capabilityValue, args, binding, errorResult, observation, typedErr)
			return errorResult, nil
		}
	}
	observation = s.normalizeCapabilityObservation(capabilityValue, observation, binding.CatalogRevision)
	s.recordCapabilityInvocation(capabilityValue, args, binding, result, observation, nil)
	return result, nil
}

// completeProducedResourceBinding preserves the typed identity contract when
// a native/provider capability declares a URI result but omits the matching
// input binding from an older descriptor. The URI is accepted only after the
// same tenant-checked resolver used by normal admission succeeds; no raw
// argument is copied into provider coordinates or execution state.
func (s *Server) completeProducedResourceBinding(
	descriptor tools.CapabilityDescriptor,
	args map[string]any,
	binding tools.ExecutionBinding,
) tools.ExecutionBinding {
	for _, produced := range descriptor.Produces {
		if strings.TrimSpace(produced.SourcePath) != "uri" {
			continue
		}
		for _, resource := range binding.Resources {
			if resource.ResourceType == produced.ResourceType && strings.TrimSpace(resource.URI) != "" {
				return binding
			}
		}
		candidate, ok := args["uri"].(string)
		if !ok || strings.TrimSpace(candidate) == "" || s == nil || s.agent == nil {
			continue
		}
		resolved, err := s.agent.ResolveResource(candidate, produced.ResourceType)
		if err != nil {
			continue
		}
		binding.Resources = append(binding.Resources, tools.BoundResource{
			Argument: "uri", ResourceType: resolved.ResourceType, TenantID: resolved.TenantID,
			ResourceID: resolved.ResourceID, URI: resolved.URI.String(), Coordinates: resolved.Values,
		})
	}
	return binding
}

func (s *Server) normalizeCapabilityObservation(value hostcapability.Capability, observation hostcapability.CapabilityObservation, catalogRevision string) hostcapability.CapabilityObservation {
	descriptor := value.Definition()
	if observation.SchemaVersion == "" {
		observation.SchemaVersion = hostcapability.ObservationSchemaVersion
	}
	if observation.OperationID == "" {
		observation.OperationID = descriptor.OperationID
	}
	if observation.CapabilityVersion == 0 {
		observation.CapabilityVersion = descriptor.Version
	}
	if observation.Status == "" {
		observation.Status = "unknown"
	}
	if strings.TrimSpace(catalogRevision) == "" {
		catalogRevision = s.CatalogSnapshot().Revision
	}
	observation.CatalogRevision = catalogRevision
	if observation.GenerationID == "" {
		observation.GenerationID = descriptor.GenerationID
	}
	return observation
}

func (s *Server) recordCapabilityInvocation(value hostcapability.Capability, rawArgs map[string]any, binding tools.ExecutionBinding, result *mcp.CallToolResult, observation hostcapability.CapabilityObservation, invocationErr error) {
	if s == nil || s.state == nil || value == nil {
		return
	}
	descriptor := value.Definition()
	argumentsJSON, err := redactEvidenceJSON(rawArgs, descriptor.InputSchema)
	if err != nil {
		return
	}
	bindingJSON, err := json.Marshal(binding)
	if err != nil {
		return
	}
	if invocationErr != nil && result == nil {
		result = tools.ErrorResult(invocationErr)
	}
	resultEnvelope := map[string]any{"isError": false}
	if result != nil {
		resultEnvelope["isError"] = result.IsError
		resultEnvelope["structured"] = redactEvidenceBySchema(result.StructuredContent, descriptor.OutputSchema)
		// Text content has no typed schema projection. Durable invocation
		// evidence retains the structured result and omits arbitrary provider
		// text rather than risking a secret-bearing diagnostic.
		resultEnvelope["content"] = []any{}
	}
	if invocationErr != nil {
		resultEnvelope["error"] = map[string]any{"message": "capability invocation failed"}
		if capabilityErr, ok := invocationErr.(*tools.CapabilityError); ok {
			resultEnvelope["error"] = map[string]any{
				"owner":   capabilityErr.Owner,
				"code":    capabilityErr.Code,
				"message": "capability invocation failed",
			}
		}
	}
	resultJSON, err := json.Marshal(resultEnvelope)
	if err != nil {
		return
	}
	observationJSON, err := json.Marshal(redactCapabilityObservation(observation, descriptor.OutputSchema))
	if err != nil {
		return
	}
	terminalStatus := observation.Status
	if invocationErr != nil {
		terminalStatus = "error"
	}
	authorization := binding.Authorization
	if authorization == "" {
		authorization = "admitted"
	}
	catalogRevision := firstNonEmpty(binding.CatalogRevision, observation.CatalogRevision)
	if catalogRevision == "" {
		catalogRevision = s.CatalogSnapshot().Revision
	}
	_ = s.state.RecordCapabilityInvocation(state.CapabilityInvocationRecord{
		InvocationID:      uuid.NewString(),
		OperationID:       descriptor.OperationID,
		CapabilityVersion: descriptor.Version,
		CatalogRevision:   catalogRevision,
		GenerationID:      descriptor.GenerationID,
		Authorization:     authorization,
		ArgumentsJSON:     argumentsJSON,
		BindingJSON:       string(bindingJSON),
		ResultJSON:        string(resultJSON),
		ObservationJSON:   string(observationJSON),
		TerminalStatus:    terminalStatus,
	})
}

func (s *Server) Tasks() *tasks.Registry {
	return s.tasks
}

func (s *Server) persistTask(rec *tasks.Record) {
	if s.state == nil || rec == nil {
		return
	}
	snapshot := s.tasks.ToGetTaskResult(rec)
	snapshot["toolName"] = rec.ToolName
	snapshot["toolArgs"] = redactTaskValue(rec.ToolArgs)
	if rec.Description != "" {
		snapshot["description"] = rec.Description
	}
	if rec.Metadata != nil {
		snapshot["metadata"] = redactTaskValue(rec.Metadata)
	}
	_ = s.state.SaveTaskSnapshot(rec.TaskID, rec.ToolName, rec.Description, snapshot)
}

func (s *Server) restoreTasks() error {
	if s.state == nil {
		return nil
	}
	snapshots, err := s.state.ListTaskSnapshots()
	if err != nil {
		return err
	}
	for _, snapshot := range snapshots {
		s.tasks.RestoreSnapshot(snapshot)
	}
	return nil
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
	for _, descriptor := range snapshot.Tools {
		s.ensureLegacyCapabilityForDescriptor(descriptor)
		s.addRegisteredCapability(descriptor)
	}
}

func (s *Server) ensureLegacyCapabilityForDescriptor(descriptor tools.CapabilityDescriptor) {
	if s == nil {
		return
	}
	s.catalogMu.Lock()
	defer s.catalogMu.Unlock()
	if _, exists := s.capabilities[descriptor.OperationID]; exists {
		return
	}
	s.capabilities[descriptor.OperationID] = hostcapability.NewLegacyAdapter(descriptor, func(ctx context.Context, args hostcapability.RawArguments, binding tools.ExecutionBinding, sink hostcapability.ExecutionSink) (*mcp.CallToolResult, error) {
		return tools.DispatchTool(ctx, s.agent, descriptor.OperationID, args, binding, sink)
	})
}

func (s *Server) addRegisteredCapability(descriptor tools.CapabilityDescriptor) {
	// The registry is the authority for generated edges. Dynamic capabilities
	// must publish the descriptor from that snapshot, not the pre-derivation
	// input supplied by the provider.
	snapshot := s.CatalogSnapshot()
	for _, candidate := range snapshot.Tools {
		if candidate.OperationID == descriptor.OperationID {
			descriptor = candidate
			break
		}
	}
	tool := &mcp.Tool{
		Name:        descriptor.Name,
		Description: descriptor.Description,
		Meta: map[string]any{
			"catalogRevision": snapshot.Revision,
			"capability":      descriptor,
		},
		InputSchema:  descriptor.InputSchema,
		OutputSchema: descriptor.OutputSchema,
	}
	name := descriptor.Name
	s.mcpServer.AddTool(tool, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return s.handleToolCall(ctx, req, name)
	})
	s.mu.Lock()
	if s.registeredToolNames == nil {
		s.registeredToolNames = make(map[string]bool)
	}
	s.registeredToolNames[name] = true
	s.mu.Unlock()
}

// refreshMCPTools republishes the complete immutable catalog projection after
// a dynamic registration. MCP tools/list is one revisioned surface; a
// per-tool overlay must not leave older tools advertising the previous
// revision or pre-derived edge metadata.
func (s *Server) refreshMCPTools() {
	s.mu.Lock()
	names := make([]string, 0, len(s.registeredToolNames))
	for name := range s.registeredToolNames {
		names = append(names, name)
	}
	s.registeredToolNames = make(map[string]bool)
	s.mu.Unlock()
	if len(names) > 0 {
		s.mcpServer.RemoveTools(names...)
	}
	s.registerTools()
}

func (s *Server) handleToolCall(ctx context.Context, req *mcp.CallToolRequest, name string) (*mcp.CallToolResult, error) {
	if requestedRevision := requestCatalogRevision(req); requestedRevision != "" {
		currentRevision := s.CatalogSnapshot().Revision
		if requestedRevision != currentRevision {
			return tools.ErrorResult(tools.NewCapabilityError("lifecycle", "catalog_revision_stale", fmt.Errorf("catalog revision %q is stale; current revision is %q", requestedRevision, currentRevision))), nil
		}
	}
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
	if result, err, handled := s.dispatchLifecycleTool(ctx, name, args); handled {
		return result, err
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
			s.persistTask(rec)
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
		if !taskExtensionDeclared(req) {
			return nil, missingTasksCapabilityError()
		}
		return s.createInputRequestTask(args)
	}
	if tools.IsTaskAware(name) || tasks.TaskAwareTools[name] {
		if !taskExtensionDeclared(req) {
			return nil, missingTasksCapabilityError()
		}
		return s.createAsyncTask(name, args)
	}
	onData := func(chunk string) {}
	return s.DispatchTool(ctx, name, args, onData)
}

func requestCatalogRevision(req *mcp.CallToolRequest) string {
	if req == nil || req.Params == nil || req.Params.Meta == nil {
		return ""
	}
	if value, ok := req.Params.Meta["catalogRevision"].(string); ok {
		return strings.TrimSpace(value)
	}
	return ""
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
	rec := s.tasks.CreateWithInput("request_task_input", s.redactTaskArgs("request_task_input", args), time.Hour, desc, nil, cancel, inputRequests, func(responses map[string]any) {
		result := tasks.ToolResult{StructuredContent: map[string]any{"response": responses["response"]}}
		s.tasks.Complete(taskID, result)
		if s.state != nil {
			_ = s.state.Complete(taskID, result)
		}
		if completed, ok := s.tasks.Get(taskID); ok {
			s.persistTask(completed)
		}
	})
	taskID = rec.TaskID
	if s.state != nil {
		_ = s.state.Create(rec.TaskID, "request_task_input", desc)
	}
	s.persistTask(rec)
	return &mcp.CallToolResult{
		Content:           []mcp.Content{&mcp.TextContent{Text: desc}},
		StructuredContent: s.tasks.ToCreateTaskResult(rec),
	}, nil
}

func (s *Server) dynamicEffect(name string) string {
	s.catalogMu.RLock()
	defer s.catalogMu.RUnlock()
	if _, ok := s.capabilities[name]; !ok {
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

func missingTasksCapabilityError() error {
	data, err := json.Marshal(mcp.MissingRequiredClientCapabilityData{
		RequiredCapabilities: &mcp.ClientCapabilities{
			Extensions: map[string]any{"io.modelcontextprotocol/tasks": map[string]any{}},
		},
	})
	if err != nil {
		return err
	}
	// The Tasks extension draft defines -32003 for this condition. The Go SDK
	// also exposes a newer SEP-2575 code, but this endpoint follows the
	// extension specification advertised by server/discover.
	return &jsonrpc.Error{
		Code:    -32003,
		Message: "Missing required client capability",
		Data:    data,
	}
}

func (s *Server) createAsyncTask(name string, args map[string]any) (*mcp.CallToolResult, error) {
	desc := fmt.Sprintf("Executing %s...", name)
	if vm, ok := args["vmName"].(string); ok && vm != "" {
		desc = fmt.Sprintf("Running %s on '%s'...", name, vm)
	}
	taskCtx, cancel := context.WithCancel(context.Background())
	rec := s.tasks.CreateWithCancel(name, s.redactTaskArgs(name, args), time.Hour, desc, nil, cancel)
	taskCtx = resource.WithOperationIdentity(taskCtx, name, rec.TaskID)
	if s.state != nil {
		_ = s.state.Create(rec.TaskID, name, desc)
	}
	s.persistTask(rec)
	go func(taskID string) {
		onData := func(chunk string) { s.tasks.AppendLog(taskID, chunk) }
		result, err := s.DispatchTool(taskCtx, name, args, onData)
		if err != nil {
			if s.state != nil {
				_ = s.state.Fail(taskID, err.Error())
			}
			s.tasks.Fail(taskID, err.Error())
			if failed, ok := s.tasks.Get(taskID); ok {
				s.persistTask(failed)
			}
			return
		}
		if result.IsError {
			redactedResult := s.redactTaskResult(name, result)
			tr := tasks.ToolResult{StructuredContent: redactedResult.StructuredContent, IsError: true}
			for _, content := range result.Content {
				if text, ok := content.(*mcp.TextContent); ok {
					tr.Content = append(tr.Content, map[string]any{"type": "text", "text": text.Text})
				}
			}
			// A tool-level failure is still a successful JSON-RPC execution. The
			// Tasks spec requires it to be a completed task containing the normal
			// CallToolResult with isError:true; failed is reserved for JSON-RPC
			// errors during execution.
			s.tasks.Complete(taskID, tr)
			if s.state != nil {
				_ = s.state.Complete(taskID, tr)
			}
			if completed, ok := s.tasks.Get(taskID); ok {
				s.persistTask(completed)
			}
			return
		}
		redactedResult := s.redactTaskResult(name, result)
		tr := tasks.ToolResult{StructuredContent: redactedResult.StructuredContent, IsError: redactedResult.IsError}
		for _, c := range result.Content {
			if tc, ok := c.(*mcp.TextContent); ok {
				tr.Content = append(tr.Content, map[string]any{"type": "text", "text": tc.Text})
			}
		}
		s.tasks.Complete(taskID, tr)
		if s.state != nil {
			_ = s.state.Complete(taskID, tr)
		}
		if completed, ok := s.tasks.Get(taskID); ok {
			s.persistTask(completed)
		}
	}(rec.TaskID)
	return &mcp.CallToolResult{
		Content:           []mcp.Content{&mcp.TextContent{Text: desc}},
		StructuredContent: s.tasks.ToCreateTaskResult(rec),
	}, nil
}

// redactTaskArgs projects task arguments through the operation's declared
// input schema. The original arguments remain available only to the running
// goroutine; durable task state never guesses sensitivity from key names.
func (s *Server) redactTaskArgs(name string, args map[string]any) map[string]any {
	descriptor, ok := s.taskCapabilityDescriptor(name)
	if !ok {
		return map[string]any{"redacted": true}
	}
	projected, ok := redactEvidenceBySchema(args, descriptor.InputSchema).(map[string]any)
	if !ok {
		return map[string]any{"redacted": true}
	}
	return projected
}

func (s *Server) redactTaskResult(name string, result *mcp.CallToolResult) *mcp.CallToolResult {
	if result == nil {
		return nil
	}
	descriptor, ok := s.taskCapabilityDescriptor(name)
	if !ok {
		return &mcp.CallToolResult{Content: result.Content, IsError: result.IsError, StructuredContent: map[string]any{"redacted": true}}
	}
	return &mcp.CallToolResult{
		Content:           result.Content,
		IsError:           result.IsError,
		StructuredContent: redactEvidenceBySchema(result.StructuredContent, descriptor.OutputSchema),
	}
}

func (s *Server) taskCapabilityDescriptor(name string) (tools.CapabilityDescriptor, bool) {
	s.catalogMu.RLock()
	defer s.catalogMu.RUnlock()
	if capabilityValue := s.capabilities[name]; capabilityValue != nil {
		return capabilityValue.Definition(), true
	}
	for _, descriptor := range s.catalog.Tools {
		if descriptor.Name == name || descriptor.OperationID == name {
			return descriptor, true
		}
	}
	return tools.CapabilityDescriptor{}, false
}

func redactTaskValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, child := range typed {
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

// HandleExtensionMethod serves Tasks and custom resources when go-sdk lacks native task support.
func (s *Server) HandleExtensionMethod(method string, params json.RawMessage) (any, error) {
	switch method {
	case "server/discover":
		return map[string]any{
			"resultType":        "complete",
			"supportedVersions": []string{"2026-07-28"},
			"capabilities": map[string]any{
				"tools":      map[string]any{},
				"extensions": map[string]any{"io.modelcontextprotocol/tasks": map[string]any{}},
			},
			"_meta": map[string]any{"io.modelcontextprotocol/serverInfo": map[string]any{"name": "host-agent", "version": version.Version}},
		}, nil
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
		if s.state != nil {
			_ = s.state.Cancel(p.TaskID)
		}
		s.persistTask(rec)
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
		updated, ok := s.tasks.Update(p.TaskID, p.InputResponses)
		if !ok {
			return nil, fmt.Errorf("task cannot accept input: %s", p.TaskID)
		}
		s.persistTask(updated)
		return map[string]any{"resultType": "complete"}, nil
	case "resources/list", "resources/read":
		return nil, &jsonrpc.Error{Code: jsonrpc.CodeMethodNotFound, Message: "Method not found: " + method}
	default:
		if method == "tasks/list" {
			return nil, &jsonrpc.Error{Code: jsonrpc.CodeMethodNotFound, Message: "Method not found: tasks/list"}
		}
		return nil, fmt.Errorf("unsupported extension method: %s", method)
	}
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
