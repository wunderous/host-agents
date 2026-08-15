//go:build linux

package ops

import (
	"os"
	"strings"
	"testing"
)

func TestWindowsPowerShellExecutableHonorsExplicitServiceConfiguration(t *testing.T) {
	t.Setenv("OPUTE_WINDOWS_POWERSHELL_PATH", "/opt/opute/test-powershell.exe")
	if got := windowsPowerShellExecutable(); got != "/opt/opute/test-powershell.exe" {
		t.Fatalf("expected configured PowerShell path, got %q", got)
	}
}

func TestWindowsPowerShellExecutableFindsWSLPathWhenAvailable(t *testing.T) {
	os.Unsetenv("OPUTE_WINDOWS_POWERSHELL_PATH")
	got := windowsPowerShellExecutable()
	if got == "" {
		t.Fatal("expected a PowerShell command")
	}
	if strings.Contains(got, "\\") {
		t.Fatalf("expected a WSL-compatible command path, got %q", got)
	}
}

func TestNativeLinuxCloudflaredUnitUsesValidTunnelFlagOrdering(t *testing.T) {
	unit := nativeLinuxCloudflaredUnit(
		"host-1:llm.example.com",
		"/tmp/tunnel.env",
		"/tmp/cloudflared",
		"/tmp/tunnel.pid",
	)
	if !strings.Contains(unit, "ExecStart=/tmp/cloudflared tunnel --no-autoupdate --pidfile /tmp/tunnel.pid run") {
		t.Fatalf("unexpected cloudflared command:\n%s", unit)
	}
	if strings.Contains(unit, "tunnel run --no-autoupdate") {
		t.Fatalf("cloudflared global option appears after run:\n%s", unit)
	}
	if strings.Contains(unit, "Opute-managed") {
		t.Fatalf("host-agent unit description must remain product-neutral:\n%s", unit)
	}
}
