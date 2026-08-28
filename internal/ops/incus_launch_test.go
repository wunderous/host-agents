package ops

import (
	"strings"
	"testing"
	"time"

	"github.com/wunderous/host-agents/internal/exec"
)

func TestIncusVMConfigEnablesAutostart(t *testing.T) {
	config := incusVMConfig(4, "4GiB")
	if config["boot.autostart"] != "true" {
		t.Fatalf("boot.autostart = %q, want true", config["boot.autostart"])
	}
	if config["limits.cpu"] != "4" || config["limits.memory"] != "4GiB" {
		t.Fatalf("resource limits were not preserved: %#v", config)
	}
}

func TestNormalizeProvisionInstanceType(t *testing.T) {
	if got := normalizeProvisionInstanceType(""); got != "container" {
		t.Fatalf("empty = %q, want container", got)
	}
	if got := normalizeProvisionInstanceType("virtual-machine"); got != "virtual-machine" {
		t.Fatalf("virtual-machine = %q", got)
	}
	if got := normalizeProvisionInstanceType("vm"); got != "virtual-machine" {
		t.Fatalf("vm = %q, want virtual-machine", got)
	}
	if got := normalizeProvisionInstanceType("container"); got != "container" {
		t.Fatalf("container = %q", got)
	}
}

func TestLaunchIncusContainerUsesDefaultResources(t *testing.T) {
	var launchArgs []string
	service := &HostOperationsService{}
	service.shared.CommandRunnerFn = func(args []string, _ func(string), _ time.Duration) (exec.Result, error) {
		if len(args) > 0 && args[0] == "launch" {
			launchArgs = append([]string(nil), args...)
		}
		return exec.Result{ExitCode: 0}, nil
	}

	if err := service.launchIncusContainer("default-resources", "images:ubuntu/24.04", "", 0, "", false, nil, time.Minute); err != nil {
		t.Fatalf("launchIncusContainer returned error: %v", err)
	}

	joined := strings.Join(launchArgs, " ")
	if !strings.Contains(joined, "limits.cpu=2") {
		t.Fatalf("launch args missing default CPU limit: %q", joined)
	}
	if !strings.Contains(joined, "limits.memory=2GiB") {
		t.Fatalf("launch args missing default memory limit: %q", joined)
	}
}
