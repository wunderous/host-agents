package config

import "testing"

func TestLoadDefaultsTenantID(t *testing.T) {
	t.Setenv("OPUTE_TENANT_ID", "")
	if got := Load().TenantID; got != "local" {
		t.Fatalf("tenant id = %q, want local", got)
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
