package hostmcp

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wunderous/host-agents/internal/cordis"
	"github.com/wunderous/host-agents/internal/hostagent"
	"github.com/wunderous/host-agents/internal/resource"
	"github.com/wunderous/host-agents/internal/tools"
)

// recordingAdmission observes the admission boundary in order. The ordering is
// the assertion: a lifecycle tool that launches a durable run must still hold
// its reservation while the run's nodes are admitted, and must release it
// exactly once, after them.
type recordingAdmission struct {
	mu     sync.Mutex
	events []string
	next   int
}

func (a *recordingAdmission) Key() cordis.ServiceKey { return resource.HostResourceServiceKey }

func (a *recordingAdmission) Snapshot() resource.CapacitySnapshot {
	return resource.CapacitySnapshot{PolicyRevision: resource.HostResourcePolicyRevision}
}

func (a *recordingAdmission) Admit(ctx context.Context, request resource.AdmissionRequest) (*resource.Reservation, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	parent := ""
	if held, ok := resource.ReservationFromContext(ctx); ok {
		parent = held.ID
	}
	a.events = append(a.events, "admit "+request.Operation+" parent="+parent)
	a.next++
	return &resource.Reservation{
		ID:        "reservation-" + request.Operation,
		Request:   request,
		CreatedAt: time.Now().UTC(),
		ExpiresAt: time.Now().UTC().Add(time.Hour),
	}, nil
}

func (a *recordingAdmission) Release(reservation *resource.Reservation) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.events = append(a.events, "release "+reservation.ID)
	return nil
}

func (a *recordingAdmission) Renew(reservation *resource.Reservation) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.events = append(a.events, "renew "+reservation.ID)
	return nil
}

func (a *recordingAdmission) BindReservationTask(reservation *resource.Reservation, taskID string) error {
	reservation.Request.TaskID = taskID
	return nil
}

func (a *recordingAdmission) ReclaimTerminalTaskReservations(map[string]struct{}) (int, error) {
	return 0, nil
}

func (a *recordingAdmission) Reconcile(context.Context, string, string) (resource.CapacitySnapshot, error) {
	return a.Snapshot(), nil
}

func (a *recordingAdmission) snapshotEvents() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.events...)
}

func indexOfEventPrefix(events []string, prefix string) int {
	for index, event := range events {
		if strings.HasPrefix(event, prefix) {
			return index
		}
	}
	return -1
}

func TestPlanRunNodesInheritTheLaunchingLifecycleReservation(t *testing.T) {
	admission := &recordingAdmission{}
	svc := hostagent.New(hostagent.Options{
		ProviderID: "incus",
		ToolsForProvider: func(providerID string) []string {
			names, err := tools.HostToolNamesForProvider(providerID)
			if err != nil {
				return nil
			}
			return names
		},
	})
	server, err := NewServer(Options{
		ProviderID:     "incus",
		Ops:            svc,
		Standalone:     true,
		AllowMutations: true,
		StateDir:       t.TempDir(),
		Admission:      admission,
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	t.Cleanup(func() { _ = server.Close() })

	planDocument := map[string]any{
		"contractVersion": "host-plan.v1",
		"planId":          "reservation-inheritance",
		"generation":      1,
		"idempotencyKey":  "reservation-inheritance-1",
		"nodes": []any{
			// A host inventory read keeps the assertion about admission rather
			// than about whether a daemon is installed on the runner.
			map[string]any{
				"id":     "platform",
				"action": map[string]any{"tool": "detect_host_platform", "args": map[string]any{}},
			},
		},
	}
	result, err := server.DispatchTool(context.Background(), "run_host_plan", map[string]any{"plan": planDocument}, nil)
	if err != nil {
		t.Fatalf("run host plan: %v", err)
	}
	payload, ok := result.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("run result = %#v", result.StructuredContent)
	}
	runID, _ := payload["runId"].(string)
	if runID == "" {
		t.Fatalf("run result omitted runId: %#v", payload)
	}

	deadline := time.Now().Add(60 * time.Second)
	for {
		record, found, getErr := server.state.GetPlan(runID)
		if getErr != nil {
			t.Fatalf("get plan: %v", getErr)
		}
		if found && (record.Status == "completed" || record.Status == "failed") {
			if record.Status != "completed" {
				t.Fatalf("plan status = %q, error = %q", record.Status, record.ErrorMessage)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("plan %s did not reach a terminal status", runID)
		}
		time.Sleep(20 * time.Millisecond)
	}
	// The release is recorded by the plan runner after the run ends, so give the
	// goroutine that owns it the same bounded window the run itself had.
	for start := time.Now(); time.Since(start) < 10*time.Second; time.Sleep(10 * time.Millisecond) {
		if indexOfEventPrefix(admission.snapshotEvents(), "release reservation-run_host_plan") >= 0 {
			break
		}
	}

	events := admission.snapshotEvents()
	launcher := indexOfEventPrefix(events, "admit run_host_plan ")
	node := indexOfEventPrefix(events, "admit detect_host_platform ")
	release := indexOfEventPrefix(events, "release reservation-run_host_plan")
	if launcher < 0 || node < 0 || release < 0 {
		t.Fatalf("admission events = %#v", events)
	}
	if events[node] != "admit detect_host_platform parent=reservation-run_host_plan" {
		t.Fatalf("plan node did not inherit the launching reservation: %q (all: %#v)", events[node], events)
	}
	if launcher >= node || node >= release {
		t.Fatalf("reservation was not held across the run: launcher=%d node=%d release=%d events=%#v", launcher, node, release, events)
	}
	releases := 0
	for _, event := range events {
		if event == "release reservation-run_host_plan" {
			releases++
		}
	}
	if releases != 1 {
		t.Fatalf("reservation released %d times, want 1: %#v", releases, events)
	}
}

func TestReservationLeaseReleasesOnceThroughWhicheverHolderOwnsIt(t *testing.T) {
	reservation := &resource.Reservation{ID: "reservation-1", ExpiresAt: time.Now().UTC().Add(time.Hour)}

	unclaimed := &recordingAdmission{}
	lease := newReservationLease(unclaimed, reservation)
	lease.releaseIfUnclaimed()
	lease.releaseIfUnclaimed()
	if got := unclaimed.snapshotEvents(); len(got) != 1 || got[0] != "release reservation-1" {
		t.Fatalf("unclaimed lease events = %#v, want one release", got)
	}

	claimed := &recordingAdmission{}
	lease = newReservationLease(claimed, reservation)
	ctx := withReservationLease(context.Background(), lease)
	if claimReservationLease(ctx) != lease {
		t.Fatalf("first claim did not take the lease")
	}
	if second := claimReservationLease(ctx); second != nil {
		t.Fatalf("lease was claimed twice: %#v", second)
	}
	// The launching call returns here; the run still holds the reservation.
	lease.releaseIfUnclaimed()
	if got := claimed.snapshotEvents(); len(got) != 0 {
		t.Fatalf("claimed lease released by its launcher: %#v", got)
	}
	lease.finish()
	lease.finish()
	if got := claimed.snapshotEvents(); len(got) != 1 || got[0] != "release reservation-1" {
		t.Fatalf("claimed lease events = %#v, want one release", got)
	}

	// A run launched without admission configured carries no lease, and must
	// not have to know that.
	var absent *reservationLease
	absent.finish()
	absent.keepAlive(context.Background())
	if claimReservationLease(context.Background()) != nil {
		t.Fatalf("claimed a lease from a context that carries none")
	}
}
