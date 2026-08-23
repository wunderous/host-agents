package ops

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/wunderous/host-agents/internal/cordis"
	"github.com/wunderous/host-agents/internal/resourceid"
)

const (
	ResourceRegistryServiceKey cordis.ServiceKey = "opute.resource.registry"
	ResourceResolverServiceKey cordis.ServiceKey = "opute.resource.resolver"
)

// ResourceRegistry is the narrow persistence boundary needed by resolution.
// Keeping it as an interface lets provider operations use the same resolver
// with standalone SQLite and test fakes without coupling them to SQL.
type ResourceRegistry interface {
	UpsertResource(record resourceid.Record) error
	GetResource(uri string) (resourceid.Record, bool, error)
	DeleteResource(uri string) error
	ListResources(resourceType, tenantID string) ([]resourceid.Record, error)
}

type ResourceRegistryService struct {
	Registry ResourceRegistry
	TenantID string
}

// inMemoryResourceRegistry is the explicit non-persistent fallback used by
// embedded/unit servers that do not request a state directory. Production
// servers replace it with the additive SQLite registry in hostmcp.NewServer.
type inMemoryResourceRegistry struct {
	mu      sync.RWMutex
	records map[string]resourceid.Record
}

func newInMemoryResourceRegistry() ResourceRegistry {
	return &inMemoryResourceRegistry{records: make(map[string]resourceid.Record)}
}

func (r *inMemoryResourceRegistry) UpsertResource(record resourceid.Record) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if record.Status == "" {
		record.Status = "active"
	}
	r.records[record.URI] = record
	return nil
}

func (r *inMemoryResourceRegistry) GetResource(uri string) (resourceid.Record, bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	record, ok := r.records[uri]
	return record, ok, nil
}

func (r *inMemoryResourceRegistry) DeleteResource(uri string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.records, uri)
	return nil
}

func (r *inMemoryResourceRegistry) ListResources(resourceType, tenantID string) ([]resourceid.Record, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]resourceid.Record, 0, len(r.records))
	for _, record := range r.records {
		if resourceType != "" && record.ResourceType != resourceType {
			continue
		}
		if tenantID != "" && record.TenantID != tenantID {
			continue
		}
		result = append(result, record)
	}
	return result, nil
}

func (s ResourceRegistryService) Key() cordis.ServiceKey { return ResourceRegistryServiceKey }

type ResourceResolverService struct {
	Resolver *HostOperationsService
	TenantID string
}

func (s ResourceResolverService) Key() cordis.ServiceKey { return ResourceResolverServiceKey }

// Coordinates is the validated result of resolving an opaque entity URI.
// Callers may only use coordinates after Resolve has checked tenant and type.
type Coordinates struct {
	URI          resourceid.URI
	ResourceType string
	TenantID     string
	ResourceID   string
	Values       map[string]any
}

func (s *HostOperationsService) SetResourceRegistry(registry ResourceRegistry) {
	if s != nil {
		s.resourceRegistry = registry
	}
}

func (s *HostOperationsService) ResourceRegistry() ResourceRegistry {
	if s == nil {
		return nil
	}
	return s.resourceRegistry
}

