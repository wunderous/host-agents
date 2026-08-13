package ops

import (
	"testing"
)

func genericServingAssignment() ServingAssignmentArgs {
	return ServingAssignmentArgs{
		ContractVersion: "serving-assignment.v1",
		AssignmentID:    "service-a",
		Generation:      1,
		IdempotencyKey:  "service-a:1",
		Service:         "web",
		Mode:            "oci-release",
		Runtime:         "kubernetes",
		Target:          map[string]any{"hostId": "host-a", "resourceId": "cell-a", "kind": "system-container", "instanceType": "container"},
		Artifact:        map[string]any{"kind": "oci", "digest": "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		Endpoints:       []any{map[string]any{"name": "web", "port": 9090, "protocol": "http"}},
		Readiness:       []any{map[string]any{"name": "web-ready", "type": "http", "endpointName": "web"}},
		Exposure:        map[string]any{"mode": "cluster-ingress", "domains": []any{"example.test"}},
	}
}

func TestReconcileServingAssignmentRejectsVMTarget(t *testing.T) {
	args := genericServingAssignment()
	args.Target = map[string]any{"hostId": "host-a", "resourceId": "vm-a", "kind": "virtual-machine", "instanceType": "vm"}
	if _, err := (&HostOperationsService{}).ReconcileServingAssignment(args, nil); err == nil {
		t.Fatal("expected VM target to be rejected")
	}
}

func TestReconcileServingAssignmentAcceptsExplicitContainerTarget(t *testing.T) {
	result, err := (&HostOperationsService{}).ReconcileServingAssignment(genericServingAssignment(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if result["status"] != "accepted" {
		t.Fatalf("unexpected status: %#v", result["status"])
	}
	if result["readinessSource"] != "host-agent-local-probe" {
		t.Fatalf("unexpected readiness source: %#v", result["readinessSource"])
	}
}

func TestReconcileServingAssignmentAllowsComposedProcessLifecycle(t *testing.T) {
	args := genericServingAssignment()
	args.Mode = "dev-process"
	args.Runtime = "process"
	args.Artifact = map[string]any{"kind": "source", "sourceDir": "/workspace", "hotReload": true, "command": []any{"bun", "run", "dev"}}
	if _, err := (&HostOperationsService{}).ReconcileServingAssignment(args, nil); err != nil {
		t.Fatal(err)
	}
}

func TestServingPidFileIsAssignmentScoped(t *testing.T) {
	if got := servingPidFile("public-edge-local-dev"); got != "/tmp/serving-assignment-public-edge-local-dev.pid" {
		t.Fatalf("unexpected serving pid file: %s", got)
	}
}
