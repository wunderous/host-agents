package ops

import "testing"

func TestIncusVMConfigEnablesAutostart(t *testing.T) {
	config := incusVMConfig(4, "4GiB")
	if config["boot.autostart"] != "true" {
		t.Fatalf("boot.autostart = %q, want true", config["boot.autostart"])
	}
	if config["limits.cpu"] != "4" || config["limits.memory"] != "4GiB" {
		t.Fatalf("resource limits were not preserved: %#v", config)
	}
}
