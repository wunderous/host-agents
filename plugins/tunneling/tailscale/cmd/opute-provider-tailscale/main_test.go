package main

import (
	"encoding/json"
	"strings"
	"testing"

	capabilitycontract "github.com/wunderous/host-agents/contracts/capability"
	providercontract "github.com/wunderous/host-agents/contracts/provider"
)

func TestTailscaleManifestValidatesAndUsesNeutralOperationIDs(t *testing.T) {
	t.Setenv("OPUTE_TAILSCALE_BACKEND", "fake")
	manifest := tailscaleManifest()
	if err := providercontract.ValidateInstallManifest(manifest, manifest.Provider); err != nil {
		t.Fatal(err)
	}
	forbidden := []string{"tailscale", "cloudflare", "k3s", "funnel"}
	for _, service := range manifest.Services {
		for _, operation := range service.Operations {
			lower := strings.ToLower(operation.ID)
			for _, token := range forbidden {
				if strings.Contains(lower, token) {
					t.Fatalf("operation ID %q contains provider-specific token %q", operation.ID, token)
				}
			}
		}
	}
	seen := map[string]bool{}
	for _, service := range manifest.Services {
		for _, operation := range service.Operations {
			seen[operation.ID] = true
		}
	}
	for _, id := range []string{
		capabilitycontract.MeshRuntimeValidateOperation,
		capabilitycontract.MeshRuntimeEnsureAgentOperation,
		capabilitycontract.MeshRuntimeEnsureControlPlaneOperation,
		capabilitycontract.MeshRuntimeStatusOperation,
		capabilitycontract.MeshMembershipEnrollOperation,
		capabilitycontract.MeshMembershipStatusOperation,
		capabilitycontract.MeshMembershipLeaveOperation,
		capabilitycontract.PrivateMeshEnsureOperation,
		capabilitycontract.PrivateMeshEnsureServiceOperation,
		capabilitycontract.PrivateMeshProbeOperation,
		capabilitycontract.PublicIngressEnsureOperation,
		capabilitycontract.PublicIngressPromoteOperation,
		capabilitycontract.PublicIngressProbeOperation,
	} {
		if !seen[id] {
			t.Fatalf("manifest missing operation %q", id)
		}
	}
	for _, id := range []string{
		capabilitycontract.NetworkOverlayValidateOperation,
		capabilitycontract.NetworkOverlayPrepareMembershipOperation,
		capabilitycontract.NetworkOverlayAttachTargetOperation,
	} {
		if seen[id] {
			t.Fatalf("manifest must not claim Cloudflare-owned operation %q", id)
		}
	}
}

func TestTailscaleTeardownPlanShape(t *testing.T) {
	plan := teardownPlan("com.opute.tailscale.teardown", "opute-provider-tailscale.service", "~/.config/systemd/user/opute-provider-tailscale.service", "host-service:local:user/opute-provider-tailscale.service", "user", providerGeneration)
	if plan["contractVersion"] != "host-plan.v1" {
		t.Fatalf("plan contractVersion = %#v", plan["contractVersion"])
	}
	if plan["planId"] != "com.opute.tailscale.teardown" {
		t.Fatalf("planId = %#v", plan["planId"])
	}
	nodes, ok := plan["nodes"].([]any)
	if !ok || len(nodes) != 1 {
		t.Fatalf("expected single inspect node, got %#v", plan["nodes"])
	}
	encoded, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), "inspect_host_service") {
		t.Fatalf("teardown plan missing inspect_host_service: %s", encoded)
	}
}

func TestTailscaleMutationsDeclareResourceCost(t *testing.T) {
	manifest := tailscaleManifest()
	for _, service := range manifest.Services {
		for _, operation := range service.Operations {
			if operation.Effect == "read" {
				continue
			}
			if operation.ResourceCost == nil || strings.TrimSpace(operation.ResourceCost.Class) == "" {
				t.Fatalf("mutating operation %q must declare resourceCost.class", operation.ID)
			}
		}
	}
}
