package resource

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/wunderous/host-agents/internal/resourceid"
)

func testServiceConfig(lockDir string) Config {
	return Config{
		LockDir:             lockDir,
		MaxNormal:           1,
		MaxHeavy:            1,
		MaxQueued:           2,
		DiskPaths:           []string{lockDir},
		PolicyRevision:      HostResourcePolicyRevision,
		EnforcementMode:     EnforcementEnforced,
		FailClosedOnUnknown: false,
		CPUCapacityCores:    64,
		MemoryCapacityBytes: 1 << 50,
		TaskCapacity:        1 << 20,
		ReservationTTL:      time.Minute,
		TenantID:            "local",
		ReconcilePolicy: func(_ context.Context, target resourceid.URI) error {
			if target.ResourceType != resourceid.TypeHostService {
				return errors.New("unexpected target type")
			}
			return nil
		},
	}
}

func zeroCostRequest(agent, operation, task string) AdmissionRequest {
	return AdmissionRequest{
		Class: ClassNormal, Operation: operation, AgentID: agent,
		OperationID: operation, TaskID: task,
	}
}

func TestTypedAdmissionPersistsAcrossCoordinatorsAndReusesNestedReservation(t *testing.T) {
	lockDir := t.TempDir()
	first, err := NewCoordinator(testServiceConfig(lockDir))
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewCoordinator(testServiceConfig(lockDir))
	if err != nil {
		t.Fatal(err)
	}

	parent, err := first.Admit(context.Background(), zeroCostRequest("agent-a", "provision", "task-a"))
	if err != nil {
		t.Fatal(err)
	}
	nestedCtx := WithReservation(context.Background(), parent)
	nested, err := second.Admit(nestedCtx, AdmissionRequest{
		Class: ClassHeavy, CPUCores: 2, MemoryBytes: 2 << 30,
		Operation: "nested-provider-callback", AgentID: "agent-a", TaskID: "task-a",
		ParentReservationID: parent.ID,
	})
	if err != nil {
		t.Fatalf("nested callback should reuse the parent reservation: %v", err)
	}
	if nested.ID != parent.ID || !nested.inherited {
		t.Fatalf("nested reservation = %#v, want inherited parent %q", nested, parent.ID)
	}
	if _, err := second.Admit(nestedCtx, AdmissionRequest{
		Class: ClassHeavy, Operation: "foreign-nested-callback", AgentID: "agent-b", TaskID: "task-a",
		ParentReservationID: parent.ID,
	}); err == nil {
		t.Fatal("expected a nested callback from another agent to be rejected")
	} else {
		var requestErr *RequestError
		if !errors.As(err, &requestErr) || requestErr.Code != "host_reservation_owner_mismatch" {
			t.Fatalf("unexpected nested owner error: %T %v", err, err)
		}
	}

	if _, err := second.Admit(context.Background(), zeroCostRequest("agent-b", "other", "task-b")); err == nil {
		t.Fatal("expected a second normal reservation to be denied by the shared durable count")
	} else {
		var admissionErr *AdmissionError
		if !errors.As(err, &admissionErr) || admissionErr.Code != "host_capacity_saturated" {
			t.Fatalf("unexpected saturation error: %T %v", err, err)
		}
	}

	if err := second.Release(nested); err != nil {
		t.Fatalf("releasing inherited reservation: %v", err)
	}
	if err := first.Release(parent); err != nil {
		t.Fatalf("releasing parent reservation: %v", err)
	}
	if got := second.Snapshot().Reservations.Count; got != 0 {
		t.Fatalf("reservations after release = %d, want 0", got)
	}
}

func TestTypedAdmissionRejectsOwnerMismatchAndHonorsCancellationAndExpiry(t *testing.T) {
	lockDir := t.TempDir()
	config := testServiceConfig(lockDir)
	config.ReservationTTL = 20 * time.Millisecond
	coordinator, err := NewCoordinator(config)
	if err != nil {
		t.Fatal(err)
	}

	reservation, err := coordinator.Admit(context.Background(), zeroCostRequest("agent-a", "operation-a", "task-a"))
	if err != nil {
		t.Fatal(err)
	}
	mismatched := *reservation
	mismatched.Request.AgentID = "agent-b"
	if err := coordinator.Release(&mismatched); err == nil {
		t.Fatal("expected owner mismatch to be rejected")
	} else {
		var requestErr *RequestError
		if !errors.As(err, &requestErr) || requestErr.Code != "host_reservation_owner_mismatch" {
			t.Fatalf("unexpected owner error: %T %v", err, err)
		}
	}

	deadline, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := coordinator.Admit(deadline, zeroCostRequest("agent-b", "cancelled", "task-b")); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled admission error = %v, want context.Canceled", err)
	}

	time.Sleep(40 * time.Millisecond)
	if _, err := coordinator.Admit(context.Background(), zeroCostRequest("agent-b", "after-expiry", "task-b")); err != nil {
		t.Fatalf("expired reservation should no longer block admission: %v", err)
	}
}

