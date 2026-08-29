package incus

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/wunderous/host-agents/internal/hostruntime"

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
	service := &Service{shared: &hostruntime.Shared{}}
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

func TestProvisionVMReusesExistingVMWithoutCreateOrResize(t *testing.T) {
	var calls [][]string
	service := &Service{shared: &hostruntime.Shared{}}
	service.shared.CommandRunnerFn = func(args []string, _ func(string), _ time.Duration) (exec.Result, error) {
		calls = append(calls, append([]string(nil), args...))
		switch {
		case len(args) >= 3 && args[0] == "list" && args[1] == "existing-vm":
			return exec.Result{ExitCode: 0, Stdout: "existing-vm\n"}, nil
		case len(args) >= 2 && args[0] == "query" && strings.Contains(args[1], "/1.0/instances/existing-vm"):
			return exec.Result{ExitCode: 0, Stdout: `{"type":"virtual-machine"}`}, nil
		default:
			return exec.Result{ExitCode: 0}, nil
		}
	}

	result, err := service.ProvisionVM(ProvisionVMArgs{VMName: "existing-vm"}, nil)
	if err != nil {
		t.Fatalf("ProvisionVM returned error: %v", err)
	}
	if result.InstanceType != "virtual-machine" || result.Status != "running" {
		t.Fatalf("result = %#v, want a running virtual-machine", result)
	}
	for _, call := range calls {
		joined := strings.Join(call, " ")
		if strings.Contains(joined, `"POST"`) || strings.Contains(joined, `"PATCH"`) || strings.Contains(joined, " launch ") {
			t.Fatalf("existing VM provisioning attempted a create/resize: %v", call)
		}
	}
}

func TestProvisionVMRejectsExplicitRuntimeKindMismatch(t *testing.T) {
	service := &Service{shared: &hostruntime.Shared{}}
	service.shared.CommandRunnerFn = func(args []string, _ func(string), _ time.Duration) (exec.Result, error) {
		switch {
		case len(args) >= 3 && args[0] == "list":
			return exec.Result{ExitCode: 0, Stdout: "existing-container\n"}, nil
		case len(args) >= 2 && args[0] == "query" && strings.Contains(args[1], "/1.0/instances/"):
			return exec.Result{ExitCode: 0, Stdout: `{"type":"container"}`}, nil
		default:
			return exec.Result{ExitCode: 0}, nil
		}
	}

	_, err := service.ProvisionVM(ProvisionVMArgs{VMName: "existing-container", InstanceType: "virtual-machine"}, nil)
	var kindErr *IncusRuntimeKindError
	if !errors.As(err, &kindErr) || kindErr.Code != "incus_runtime_kind_mismatch" {
		t.Fatalf("error = %T %v, want typed runtime-kind mismatch", err, err)
	}
}

func TestProvisionContainerRejectsExistingVM(t *testing.T) {
	service := &Service{shared: &hostruntime.Shared{}}
	service.shared.CommandRunnerFn = func(args []string, _ func(string), _ time.Duration) (exec.Result, error) {
		switch {
		case len(args) >= 3 && args[0] == "list":
			return exec.Result{ExitCode: 0, Stdout: "existing-vm\n"}, nil
		case len(args) >= 2 && args[0] == "query" && strings.Contains(args[1], "/1.0/instances/"):
			return exec.Result{ExitCode: 0, Stdout: `{"type":"virtual-machine"}`}, nil
		default:
			return exec.Result{ExitCode: 0}, nil
		}
	}

	_, err := service.ProvisionContainer(ProvisionContainerArgs{ContainerName: "existing-vm"}, nil)
	var kindErr *IncusRuntimeKindError
	if !errors.As(err, &kindErr) || kindErr.Code != "incus_runtime_kind_mismatch" {
		t.Fatalf("error = %T %v, want typed runtime-kind mismatch", err, err)
	}
}
