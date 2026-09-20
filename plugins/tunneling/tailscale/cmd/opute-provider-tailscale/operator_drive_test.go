package main

import (
	capabilitycontract "github.com/wunderous/host-agents/contracts/capability"
	"os"
	"strings"
	"testing"
)

func TestLiveDriveOperatorStableIngress(t *testing.T) {
	if strings.TrimSpace(os.Getenv("OPUTE_LIVE_DRIVE")) != "1" {
		t.Skip("set OPUTE_LIVE_DRIVE=1")
	}
	t.Setenv("OPUTE_TAILSCALE_BACKEND", "live")
	resetOwnershipStoreForTest()
	hostID := firstNonEmpty(os.Getenv("OPUTE_HOST_AGENT_ID"), "host-zephyrus-ef47fbbf")
	base := map[string]any{"hostAgentId": hostID, "credentialKind": "auth-key"}
	enrollA, err := dispatchOverlayOperation(t.Context(), capabilitycontract.NetworkOverlayEnrollOperation, mergeMaps(base, map[string]any{
		"targetUri": "container:local:opute-ha-a", "name": "opute-ha-a",
	}))
	if err != nil {
		t.Fatal(err)
	}
	a := asMap(t, enrollA.StructuredContent)
	_, err = dispatchOverlayOperation(t.Context(), capabilitycontract.NetworkOverlayEnsurePublicIngressOperation, mergeMaps(base, map[string]any{
		"targetUri":        "container:local:opute-ha-a",
		"membershipRef":    a["membershipRef"],
		"localTarget":      "http://127.0.0.1:30950",
		"operatorMode":     true,
		"ingressClassName": "tailscale",
		"operatorEvidence": "ingress/opute-public/public-demo-funnel address=opute-public.tail229553.ts.net",
		"hostname":         "opute-public.tail229553.ts.net",
		"endpoint":         "https://opute-public.tail229553.ts.net",
	}))
	if err != nil {
		t.Fatal(err)
	}
}
