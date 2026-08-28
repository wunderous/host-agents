package kubernetes

import (
	"context"
	"fmt"
	"testing"

	"github.com/wunderous/host-agents/internal/hostruntime"
	"github.com/wunderous/host-agents/internal/resourceid"
)

type recordingKubernetesExecutor struct {
	operation string
	request   KubernetesProviderRequest
}

func (r *recordingKubernetesExecutor) Execute(_ context.Context, operation string, request KubernetesProviderRequest) (map[string]any, error) {
	r.operation = operation
	r.request = request
	return map[string]any{"uri": request.TargetURI, "applied": true}, nil
}

func TestGenericKubernetesOperationDelegatesAfterCanonicalResolution(t *testing.T) {
	service := testService("tenant-a")
	if err := service.shared.RegisterResource("cluster:tenant-a:k3s", map[string]any{
		"providerInstanceName": "k3s-container",
		"instanceType":         "container",
	}); err != nil {
		t.Fatal(err)
	}
	executor := &recordingKubernetesExecutor{}
	service.SetKubernetesProviderExecutor(executor)
	out, err := service.ApplyManifest(ApplyManifestArgs{URI: "cluster:tenant-a:k3s", Manifest: "apiVersion: v1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out["applied"] != true || executor.operation != KubernetesApplyManifestOperation {
		t.Fatalf("delegation = %#v operation=%q", out, executor.operation)
	}
	if executor.request.TargetURI != "cluster:tenant-a:k3s" || executor.request.ProviderInstanceName != "k3s-container" || executor.request.InstanceType != "container" {
		t.Fatalf("resolved provider request = %#v", executor.request)
	}
	if _, err := service.ApplyManifest(ApplyManifestArgs{URI: "cluster:tenant-b:k3s", Manifest: "apiVersion: v1"}, nil); err == nil {
		t.Fatal("foreign tenant target was delegated")
	}
}

// testService builds the domain over a real in-memory registry and the registry
// half of resource resolution.
//
// The real ResolveResource can fall back to asking incus whether an instance
// exists, which is why it is an injected dep rather than a hostruntime member.
// Cluster URIs never take that path -- they must already be registered -- so
// resolving straight from the registry is the whole of the behaviour these
// tests exercise, and a cluster that is absent still fails the way it should.
func testService(tenant string) *Service {
	shared := &hostruntime.Shared{TenantID: tenant, ResourceRegistry: hostruntime.NewInMemoryResourceRegistry()}
	return New(shared, Deps{
		EnsureHostTool: func(string, func(string)) (map[string]any, error) {
			panic("kubernetes provider tests must not reach the host domain")
		},
		ResolveResource: func(uri, wantType string) (hostruntime.Coordinates, error) {
			parsed, err := resourceid.Parse(uri)
			if err != nil {
				return hostruntime.Coordinates{}, err
			}
			if parsed.TenantID != tenant {
				return hostruntime.Coordinates{}, fmt.Errorf("%w: active tenant %q", resourceid.ErrForeignTenant, tenant)
			}
			if wantType != "" && parsed.ResourceType != wantType {
				return hostruntime.Coordinates{}, fmt.Errorf("%w: expected %q", resourceid.ErrInvalidURI, wantType)
			}
			record, found, err := shared.ResourceRegistry.GetResource(parsed.String())
			if err != nil || !found {
				return hostruntime.Coordinates{}, fmt.Errorf("unknown resource %s", uri)
			}
			return hostruntime.Coordinates{
				URI: parsed, ResourceType: parsed.ResourceType, TenantID: parsed.TenantID,
				ResourceID: parsed.ResourceID, Values: record.Coordinates,
			}, nil
		},
	})
}
