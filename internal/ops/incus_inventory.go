package ops

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/wunderous/host-agents/internal/resourceid"
)

const oputeK3sInstalledLabel = "user.opute.k3s_installed"

type incusListItem struct {
	Name            string                    `json:"name"`
	Status          string                    `json:"status"`
	Type            string                    `json:"type"`
	Config          map[string]string         `json:"config,omitempty"`
	ExpandedConfig  map[string]string         `json:"expanded_config,omitempty"`
	Devices         map[string]map[string]any `json:"devices,omitempty"`
	ExpandedDevices map[string]map[string]any `json:"expanded_devices,omitempty"`
	State           map[string]any            `json:"state,omitempty"`
}

type incusInstanceState struct {
	Network map[string]struct {
		Addresses []struct {
			Address string `json:"address"`
			Family  string `json:"family"`
			Scope   string `json:"scope"`
		} `json:"addresses"`
	} `json:"network"`
}

func (s *HostOperationsService) ListVMs(fast bool) (VMListResult, error) {
	items, err := s.listIncusVirtualMachines()
	if err != nil {
		return VMListResult{}, err
	}
	vms := make([]VMInfo, 0, len(items))
	for _, item := range items {
		info, err := s.mapIncusListItem(item, fast)
		if err != nil {
			return VMListResult{}, err
		}
		vms = append(vms, info)
	}
	return VMListResult{VMs: vms}, nil
}

// VMInventoryCapacity describes declared Incus allocations and instance mix.
// It is intentionally based on Incus configuration, not guest-reported usage,
// so callers can see oversubscription before the guest becomes unhealthy.
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

func (s *HostOperationsService) VMInventoryCapacity() (VMInventoryCapacity, error) {
	items, err := s.listIncusVirtualMachines()
	if err != nil {
		return VMInventoryCapacity{}, err
	}
	var capacity VMInventoryCapacity
	for _, item := range items {
		capacity.TotalVMCount++
		running := strings.EqualFold(item.Status, "running")
		isQEMU := strings.EqualFold(mapIncusInstanceType(item.Type), "vm")
		if isQEMU {
			capacity.TotalQEMUCount++
		} else {
			capacity.TotalContainerCount++
		}
		if running {
			capacity.RunningVMCount++
			if isQEMU {
				capacity.RunningQEMUCount++
			} else {
				capacity.RunningContainerCount++
			}
		}
		if cpus := extractIncusCPUCount(item); cpus > 0 {
			capacity.TotalVMCPULimitCores += cpus
			if running {
				capacity.RunningVMCPULimitCores += cpus
			}
		}
		if memory := parseCapacityBytes(pickIncusConfigValue(item, "limits.memory")); memory > 0 {
			capacity.TotalVMMemoryLimitBytes += memory
			if running {
				capacity.RunningVMMemoryLimitBytes += memory
			}
		}
		if disk := parseCapacityBytes(pickIncusDiskLimit(item)); disk > 0 {
			capacity.TotalVMDiskLimitBytes += disk
			if running {
				capacity.RunningVMDiskLimitBytes += disk
			}
		}
	}
	return capacity, nil
}

// VMInventoryStats preserves the compact heartbeat helper used by older callers.
func (s *HostOperationsService) VMInventoryStats() (running int, total int, err error) {
	capacity, err := s.VMInventoryCapacity()
	if err != nil {
		return 0, 0, err
	}
	return capacity.RunningVMCount, capacity.TotalVMCount, nil
}

func pickIncusDiskLimit(item incusListItem) string {
	if value := strings.TrimSpace(pickIncusConfigValue(item, "limits.disk")); value != "" && value != "0B" && value != "-1" {
		return value
	}
	if size := extractIncusRootDeviceSize(item.Devices); size != "" {
		return size
	}
	return extractIncusRootDeviceSize(item.ExpandedDevices)
}

