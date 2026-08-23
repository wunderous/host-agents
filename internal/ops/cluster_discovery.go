package ops

import (
	"fmt"
	"strings"

	"github.com/wunderous/host-agents/internal/resourceid"
)

type ClusterListResult struct {
	Clusters []ClusterDetail `json:"clusters"`
}

type ClusterNode struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Roles   string `json:"roles"`
	Age     string `json:"age"`
	Version string `json:"version"`
}

type ClusterDetail struct {
	URI                    string        `json:"uri"`
	ID                     string        `json:"id"`
	Name                   string        `json:"name"`
	Status                 string        `json:"status"`
	Provider               string        `json:"provider,omitempty"`
	InfraProvider          string        `json:"infraProvider,omitempty"`
	Version                string        `json:"version,omitempty"`
	NodeCount              int           `json:"nodeCount,omitempty"`
	APIEndpoint            string        `json:"apiEndpoint,omitempty"`
	IPv4                   []string      `json:"ipv4,omitempty"`
	CPU                    *int          `json:"cpu,omitempty"`
	Memory                 string        `json:"memory,omitempty"`
	Disk                   string        `json:"disk,omitempty"`
	AgentReady             *bool         `json:"agentReady,omitempty"`
	K3sInstalled           *bool         `json:"k3sInstalled,omitempty"`
	Nodes                  []ClusterNode `json:"nodes"`
	Logs                   []string      `json:"logs"`
	NodeInventoryAvailable *bool         `json:"nodeInventoryAvailable,omitempty"`
	// VMName is the backing Incus instance (cluster resource id).
	VMName string `json:"vmName,omitempty"`
	// HostId is the owning host agent identity (durable execution owner).
	HostId string `json:"hostId,omitempty"`
}

func (s *HostOperationsService) ListClusters(fast bool) (ClusterListResult, error) {
	vms, err := s.ListVMs(fast)
	if err != nil {
		return ClusterListResult{}, err
	}
	clusters := make([]ClusterDetail, 0)
	for _, vm := range vms.VMs {
		if vm.K3sInstalled == nil || !*vm.K3sInstalled {
			continue
		}
		detail, err := s.buildClusterDetailFromVM(vm, fast, false)
		if err != nil {
			return ClusterListResult{}, err
		}
		clusters = append(clusters, detail)
	}
	return ClusterListResult{Clusters: clusters}, nil
}

func (s *HostOperationsService) GetClusterDetails(vmName string, fast bool) (ClusterDetail, error) {
	vm, err := s.GetVMInfo(vmName, fast)
	if err != nil {
		return ClusterDetail{}, err
	}
	return s.buildClusterDetailFromVM(vm, fast, false)
}

func (s *HostOperationsService) GetClusterRuntimeDetails(vmName string) (ClusterDetail, error) {
	vm, err := s.GetVMInfo(vmName, false)
	if err != nil {
		return ClusterDetail{}, err
	}
	return s.buildClusterDetailFromVM(vm, false, true)
}

func (s *HostOperationsService) buildClusterDetailFromVM(vm VMInfo, fast bool, runtime bool) (ClusterDetail, error) {
	detail := buildBaseClusterDetail(vm)
	if uri, err := resourceid.ClusterURI(s.tenantID, vm.Name); err == nil {
		detail.URI = uri.String()
		if s.resourceRegistry != nil {
			_ = s.RegisterResource(detail.URI, map[string]any{
				"providerInstanceName": vm.Name,
				"vmName":               vm.Name,
				"displayName":          vm.Name,
			})
		}
	}
	if runtime || (!fast && vm.K3sInstalled != nil && *vm.K3sInstalled && strings.EqualFold(vm.Status, "running")) {
		enriched, err := s.enrichClusterDetailRuntime(vm, detail)
		if err != nil {
			return detail, nil
		}
		return enriched, nil
	}
	return detail, nil
}

