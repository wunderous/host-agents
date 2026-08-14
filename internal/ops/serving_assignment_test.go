package ops

import (
	"net"
	"os"
	"os/exec"
	"strconv"
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

func TestClaimServingLaunchReclaimsDeadProcess(t *testing.T) {
	args := genericServingAssignment()
	args.AssignmentID = "restartable-service"

	servingLaunches.Lock()
	servingLaunches.started = make(map[string]bool)
	servingLaunches.Unlock()
	t.Cleanup(func() { releaseServingLaunch(args) })

	if !claimServingLaunch(args) {
		t.Fatal("expected first launch claim")
	}
	if !claimServingLaunch(args) {
		t.Fatal("expected a dead process launch claim to be recoverable")
	}
}

func TestReconcileServingAssignmentDoesNotDuplicateLiveProcess(t *testing.T) {
	args := genericServingAssignment()
	args.AssignmentID = "live-process-service"
	args.Runtime = "process"
	args.Mode = "dev-process"
	args.Artifact = map[string]any{"kind": "source", "sourceDir": "/workspace", "command": []any{"sh", "-c", "exit 0"}}
	// The shared dev stack may legitimately own port 9090. This test is about
	// the assignment-scoped PID claim, so reserve a currently unused port for
	// the synthetic unavailable endpoint instead of coupling the unit test to
	// a workstation port.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("unable to reserve a test port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	args.Endpoints = []any{map[string]any{"name": "web", "port": port, "protocol": "http"}}
	pidFile := servingPidFile(args.AssignmentID)
	process := exec.Command("sleep", "30")
	if err := process.Start(); err != nil {
		t.Skipf("sleep is unavailable on this test host: %v", err)
	}
	t.Cleanup(func() {
		_ = process.Process.Kill()
		_ = process.Wait()
	})
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(process.Process.Pid)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(pidFile) })

	result, err := (&HostOperationsService{}).ReconcileServingAssignment(args, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result["started"] == true {
		t.Fatal("reconcile launched a duplicate while the assigned process was alive")
	}
	if result["starting"] != true {
		t.Fatalf("expected starting state, got %#v", result)
	}
}
