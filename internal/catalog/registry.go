package catalog

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/wunderous/host-agents/internal/capability"
	"github.com/wunderous/host-agents/internal/resourceid"
	"github.com/wunderous/host-agents/internal/selectors"
	"github.com/wunderous/host-agents/internal/tools"
)

var operationNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,127}$`)

type Options struct {
	ProviderID             string
	AuthorizedProviders    map[string]bool
	KnownResourceKinds     map[string]bool
	AllowedImplementations map[string]bool
}

type Registration struct {
	Descriptor     tools.CapabilityDescriptor
	ProviderID     string
	Implementation string
	Capability     capability.Capability
}

var _ capability.CapabilityRegistry = (*Registry)(nil)

// Registry is an in-process, guarded metadata overlay. It never loads code,
// accepts shell text, or makes an unregistered descriptor executable. A host
// adapter must bind a registration to a trusted implementation before calling
// Register.
type Registry struct {
	mu                     sync.RWMutex
	provider               string
	authorizedProviders    map[string]bool
	known                  map[string]bool
	allowedImplementations map[string]bool
	base                   tools.CapabilityCatalogSnapshot
	overlays               map[string]Registration
}

func NewRegistry(snapshot tools.CapabilityCatalogSnapshot, options Options) *Registry {
	provider := tools.NormalizeProviderID(options.ProviderID)
	if provider == "" {
		provider = snapshot.ProviderID
	}
	known := make(map[string]bool, len(options.KnownResourceKinds))
	for kind, allowed := range options.KnownResourceKinds {
		known[kind] = allowed
	}
	allowed := make(map[string]bool, len(options.AllowedImplementations))
	for implementation, enabled := range options.AllowedImplementations {
		allowed[implementation] = enabled
	}
	authorized := make(map[string]bool, len(options.AuthorizedProviders)+1)
	authorized[provider] = true
	for candidate, enabled := range options.AuthorizedProviders {
		if enabled {
			authorized[normalizeRegistrationProvider(candidate)] = true
		}
	}
	return &Registry{provider: provider, authorizedProviders: authorized, known: known, allowedImplementations: allowed, base: tools.CloneCapabilityCatalogSnapshot(snapshot), overlays: make(map[string]Registration)}
}

func (r *Registry) Snapshot() tools.CapabilityCatalogSnapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	descriptors := make([]tools.CapabilityDescriptor, 0, len(r.base.Tools)+len(r.overlays))
	for _, descriptor := range r.base.Tools {
		descriptors = append(descriptors, descriptor)
	}
	for _, registration := range r.overlays {
		descriptors = append(descriptors, registration.Descriptor)
	}
	sort.Slice(descriptors, func(i, j int) bool { return descriptors[i].OperationID < descriptors[j].OperationID })
	return tools.BuildCapabilityCatalogFromDescriptors(r.provider, descriptors)
}

// ValidateBase checks the static catalog with the same schema and typed
// binding rules used for dynamic registrations. Static definitions are not
// executable registrations, so this deliberately omits provider and
// implementation checks.
func (r *Registry) ValidateBase() error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, descriptor := range r.base.Tools {
		if err := r.validateDescriptor(descriptor); err != nil {
			return err
		}
	}
	return nil
}

func (r *Registry) Register(value capability.Capability) error {
	if value == nil {
		return fmt.Errorf("capability implementation is required")
	}
	descriptor := value.Definition()
	return r.RegisterRegistration(Registration{
		Descriptor: descriptor, ProviderID: descriptor.Provider,
		Implementation: descriptor.Implementation, Capability: value,
	})
}

func (r *Registry) RegisterRegistration(registration Registration) error {
	registration = normalizeRegistration(registration)
	if err := r.validate(registration); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.baseConflict(registration.Descriptor.OperationID) {
		return fmt.Errorf("capability %q conflicts with the base catalog", registration.Descriptor.OperationID)
	}
	if _, exists := r.overlays[registration.Descriptor.OperationID]; exists {
		return fmt.Errorf("capability %q is already registered", registration.Descriptor.OperationID)
	}
	r.overlays[registration.Descriptor.OperationID] = registration
	return nil
}

// RegisterCapability binds an executable capability to its declarative
// registration. The registry never inspects the capability's internals, but a
// capability-owned descriptor must already carry a positive version; versions
// are never defaulted on the capability's behalf.
func (r *Registry) RegisterCapability(capabilityValue capability.Capability, providerID, implementation string) error {
	registration, err := capabilityRegistration(capabilityValue, providerID, implementation)
	if err != nil {
		return err
	}
	return r.RegisterRegistration(registration)
}

// ReplaceGeneration atomically replaces the executable overlay for one
// provider generation. A failed replacement leaves the prior overlay intact.
func (r *Registry) ReplaceGeneration(generationID string, values []capability.Capability) error {
	if strings.TrimSpace(generationID) == "" {
		return fmt.Errorf("generation id is required")
	}
	registrations := make([]Registration, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		if value == nil {
			return fmt.Errorf("generation contains a nil capability")
		}
		descriptor := value.Definition()
		if descriptor.GenerationID != generationID {
			return fmt.Errorf("capability %q does not belong to generation %q", descriptor.OperationID, generationID)
		}
		if seen[descriptor.OperationID] {
			return fmt.Errorf("generation contains duplicate capability %q", descriptor.OperationID)
		}
		seen[descriptor.OperationID] = true
		registrations = append(registrations, Registration{Descriptor: descriptor, ProviderID: descriptor.Provider, Implementation: descriptor.Implementation, Capability: value})
	}
	for index := range registrations {
		registrations[index] = normalizeRegistration(registrations[index])
		if err := r.validate(registrations[index]); err != nil {
			return err
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	providerID := ""
	if len(registrations) > 0 {
		providerID = registrations[0].ProviderID
	}
	if len(registrations) > 1 {
		for _, registration := range registrations[1:] {
			if registration.ProviderID != providerID {
				return fmt.Errorf("generation %q contains capabilities from multiple providers: %q and %q", generationID, providerID, registration.ProviderID)
			}
		}
	}
	remove := make(map[string]bool)
	for operationID, registration := range r.overlays {
		if registration.Descriptor.GenerationID == generationID || (providerID != "" && registration.ProviderID == providerID) {
			remove[operationID] = true
		}
	}
	for _, registration := range registrations {
		if r.baseConflict(registration.Descriptor.OperationID) {
			return fmt.Errorf("capability %q conflicts with the base catalog", registration.Descriptor.OperationID)
		}
		if _, exists := r.overlays[registration.Descriptor.OperationID]; exists && !remove[registration.Descriptor.OperationID] {
			return fmt.Errorf("capability %q is already registered by another overlay", registration.Descriptor.OperationID)
		}
	}
	for operationID := range remove {
		delete(r.overlays, operationID)
	}
	for _, registration := range registrations {
		r.overlays[registration.Descriptor.OperationID] = registration
	}
	return nil
}

// ReplaceProvider atomically replaces an executable overlay for a provider
// whose capabilities are not associated with a durable lifecycle generation.
// Validation and conflict checks complete before any existing overlay is
// removed, so a failed refresh cannot leave a partially published provider.
func (r *Registry) ReplaceProvider(providerID string, values []capability.Capability) error {
	providerID = normalizeRegistrationProvider(providerID)
	if providerID == "" {
		return fmt.Errorf("provider id is required")
	}
	registrations := make([]Registration, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		if value == nil {
			return fmt.Errorf("provider %q contains a nil capability", providerID)
		}
		registration, err := capabilityRegistration(value, providerID, value.Definition().Implementation)
		if err != nil {
			return err
		}
		if normalizeRegistrationProvider(registration.Descriptor.Provider) != providerID {
			return fmt.Errorf("capability %q belongs to provider %q, not %q", registration.Descriptor.OperationID, registration.Descriptor.Provider, providerID)
		}
		if seen[registration.Descriptor.OperationID] {
			return fmt.Errorf("provider %q contains duplicate capability %q", providerID, registration.Descriptor.OperationID)
		}
		seen[registration.Descriptor.OperationID] = true
		registrations = append(registrations, registration)
	}
	for index := range registrations {
		registrations[index] = normalizeRegistration(registrations[index])
		if err := r.validate(registrations[index]); err != nil {
			return err
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	remove := make(map[string]bool)
	for operationID, registration := range r.overlays {
		if normalizeRegistrationProvider(registration.ProviderID) == providerID {
			remove[operationID] = true
		}
	}
	for _, registration := range registrations {
		if r.baseConflict(registration.Descriptor.OperationID) {
			return fmt.Errorf("capability %q conflicts with the base catalog", registration.Descriptor.OperationID)
		}
		if _, exists := r.overlays[registration.Descriptor.OperationID]; exists && !remove[registration.Descriptor.OperationID] {
			return fmt.Errorf("capability %q is already registered by another provider", registration.Descriptor.OperationID)
		}
	}
	for operationID := range remove {
		delete(r.overlays, operationID)
	}
	for _, registration := range registrations {
		r.overlays[registration.Descriptor.OperationID] = registration
	}
	return nil
}

func (r *Registry) Resolve(operationID, catalogRevision string) (capability.Capability, error) {
	if strings.TrimSpace(catalogRevision) == "" {
		return nil, fmt.Errorf("catalog revision is required")
	}
	if snapshot := r.Snapshot(); snapshot.Revision != catalogRevision {
		return nil, fmt.Errorf("catalog revision %q is stale; current revision is %q", catalogRevision, snapshot.Revision)
	}
	value, ok := r.ResolveCapability(operationID)
	if !ok {
		return nil, fmt.Errorf("capability %q is not registered", operationID)
	}
	return value, nil
}

// Upsert replaces an existing provider overlay only when its provider and
// implementation identity are unchanged. This lets a reloaded generation
// refresh schemas without creating a second MCP command registration.
func (r *Registry) Upsert(registration Registration) error {
	registration = normalizeRegistration(registration)
	if err := r.validate(registration); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.baseConflict(registration.Descriptor.OperationID) {
		return fmt.Errorf("capability %q conflicts with the base catalog", registration.Descriptor.OperationID)
	}
	if existing, exists := r.overlays[registration.Descriptor.OperationID]; exists {
		if existing.ProviderID != registration.ProviderID || existing.Implementation != registration.Implementation {
			return fmt.Errorf("capability %q is already registered by another provider", registration.Descriptor.OperationID)
		}
		if registration.Descriptor.Version < existing.Descriptor.Version {
			return fmt.Errorf("capability %q cannot be downgraded from version %d to %d", registration.Descriptor.OperationID, existing.Descriptor.Version, registration.Descriptor.Version)
		}
	}
	r.overlays[registration.Descriptor.OperationID] = registration
	return nil
}

func (r *Registry) baseConflict(operationID string) bool {
	for _, descriptor := range r.base.Tools {
		if descriptor.OperationID == operationID {
			return true
		}
	}
	return false
}

// UpsertCapability refreshes a dynamic provider capability and its executable
// wrapper together.
func (r *Registry) UpsertCapability(capabilityValue capability.Capability, providerID, implementation string) error {
	registration, err := capabilityRegistration(capabilityValue, providerID, implementation)
	if err != nil {
		return err
	}
	return r.Upsert(registration)
}

func capabilityRegistration(value capability.Capability, providerID, implementation string) (Registration, error) {
	if value == nil {
		return Registration{}, fmt.Errorf("capability implementation is required")
	}
	descriptor := value.Definition()
	if descriptor.Version < 1 {
		return Registration{}, fmt.Errorf("capability %q must declare a positive version", descriptor.OperationID)
	}
	if descriptor.Implementation == "" {
		descriptor.Implementation = implementation
	}
	if descriptor.Provider == "" {
		descriptor.Provider = providerID
	}
	return Registration{
		Descriptor:     descriptor,
		ProviderID:     providerID,
		Implementation: implementation,
		Capability:     value,
	}, nil
}

// ResolveCapability returns the executable capability associated with an
// overlay. Metadata-only registrations are intentionally not executable.
func (r *Registry) ResolveCapability(operationID string) (capability.Capability, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	registration, ok := r.overlays[operationID]
	if !ok || registration.Capability == nil {
		return nil, false
	}
	return registration.Capability, true
}

func (r *Registry) AuthorizeProvider(providerID string) error {
	providerID = normalizeRegistrationProvider(providerID)
	if providerID == "" {
		return fmt.Errorf("provider id is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.authorizedProviders[providerID] = true
	return nil
}

func (r *Registry) Unregister(operationID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.overlays[operationID]; !exists {
		return fmt.Errorf("capability %q is not an overlay", operationID)
	}
	delete(r.overlays, operationID)
	return nil
}

func (r *Registry) validate(registration Registration) error {
	descriptor := registration.Descriptor
	if registration.Capability == nil {
		return fmt.Errorf("capability %q must provide an executable validation boundary", descriptor.OperationID)
	}
	capabilityDescriptor := registration.Capability.Definition()
	if capabilityDescriptor.OperationID != descriptor.OperationID || capabilityDescriptor.Name != descriptor.Name {
		return fmt.Errorf("capability %q descriptor does not match its registration", descriptor.OperationID)
	}
	if !operationNamePattern.MatchString(descriptor.OperationID) || descriptor.OperationID != descriptor.Name {
		return fmt.Errorf("operationId and name must be the same valid operation identifier")
	}
	registrationProvider := normalizeRegistrationProvider(registration.ProviderID)
	if registrationProvider == "" || !r.authorizedProviders[registrationProvider] {
		return fmt.Errorf("registration provider does not match the authorized provider")
	}
	if strings.TrimSpace(registration.Implementation) == "" {
		return fmt.Errorf("registration must name an installed implementation")
	}
	if strings.TrimSpace(descriptor.Implementation) == "" || descriptor.Implementation != registration.Implementation {
		return fmt.Errorf("capability %q implementation must match the installed implementation", descriptor.OperationID)
	}
	if len(r.allowedImplementations) > 0 && !r.allowedImplementations[registration.Implementation] {
		return fmt.Errorf("registration implementation %q is not authorized", registration.Implementation)
	}
	if descriptor.Provider != "" && normalizeRegistrationProvider(descriptor.Provider) != registrationProvider {
		return fmt.Errorf("descriptor provider does not match the authorized provider")
	}
	if descriptor.InputSchema == nil || descriptor.OutputSchema == nil {
		return fmt.Errorf("capability %q must provide input and output schemas", descriptor.OperationID)
	}
	return r.validateDescriptor(descriptor)
}

func (r *Registry) validateDescriptor(descriptor tools.CapabilityDescriptor) error {
	if descriptor.InputSchema == nil || descriptor.OutputSchema == nil {
		if len(descriptor.Requires) > 0 || len(descriptor.Produces) > 0 || len(descriptor.ResourceKinds) > 0 {
			return fmt.Errorf("capability %q has typed metadata but is missing input or output schemas", descriptor.OperationID)
		}
		// A few legacy lifecycle-only entries are intentionally metadata-only
		// and have no resource contract to validate.
		return nil
	}
	if schemaType, ok := descriptor.InputSchema["type"].(string); !ok || schemaType != "object" {
		return fmt.Errorf("capability %q input schema must declare type object", descriptor.OperationID)
	}
	if err := validateJSONSchema(descriptor.OperationID+" input", descriptor.InputSchema); err != nil {
		return err
	}
	if err := validateJSONSchema(descriptor.OperationID+" output", descriptor.OutputSchema); err != nil {
		return err
	}
	if err := selectors.Validate(descriptor.OutputSchema, descriptor.OutputType, descriptor.ResultTypes); err != nil {
		return fmt.Errorf("capability %q result selectors: %w", descriptor.OperationID, err)
	}
	if descriptor.Version < 1 {
		return fmt.Errorf("capability %q must declare a positive version", descriptor.OperationID)
	}
	switch descriptor.Effect {
	case "read", "mutation", "destructive", "credential_bearing":
	default:
		return fmt.Errorf("capability %q has unsupported effect %q", descriptor.OperationID, descriptor.Effect)
	}
	for _, kind := range descriptor.ResourceKinds {
		if !resourceid.IsKnownType(kind) || (len(r.known) > 0 && !r.known[kind]) {
			return fmt.Errorf("capability %q references unknown resource kind %q", descriptor.OperationID, kind)
		}
	}
	for _, binding := range descriptor.Requires {
		if err := r.validateBinding(descriptor, binding, descriptor.InputSchema, "requires"); err != nil {
			return err
		}
	}
	for _, binding := range descriptor.Produces {
		if err := r.validateBinding(descriptor, binding, descriptor.OutputSchema, "produces"); err != nil {
			return err
		}
	}
	return nil
}

func (r *Registry) validateBinding(descriptor tools.CapabilityDescriptor, binding tools.ResourceBinding, schema map[string]any, direction string) error {
	if strings.TrimSpace(binding.ResourceType) == "" {
		return fmt.Errorf("capability %q has a resource binding without a resource type", descriptor.OperationID)
	}
	if !resourceid.IsKnownType(binding.ResourceType) || (len(r.known) > 0 && !r.known[binding.ResourceType]) {
		return fmt.Errorf("capability %q references unknown binding resource kind %q", descriptor.OperationID, binding.ResourceType)
	}
	path := binding.Argument
	if direction == "produces" {
		path = binding.SourcePath
	}
	if strings.TrimSpace(path) == "" || !schemaPathExists(schema, path) {
		if direction == "produces" {
			return fmt.Errorf("capability %q produces binding sourcePath %q in its output schema", descriptor.OperationID, path)
		}
		return fmt.Errorf("capability %q requires binding argument %q in its input schema", descriptor.OperationID, path)
	}
	if schemaPathType(schema, path) != "string" {
		return fmt.Errorf("capability %q resource binding path %q must be a string", descriptor.OperationID, path)
	}
	if direction == "produces" && binding.SelectorID != "" {
		selector, ok := selectors.Find(descriptor.OutputType, binding.SelectorID, descriptor.ResultTypes)
		if !ok {
			return fmt.Errorf("capability %q produced binding references unknown selector %q", descriptor.OperationID, binding.SelectorID)
		}
		if selector.SourcePath != binding.SourcePath {
			return fmt.Errorf("capability %q selector %q sourcePath %q disagrees with produced binding path %q", descriptor.OperationID, binding.SelectorID, selector.SourcePath, binding.SourcePath)
		}
	}
	return nil
}

func validateJSONSchema(label string, value map[string]any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("%s schema is not JSON: %w", label, err)
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(encoded, &schema); err != nil {
		return fmt.Errorf("%s schema is malformed: %w", label, err)
	}
	if _, err := schema.Resolve(nil); err != nil {
		return fmt.Errorf("%s schema is invalid: %w", label, err)
	}
	return nil
}

func schemaPathExists(schema map[string]any, path string) bool {
	_, ok := schemaPath(schema, path)
	return ok
}

func schemaPathType(schema map[string]any, path string) string {
	value, ok := schemaPath(schema, path)
	if !ok {
		return ""
	}
	typeName, _ := value["type"].(string)
	return typeName
}

func schemaPath(schema map[string]any, path string) (map[string]any, bool) {
	current := schema
	for _, segment := range strings.Split(path, ".") {
		segment = strings.TrimSuffix(segment, "[]")
		properties, ok := current["properties"].(map[string]any)
		if !ok {
			return nil, false
		}
		child, ok := properties[segment].(map[string]any)
		if !ok {
			return nil, false
		}
		current = child
		if items, ok := current["items"].(map[string]any); ok {
			current = items
		}
	}
	return current, true
}

func normalizeRegistration(registration Registration) Registration {
	if registration.Descriptor.Version == 0 {
		registration.Descriptor.Version = 1
	}
	return registration
}

func normalizeRegistrationProvider(providerID string) string {
	return strings.ToLower(strings.TrimSpace(providerID))
}
