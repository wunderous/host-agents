package hostagent

import (
	"context"
	"errors"
	"fmt"
	"testing"

	capabilitycontract "github.com/wunderous/host-agents/contracts/capability"
	"github.com/wunderous/host-agents/internal/domain/kubernetes"
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
	service := New(Options{TenantID: "tenant-a", ResourceRegistry: registry})
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

// clusterListExecutor answers only cluster discovery, and counts the calls so a
// test can tell adoption from a registration that some earlier listing left
// behind.
type clusterListExecutor struct {
	clusters []map[string]any
	calls    int
}

func (e *clusterListExecutor) Execute(_ context.Context, operation string, _ kubernetes.KubernetesProviderRequest) (map[string]any, error) {
	if operation != capabilitycontract.KubernetesListClustersOperation {
		return nil, fmt.Errorf("unexpected operation %q", operation)
	}
	e.calls++
	return map[string]any{"clusters": e.clusters, "total": len(e.clusters)}, nil
}

func TestResourceResolverAdoptsProvisionedClusterWithoutPriorDiscovery(t *testing.T) {
	executor := &clusterListExecutor{clusters: []map[string]any{
		{"name": "lifecycle-cluster", "status": "Running", "instanceType": "container"},
	}}
	service := New(Options{TenantID: "tenant-a", ResourceRegistry: &memoryResourceRegistry{}})
	service.Kubernetes().SetKubernetesProviderExecutor(executor)

	// The registry has never seen this cluster: this is the state right after a
	// provision, before anything called list_kubernetes_clusters.
	resolved, err := service.ResolveResource("cluster:tenant-a:lifecycle-cluster", "cluster")
	if err != nil {
		t.Fatalf("resolve provisioned cluster: %v", err)
	}
	if executor.calls == 0 {
		t.Fatal("cluster was resolved without asking the Kubernetes domain whether it exists")
	}
	if resolved.Values["providerInstanceName"] != "lifecycle-cluster" || resolved.Values["instanceType"] != "container" {
		t.Fatalf("adopted coordinates = %+v", resolved.Values)
	}

	// A cluster the provider does not report stays unresolved: adoption observes,
	// it does not invent.
	if _, err := service.ResolveResource("cluster:tenant-a:absent-cluster", "cluster"); err == nil {
		t.Fatal("an unlisted cluster resolved")
	}
}