func parseCapacityBytes(value string) int64 {
	trimmed := strings.TrimSpace(strings.ToLower(value))
	if trimmed == "" || trimmed == "max" {
		return 0
	}
	units := []struct {
		suffix string
		factor float64
	}{
		{"tib", 1 << 40}, {"tb", 1e12}, {"ti", 1 << 40}, {"t", 1 << 40},
		{"gib", 1 << 30}, {"gb", 1e9}, {"gi", 1 << 30}, {"g", 1 << 30},
		{"mib", 1 << 20}, {"mb", 1e6}, {"mi", 1 << 20}, {"m", 1 << 20},
		{"kib", 1 << 10}, {"kb", 1e3}, {"ki", 1 << 10}, {"k", 1 << 10},
		{"b", 1},
	}
	factor := float64(1)
	number := trimmed
	for _, unit := range units {
		if strings.HasSuffix(trimmed, unit.suffix) {
			number = strings.TrimSpace(strings.TrimSuffix(trimmed, unit.suffix))
			factor = unit.factor
			break
		}
	}
	parsed, err := strconv.ParseFloat(number, 64)
	if err != nil || parsed <= 0 || parsed*factor > float64(^uint64(0)>>1) {
		return 0
	}
	return int64(parsed * factor)
}

func (s *HostOperationsService) GetVMInfo(vmName string, fast bool) (VMInfo, error) {
	vmName = strings.TrimSpace(vmName)
	if vmName == "" {
		return VMInfo{}, errors.New("vmName is required")
	}
	if err := s.assertIncusOwnership(vmName, "get_vm_info"); err != nil {
		return VMInfo{}, err
	}
	items, err := s.listIncusVirtualMachines()
	if err != nil {
		return VMInfo{}, err
	}
	for _, item := range items {
		if !s.ownedIncusItem(item) {
			continue
		}
		if item.Name == vmName {
			return s.mapIncusListItem(item, fast)
		}
	}
	return VMInfo{}, fmt.Errorf("VM '%s' not found", vmName)
}

func (s *HostOperationsService) listIncusVirtualMachines() ([]incusListItem, error) {
	res, err := s.commandRunner([]string{"list", "--format", "json"}, nil, defaultDiscoveryTimeout)
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("%s", firstNonEmpty(res.Stderr, res.Stdout, "incus list failed"))
	}
	var items []incusListItem
	if err := json.Unmarshal([]byte(res.Stdout), &items); err != nil {
		return nil, errors.New("incus list returned invalid JSON")
	}
	filtered := make([]incusListItem, 0, len(items))
	for _, item := range items {
		if item.Name == "" {
			continue
		}
		if isIncusVirtualMachine(item.Type) && s.ownedIncusItem(item) {
			filtered = append(filtered, item)
		}
	}
	return filtered, nil
}

func (s *HostOperationsService) mapIncusListItem(item incusListItem, fast bool) (VMInfo, error) {
	status := mapIncusStatus(item.Status)
	var agentReady *bool
	if status == "running" && !fast {
		ready := s.probeIncusAgent(item.Name)
		agentReady = &ready
	}
	k3sInstalled := resolveK3sInstalledFromLabel(item)
	if k3sInstalled == nil && status == "running" && !fast && agentReady != nil && *agentReady {
		installed := s.probeK3sInstalled(item.Name)
		k3sInstalled = &installed
		if installed {
			_ = s.setIncusInstanceConfig(item.Name, oputeK3sInstalledLabel, "true")
		}
	}
	info := buildVMInfoFromIncusListItem(item, agentReady, k3sInstalled)
	info.HostId = strings.TrimSpace(s.agentID)
	resourceType := resourceid.TypeContainer
	if strings.EqualFold(mapIncusInstanceType(item.Type), "vm") {
		resourceType = resourceid.TypeVM
	}
	if uri, uriErr := resourceid.New(resourceType, s.tenantID, item.Name); uriErr == nil {
		info.URI = uri.String()
		if s.resourceRegistry != nil {
			_ = s.RegisterResource(info.URI, map[string]any{
				"providerInstanceName": item.Name,
				"displayName":          info.Name,
				"instanceType":         info.Type,
			})
		}
	}
	if fast && len(info.IPv4) == 0 && status == "running" {
		if ips, err := s.readIncusInstanceIPv4(item.Name); err == nil {
			info.IPv4 = normalizeClusterIpv4(ips)
		}
	}
	if !fast {
		if ips, err := s.readIncusInstanceIPv4(item.Name); err == nil {
			info.IPv4 = normalizeClusterIpv4(ips)
		}
		if info.CPUs == nil && agentReady != nil && *agentReady {
			if cpus, err := s.readGuestCpuCount(item.Name); err == nil && cpus > 0 {
				info.CPUs = &cpus
			}
		}
	}
	if info.IPv4 == nil {
		info.IPv4 = []string{}
	}
	return info, nil
}