func buildBaseClusterDetail(vm VMInfo) ClusterDetail {
	ipv4 := normalizeClusterIpv4(vm.IPv4)
	k3sInstalled := vm.K3sInstalled
	status := "Unknown"
	stoppedOrUnknown := "Stopped"
	if !strings.EqualFold(vm.Status, "running") {
		stoppedOrUnknown = "Unknown"
	}

	if k3sInstalled != nil && *k3sInstalled {
		if strings.EqualFold(vm.Status, "running") {
			status = "Ready"
		} else {
			status = "Stopped"
		}
	} else if k3sInstalled != nil && !*k3sInstalled {
		if strings.EqualFold(vm.Status, "running") {
			status = "NotInstalled"
		} else {
			status = stoppedOrUnknown
		}
	} else if !strings.EqualFold(vm.Status, "running") {
		status = stoppedOrUnknown
	}

	// Runtime metrics are evidence, not optimistic placeholders. A stopped
	// guest cannot report its k3s version or node inventory, and a failed live
	// probe must remain visibly unavailable instead of looking authoritative.
	version := ""
	if k3sInstalled != nil && !*k3sInstalled {
		version = "Not installed"
	}

	nodeCount := 0

	detail := ClusterDetail{
		URI: vm.URI,
		// Cluster ids are typed entity identifiers on the Platform boundary.
		// Keep the provider namespace while using the shared identifier charset;
		// the old colon form was rejected by the MCP output schema.
		ID:            fmt.Sprintf("k3s-%s", vm.Name),
		Name:          vm.Name,
		Status:        status,
		Provider:      "k3s",
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
	}
	if k3sInstalled != nil {
		detail.K3sInstalled = k3sInstalled
	}
	return detail
}

func (s *HostOperationsService) enrichClusterDetailRuntime(vm VMInfo, detail ClusterDetail) (ClusterDetail, error) {
	if vm.K3sInstalled == nil || !*vm.K3sInstalled || !strings.EqualFold(vm.Status, "running") {
		return detail, nil
	}
	nodes, err := s.discoverClusterNodes(vm.Name)
	if err != nil {
		nodes = []ClusterNode{}
	}
	version := detail.Version
	if discovered, err := s.discoverK3sVersion(vm.Name); err == nil && discovered != "" {
		version = discovered
	}
	nodeCount := len(nodes)
	if nodeCount == 0 {
		nodeCount = detail.NodeCount
	}
	available := vm.K3sInstalled != nil && *vm.K3sInstalled && len(nodes) > 0
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
		K3sInstalled:           detail.K3sInstalled,
		Nodes:                  nodes,
		Logs:                   detail.Logs,
		NodeInventoryAvailable: boolPtr(available),
	}, nil
}

func (s *HostOperationsService) discoverClusterNodes(vmName string) ([]ClusterNode, error) {
	res, err := s.commandRunner([]string{
		"exec", vmName, "--", "k3s", "kubectl", "get", "nodes",
		"-o", "custom-columns=NAME:.metadata.name,STATUS:.status.conditions[-1].type,VERSION:.status.nodeInfo.kubeletVersion",
		"--no-headers",
	}, nil, defaultDiscoveryTimeout)
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("%s", firstNonEmpty(res.Stderr, res.Stdout, "kubectl get nodes failed"))
	}
	return parseClusterNodes(res.Stdout, vmName), nil
}

func (s *HostOperationsService) discoverK3sVersion(vmName string) (string, error) {
	res, err := s.commandRunner([]string{"exec", vmName, "--", "k3s", "--version"}, nil, defaultDiscoveryTimeout)
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", fmt.Errorf("%s", firstNonEmpty(res.Stderr, res.Stdout, "k3s --version failed"))
	}
	lines := strings.Split(strings.TrimSpace(res.Stdout), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) == "" {
		return "", nil
	}
	first := strings.TrimSpace(lines[0])
	lower := strings.ToLower(first)
	const prefix = "k3s version "
	if strings.HasPrefix(lower, prefix) {
		return strings.TrimSpace(first[len(prefix):]), nil
	}
	return first, nil
}

func parseClusterNodes(output string, fallbackName string) []ClusterNode {
	lines := strings.Split(output, "\n")
	nodes := make([]ClusterNode, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		parts := strings.Fields(trimmed)
		if len(parts) == 0 {
			continue
		}
		name := parts[0]
		if name == "" {
			name = fallbackName
		}
		status := "Unknown"
		if len(parts) > 1 {
			status = parts[1]
		}
		roles := "control-plane"
		age := ""
		version := ""
		for i := 2; i < len(parts); i++ {
			part := parts[i]
			if strings.HasPrefix(part, "v") && strings.Contains(part, ".") {
				version = part
			} else if roles == "control-plane" && !strings.HasPrefix(part, "v") {
				roles = part
			} else if age == "" {
				age = part
			}
		}
		if version == "" && len(parts) > 2 {
			version = parts[2]
		}
		nodes = append(nodes, ClusterNode{
			Name:    name,
			Status:  status,
			Roles:   roles,
			Age:     age,
			Version: version,
		})
	}
	return nodes
}

func buildClusterAPIEndpoint(ipv4 []string) string {
	normalized := normalizeClusterIpv4(ipv4)
	if len(normalized) == 0 {
		return "—"
	}
	return fmt.Sprintf("https://%s:6443", normalized[0])
}

func boolPtr(value bool) *bool {
	return &value
}
