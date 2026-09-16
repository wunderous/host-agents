package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
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

// runMeshReadinessProbe executes the install script's readiness predicates
// against stubbed `warp-cli` and `ip` binaries. The predicates are the part of
// the script that decides whether a Mesh node came up, so they are worth
// exercising rather than grepping for: the defect they replace was two
// substring tests that both matched the failure case.
func runMeshReadinessProbe(t *testing.T, warpStatus, ipAddrOutput string) (connected bool, address string) {
	t.Helper()
	return runMeshReadinessProbeOnStream(t, warpStatus, "", ipAddrOutput)
}

// runMeshReadinessProbeOnStream stubs `warp-cli` reporting on the given stream
// (`>&2 ` for stderr, empty for stdout) so readiness can be exercised against
// every shape the packaged client has been observed to produce, including the
// silent one it produces under a non-interactive exec.
func runMeshReadinessProbeOnStream(t *testing.T, warpStatus, redirect, ipAddrOutput string) (connected bool, address string) {
	t.Helper()
	dir := t.TempDir()
	for name, output := range map[string]string{"warp-cli": warpStatus, "ip": ipAddrOutput} {
		stream := ""
		if name == "warp-cli" {
			stream = redirect
		}
		stub := "#!/bin/sh\ncat " + stream + "<<'STUB_EOF'\n" + output + "\nSTUB_EOF\n"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(stub), 0o755); err != nil {
			t.Fatalf("write %s stub: %v", name, err)
		}
	}

	script := cloudflareMeshInstallScript()
	start := strings.Index(script, "mesh_address() {")
	end := strings.Index(script, "if ! mesh_ready; then")
	if start < 0 || end < 0 || end < start {
		t.Fatalf("install script no longer defines the readiness predicates")
	}
	probe := script[start:end] + "\nif mesh_ready; then echo CONNECTED; fi\nmesh_address\n"

	// The same interpreter the install script runs under. The provider invokes
	// it as `bash -lc`, and `set -o pipefail` is a bashism: asking `sh` for it
	// passed on a box where /bin/sh is bash and failed on a runner where it is
	// dash, which said nothing about the predicates. `-c` rather than `-lc` so
	// the stub PATH below is not overwritten by a login profile.
	cmd := exec.Command("bash", "-c", "set -euo pipefail\n"+probe)
	// Stubs take precedence; the real grep/awk the predicates use stay reachable.
	cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("readiness probe failed: %v (%s)", err, out)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		switch {
		case line == "CONNECTED":
			connected = true
		case line != "":
			address = line
		}
	}
	return connected, address
}

func TestCloudflareMeshReadinessRejectsNodeWithOnlyItsGuestAddress(t *testing.T) {
	// The exact shape of the run that failed: no Mesh address was assigned, and
	// the guest's own 10.0.100.225 address contains the Mesh prefix as a
	// substring.
	connected, address := runMeshReadinessProbe(t,
		"Status update: Disconnected",
		"2: enp5s0    inet 10.0.100.225/24 metric 100 brd 10.0.100.255 scope global dynamic enp5s0")
	if connected {
		t.Fatalf("readiness accepted a node with no Mesh address")
	}
	if address != "" {
		t.Fatalf("mesh_address = %q, want empty for a guest-only address", address)
	}
}

func TestCloudflareMeshReadinessAcceptsANodeHoldingAMeshAddress(t *testing.T) {
	connected, address := runMeshReadinessProbe(t,
		"Status update: Connected",
		"2: enp5s0    inet 10.0.100.225/24 scope global enp5s0\n3: CloudflareWARP    inet 100.96.4.17/32 scope global CloudflareWARP")
	if !connected {
		t.Fatalf("readiness rejected a node holding a Mesh address")
	}
	if address != "100.96.4.17/32" {
		t.Fatalf("mesh_address = %q, want the Mesh CIDR address", address)
	}
}

func TestMeshCommandDiagnosticsKeepsTheFailureTailOverInstallChatter(t *testing.T) {
	// More install chatter than the diagnostic budget, so the failure tail can
	// only survive if trimming keeps the END of the output.
	chatter := make([]string, 0, meshDiagnosticLineBudget+4)
	for i := 0; i < meshDiagnosticLineBudget+4; i++ {
		chatter = append(chatter, fmt.Sprintf("Setting up cloudflare-warp-dep-%d ...", i))
	}
	output := strings.Join(append(chatter,
		"Status update: Registration missing",
		"warp-cli error: unable to reach the Zero Trust registration endpoint",
		"Status update: Disconnected",
	), "\n")

	diagnostics := meshCommandDiagnostics(output, "")

	for _, want := range []string{"Registration missing", "unable to reach", "Disconnected"} {
		if !strings.Contains(diagnostics, want) {
			t.Fatalf("diagnostics %q missing failure detail %q", diagnostics, want)
		}
	}
	if strings.Contains(diagnostics, "cloudflare-warp-dep-0 ") {
		t.Fatalf("diagnostics %q still leads with install chatter", diagnostics)
	}
}

func TestCloudflareMeshReadinessDoesNotDependOnStatusText(t *testing.T) {
	// The failure this replaces, reproduced: the packaged 2026.7.x client emits
	// nothing for `status` under a non-interactive exec -- on either stream --
	// so three runs failed the bounded wait against guests that already held
	// CloudflareWARP 100.96.0.131/32 and 100.96.0.132/32. The address assignment
	// is the enrollment evidence; the status text is not available to gate on.
	for _, stream := range []string{"", ">&2 "} {
		connected, address := runMeshReadinessProbeOnStream(t,
			"",
			stream,
			"2: enp5s0    inet 10.122.20.130/24 scope global enp5s0\n4: CloudflareWARP    inet 100.96.0.131/32 scope global CloudflareWARP")
		if !connected {
			t.Fatalf("readiness rejected a node holding a Mesh address while warp-cli printed nothing (stream %q)", stream)
		}
		if address != "100.96.0.131/32" {
			t.Fatalf("mesh_address = %q, want the Mesh CIDR address (stream %q)", address, stream)
		}
	}
}

func TestCloudflareMeshReadinessRejectsCGNATAddressOutsideTheMeshCIDR(t *testing.T) {
	// 100.112.0.1 is inside the shared CGNAT space but outside 100.96.0.0/12,
	// which is the range findMeshIP enforces. The readiness predicate has to
	// draw the same line, or the script would publish the Mesh route over an
	// interface the overlay does not own.
	connected, address := runMeshReadinessProbe(t,
		"Status update: Connected",
		"2: enp5s0    inet 100.112.0.1/24 scope global enp5s0")
	if connected {
		t.Fatalf("readiness accepted an address outside the Mesh CIDR")
	}
	if address != "" {
		t.Fatalf("mesh_address = %q, want empty for an address outside the Mesh CIDR", address)
	}
}
