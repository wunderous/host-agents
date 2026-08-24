package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/wunderous/host-agents/internal/capability"
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
		descriptors = append(descriptors, tools.CloneCapabilityDescriptor(descriptor))
	}
	for _, registration := range r.overlays {
		descriptors = append(descriptors, tools.CloneCapabilityDescriptor(registration.Descriptor))
	}
	sort.Slice(descriptors, func(i, j int) bool { return descriptors[i].OperationID < descriptors[j].OperationID })
	return tools.CapabilityCatalogSnapshot{ProviderID: r.provider, Revision: revision(r.provider, descriptors), Tools: descriptors}
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
	for _, descriptor := range r.base.Tools {
		if descriptor.OperationID == registration.Descriptor.OperationID {
			return fmt.Errorf("capability %q conflicts with the base catalog", descriptor.OperationID)
		}
	}
	if _, exists := r.overlays[registration.Descriptor.OperationID]; exists {
		return fmt.Errorf("capability %q is already registered", registration.Descriptor.OperationID)
	}
	r.overlays[registration.Descriptor.OperationID] = registration
	return nil
}

// RegisterCapability binds an executable capability to its declarative
// registration. The registry never inspects the capability's internals.
func (r *Registry) RegisterCapability(capabilityValue capability.Capability, providerID, implementation string) error {
	if capabilityValue == nil {
		return fmt.Errorf("capability implementation is required")
	}
	descriptor := capabilityValue.Definition()
	if descriptor.Implementation == "" {
		descriptor.Implementation = implementation
	}
	if descriptor.Provider == "" {
		descriptor.Provider = providerID
	}
	return r.RegisterRegistration(Registration{
		Descriptor:     descriptor,
		ProviderID:     providerID,
		Implementation: implementation,
		Capability:     capabilityValue,
	})
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
	for _, registration := range registrations {
		registration = normalizeRegistration(registration)
		if err := r.validate(registration); err != nil {
			return err
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for operationID, registration := range r.overlays {
		if registration.Descriptor.GenerationID == generationID || (len(registrations) > 0 && registration.ProviderID == registrations[0].ProviderID) {
			delete(r.overlays, operationID)
		}
	}
	for _, registration := range registrations {
		if _, exists := r.overlays[registration.Descriptor.OperationID]; exists {
			return fmt.Errorf("capability %q is already registered by another overlay", registration.Descriptor.OperationID)
		}
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
	for _, descriptor := range r.base.Tools {
		if descriptor.OperationID == registration.Descriptor.OperationID {
			return fmt.Errorf("capability %q conflicts with the base catalog", registration.Descriptor.OperationID)
		}
	}
	if existing, exists := r.overlays[registration.Descriptor.OperationID]; exists {
		if existing.ProviderID != registration.ProviderID || existing.Implementation != registration.Implementation {
			return fmt.Errorf("capability %q is already registered by another provider", registration.Descriptor.OperationID)
		}
	}
	r.overlays[registration.Descriptor.OperationID] = registration
	return nil
}

// UpsertCapability refreshes a dynamic provider capability and its executable
// wrapper together.
func (r *Registry) UpsertCapability(capabilityValue capability.Capability, providerID, implementation string) error {
	if capabilityValue == nil {
		return fmt.Errorf("capability implementation is required")
	}
	descriptor := capabilityValue.Definition()
	if descriptor.Implementation == "" {
		descriptor.Implementation = implementation
	}
	if descriptor.Provider == "" {
		descriptor.Provider = providerID
	}
	return r.Upsert(Registration{
		Descriptor:     descriptor,
		ProviderID:     providerID,
		Implementation: implementation,
		Capability:     capabilityValue,
	})
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
	if descriptor.Version < 1 {
		return fmt.Errorf("capability %q must declare a positive version", descriptor.OperationID)
	}
	switch descriptor.Effect {
	case "read", "mutation", "destructive", "credential_bearing":
	default:
		return fmt.Errorf("capability %q has unsupported effect %q", descriptor.OperationID, descriptor.Effect)
	}
	for _, kind := range descriptor.ResourceKinds {
		if len(r.known) > 0 && !r.known[kind] {
			return fmt.Errorf("capability %q references unknown resource kind %q", descriptor.OperationID, kind)
		}
	}
	for _, binding := range descriptor.Requires {
		if strings.TrimSpace(binding.ResourceType) == "" {
			return fmt.Errorf("capability %q has a resource binding without a resource type", descriptor.OperationID)
		}
		if len(r.known) > 0 && !r.known[binding.ResourceType] {
			return fmt.Errorf("capability %q references unknown binding resource kind %q", descriptor.OperationID, binding.ResourceType)
		}
		if strings.TrimSpace(binding.Argument) == "" || !schemaPathExists(descriptor.InputSchema, binding.Argument) {
			return fmt.Errorf("capability %q requires binding argument %q in its input schema", descriptor.OperationID, binding.Argument)
		}
	}
	for _, binding := range descriptor.Produces {
		if strings.TrimSpace(binding.ResourceType) == "" {
			return fmt.Errorf("capability %q has a resource binding without a resource type", descriptor.OperationID)
		}
		if len(r.known) > 0 && !r.known[binding.ResourceType] {
			return fmt.Errorf("capability %q references unknown binding resource kind %q", descriptor.OperationID, binding.ResourceType)
		}
		if strings.TrimSpace(binding.SourcePath) == "" || !schemaPathExists(descriptor.OutputSchema, binding.SourcePath) {
			return fmt.Errorf("capability %q produces binding sourcePath %q in its output schema", descriptor.OperationID, binding.SourcePath)
		}
	}
	return nil
}

func schemaPathExists(schema map[string]any, path string) bool {
	current := schema
	for _, segment := range strings.Split(path, ".") {
		segment = strings.TrimSuffix(segment, "[]")
		properties, ok := current["properties"].(map[string]any)
		if !ok {
			return false
		}
		child, ok := properties[segment].(map[string]any)
		if !ok {
			return false
		}
		current = child
		if items, ok := current["items"].(map[string]any); ok {
			current = items
		}
	}
	return true
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

func revision(provider string, descriptors []tools.CapabilityDescriptor) string {
	encoded, _ := json.Marshal(struct {
		Provider string                       `json:"provider"`
		Tools    []tools.CapabilityDescriptor `json:"tools"`
	}{Provider: provider, Tools: descriptors})
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:])
}
