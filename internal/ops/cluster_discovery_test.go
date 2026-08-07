package ops

import "testing"

func TestBuildBaseClusterDetailDoesNotInventRuntimeMetrics(t *testing.T) {
	installed := true
	detail := buildBaseClusterDetail(VMInfo{
		Name:         "opute-dev-cnpg",
		Status:       "Stopped",
		ProviderID:   "incus",
		K3sInstalled: &installed,
	})

	if detail.Status != "Stopped" {
		t.Fatalf("status = %q, want Stopped", detail.Status)
	}
	if detail.Version != "" {
		t.Fatalf("version = %q, want empty until runtime evidence exists", detail.Version)
	}
	if detail.NodeCount != 0 {
		t.Fatalf("nodeCount = %d, want 0 until node evidence exists", detail.NodeCount)
	}
	if detail.APIEndpoint != "—" {
		t.Fatalf("apiEndpoint = %q, want unavailable marker", detail.APIEndpoint)
	}
	if detail.VMName != "opute-dev-cnpg" {
		t.Fatalf("vmName = %q, want backing VM identity", detail.VMName)
	}
}