func (s *HostOperationsService) ResolveResource(uri, wantType string) (Coordinates, error) {
	parsed, err := resourceid.Parse(uri)
	if err != nil {
		return Coordinates{}, err
	}
	if tenant := strings.TrimSpace(s.tenantID); tenant != "" && parsed.TenantID != tenant {
		return Coordinates{}, fmt.Errorf("%w: active tenant %q", resourceid.ErrForeignTenant, tenant)
	}
	if wantType != "" && parsed.ResourceType != wantType {
		return Coordinates{}, fmt.Errorf("%w: expected %q, got %q", resourceid.ErrInvalidURI, wantType, parsed.ResourceType)
	}
	if s.resourceRegistry == nil {
		return Coordinates{}, errors.New("resource registry is not configured")
	}
	record, found, err := s.resourceRegistry.GetResource(parsed.String())
	if err != nil {
		return Coordinates{}, fmt.Errorf("resolve resource %s: %w", parsed, err)
	}
	if (!found || record.Status != "active") && (parsed.ResourceType == resourceid.TypeVM || parsed.ResourceType == resourceid.TypeContainer || parsed.ResourceType == resourceid.TypeCluster) {
		// Incus inventory is a discoverable single-key resource. Adopt it only
		// after the provider confirms the instance exists; never turn a display
		// name into coordinates without that observation.
		info, infoErr := s.GetVMInfo(parsed.ResourceID, true)
		if infoErr == nil && strings.TrimSpace(info.Name) != "" {
			actualType := resourceid.TypeContainer
			if strings.EqualFold(info.Type, "vm") {
				actualType = resourceid.TypeVM
			}
			if parsed.ResourceType != resourceid.TypeCluster && parsed.ResourceType != actualType {
				return Coordinates{}, fmt.Errorf("resource type mismatch: %s resolves to %s", parsed, actualType)
			}
			coordinates := map[string]any{
				"providerInstanceName": info.Name,
				"displayName":          info.Name,
				"instanceType":         info.Type,
			}
			if parsed.ResourceType == resourceid.TypeCluster {
				coordinates["vmName"] = info.Name
			}
			if registerErr := s.RegisterResource(parsed.String(), coordinates); registerErr != nil {
				return Coordinates{}, registerErr
			}
			record, found, err = s.resourceRegistry.GetResource(parsed.String())
			if err != nil {
				return Coordinates{}, err
			}
		}
	}
	if (!found || record.Status != "active") && parsed.ResourceType == resourceid.TypeHostService {
		parts := strings.SplitN(parsed.ResourceID, "/", 2)
		if len(parts) != 2 || (parts[0] != "user" && parts[0] != "system") || strings.TrimSpace(parts[1]) == "" {
			return Coordinates{}, fmt.Errorf("host-service URI must use <scope>/<service-name>: %s", parsed)
		}
		observed, inspectErr := s.InspectHostService(InspectHostServiceArgs{ServiceName: parts[1], Scope: parts[0]}, nil)
		if inspectErr != nil {
			return Coordinates{}, fmt.Errorf("adopt host service %s: %w", parsed, inspectErr)
		}
		status, _ := observed["status"].(string)
		if status == "not-found" || status == "failed" || status == "" {
			return Coordinates{}, fmt.Errorf("host service not found: %s", parsed)
		}
		if registerErr := s.RegisterResource(parsed.String(), map[string]any{
			"serviceName": parts[1],
			"scope":       parts[0],
		}); registerErr != nil {
			return Coordinates{}, registerErr
		}
		record, found, err = s.resourceRegistry.GetResource(parsed.String())
		if err != nil {
			return Coordinates{}, err
		}
	}
	if !found || record.Status != "active" {
		return Coordinates{}, fmt.Errorf("resource not found: %s", parsed)
	}
	return Coordinates{URI: parsed, ResourceType: parsed.ResourceType, TenantID: parsed.TenantID, ResourceID: parsed.ResourceID, Values: record.Coordinates}, nil
}

func (s *HostOperationsService) RegisterResource(uri string, coordinates map[string]any) error {
	parsed, err := resourceid.Parse(uri)
	if err != nil {
		return err
	}
	if tenant := strings.TrimSpace(s.tenantID); tenant != "" && parsed.TenantID != tenant {
		return fmt.Errorf("%w: active tenant %q", resourceid.ErrForeignTenant, tenant)
	}
	if s.resourceRegistry == nil {
		return errors.New("resource registry is not configured")
	}
	return s.resourceRegistry.UpsertResource(resourceid.Record{
		URI: parsed.String(), ResourceType: parsed.ResourceType, TenantID: parsed.TenantID,
		ResourceID: parsed.ResourceID, Coordinates: coordinates, Status: "active",
	})
}

func (s *HostOperationsService) DeregisterResource(uri string) error {
	parsed, err := resourceid.Parse(uri)
	if err != nil {
		return err
	}
	if tenant := strings.TrimSpace(s.tenantID); tenant != "" && parsed.TenantID != tenant {
		return fmt.Errorf("%w: active tenant %q", resourceid.ErrForeignTenant, tenant)
	}
	if s.resourceRegistry == nil {
		return errors.New("resource registry is not configured")
	}
	return s.resourceRegistry.DeleteResource(parsed.String())
}

func (s *HostOperationsService) ResourceURIForProviderName(providerName string) string {
	if s == nil || s.resourceRegistry == nil {
		return ""
	}
	records, err := s.resourceRegistry.ListResources("", s.tenantID)
	if err != nil {
		return ""
	}
	for _, record := range records {
		if value, ok := record.Coordinates["providerInstanceName"].(string); ok && value == providerName {
			return record.URI
		}
	}
	return ""
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
		uri, err := resourceid.ModelURI(s.tenantID, model.Name)
		if err != nil {
			continue
		}
		model.URI = uri.String()
		if s.resourceRegistry != nil {
			_ = s.RegisterResource(model.URI, map[string]any{
				"modelRef": model.Name,
				"runtime":  result.Runtime,
			})
		}
	}
}
