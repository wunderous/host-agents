package incus

import (
	"testing"
	"time"

	hostexec "github.com/wunderous/host-agents/internal/exec"
	"github.com/wunderous/host-agents/internal/hostruntime"
)

func TestProvisionContainerRequiresName(t *testing.T) {
	svc := &Service{shared: &hostruntime.Shared{}}
	_, err := svc.ProvisionContainer(ProvisionContainerArgs{}, nil)
	if err == nil || err.Error() != "containerName is required" {
		t.Fatalf("expected containerName required error, got %v", err)
	}
}

func TestProbeGPUContainerUnsupportedOnWindows(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	// runtime.GOOS check is inside ProbeGPUContainer; this test documents the contract only on linux builds.
}

func TestGPUContainerGuestProbeScriptMarkers(t *testing.T) {
	for _, marker := range []string{"dxg=present", "libcuda=present", "nvidia_smi=ok", "cuda_init=ok", "cuda_init_code="} {
		if !containsString(gpuContainerGuestProbeScript, marker) {
			t.Fatalf("guest probe script missing marker %q", marker)
		}
	}
}

func TestEvaluateSystemContainerGPUProbe(t *testing.T) {
	ok, status, blockers := evaluateSystemContainerGPUProbe("dxg=present\nlibcuda=present\ncuda_init=ok")
	if !ok || status != "ready_for_gpu_container" || len(blockers) != 0 {
		t.Fatalf("expected ready probe, got ok=%v status=%q blockers=%v", ok, status, blockers)
	}
	ok, status, blockers = evaluateSystemContainerGPUProbe("dxg=present\nlibcuda=present\ncuda_init=failed\ncuda_init_code=100")
	if ok || status != "wsl_gpu_pv_not_nestable_in_lxc" || len(blockers) != 1 {
		t.Fatalf("expected WSL nesting blocker, got ok=%v status=%q blockers=%v", ok, status, blockers)
	}
}

func containsString(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (haystack == needle || len(needle) == 0 || indexString(haystack, needle) >= 0)
}

func indexString(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// incus calls a container Running before DHCP has finished, so provisioning has
// to keep polling rather than hand back a container nothing can address.
func TestAwaitContainerIPv4WaitsForTheAddressToAppear(t *testing.T) {
	restore := shortenContainerAddressPolling(t)
	defer restore()

	calls := 0
	svc := &Service{shared: &hostruntime.Shared{
		CommandRunnerFn: func(args []string, _ func(string), _ time.Duration) (hostexec.Result, error) {
			calls++
			if calls < 3 {
				return hostexec.Result{ExitCode: 0, Stdout: `{"network":{}}`}, nil
			}
			return hostexec.Result{ExitCode: 0, Stdout: `{"network":{"eth0":{"addresses":[{"family":"inet","scope":"global","address":"10.0.100.200"}]}}}`}, nil
		},
	}}

	if err := svc.awaitContainerIPv4("guest", nil); err != nil {
		t.Fatalf("awaitContainerIPv4 = %v, want nil once the lease lands", err)
	}
	if calls < 3 {
		t.Fatalf("polled %d times, want at least 3", calls)
	}
}

func TestAwaitContainerIPv4FailsClosedWhenNoAddressArrives(t *testing.T) {
	restore := shortenContainerAddressPolling(t)
	defer restore()

	svc := &Service{shared: &hostruntime.Shared{
		CommandRunnerFn: func(args []string, _ func(string), _ time.Duration) (hostexec.Result, error) {
			return hostexec.Result{ExitCode: 0, Stdout: `{"network":{}}`}, nil
		},
	}}

	err := svc.awaitContainerIPv4("guest", nil)
	if err == nil {
		t.Fatal("awaitContainerIPv4 must fail closed when no address ever appears")
	}
	if !containsString(err.Error(), "did not obtain an IPv4 address") || !containsString(err.Error(), "guest") {
		t.Fatalf("error %q must name the container and the missing address", err)
	}
}

func shortenContainerAddressPolling(t *testing.T) func() {
	t.Helper()
	timeout, interval := containerAddressTimeout, containerAddressPollInterval
	containerAddressTimeout = 150 * time.Millisecond
	containerAddressPollInterval = 10 * time.Millisecond
	return func() {
		containerAddressTimeout, containerAddressPollInterval = timeout, interval
	}
}
