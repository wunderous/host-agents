package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestFindMeshIPAcceptsIPCommandCIDR(t *testing.T) {
	output := "2: enp5s0    inet 10.0.100.56/24 scope global enp5s0\n4: CloudflareWARP    inet 100.96.0.2/32 scope global CloudflareWARP"
	if got := findMeshIP(output); got != "100.96.0.2" {
		t.Fatalf("findMeshIP() = %q, want Mesh CIDR address", got)
	}
}

func TestFindMeshIPRejectsNonMeshAddress(t *testing.T) {
	if got := findMeshIP("2: enp5s0 inet 10.0.100.56/24 scope global enp5s0"); got != "" {
		t.Fatalf("findMeshIP() = %q, want empty result", got)
	}
}

func TestValidateMeshProbeResultRejectsUnreachablePeer(t *testing.T) {
	err := validateMeshProbeResult(map[string]any{
		"exitCode": 1,
		"stdout":   "Destination Host Unreachable",
	})
	if err == nil {
		t.Fatal("validateMeshProbeResult() accepted a failed ping")
	}
}

func TestValidateMeshProbeResultAcceptsReachablePeer(t *testing.T) {
	if err := validateMeshProbeResult(map[string]any{
		"exitCode": 0,
		"stdout":   "1 packets transmitted, 1 received",
	}); err != nil {
		t.Fatalf("validateMeshProbeResult() rejected a successful ping: %v", err)
	}
}

func TestEnsureMeshConnectivityEnablesMissingSettings(t *testing.T) {
	previousBase := os.Getenv("CLOUDFLARE_API_BASE")
	t.Cleanup(func() { _ = os.Setenv("CLOUDFLARE_API_BASE", previousBase) })

	var patch map[string]bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"success":true,"result":{"offramp_warp_enabled":false,"icmp_proxy_enabled":false}}`))
		case r.Method == http.MethodPatch:
			if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
				t.Fatalf("decode PATCH body: %v", err)
			}
			_, _ = w.Write([]byte(`{"success":true,"result":{"offramp_warp_enabled":true,"icmp_proxy_enabled":true}}`))
		default:
			http.Error(w, "unexpected request", http.StatusMethodNotAllowed)
		}
	}))
	t.Cleanup(server.Close)
	if err := os.Setenv("CLOUDFLARE_API_BASE", server.URL); err != nil {
		t.Fatalf("set CLOUDFLARE_API_BASE: %v", err)
	}

	if err := ensureMeshConnectivity(context.Background(), "account", "token"); err != nil {
		t.Fatalf("ensureMeshConnectivity() error = %v", err)
	}
	if len(patch) != 2 || !patch["offramp_warp_enabled"] || !patch["icmp_proxy_enabled"] {
		t.Fatalf("PATCH body = %#v, want both settings enabled", patch)
	}
}

func TestCloudflareMeshInstallScriptRequiresIPv6AndBoundsWARPSetup(t *testing.T) {
	script := cloudflareMeshInstallScript()
	for _, required := range []string{
		"set -euo pipefail",
		"/proc/net/if_inet6",
		"/proc/sys/net/ipv6/conf/all/disable_ipv6",
		"Cloudflare Mesh requires an IPv6-capable guest kernel",
		"exit 78",
		"timeout 45s warp-cli --accept-tos connector new",
		"timeout 45s warp-cli --accept-tos connect",
		"bounded wait",
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("Cloudflare Mesh install script missing %q", required)
		}
	}
}

func TestEndpointHTTPStatusAcceptableIncludesKubernetesAuthChallenge(t *testing.T) {
	for _, status := range []int{200, 301, 401, 403} {
		if !endpointHTTPStatusAcceptable(status) {
			t.Fatalf("endpointHTTPStatusAcceptable(%d) = false, want true", status)
		}
	}
	for _, status := range []int{404, 500, 502, 503} {
		if endpointHTTPStatusAcceptable(status) {
			t.Fatalf("endpointHTTPStatusAcceptable(%d) = true, want false", status)
		}
	}
}
