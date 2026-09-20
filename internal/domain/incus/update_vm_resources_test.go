package incus

import (
	"strings"
	"testing"
	"time"

	"github.com/wunderous/host-agents/internal/hostruntime"

	"github.com/wunderous/host-agents/internal/exec"
)

func TestUpdateVMResourcesRequiresVMName(t *testing.T) {
	svc := &Service{shared: &hostruntime.Shared{}}
	_, err := svc.UpdateVMResources(UpdateVMResourcesArgs{CPUs: 3, Memory: "3GiB"}, nil)
	if err == nil || !strings.Contains(err.Error(), "vmName is required") {
		t.Fatalf("expected vmName gate, got %v", err)
	}
}

func TestUpdateVMResourcesRequiresAtLeastOneLimit(t *testing.T) {
	svc := &Service{shared: &hostruntime.Shared{}}
	_, err := svc.UpdateVMResources(UpdateVMResourcesArgs{VMName: "vm-a"}, nil)
	if err == nil || !strings.Contains(err.Error(), "at least one of cpus, memory, or disk") {
		t.Fatalf("expected limit gate, got %v", err)
	}
}

func TestUpdateVMResourcesDiskOnlyIsAdmitted(t *testing.T) {
	svc := &Service{shared: &hostruntime.Shared{}}
	svc.shared.CommandRunnerFn = func(args []string, _ func(string), _ time.Duration) (exec.Result, error) {
		return exec.Result{ExitCode: 1, Stderr: "instance query stub"}, nil
	}
	_, err := svc.UpdateVMResources(UpdateVMResourcesArgs{VMName: "vm-a", Disk: "20GiB"}, nil)
	if err == nil {
		t.Fatal("expected instance query to run rather than the cpu/memory gate")
	}
	if strings.Contains(err.Error(), "at least one of cpus, memory, or disk") {
		t.Fatalf("disk-only update must be a valid request, got %v", err)
	}
	if !strings.Contains(err.Error(), "instance query stub") {
		t.Fatalf("disk-only update must reach the instance root device, got %v", err)
	}
}

func TestRefuseRootDiskShrink(t *testing.T) {
	if err := refuseRootDiskShrink("10GiB", "20GiB"); err == nil || !strings.Contains(err.Error(), "grow-only") {
		t.Fatalf("expected shrink refusal, got %v", err)
	}
	if err := refuseRootDiskShrink("20GiB", "20Gi"); err != nil {
		t.Fatalf("equal GiB/Gi sizes must be allowed: %v", err)
	}
	if err := refuseRootDiskShrink("30GiB", "20GiB"); err != nil {
		t.Fatalf("grow must be allowed: %v", err)
	}
	if err := refuseRootDiskShrink("10GiB", ""); err != nil {
		t.Fatalf("first bound on an unbounded guest must be allowed: %v", err)
	}
}

func TestUpdateVMResourcesAppliesDiskOnEnforcingPool(t *testing.T) {
	withProcMounts(t, "/dev/sda1 / ext4 rw,relatime 0 0\n")
	var patched string
	service := stubQuotaService(t, "zfs", "")
	service.shared.CommandRunnerFn = func(args []string, _ func(string), _ time.Duration) (exec.Result, error) {
		joined := strings.Join(args, " ")
		switch {
		case strings.Contains(joined, "/1.0/instances/vm-a") && !strings.Contains(joined, "-X"):
			return exec.Result{ExitCode: 0, Stdout: `{"devices":{"root":{"type":"disk","path":"/","pool":"default","size":"10GiB"}}}`}, nil
		case strings.Contains(joined, "/1.0/storage-pools/default") && !strings.HasSuffix(joined, "/1.0/storage-pools"):
			return exec.Result{ExitCode: 0, Stdout: `{"name":"default","driver":"zfs","config":{}}`}, nil
		case strings.Contains(joined, "-X PATCH"):
			patched = joined
			return exec.Result{ExitCode: 0}, nil
		}
		return exec.Result{ExitCode: 0}, nil
	}

	out, err := service.UpdateVMResources(UpdateVMResourcesArgs{VMName: "vm-a", Disk: "20GiB"}, nil)
	if err != nil {
		t.Fatalf("update disk: %v", err)
	}
	if out["disk"] != "20GiB" || out["enforced"] != "true" || out["driver"] != "zfs" {
		t.Fatalf("applied quota not reported: %#v", out)
	}
	if !strings.Contains(patched, "20GiB") {
		t.Fatalf("expected a root-disk PATCH, got %q", patched)
	}
}

