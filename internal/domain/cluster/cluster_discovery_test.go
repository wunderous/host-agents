package cluster

import (
	"testing"

	"github.com/wunderous/host-agents/internal/contract/vminfo"
)

func TestBuildBaseClusterDetailDoesNotInventRuntimeMetrics(t *testing.T) {
	detail := buildBaseClusterDetail(vminfo.VMInfo{
		Name:       "opute-dev-cnpg",
		Status:     "Stopped",
		ProviderID: "incus",
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
	if detail.ID != "opute-dev-cnpg" {
		t.Fatalf("id = %q, want provider-neutral cluster identity", detail.ID)
	}
}

func TestParseClusterNodesPreservesSchemaFieldsWhenAgeIsUnavailable(t *testing.T) {
	nodes := parseClusterNodes("node-a Ready v1.32.6+k3s1\n", "fallback")
	if len(nodes) != 1 {
		t.Fatalf("nodes = %#v, want one node", nodes)
	}
	if nodes[0].Age != "" {
		t.Fatalf("age = %q, want empty unavailable value", nodes[0].Age)
	}
	if nodes[0].Roles == "" || nodes[0].Version == "" {
		t.Fatalf("node = %#v, want schema-complete role and version fields", nodes[0])
	}
}
