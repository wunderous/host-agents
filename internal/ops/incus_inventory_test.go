package ops

import (
	"encoding/json"
	"testing"
)

func TestBuildVMInfoFromIncusListItemMapsResources(t *testing.T) {
	raw := `{
		"name": "opute-k3s-dogfood",
		"status": "Running",
		"type": "virtual-machine",
		"config": {
			"limits.cpu": "4",
			"limits.memory": "4GiB",
			"image.release": "jammy"
		},
		"devices": {
			"root": {
				"path": "/",
				"pool": "default",
				"size": "80GiB",
				"type": "disk"
			}
		},
		"state": {
			"network": {
				"enp5s0": {
					"addresses": [
						{"family": "inet", "address": "10.123.133.201", "scope": "global"}
					]
				}
			}
		}
	}`

	var item incusListItem
	if err := json.Unmarshal([]byte(raw), &item); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	ready := true
	info := buildVMInfoFromIncusListItem(item, &ready, nil)
	if info.Name != "opute-k3s-dogfood" {
		t.Fatalf("name = %q", info.Name)
	}
	if info.Kind != "vm" {
		t.Fatalf("kind = %q want vm", info.Kind)
	}
	if info.Status != "running" {
		t.Fatalf("status = %q", info.Status)
	}
	if info.CPUs == nil || *info.CPUs != 4 {
		t.Fatalf("cpus = %v want 4", info.CPUs)
	}
	if info.Memory != "4GiB" {
		t.Fatalf("memory = %q want 4GiB", info.Memory)
	}
	if info.Disk != "80GiB" {
		t.Fatalf("disk = %q want 80GiB", info.Disk)
	}
	if info.Release != "jammy" {
		t.Fatalf("release = %q want jammy", info.Release)
	}
	if len(info.IPv4) != 1 || info.IPv4[0] != "10.123.133.201" {
		t.Fatalf("ipv4 = %#v", info.IPv4)
	}
	if info.AgentReady == nil || !*info.AgentReady {
		t.Fatal("expected agentReady true")
	}
	if _, hasNetwork := info.State["network"]; hasNetwork {
		t.Fatal("list/get state must omit Incus network dump (chat/context bloat)")
	}
	if info.State["incusStatus"] != "Running" {
		t.Fatalf("incusStatus = %#v want Running", info.State["incusStatus"])
	}
}

func TestBuildVMInfoFromIncusListItemOmitsAgentReadyOnFastPath(t *testing.T) {
	item := incusListItem{
		Name:   "fast-vm",
		Status: "Running",
		Type:   "virtual-machine",
	}
	info := buildVMInfoFromIncusListItem(item, nil, nil)
	if info.AgentReady != nil {
		t.Fatalf("agentReady = %v want omitted", info.AgentReady)
	}
}

func TestNormalizeClusterIpv4PrefersRoutableAddress(t *testing.T) {
	got := normalizeClusterIpv4([]string{"10.42.0.10", "10.123.133.201"})
	if len(got) != 2 || got[0] != "10.123.133.201" {
		t.Fatalf("ipv4 order = %#v", got)
	}
}

func TestResolveK3sInstalledFromLabel(t *testing.T) {
	item := incusListItem{
		Config: map[string]string{oputeK3sInstalledLabel: "true"},
	}
	got := resolveK3sInstalledFromLabel(item)
	if got == nil || !*got {
		t.Fatalf("k3sInstalled = %#v want true", got)
	}
}

func TestBuildVMInfoFromIncusListItemUsesExpandedConfigAndDevices(t *testing.T) {
	item := incusListItem{
		Name:   "profile-only-vm",
		Status: "Stopped",
		Type:   "virtual-machine",
		ExpandedConfig: map[string]string{
			"limits.cpu":    "2",
			"limits.memory": "2GB",
			"image.release": "22.04",
		},
		ExpandedDevices: map[string]map[string]any{
			"root": {
				"type": "disk",
				"size": "12GiB",
			},
		},
	}

	info := buildVMInfoFromIncusListItem(item, nil, nil)
	if info.CPUs == nil || *info.CPUs != 2 {
		t.Fatalf("cpus = %v want 2", info.CPUs)
	}
	if info.Memory != "2GiB" {
		t.Fatalf("memory = %q want 2GiB", info.Memory)
	}
	if info.Disk != "12GiB" {
		t.Fatalf("disk = %q want 12GiB", info.Disk)
	}
	if info.Release != "22.04" {
		t.Fatalf("release = %q", info.Release)
	}
}

func TestBuildVMInfoFromIncusListItemFallsBackToStateUsage(t *testing.T) {
	item := incusListItem{
		Name:   "legacy-vm",
		Status: "Running",
		Type:   "virtual-machine",
		State: map[string]any{
			"memory": map[string]any{"usage": float64(2147483648)},
			"disk": map[string]any{
				"root": map[string]any{"usage": float64(1073741824)},
			},
		},
	}

	info := buildVMInfoFromIncusListItem(item, nil, nil)
	if info.CPUs != nil {
		t.Fatalf("cpus = %v want nil", info.CPUs)
	}
	if info.Memory != "2GiB" {
		t.Fatalf("memory = %q want 2GiB", info.Memory)
	}
	if info.Disk != "1GiB" {
		t.Fatalf("disk = %q want 1GiB", info.Disk)
	}
	if info.Release != "unknown" {
		t.Fatalf("release = %q want unknown", info.Release)
	}
}

func TestNormalizeIncusMemory(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"4GiB", "4GiB"},
		{"2GB", "2GiB"},
		{"512MB", "512MiB"},
		{"4G", "4GiB"},
		{"2048M", "2048MiB"},
	}
	for _, tc := range tests {
		got := normalizeIncusMemory(tc.in)
		if got != tc.want {
			t.Fatalf("normalizeIncusMemory(%q) = %q want %q", tc.in, got, tc.want)
		}
	}
}

func TestParseCapacityBytes(t *testing.T) {
	tests := []struct {
		in   string
		want int64
	}{
		{"4GiB", 4 << 30},
		{"2GB", 2_000_000_000},
		{"512MiB", 512 << 20},
		{"80GiB", 80 << 30},
		{"max", 0},
		{"", 0},
	}
	for _, tc := range tests {
		if got := parseCapacityBytes(tc.in); got != tc.want {
			t.Fatalf("parseCapacityBytes(%q) = %d want %d", tc.in, got, tc.want)
		}
	}
}
