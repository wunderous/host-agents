package main

import "testing"

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
