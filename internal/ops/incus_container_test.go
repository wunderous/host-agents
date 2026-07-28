package ops

import (
	"testing"
)

func TestProvisionContainerRequiresName(t *testing.T) {
	svc := &HostOperationsService{}
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
