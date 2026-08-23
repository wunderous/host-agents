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
}

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
	return &Registry{provider: provider, authorizedProviders: authorized, known: known, allowedImplementations: allowed, base: snapshot, overlays: make(map[string]Registration)}
}

func (r *Registry) Snapshot() tools.CapabilityCatalogSnapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	descriptors := append([]tools.CapabilityDescriptor(nil), r.base.Tools...)
	for _, registration := range r.overlays {
		descriptors = append(descriptors, registration.Descriptor)
	}
	sort.Slice(descriptors, func(i, j int) bool { return descriptors[i].OperationID < descriptors[j].OperationID })
	return tools.CapabilityCatalogSnapshot{ProviderID: r.provider, Revision: revision(r.provider, descriptors), Tools: descriptors}
}

func (r *Registry) Register(registration Registration) error {
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

// Upsert replaces an existing provider overlay only when its provider and
// implementation identity are unchanged. This lets a reloaded generation
// refresh schemas without creating a second MCP command registration.
func (r *Registry) Upsert(registration Registration) error {
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
	return nil
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
