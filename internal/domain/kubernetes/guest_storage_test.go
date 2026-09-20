package kubernetes

import (
	"testing"
)

func TestGuestStorageDelegatesToProviderOperations(t *testing.T) {
	service := testService("tenant-a")
	if err := service.shared.RegisterResource("cluster:tenant-a:k3s", map[string]any{
		"providerInstanceName": "opute-ha-b",
		"instanceType":         "container",
	}); err != nil {
		t.Fatal(err)
	}
	executor := &recordingKubernetesExecutor{}
	service.SetKubernetesProviderExecutor(executor)

	if _, err := service.InspectGuestStorage(GuestStorageArgs{}); err == nil {
		t.Fatal("missing uri was delegated")
	}
	out, err := service.InspectGuestStorage(GuestStorageArgs{URI: "cluster:tenant-a:k3s", IncludeRegistry: true, ExtraKeepTags: []string{"keep:tag"}})
	if err != nil {
		t.Fatal(err)
	}
	if executor.operation != KubernetesInspectGuestStorageOperation {
		t.Fatalf("operation = %q", executor.operation)
	}
	if out["clusterId"] != "k3s" || executor.request.Arguments["includeRegistry"] != true {
		t.Fatalf("inspect = %#v request=%#v", out, executor.request.Arguments)
	}

	minAge := int64(3600)
	if _, err := service.PruneUnusedClusterImages(GuestStorageArgs{URI: "cluster:tenant-a:k3s", DryRun: true, MinAgeSeconds: &minAge}); err != nil {
		t.Fatal(err)
	}
	if executor.operation != KubernetesPruneUnusedImagesOperation || executor.request.Arguments["dryRun"] != true {
		t.Fatalf("prune request = %#v", executor.request.Arguments)
	}

	if _, err := service.GarbageCollectClusterRegistry(GuestStorageArgs{URI: "cluster:tenant-a:k3s", IncludeRegistry: true}); err != nil {
		t.Fatal(err)
	}
	if executor.operation != KubernetesGarbageCollectRegistryOperation {
		t.Fatalf("gc operation = %q", executor.operation)
	}

	if _, err := service.TrimGuestStorage(GuestStorageArgs{URI: "cluster:tenant-a:k3s"}); err != nil {
		t.Fatal(err)
	}
	if executor.operation != KubernetesTrimGuestStorageOperation {
		t.Fatalf("trim operation = %q", executor.operation)
	}
}
