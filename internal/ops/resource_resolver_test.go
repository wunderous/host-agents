package ops

import (
	"errors"
	"testing"

	"github.com/wunderous/host-agents/internal/resourceid"
)

type memoryResourceRegistry struct{ records map[string]resourceid.Record }

func (m *memoryResourceRegistry) UpsertResource(record resourceid.Record) error {
	if m.records == nil {
		m.records = map[string]resourceid.Record{}
	}
	m.records[record.URI] = record
	return nil
}
func (m *memoryResourceRegistry) GetResource(uri string) (resourceid.Record, bool, error) {
	record, ok := m.records[uri]
	return record, ok, nil
}
func (m *memoryResourceRegistry) DeleteResource(uri string) error { delete(m.records, uri); return nil }
func (m *memoryResourceRegistry) ListResources(resourceType, tenantID string) ([]resourceid.Record, error) {
	return nil, nil
}

func TestResourceResolverEnforcesTenantAndRegistry(t *testing.T) {
	registry := &memoryResourceRegistry{}
	service := NewHostOperationsService(Options{TenantID: "tenant-a", ResourceRegistry: registry})
	if err := service.RegisterResource("vm:tenant-a:worker-01", map[string]any{"providerInstanceName": "worker-01"}); err != nil {
		t.Fatal(err)
	}
	resolved, err := service.ResolveResource("vm:tenant-a:worker-01", "vm")
	if err != nil || resolved.Values["providerInstanceName"] != "worker-01" {
		t.Fatalf("resolved = %+v err=%v", resolved, err)
	}
	if _, err := service.ResolveResource("vm:tenant-b:worker-01", "vm"); !errors.Is(err, resourceid.ErrForeignTenant) {
		t.Fatalf("foreign tenant error = %v", err)
	}
	if _, err := service.ResolveResource("container:tenant-a:worker-01", "vm"); err == nil {
		t.Fatal("wrong resource type was accepted")
	}
}