func TestTerminalTaskReservationsAreReclaimedAfterRestart(t *testing.T) {
	config := testServiceConfig(t.TempDir())
	config.MaxNormal = 2
	coordinator, err := NewCoordinator(config)
	if err != nil {
		t.Fatal(err)
	}
	terminal, err := coordinator.Admit(context.Background(), zeroCostRequest("agent-a", "failed-operation", "task-terminal"))
	if err != nil {
		t.Fatal(err)
	}
	working, err := coordinator.Admit(context.Background(), AdmissionRequest{
		Class: ClassNormal, Operation: "working-operation", AgentID: "agent-a",
		OperationID: "working-operation", TaskID: "task-working",
	})
	if err != nil {
		t.Fatal(err)
	}

	removed, err := coordinator.ReclaimTerminalTaskReservations(map[string]struct{}{"task-terminal": {}})
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("removed reservations = %d, want 1", removed)
	}
	if got := coordinator.Snapshot().Reservations.Count; got != 1 {
		t.Fatalf("reservations after terminal reclaim = %d, want working reservation only", got)
	}
	if err := coordinator.Release(working); err != nil {
		t.Fatal(err)
	}
	if got := coordinator.Snapshot().Reservations.Count; got != 0 {
		t.Fatalf("reservations after working release = %d, want 0", got)
	}
	_ = terminal
}

func TestTypedAdmissionReconcileRequiresExactPolicyAndHostServiceTarget(t *testing.T) {
	coordinator, err := NewCoordinator(testServiceConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := coordinator.Reconcile(context.Background(), "other-policy.v1", "host-service:local:user/opute-host-agent"); err == nil {
		t.Fatal("expected policy revision mismatch")
	} else {
		var reconcileErr *ReconcileError
		if !errors.As(err, &reconcileErr) || reconcileErr.Code != "host_resource_policy_revision_mismatch" {
			t.Fatalf("unexpected policy error: %T %v", err, err)
		}
	}
	if _, err := coordinator.Reconcile(context.Background(), HostResourcePolicyRevision, "vm:local:vm-1"); err == nil {
		t.Fatal("expected non-host-service target to be rejected")
	} else {
		var reconcileErr *ReconcileError
		if !errors.As(err, &reconcileErr) || reconcileErr.Code != "host_resource_target_invalid" {
			t.Fatalf("unexpected target error: %T %v", err, err)
		}
	}
	if _, err := coordinator.Reconcile(context.Background(), HostResourcePolicyRevision, "host-service:local:user/opute-host-agent"); err != nil {
		t.Fatalf("exact host-service reconciliation failed: %v", err)
	}
}

func TestTypedAdmissionReconcileFailsClosedWithoutConcreteBackend(t *testing.T) {
	config := testServiceConfig(t.TempDir())
	config.ReconcilePolicy = nil
	coordinator, err := NewCoordinator(config)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := coordinator.Reconcile(context.Background(), HostResourcePolicyRevision, "host-service:local:user/opute-host-agent"); err == nil {
		t.Fatal("expected reconciliation without a backend to fail closed")
	} else {
		var reconcileErr *ReconcileError
		if !errors.As(err, &reconcileErr) || reconcileErr.Code != "host_resource_reconcile_unsupported" {
			t.Fatalf("unexpected unsupported error: %T %v", err, err)
		}
	}
}

// A durable plan run holds its reservation for the life of the run, and a run
// that installs a Kubernetes server outlives the reservation TTL. The TTL is
// what reclaims a crashed holder's capacity, so the answer is renewal rather
// than a longer lease: this asserts that a renewed reservation is still the one
// the run's own nodes inherit after its original expiry has passed.
func TestRenewKeepsALongRunningOperationsReservationInheritable(t *testing.T) {
	config := testServiceConfig(t.TempDir())
	config.ReservationTTL = 80 * time.Millisecond
	coordinator, err := NewCoordinator(config)
	if err != nil {
		t.Fatal(err)
	}
	parent, err := coordinator.Admit(context.Background(), zeroCostRequest("agent-a", "run_host_plan", "task-a"))
	if err != nil {
		t.Fatal(err)
	}
	originalExpiry := parent.expiry()

	time.Sleep(50 * time.Millisecond)
	if err := coordinator.Renew(parent); err != nil {
		t.Fatalf("renew a held reservation: %v", err)
	}
	if !parent.expiry().After(originalExpiry) {
		t.Fatalf("renewed expiry = %s, want later than %s", parent.expiry(), originalExpiry)
	}

	time.Sleep(50 * time.Millisecond)
	if !time.Now().After(originalExpiry) {
		t.Fatalf("test did not outlive the original lease; expiry = %s", originalExpiry)
	}
	nested, err := coordinator.Admit(WithReservation(context.Background(), parent), AdmissionRequest{
		Class: ClassNormal, Operation: "inspect_host_service", AgentID: "agent-a", TaskID: "task-a",
		ParentReservationID: parent.ID,
	})
	if err != nil {
		t.Fatalf("plan node after renewal should inherit the run's reservation: %v", err)
	}
	if nested.ID != parent.ID || !nested.inherited {
		t.Fatalf("nested reservation = %#v, want inherited parent %q", nested, parent.ID)
	}
	if got := coordinator.Snapshot().Reservations.Count; got != 1 {
		t.Fatalf("reservations after renewal = %d, want the renewed one", got)
	}

	if err := coordinator.Release(parent); err != nil {
		t.Fatal(err)
	}
	err = coordinator.Renew(parent)
	var requestErr *RequestError
	if !errors.As(err, &requestErr) || requestErr.Code != "host_reservation_expired" {
		t.Fatalf("renew after release = %T %v, want host_reservation_expired", err, err)
	}
}
