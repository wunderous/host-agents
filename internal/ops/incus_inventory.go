package ops

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

type incusListItem struct {
	Name   string         `json:"name"`
	Status string         `json:"status"`
	Type   string         `json:"type"`
	State  map[string]any `json:"state,omitempty"`
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

func (s *HostOperationsService) GetVMInfo(vmName string, fast bool) (VMInfo, error) {
	vmName = strings.TrimSpace(vmName)
	if vmName == "" {
		return VMInfo{}, errors.New("vmName is required")
	}
	items, err := s.listIncusVirtualMachines()
	if err != nil {
		return VMInfo{}, err
	}
	for _, item := range items {
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
		if isIncusVirtualMachine(item.Type) {
			filtered = append(filtered, item)
		}
	}
	return filtered, nil
}

func (s *HostOperationsService) mapIncusListItem(item incusListItem, fast bool) (VMInfo, error) {
	status := mapIncusStatus(item.Status)
	agentReady := false
	if status == "running" && !fast {
		agentReady = s.probeIncusAgent(item.Name)
	}
	info := VMInfo{
		Name:       item.Name,
		Status:     status,
		State:      map[string]any{"incusStatus": item.Status},
		IPv4:       extractIPv4FromState(item.State),
		Release:    "unknown",
		ProviderID: "incus",
		AgentReady: agentReady,
	}
	if fast && len(info.IPv4) == 0 && status == "running" {
		if ips, err := s.readIncusInstanceIPv4(item.Name); err == nil {
			info.IPv4 = ips
		}
	}
	if !fast {
		if ips, err := s.readIncusInstanceIPv4(item.Name); err == nil {
			info.IPv4 = ips
		}
	}
	if info.IPv4 == nil {
		info.IPv4 = []string{}
	}
	return info, nil
}

func isIncusVirtualMachine(typeName string) bool {
	normalized := strings.ToLower(strings.TrimSpace(typeName))
	return normalized == "virtual-machine" || normalized == "virtual machine"
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
