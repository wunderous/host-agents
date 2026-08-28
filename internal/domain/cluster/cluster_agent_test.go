package cluster

import (
	"strings"
	"testing"
)

func TestNormalizeClusterAgentArch(t *testing.T) {
	if got := normalizeClusterAgentArch("x86_64"); got != clusterAgentArchX64 {
		t.Fatalf("x86_64: got %q", got)
	}
	if got := normalizeClusterAgentArch("aarch64"); got != clusterAgentArchARM64 {
		t.Fatalf("aarch64: got %q", got)
	}
}

func TestRenderClusterAgentInstallScriptDownloadsArtifact(t *testing.T) {
	script := renderClusterAgentInstallScript(
		"http://172.23.118.1:9093",
		clusterAgentArchX64,
		[]byte(`{"bridgeUrl":"http://172.23.118.1:9093"}`),
	)
	for _, want := range []string{
		"curl -sfL --max-time 180",
		"/artifacts/cluster-agent/x64.gz",
		clusterAgentBinaryPath,
		clusterAgentConfigPath,
		"systemctl enable --now " + clusterAgentServiceName,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("script missing %q:\n%s", want, script)
		}
	}
	if strings.Contains(script, "sleep infinity") {
		t.Fatalf("script must not install placeholder binary")
	}
}

func TestRenderHostNativeClusterAgentInstallScriptUsesPrivilegeBoundary(t *testing.T) {
	script := renderHostNativeClusterAgentInstallScript(
		"http://172.23.118.1:9093",
		clusterAgentArchX64,
		[]byte("{\"clusterId\":\"cluster-1\"}"),
	)
	for _, want := range []string{
		"sudo -n bash -lc",
		"passwordless sudo is required",
		"/artifacts/cluster-agent/x64.gz",
		"systemctl enable --now " + clusterAgentServiceName,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("host-native script missing %q:\n%s", want, script)
		}
	}
}

func TestIsLoopbackBridgeURL(t *testing.T) {
	if !isLoopbackBridgeURL("http://127.0.0.1:9093") {
		t.Fatal("expected loopback")
	}
	if isLoopbackBridgeURL("http://172.23.118.1:9093") {
		t.Fatal("expected non-loopback")
	}
}