func TestUpdateVMResourcesRefusesUnenforceableDisk(t *testing.T) {
	withProcMounts(t, "/dev/sda1 / ext4 rw,relatime 0 0\n")
	service := stubQuotaService(t, "dir", "/var/lib/incus/storage-pools/default")
	service.shared.CommandRunnerFn = func(args []string, _ func(string), _ time.Duration) (exec.Result, error) {
		joined := strings.Join(args, " ")
		switch {
		case strings.Contains(joined, "/1.0/instances/vm-a"):
			return exec.Result{ExitCode: 0, Stdout: `{"expanded_devices":{"root":{"type":"disk","path":"/","pool":"default"}}}`}, nil
		case strings.HasSuffix(joined, "/1.0/storage-pools"):
			return exec.Result{ExitCode: 0, Stdout: `["/1.0/storage-pools/default"]`}, nil
		case strings.Contains(joined, "/1.0/storage-pools/default"):
			return exec.Result{ExitCode: 0, Stdout: `{"name":"default","driver":"dir","config":{"source":"/var/lib/incus/storage-pools/default"}}`}, nil
		}
		return exec.Result{ExitCode: 0}, nil
	}

	_, err := service.UpdateVMResources(UpdateVMResourcesArgs{VMName: "vm-a", Disk: "20GiB"}, nil)
	if err == nil || !strings.Contains(err.Error(), "cannot be enforced") {
		t.Fatalf("expected fail-closed admission, got %v", err)
	}
}

func TestUpdateVMResourcesUsesInstancePoolNotProfileDefault(t *testing.T) {
	withProcMounts(t, "/dev/sda1 / ext4 rw,relatime 0 0\n")
	var admittedPool string
	service := &Service{shared: &hostruntime.Shared{}}
	service.shared.CommandRunnerFn = func(args []string, _ func(string), _ time.Duration) (exec.Result, error) {
		joined := strings.Join(args, " ")
		switch {
		case strings.Contains(joined, "/1.0/instances/vm-a") && !strings.Contains(joined, "-X"):
			return exec.Result{ExitCode: 0, Stdout: `{"devices":{"root":{"type":"disk","path":"/","pool":"fast","size":"10GiB"}}}`}, nil
		case strings.Contains(joined, "/1.0/profiles/default"):
			return exec.Result{ExitCode: 0, Stdout: `{"devices":{"root":{"type":"disk","pool":"default"}}}`}, nil
		case strings.Contains(joined, "/1.0/storage-pools/fast"):
			admittedPool = "fast"
			return exec.Result{ExitCode: 0, Stdout: `{"name":"fast","driver":"zfs","config":{}}`}, nil
		case strings.Contains(joined, "/1.0/storage-pools/default"):
			t.Fatal("admission must not fall back to the profile default pool")
		case strings.Contains(joined, "-X PATCH"):
			return exec.Result{ExitCode: 0}, nil
		}
		return exec.Result{ExitCode: 0}, nil
	}

	out, err := service.UpdateVMResources(UpdateVMResourcesArgs{VMName: "vm-a", Disk: "20GiB"}, nil)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if admittedPool != "fast" || out["pool"] != "fast" {
		t.Fatalf("instance pool was not admitted: pool=%q out=%#v", admittedPool, out)
	}
}