func resolveK3sInstalledFromLabel(item incusListItem) *bool {
	switch pickIncusConfigValue(item, oputeK3sInstalledLabel) {
	case "true":
		installed := true
		return &installed
	case "false":
		notInstalled := false
		return &notInstalled
	default:
		return nil
	}
}

func buildVMInfoFromIncusListItem(item incusListItem, agentReady *bool, k3sInstalled *bool) VMInfo {
	cpus := extractIncusCPUCount(item)
	memory := extractIncusMemory(item)
	disk := extractIncusDisk(item)
	info := VMInfo{
		Kind:         resourceid.TypeVM,
		Name:         item.Name,
		Type:         mapIncusInstanceType(item.Type),
		Status:       mapIncusStatus(item.Status),
		State:        map[string]any{"incusStatus": item.Status},
		IPv4:         normalizeClusterIpv4(extractIPv4FromState(item.State)),
		Release:      extractIncusRelease(item),
		ProviderID:   "incus",
		Memory:       memory,
		Disk:         disk,
		AgentReady:   agentReady,
		K3sInstalled: k3sInstalled,
	}
	if !strings.EqualFold(info.Type, "vm") {
		info.Kind = resourceid.TypeContainer
	}
	if cpus > 0 {
		info.CPUs = &cpus
	}
	// List/get inventory must stay chat- and UI-safe: top-level ipv4 already
	// carries addresses. Dumping Incus state.network (every veth + counters)
	// blows LLM context and stalls public chat list_vms turns on finishReason=length.
	return info
}

func pickIncusConfigValue(item incusListItem, key string) string {
	if item.Config != nil {
		if value := strings.TrimSpace(item.Config[key]); value != "" {
			return value
		}
	}
	if item.ExpandedConfig != nil {
		if value := strings.TrimSpace(item.ExpandedConfig[key]); value != "" {
			return value
		}
	}
	return ""
}

func extractIncusCPUCount(item incusListItem) int {
	raw := pickIncusConfigValue(item, "limits.cpu")
	if raw == "" {
		return 0
	}
	cpus, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || cpus <= 0 {
		return 0
	}
	return cpus
}

func extractIncusRelease(item incusListItem) string {
	if release := pickIncusConfigValue(item, "image.release"); release != "" {
		return release
	}
	return "unknown"
}

func normalizeIncusMemory(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	lower := strings.ToLower(trimmed)
	if strings.HasSuffix(lower, "gib") || strings.HasSuffix(lower, "mib") || strings.HasSuffix(lower, "tib") {
		return trimmed
	}
	if strings.HasSuffix(lower, "gb") {
		num := strings.TrimSpace(trimmed[:len(trimmed)-2])
		if parsed, err := strconv.ParseFloat(num, 64); err == nil {
			if parsed == float64(int64(parsed)) {
				return fmt.Sprintf("%dGiB", int64(parsed))
			}
			return fmt.Sprintf("%dMiB", int64(parsed*1024))
		}
	}
	if strings.HasSuffix(lower, "mb") {
		num := strings.TrimSpace(trimmed[:len(trimmed)-2])
		if parsed, err := strconv.ParseFloat(num, 64); err == nil {
			return fmt.Sprintf("%dMiB", int64(parsed))
		}
	}
	if matched := strings.TrimSuffix(lower, "g"); matched != lower {
		if parsed, err := strconv.ParseFloat(strings.TrimSpace(trimmed[:len(trimmed)-1]), 64); err == nil {
			if parsed == float64(int64(parsed)) {
				return fmt.Sprintf("%dGiB", int64(parsed))
			}
			return fmt.Sprintf("%dMiB", int64(parsed*1024))
		}
	}
	if matched := strings.TrimSuffix(lower, "m"); matched != lower {
		if parsed, err := strconv.ParseFloat(strings.TrimSpace(trimmed[:len(trimmed)-1]), 64); err == nil {
			return fmt.Sprintf("%dMiB", int64(parsed))
		}
	}
	return trimmed
}

