package main

import (
	"os"
	"strings"
	"testing"

	capabilitycontract "github.com/wunderous/host-agents/contracts/capability"
)

func TestLiveDrivePublicIngressMCP(t *testing.T) {
	if strings.TrimSpace(os.Getenv("OPUTE_LIVE_DRIVE")) != "1" {
		t.Skip("set OPUTE_LIVE_DRIVE=1 to run live MCP drive")
	}
	t.Setenv("OPUTE_TAILSCALE_BACKEND", "live")
	resetOwnershipStoreForTest()

	hostID := firstNonEmpty(os.Getenv("OPUTE_HOST_AGENT_ID"), "host-zephyrus-ef47fbbf")
	base := map[string]any{
		"hostAgentId":    hostID,
		"credentialKind": "auth-key",
	}

	enrollA, err := dispatchOverlayOperation(t.Context(), capabilitycontract.NetworkOverlayEnrollOperation, mergeMaps(base, map[string]any{
		"targetUri": "container:local:opute-ha-a",
		"name":      "opute-ha-a",
	}))
	if err != nil {
		t.Fatalf("enroll a: %v", err)
	}
	a := asMap(t, enrollA.StructuredContent)
	t.Logf("enroll a: %#v", a)

	// ha-b is on a remote Incus host; mesh adopts it via Tailscale API + peerMeshIp.
	meshArgs := mergeMaps(base, map[string]any{
		"targetUri":         "container:local:opute-ha-a",
		"peerTargetUri":     "container:local:opute-ha-b",
		"membershipRef":     a["membershipRef"],
		"peerMeshIp":        firstNonEmpty(os.Getenv("OPUTE_PEER_MESH_IP"), "100.93.139.86"),
		"datastoreMode":     "embedded-etcd",
		"availabilityClass": "serving-continuity",
	})
	mesh, err := dispatchOverlayOperation(t.Context(), capabilitycontract.NetworkOverlayEnsurePrivateMeshOperation, meshArgs)
	if err != nil {
		t.Fatalf("ensure-private-mesh: %v", err)
	}
	t.Logf("mesh: %#v", asMap(t, mesh.StructuredContent))

	ingress, err := dispatchOverlayOperation(t.Context(), capabilitycontract.NetworkOverlayEnsurePublicIngressOperation, mergeMaps(base, map[string]any{
		"targetUri":     "container:local:opute-ha-a",
		"membershipRef": a["membershipRef"],
		"localTarget":   firstNonEmpty(os.Getenv("OPUTE_PUBLIC_LOCAL_TARGET"), "http://127.0.0.1:30950"),
		"operatorMode":  false,
	}))
	if err != nil {
		t.Fatalf("ensure-public-ingress: %v", err)
	}
	ing := asMap(t, ingress.StructuredContent)
	t.Logf("ingress: %#v", ing)
	if ing["stable"] == true {
		t.Fatal("node-specific Funnel must report stable=false")
	}
	if ready, _ := ing["ready"].(bool); !ready {
		t.Fatalf("ingress not ready: %#v", ing)
	}

	probe, err := dispatchOverlayOperation(t.Context(), capabilitycontract.NetworkOverlayProbeOperation, mergeMaps(base, map[string]any{
		"targetUri":   "container:local:opute-ha-a",
		"pathClass":   "public-ingress",
		"endpoint":    ing["endpoint"],
		"endpointRef": ing["endpointRef"],
	}))
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	t.Logf("probe: %#v", asMap(t, probe.StructuredContent))
}

func mergeMaps(values ...map[string]any) map[string]any {
	out := map[string]any{}
	for _, value := range values {
		for k, v := range value {
			out[k] = v
		}
	}
	return out
}
