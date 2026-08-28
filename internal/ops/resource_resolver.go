package ops

import (
	"fmt"
	"strings"

	"github.com/wunderous/host-agents/internal/cordis"
	"github.com/wunderous/host-agents/internal/hostruntime"
	"github.com/wunderous/host-agents/internal/resourceid"
)

const (
	ResourceRegistryServiceKey cordis.ServiceKey = "opute.resource.registry"
	ResourceResolverServiceKey cordis.ServiceKey = "opute.resource.resolver"
)

// ResourceRegistry is the narrow persistence boundary needed by resolution.
// Keeping it as an interface lets provider operations use the same resolver
// with standalone SQLite and test fakes without coupling them to SQL.
// It lives in hostruntime: every domain persists what it creates, and the
// interface names no domain type. This alias keeps the existing spelling for
// the ops callers that have not moved yet.
type ResourceRegistry = hostruntime.ResourceRegistry

type ResourceRegistryService struct {
	Registry ResourceRegistry
	TenantID string
}

// inMemoryResourceRegistry is the explicit non-persistent fallback used by
// embedded/unit servers that do not request a state directory. Production
// servers replace it with the additive SQLite registry in hostmcp.NewServer.

func (s ResourceRegistryService) Key() cordis.ServiceKey { return ResourceRegistryServiceKey }

type ResourceResolverService struct {
	Resolver *HostOperationsService
	TenantID string
}

func (s ResourceResolverService) Key() cordis.ServiceKey { return ResourceResolverServiceKey }

// Coordinates is the validated result of resolving an opaque entity URI.
// Callers may only use coordinates after Resolve has checked tenant and type.
// Coordinates and the registry bookkeeping below it moved to hostruntime: they
// name no domain type and every domain records what it creates. Resolution
// stayed here -- it asks incus whether an instance exists, which makes it an
// operation under S9.2 rule 3.
type Coordinates = hostruntime.Coordinates

func (s *HostOperationsService) RegisterResource(uri string, coordinates map[string]any) error {
	return s.shared.RegisterResource(uri, coordinates)
}

func (s *HostOperationsService) DeregisterResource(uri string) error {
	return s.shared.DeregisterResource(uri)
}

func (s *HostOperationsService) ResourceURIForProviderName(providerName string) string {
	return s.shared.ResourceURIForProviderName(providerName)
}

func (s *HostOperationsService) SetResourceRegistry(registry ResourceRegistry) {
	if s != nil {
		s.shared.ResourceRegistry = registry
	}
}

func (s *HostOperationsService) ResourceRegistry() ResourceRegistry {
	if s == nil {
		return nil
	}
	return s.shared.ResourceRegistry
}

func (s *HostOperationsService) ResolveResource(uri, wantType string) (Coordinates, error) {
	return s.shared.ResolveResource(uri, wantType, s.adoptResource)
}

// adoptResource observes a resource the registry has never seen. Both branches
// are deliberately ASKS, not assumptions: never turn a display name into
// coordinates without a domain confirming the thing is really there.
func (s *HostOperationsService) adoptResource(parsed resourceid.URI) (map[string]any, error) {
	switch parsed.ResourceType {
	case resourceid.TypeVM, resourceid.TypeContainer:
		info, err := s.GetVMInfo(parsed.ResourceID, true)
		if err != nil || strings.TrimSpace(info.Name) == "" {
			return nil, nil
		}
		actualType := resourceid.TypeContainer
		if strings.EqualFold(info.Type, "vm") {
			actualType = resourceid.TypeVM
		}
		if parsed.ResourceType != actualType {
			return nil, fmt.Errorf("resource type mismatch: %s resolves to %s", parsed, actualType)
		}
		return map[string]any{
			"providerInstanceName": info.Name,
			"displayName":          info.Name,
			"instanceType":         info.Type,
		}, nil
	case resourceid.TypeHostService:
		parts := strings.SplitN(parsed.ResourceID, "/", 2)
		if len(parts) != 2 || (parts[0] != "user" && parts[0] != "system") || strings.TrimSpace(parts[1]) == "" {
			return nil, fmt.Errorf("host-service URI must use <scope>/<service-name>: %s", parsed)
		}
		observed, err := s.InspectHostService(InspectHostServiceArgs{ServiceName: parts[1], Scope: parts[0]}, nil)
		if err != nil {
			return nil, fmt.Errorf("adopt host service %s: %w", parsed, err)
		}
		switch status, _ := observed["status"].(string); status {
		case "not-found", "failed", "":
			return nil, fmt.Errorf("host service not found: %s", parsed)
		}
		return map[string]any{"serviceName": parts[1], "scope": parts[0]}, nil
	}
	return nil, nil
}

// AttachLocalLLMModelURIs projects model observations into the same opaque,
// tenant-scoped identity boundary as Incus inventory. The model reference is
// provider-native data stored in coordinates; it is never reconstructed from
// a client-supplied display label during a later mutation.
func (s *HostOperationsService) AttachLocalLLMModelURIs(result *LocalLLMProbeResult) {
	if s == nil || result == nil {
		return
	}
	for index := range result.Models {
		model := &result.Models[index]
		uri, err := resourceid.ModelURI(s.shared.TenantID, model.Name)
		if err != nil {
			continue
		}
		model.URI = uri.String()
		if s.shared.ResourceRegistry != nil {
			_ = s.RegisterResource(model.URI, map[string]any{
				"modelRef": model.Name,
				"runtime":  result.Runtime,
			})
		}
	}
}