func extractIncusMemory(item incusListItem) string {
	if memory := normalizeIncusMemory(pickIncusConfigValue(item, "limits.memory")); memory != "" {
		return memory
	}
	if item.State == nil {
		return ""
	}
	memoryState, ok := item.State["memory"].(map[string]any)
	if !ok {
		return ""
	}
	return formatIncusBytes(memoryState["usage"])
}

func extractIncusDisk(item incusListItem) string {
	if disk := strings.TrimSpace(pickIncusConfigValue(item, "limits.disk")); disk != "" {
		if !strings.HasPrefix(disk, "-") && !strings.EqualFold(disk, "0B") {
			return disk
		}
	}
	if size := extractIncusRootDeviceSize(item.Devices); size != "" {
		return size
	}
	if size := extractIncusRootDeviceSize(item.ExpandedDevices); size != "" {
		return size
	}
	if item.State == nil {
		return ""
	}
	diskState, ok := item.State["disk"].(map[string]any)
	if !ok || diskState == nil {
		return ""
	}
	root, ok := diskState["root"].(map[string]any)
	if !ok {
		return ""
	}
	return formatIncusBytes(root["usage"])
}

func extractIncusRootDeviceSize(devices map[string]map[string]any) string {
	if devices == nil {
		return ""
	}
	root, ok := devices["root"]
	if !ok {
		return ""
	}
	typeName, _ := root["type"].(string)
	if !strings.EqualFold(strings.TrimSpace(typeName), "disk") {
		return ""
	}
	switch size := root["size"].(type) {
	case string:
		s := strings.TrimSpace(size)
		if s == "" || strings.HasPrefix(s, "-") || strings.EqualFold(s, "0B") {
			return ""
		}
		return s
	case float64:
		if size > 0 {
			return formatIncusBytes(size)
		}
	case json.Number:
		if parsed, err := size.Float64(); err == nil && parsed > 0 {
			return formatIncusBytes(parsed)
		}
	}
	return ""
}

func formatIncusBytes(value any) string {
	var bytes float64
	switch typed := value.(type) {
	case float64:
		bytes = typed
	case int:
		bytes = float64(typed)
	case int64:
		bytes = float64(typed)
	case json.Number:
		parsed, err := typed.Float64()
		if err != nil {
			return ""
		}
		bytes = parsed
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		if err != nil {
			return ""
		}
		bytes = parsed
	default:
		return ""
	}
	if bytes <= 0 {
		return ""
	}
	const kib = 1024.0
	const mib = kib * 1024
	const gib = mib * 1024
	if bytes >= gib {
		asGiB := bytes / gib
		if asGiB == float64(int64(asGiB)) {
			return fmt.Sprintf("%dGiB", int64(asGiB))
		}
		return fmt.Sprintf("%dMiB", int64(bytes/mib))
	}
	if bytes >= mib {
		return fmt.Sprintf("%dMiB", int64(bytes/mib))
	}
	return fmt.Sprintf("%dB", int64(bytes))
}

func (s *HostOperationsService) readGuestCpuCount(vmName string) (int, error) {
	res, err := s.commandRunner([]string{"exec", vmName, "--", "nproc"}, nil, 15*time.Second)
	if err != nil {
		return 0, err
	}
	if res.ExitCode != 0 {
		return 0, fmt.Errorf("%s", firstNonEmpty(res.Stderr, res.Stdout, "nproc failed"))
	}
	cpus, err := strconv.Atoi(strings.TrimSpace(res.Stdout))
	if err != nil || cpus <= 0 {
		return 0, fmt.Errorf("invalid nproc output %q", strings.TrimSpace(res.Stdout))
	}
	return cpus, nil
}

func isIncusVirtualMachine(typeName string) bool {
	normalized := strings.ToLower(strings.TrimSpace(typeName))
	return normalized == "virtual-machine" || normalized == "virtual machine" || normalized == "container"
}

