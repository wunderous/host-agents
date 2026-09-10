package main

import (
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
