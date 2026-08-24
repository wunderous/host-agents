package ops

import (
	"context"
	"testing"
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
	registry := newInMemoryResourceRegistry()
	service := NewHostOperationsService(Options{TenantID: "tenant-a", ResourceRegistry: registry})
	if err := service.RegisterResource("cluster:tenant-a:k3s", map[string]any{
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
