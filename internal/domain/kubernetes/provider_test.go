package kubernetes

import (
	"context"
	"testing"

	"github.com/wunderous/host-agents/internal/hostruntime"
)

type recordingKubernetesExecutor struct {
	operation string
	request   KubernetesProviderRequest
	result    map[string]any
}

func (r *recordingKubernetesExecutor) Execute(_ context.Context, operation string, request KubernetesProviderRequest) (map[string]any, error) {
	r.operation = operation
	r.request = request
	if r.result != nil {
		return r.result, nil
	}
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

func TestListKubernetesClustersPreservesMultipleNodeRoles(t *testing.T) {
	service := testService("tenant-a")
	executor := &recordingKubernetesExecutor{result: map[string]any{
		"clusters": []any{map[string]any{
			"name":         "k3s-proof",
			"instanceType": "container",
			"nodes": []any{map[string]any{
				"name":    "server-a",
				"status":  "Ready",
				"roles":   []string{"control-plane", "etcd"},
				"version": "v1.31.8+k3s1",
			}},
		}},
	}}
	service.SetKubernetesProviderExecutor(executor)

	got, err := service.ListKubernetesClusters("")
	if err != nil {
		t.Fatal(err)
	}
	if executor.operation != KubernetesListClustersOperation {
		t.Fatalf("provider operation = %q, want %q", executor.operation, KubernetesListClustersOperation)
	}
	if len(got.Clusters) != 1 || len(got.Clusters[0].Nodes) != 1 {
		t.Fatalf("clusters = %#v, want one cluster with one node", got.Clusters)
	}
	roles := got.Clusters[0].Nodes[0].Roles
	if len(roles) != 2 || roles[0] != "control-plane" || roles[1] != "etcd" {
		t.Fatalf("node roles = %#v, want both provider-observed labels", roles)
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
		// Registry-only resolution: cluster URIs must already be registered,
		// and passing nil for the adopter means a missing one still fails the
		// way it should.
		ResolveResource: func(uri, wantType string) (hostruntime.Coordinates, error) {
			return shared.ResolveResource(uri, wantType, nil)
		},
	})
}
