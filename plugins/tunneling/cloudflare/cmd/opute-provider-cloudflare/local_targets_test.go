package main

import (
	"strings"
	"testing"
)

func mappingInput(port float64, target string) map[string]any {
	return map[string]any{"localPort": port, "target": target}
}

// The mappings used to reach the manifest as a YAML comment, so a connector
// that routed nothing installed cleanly and reported ready. The sidecar is what
// makes `localhost:<port>` -- the address the tunnel's remote ingress names --
// actually answer inside the pod.
func TestConnectorManifestForwardsEachLocalTarget(t *testing.T) {
	targets, err := parseLocalTargets([]any{
		mappingInput(9190, "platform-opute-web.opute-platform.svc.cluster.local:9090"),
		mappingInput(9191, "platform-opute-mcp.opute-platform.svc.cluster.local:9091"),
	})
	if err != nil {
		t.Fatal(err)
	}
	manifest := cloudflaredManifest("edge-system", "cloudflared", "cloudflare/cloudflared:test", 2, targets, defaultForwarderImage)

	for _, want := range []string{
		"      - name: forward-9190\n",
		"TCP-LISTEN:9190,fork,reuseaddr,bind=127.0.0.1",
		"TCP:platform-opute-web.opute-platform.svc.cluster.local:9090",
		"      - name: forward-9191\n",
		"TCP-LISTEN:9191,fork,reuseaddr,bind=127.0.0.1",
		"TCP:platform-opute-mcp.opute-platform.svc.cluster.local:9091",
		"image: " + yamlQuote(defaultForwarderImage),
	} {
		if !strings.Contains(manifest, want) {
			t.Fatalf("manifest is missing %q:\n%s", want, manifest)
		}
	}

	// The forwarders are containers of the connector Deployment's pod, not a
	// second workload: sharing the network namespace is the entire mechanism.
	if strings.Count(manifest, "kind: Deployment") != 1 {
		t.Fatalf("forwarding must not add a workload:\n%s", manifest)
	}
	if strings.Contains(manifest, "# localTargets") {
		t.Fatal("mappings are still being rendered as a comment")
	}
}

// A connector with no mappings must render exactly what it always did. Callers
// that never asked for forwarding do not get a new image pulled into their
// cluster.
func TestConnectorManifestWithoutLocalTargetsAddsNoSidecar(t *testing.T) {
	targets, err := parseLocalTargets(nil)
	if err != nil {
		t.Fatal(err)
	}
	manifest := cloudflaredManifest("edge-system", "cloudflared", "cloudflare/cloudflared:test", 1, targets, defaultForwarderImage)
	if strings.Contains(manifest, "forward-") || strings.Contains(manifest, defaultForwarderImage) {
		t.Fatalf("empty mappings rendered a sidecar:\n%s", manifest)
	}
}

func TestLocalTargetsAreRefusedRatherThanDropped(t *testing.T) {
	for name, raw := range map[string]any{
		"not an array":      map[string]any{"localPort": 9190.0},
		"not an object":     []any{"9190=web:9090"},
		"missing port":      []any{map[string]any{"target": "web:9090"}},
		"port out of range": []any{mappingInput(70000, "web:9090")},
		"missing target":    []any{mappingInput(9190, "   ")},
		"bare hostname":     []any{mappingInput(9190, "platform-opute-web")},
		"empty host":        []any{mappingInput(9190, ":9090")},
		"duplicate port":    []any{mappingInput(9190, "web:9090"), mappingInput(9190, "mcp:9091")},
	} {
		if _, err := parseLocalTargets(raw); err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
}

// The forwarder image is pinned but overridable: a cluster that mirrors its
// images has nowhere to pull alpine/socat from.
func TestForwarderImageIsOverridable(t *testing.T) {
	targets, err := parseLocalTargets([]any{mappingInput(9190, "web.default.svc.cluster.local:9090")})
	if err != nil {
		t.Fatal(err)
	}
	manifest := cloudflaredManifest("edge-system", "cloudflared", "cloudflare/cloudflared:test", 1, targets, "registry.internal/socat:1.8.0.0")
	if !strings.Contains(manifest, "image: "+yamlQuote("registry.internal/socat:1.8.0.0")) || strings.Contains(manifest, defaultForwarderImage) {
		t.Fatalf("forwarder image was not honoured:\n%s", manifest)
	}
}
