// Package vminfo holds the VM inventory wire shapes.
//
// A contract package, not a domain: no behaviour, no internal imports. The
// incus domain measures capacity and the host domain reports it as part of the
// host description, so neither can own the struct without the other importing
// it. VMInfo is here for the same reason: incus produces it, cluster and host
// read it.
package vminfo

import (
	"sort"
	"strings"
)

type VMInventoryCapacity struct {
	RunningVMCount            int   `json:"runningVmCount"`
	TotalVMCount              int   `json:"totalVmCount"`
	RunningVMCPULimitCores    int   `json:"runningVmCpuLimitCores"`
	TotalVMCPULimitCores      int   `json:"totalVmCpuLimitCores"`
	RunningVMMemoryLimitBytes int64 `json:"runningVmMemoryLimitBytes"`
	TotalVMMemoryLimitBytes   int64 `json:"totalVmMemoryLimitBytes"`
	RunningVMDiskLimitBytes   int64 `json:"runningVmDiskLimitBytes"`
	TotalVMDiskLimitBytes     int64 `json:"totalVmDiskLimitBytes"`
	RunningQEMUCount          int   `json:"runningQemuCount"`
	TotalQEMUCount            int   `json:"totalQemuCount"`
	RunningContainerCount     int   `json:"runningContainerCount"`
	TotalContainerCount       int   `json:"totalContainerCount"`
}

// RootDiskQuotaSupport reports whether a root disk size requested at
// provisioning time would be a real bound on this host. Enforcement is a
// property of the storage pool, so incus resolves it and the host description
// carries it: a caller reads the constraint here instead of discovering it
// from a rejected launch.
type RootDiskQuotaSupport struct {
	Pool     string `json:"pool"`
	Driver   string `json:"driver"`
	Enforced bool   `json:"enforced"`
	Reason   string `json:"reason,omitempty"`
}

type VMInfo struct {
	URI        string         `json:"uri"`
	Kind       string         `json:"kind"`
	Name       string         `json:"name"`
	Type       string         `json:"type,omitempty"`
	Status     string         `json:"status"`
	State      map[string]any `json:"state"`
	IPv4       []string       `json:"ipv4"`
	Release    string         `json:"release"`
	ProviderID string         `json:"providerId"`
	CPUs       *int           `json:"cpus,omitempty"`
	Memory     string         `json:"memory,omitempty"`
	Disk       string         `json:"disk,omitempty"`
	AgentReady *bool          `json:"agentReady,omitempty"`
	// HostId is the owning host agent identity (durable execution owner).
	HostId string `json:"hostId,omitempty"`
}

// NormalizeClusterIPv4 deduplicates a VM's addresses and orders them so the
// address a cluster is reachable on comes first. incus produces the raw list
// and cluster reads the ordered one, so neither can own this.
func NormalizeClusterIPv4(ips []string) []string {
	if len(ips) == 0 {
		return []string{}
	}
	seen := make(map[string]bool, len(ips))
	unique := make([]string, 0, len(ips))
	for _, ip := range ips {
		trimmed := strings.TrimSpace(ip)
		if trimmed == "" || seen[trimmed] {
			continue
		}
		seen[trimmed] = true
		unique = append(unique, trimmed)
	}
	sort.Slice(unique, func(i, j int) bool {
		scoreDiff := ipPreferenceScore(unique[i]) - ipPreferenceScore(unique[j])
		if scoreDiff != 0 {
			return scoreDiff < 0
		}
		return unique[i] < unique[j]
	})
	return unique
}

func ipPreferenceScore(ip string) int {
	normalized := strings.TrimSpace(strings.ToLower(ip))
	switch normalized {
	case "127.0.0.1", "::1", "localhost":
		return 100
	default:
		if strings.HasPrefix(normalized, "10.42.") || strings.HasPrefix(normalized, "10.43.") || strings.HasPrefix(normalized, "fd42:") {
			return 80
		}
		if strings.HasPrefix(normalized, "10.") || strings.HasPrefix(normalized, "192.168.") || strings.HasPrefix(normalized, "172.") {
			return 40
		}
		return 0
	}
}
