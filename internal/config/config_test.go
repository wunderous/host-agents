package config

import "testing"

func TestLoadDefaultsTenantID(t *testing.T) {
	t.Setenv("OPUTE_TENANT_ID", "")
	if got := Load().TenantID; got != "local" {
		t.Fatalf("tenant id = %q, want local", got)
	}
}

func TestLoadDefaultsResourceMemoryCapacityToElevenGiB(t *testing.T) {
	t.Setenv("OPUTE_HOST_RESOURCE_MEMORY_CAPACITY_BYTES", "")
	if got := Load().HostResourceMemoryCapacity; got != 11<<30 {
		t.Fatalf("host resource memory capacity = %d, want %d", got, 11<<30)
	}
}

func TestLoadPrefixToolNamesDefaultOff(t *testing.T) {
	t.Setenv("OPUTE_MCP_PREFIX_TOOL_NAMES", "")
	if Load().PrefixToolNames {
		t.Fatal("prefix tool names must default off")
	}
	t.Setenv("OPUTE_MCP_PREFIX_TOOL_NAMES", "true")
	if !Load().PrefixToolNames {
		t.Fatal("OPUTE_MCP_PREFIX_TOOL_NAMES=true must enable prefixing")
	}
}

func TestLoadPublicMCPDisablesLocalhostProtectionOnlyWhenOptedIn(t *testing.T) {
	t.Setenv("OPUTE_MCP_DISABLE_LOCALHOST_PROTECTION", "")
	if Load().DisableLocalhostProtection {
		t.Fatal("localhost protection must remain enabled by default")
	}
	t.Setenv("OPUTE_MCP_DISABLE_LOCALHOST_PROTECTION", "true")
	if !Load().DisableLocalhostProtection {
		t.Fatal("OPUTE_MCP_DISABLE_LOCALHOST_PROTECTION=true must enable the public exposure setting")
	}
}

func TestTenantIDValidation(t *testing.T) {
	for _, value := range []string{"tenant-a", "a1", "local"} {
		if err := validateTenantID(value); err != nil {
			t.Errorf("validateTenantID(%q): %v", value, err)
		}
	}
	for _, value := range []string{"", "Tenant-A", "-tenant", "tenant_1", "tenant with spaces"} {
		if err := validateTenantID(value); err == nil {
			t.Errorf("validateTenantID(%q) unexpectedly succeeded", value)
		}
	}
}
