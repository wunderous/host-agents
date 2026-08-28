package cluster

import (
	"encoding/json"
	"fmt"
	"strings"

	capabilitycontract "github.com/wunderous/host-agents/contracts/capability"

	"github.com/wunderous/host-agents/internal/contract/clusterinfo"
	"github.com/wunderous/host-agents/internal/contract/vminfo"
	"github.com/wunderous/host-agents/internal/resourceid"
)

// The cluster inventory shapes live in internal/contract/clusterinfo. Two
// domains speak them -- kubernetes produces the inventory from the provider
// plugin, cluster enriches and serves it -- so neither may own the type
// without making the other import it.
type (
	ClusterListResult = clusterinfo.ClusterListResult
	ClusterNode       = clusterinfo.ClusterNode
	ClusterDetail     = clusterinfo.ClusterDetail
)

func (s *Service) ListClusters(fast bool) (ClusterListResult, error) {
	result, err := s.deps.ListKubernetesClusters("")
	if err != nil {
		return ClusterListResult{}, err
	}
	if fast {
		return result, nil
	}
	for index := range result.Clusters {
		cluster := &result.Clusters[index]
		if enriched, enrichErr := s.enrichClusterDetailRuntimeByURI(cluster.URI, *cluster); enrichErr == nil {
			*cluster = enriched
		}
	}
	return result, nil
}

func (s *Service) GetClusterDetails(vmName string, fast bool) (ClusterDetail, error) {
	vm, err := s.deps.GetVMInfo(vmName, fast)
	if err != nil {
		return ClusterDetail{}, err
	}
	return s.buildClusterDetailFromVM(vm, fast, false)
}

func (s *Service) GetClusterRuntimeDetails(vmName string) (ClusterDetail, error) {
	vm, err := s.deps.GetVMInfo(vmName, false)
	if err != nil {
		return ClusterDetail{}, err
	}
	return s.buildClusterDetailFromVM(vm, false, true)
}

func (s *Service) buildClusterDetailFromVM(vm vminfo.VMInfo, fast bool, runtime bool) (ClusterDetail, error) {
	detail := buildBaseClusterDetail(vm)
	if uri, err := resourceid.ClusterURI(s.shared.TenantID, vm.Name); err == nil {
		detail.URI = uri.String()
		if s.shared.ResourceRegistry != nil {
			instanceType := strings.TrimSpace(vm.Type)
			if instanceType == "virtual-machine" {
				instanceType = "vm"
			}
			_ = s.shared.RegisterResource(detail.URI, map[string]any{
				"providerInstanceName": vm.Name,
				"vmName":               vm.Name,
				"displayName":          vm.Name,
				"instanceType":         instanceType,
			})
		}
	}
	if runtime || (!fast && strings.EqualFold(vm.Status, "running")) {
		enriched, err := s.enrichClusterDetailRuntimeByURI(detail.URI, detail)
		if err != nil {
			return detail, nil
		}
		return enriched, nil
	}
	return detail, nil
}

func buildBaseClusterDetail(vm vminfo.VMInfo) ClusterDetail {
	ipv4 := vminfo.NormalizeClusterIPv4(vm.IPv4)
	status := "Unknown"
	if strings.EqualFold(vm.Status, "running") {
		status = "Running"
	} else if strings.EqualFold(vm.Status, "stopped") {
		status = "Stopped"
	}

	// Runtime metrics are evidence, not optimistic placeholders. A stopped
	// guest cannot report its k3s version or node inventory, and a failed live
	// probe must remain visibly unavailable instead of looking authoritative.
	version := ""
	nodeCount := 0

	detail := ClusterDetail{
		URI: vm.URI,
		// Cluster ids are typed entity identifiers on the Platform boundary.
		// Keep the provider namespace while using the shared identifier charset;
		// the old colon form was rejected by the MCP output schema.
		ID:            vm.Name,
		Name:          vm.Name,
		Status:        status,
		Provider:      "kubernetes",
		InfraProvider: vm.ProviderID,
		Version:       version,
		NodeCount:     nodeCount,
		APIEndpoint:   buildClusterAPIEndpoint(ipv4),
		IPv4:          ipv4,
		CPU:           vm.CPUs,
		Memory:        vm.Memory,
		Disk:          vm.Disk,
		AgentReady:    vm.AgentReady,
		Nodes:         []ClusterNode{},
		Logs:          []string{},
		VMName:        vm.Name,
		HostId:        strings.TrimSpace(vm.HostId),
		InstanceType:  strings.TrimSpace(vm.Type),
	}
	return detail
}

func (s *Service) enrichClusterDetailRuntimeByURI(targetURI string, detail ClusterDetail) (ClusterDetail, error) {
	if strings.TrimSpace(targetURI) == "" {
		return detail, fmt.Errorf("canonical cluster URI is required for runtime discovery")
	}
	out, delegated, err := s.deps.ExecuteKubernetesProvider(capabilitycontract.KubernetesGetClusterInfoOperation, targetURI, nil)
	if !delegated {
		return detail, fmt.Errorf("Kubernetes provider is required for runtime discovery")
	}
	if err != nil {
		return detail, err
	}
	version, _ := out["version"].(string)
	if strings.TrimSpace(version) == "" {
		version = detail.Version
	}
	nodes := make([]ClusterNode, 0)
	if encoded, marshalErr := json.Marshal(out["nodes"]); marshalErr == nil {
		_ = json.Unmarshal(encoded, &nodes)
	}
	nodeCount := len(nodes)
	if nodeCount == 0 {
		nodeCount = detail.NodeCount
	}
	available := len(nodes) > 0
	return ClusterDetail{
		URI:                    detail.URI,
		ID:                     detail.ID,
		Name:                   detail.Name,
		Status:                 "Ready",
		Provider:               detail.Provider,
		InfraProvider:          detail.InfraProvider,
		Version:                version,
		NodeCount:              nodeCount,
		APIEndpoint:            detail.APIEndpoint,
		IPv4:                   detail.IPv4,
		CPU:                    detail.CPU,
		Memory:                 detail.Memory,
		Disk:                   detail.Disk,
		AgentReady:             detail.AgentReady,
		Nodes:                  nodes,
		Logs:                   detail.Logs,
		NodeInventoryAvailable: boolPtr(available),
		InstanceType:           detail.InstanceType,
	}, nil
}

// parseClusterNodes only interprets provider-returned inventory. The provider
// owns concrete control-plane commands; Host Agent keeps this response shaping
// neutral so callers receive the established cluster detail contract.
func parseClusterNodes(output, fallbackName string) []ClusterNode {
	lines := strings.Split(output, "\n")
	nodes := make([]ClusterNode, 0, len(lines))
	for _, line := range lines {
		parts := strings.Fields(strings.TrimSpace(line))
		if len(parts) == 0 {
			continue
		}
		name := parts[0]
		if name == "" {
			name = fallbackName
		}
		node := ClusterNode{Name: name, Status: "Unknown", Roles: "control-plane"}
		if len(parts) > 1 {
			node.Status = parts[1]
		}
		if len(parts) > 2 {
			node.Version = parts[2]
		}
		nodes = append(nodes, node)
	}
	return nodes
}

func buildClusterAPIEndpoint(ipv4 []string) string {
	normalized := vminfo.NormalizeClusterIPv4(ipv4)
	if len(normalized) == 0 {
		return "—"
	}
	return fmt.Sprintf("https://%s:6443", normalized[0])
}

func boolPtr(value bool) *bool {
	return &value
}