func mapIncusInstanceType(typeName string) string {
	normalized := strings.ToLower(strings.TrimSpace(typeName))
	switch normalized {
	case "container":
		return "container"
	case "virtual-machine", "virtual machine":
		return "vm"
	default:
		if normalized != "" {
			return normalized
		}
		return "vm"
	}
}

func mapIncusStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "running":
		return "running"
	case "stopped":
		return "stopped"
	case "frozen":
		return "frozen"
	default:
		return strings.ToLower(strings.TrimSpace(status))
	}
}

func getIpPreferenceScore(ip string) int {
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

func normalizeClusterIpv4(ips []string) []string {
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
		scoreDiff := getIpPreferenceScore(unique[i]) - getIpPreferenceScore(unique[j])
		if scoreDiff != 0 {
			return scoreDiff < 0
		}
		return unique[i] < unique[j]
	})
	return unique
}

func extractIPv4FromState(state map[string]any) []string {
	if state == nil {
		return []string{}
	}
	network, ok := state["network"].(map[string]any)
	if !ok {
		return []string{}
	}
	var ips []string
	for _, iface := range network {
		ifaceMap, ok := iface.(map[string]any)
		if !ok {
			continue
		}
		addresses, ok := ifaceMap["addresses"].([]any)
		if !ok {
			continue
		}
		for _, addr := range addresses {
			addrMap, ok := addr.(map[string]any)
			if !ok {
				continue
			}
			family, _ := addrMap["family"].(string)
			scope, _ := addrMap["scope"].(string)
			address, _ := addrMap["address"].(string)
			if family == "inet" && scope == "global" && address != "" {
				ips = append(ips, address)
			}
		}
	}
	return ips
}

func (s *HostOperationsService) readIncusInstanceIPv4(vmName string) ([]string, error) {
	if err := s.assertIncusOwnership(vmName, "read_instance_state"); err != nil {
		return nil, err
	}
	path := fmt.Sprintf("/1.0/instances/%s/state", urlPathEscape(vmName))
	res, err := s.commandRunner([]string{"query", path}, nil, defaultDiscoveryTimeout)
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("%s", firstNonEmpty(res.Stderr, res.Stdout, "incus query failed"))
	}
	var state incusInstanceState
	if err := json.Unmarshal([]byte(res.Stdout), &state); err != nil {
		return nil, err
	}
	var ips []string
	for _, iface := range state.Network {
		for _, addr := range iface.Addresses {
			if addr.Family == "inet" && addr.Scope == "global" && addr.Address != "" {
				ips = append(ips, addr.Address)
			}
		}
	}
	return ips, nil
}

func urlPathEscape(name string) string {
	return strings.ReplaceAll(name, "/", "%2F")
}

func (s *HostOperationsService) waitForIncusAgent(vmName string, timeout time.Duration, onData func(string)) error {
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		res, err := s.commandRunner([]string{"exec", vmName, "--", "true"}, onData, 30*time.Second)
		if err == nil && res.ExitCode == 0 {
			return nil
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("timed out waiting for Incus VM agent on %q", vmName)
}

func (s *HostOperationsService) probeIncusAgent(vmName string) bool {
	res, err := s.commandRunner([]string{"exec", vmName, "--", "true"}, nil, 15*time.Second)
	return err == nil && res.ExitCode == 0
}

func (s *HostOperationsService) probeK3sInstalled(vmName string) bool {
	script := "test -x /usr/local/bin/k3s && systemctl is-active k3s >/dev/null 2>&1 && printf installed"
	res, err := s.commandRunner([]string{"exec", vmName, "--", "/bin/sh", "-lc", script}, nil, 15*time.Second)
	return err == nil && res.ExitCode == 0 && strings.TrimSpace(res.Stdout) == "installed"
}

func (s *HostOperationsService) setIncusInstanceConfig(vmName, key, value string) error {
	if key != oputeIncusOwnerLabel {
		if err := s.assertIncusOwnership(vmName, "set_instance_config"); err != nil {
			return err
		}
	}
	res, err := s.commandRunner([]string{"config", "set", vmName, key, value}, nil, defaultDiscoveryTimeout)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("%s", firstNonEmpty(res.Stderr, res.Stdout, "incus config set failed"))
	}
	return nil
}
