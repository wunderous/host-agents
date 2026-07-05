package ops

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestProbeHostExposureReadyWithShim(t *testing.T) {
	os.Setenv("OPUTE_E2E_EXPOSURE_SHIM", "linux")
	t.Cleanup(func() {
		os.Unsetenv("OPUTE_E2E_EXPOSURE_SHIM")
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("e2e-exposure-ok"))
	}))
	t.Cleanup(server.Close)

	svc := NewHostOperationsService(Options{ProviderID: "incus"})
	ensureOut, err := svc.EnsureCloudflaredTunnel(EnsureCloudflaredTunnelArgs{
		BindingID:   "binding-1",
		Hostname:    "e2e.example.com",
		LocalTarget: server.URL,
		RunToken:    "token",
	})
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if !ensureOut.ServiceRunning {
		t.Fatalf("expected service running")
	}

	probeOut, err := svc.ProbeHostExposure(ProbeHostExposureArgs{
		BindingID:   "binding-1",
		LocalTarget: server.URL,
	})
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if probeOut.Summary != "ready" {
		t.Fatalf("expected ready, got %s", probeOut.Summary)
	}
}
