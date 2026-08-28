package hostruntime

import (
	"sync"

	"github.com/wunderous/host-agents/internal/resourceid"
)

// NewInMemoryResourceRegistry is the default registry: durable enough for a
// single process, and the one every domain test uses. It lives beside the
// interface it implements so a domain can build a real Shared without reaching
// back into a domain.
func NewInMemoryResourceRegistry() ResourceRegistry {
	return &inMemoryResourceRegistry{records: map[string]resourceid.Record{}}
}

type inMemoryResourceRegistry struct {
	mu      sync.RWMutex
	records map[string]resourceid.Record
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
