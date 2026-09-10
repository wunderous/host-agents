package host

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	hostexec "github.com/wunderous/host-agents/internal/exec"
	"github.com/wunderous/host-agents/internal/hostruntime"
)

func TestResolvePublicMCPQuickPathsRestrictsOriginToLoopback(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	paths, localTarget, originBase, err := resolvePublicMCPQuickPaths("quick-42", "http://127.0.0.1:3014/mcp", "", "", "user", true)
	if err != nil {
		t.Fatal(err)
	}
	if localTarget != "http://127.0.0.1:3014/mcp" || originBase != "http://127.0.0.1:3014" {
		t.Fatalf("targets = %q, %q", localTarget, originBase)
	}
	if paths.serviceName != "opute-cloudflared-quick-quick-42.service" {
		t.Fatalf("service name = %q", paths.serviceName)
	}
	if !strings.HasPrefix(paths.artifactPath, filepath.Join(home, ".local", "share", "opute", "tunnels", "quick")) {
		t.Fatalf("artifact escaped quick tunnel root: %q", paths.artifactPath)
	}
	if !strings.HasSuffix(paths.logFile, filepath.Join("quick-42", "quick.log")) || !strings.HasSuffix(paths.stateFile, filepath.Join("quick-42", "endpoint.json")) {
		t.Fatalf("quick state paths = %q, %q", paths.logFile, paths.stateFile)
	}
}

func TestResolvePublicMCPQuickPathsRejectsRemoteAndNonMCPOrigins(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for name, localTarget := range map[string]string{
		"remote":     "http://10.0.0.8:3014/mcp",
		"wrong path": "http://127.0.0.1:3014/health",
		"query":      "http://127.0.0.1:3014/mcp?x=1",
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, _, err := resolvePublicMCPQuickPaths("quick-42", localTarget, "", "", "user", true); err == nil {
				t.Fatal("unsafe quick origin was accepted")
			}
		})
	}
}

func TestValidatePublicMCPQuickEndpointRequiresTryCloudflareHost(t *testing.T) {
	if got := validatePublicMCPQuickEndpoint("https://random.trycloudflare.com"); got != "https://random.trycloudflare.com/mcp" {
		t.Fatalf("normalized endpoint = %q", got)
	}
	for _, raw := range []string{
		"https://public.example",
		"http://random.trycloudflare.com",
		"https://random.trycloudflare.com/mcp",
		"https://random.trycloudflare.com?token=secret",
	} {
		if got := validatePublicMCPQuickEndpoint(raw); got != "" {
			t.Fatalf("endpoint %q was accepted as %q", raw, got)
		}
	}
}

func TestReadPublicMCPQuickEndpointIgnoresUntrustedURLs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "quick.log")
	if err := os.WriteFile(path, []byte("origin https://public.example https://abc.trycloudflare.com\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := readPublicMCPQuickEndpoint(path); got != "https://abc.trycloudflare.com/mcp" {
		t.Fatalf("endpoint = %q", got)
	}
}

func TestRenderPublicMCPQuickUnitUsesLoopbackAndOwnedLog(t *testing.T) {
	unit := renderPublicMCPQuickUnit(publicMCPQuickPaths{
		scope: "user", unitArtifact: "%h/.local/share/opute/tunnels/quick/binding/cloudflared", unitLogFile: "%h/.local/share/opute/tunnels/quick/binding/quick.log",
	}, "http://127.0.0.1:3014")
	if !strings.Contains(unit, "tunnel --no-autoupdate --url") || !strings.Contains(unit, "http://127.0.0.1:3014") || !strings.Contains(unit, "quick.log") || !strings.Contains(unit, ">>") {
		t.Fatalf("quick unit does not contain the expected command: %q", unit)
	}
	if strings.Contains(unit, "tunnelToken") || strings.Contains(unit, "OPUTE_CLOUDFLARED_TUNNEL_TOKEN") {
		t.Fatalf("quick unit contains a credential: %q", unit)
	}
}

func TestRemovePublicMCPQuickTunnelReportsForeignFileAndDoesNotDeleteIt(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	paths, _, _, err := resolvePublicMCPQuickPaths("quick-foreign", "", "", "", "user", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(paths.serviceFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.serviceFile, []byte("[Unit]\nDescription=foreign\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	service := &Service{shared: &hostruntime.Shared{HostCommandRunnerFn: func([]string, func(string), time.Duration) (hostexec.Result, error) {
		return hostexec.Result{ExitCode: 0}, nil
	}}}
	result, err := service.RemovePublicMcpQuickTunnel(context.Background(), RemovePublicMcpQuickTunnelArgs{BindingID: "quick-foreign", Confirm: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result["ready"] != false || result["status"] != "cleanup_failed" {
		t.Fatalf("cleanup result = %#v", result)
	}
	if _, err := os.Stat(paths.serviceFile); err != nil {
		t.Fatalf("foreign service file was removed: %v", err)
	}
}
