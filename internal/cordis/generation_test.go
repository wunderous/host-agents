package cordis

import (
	"context"
	"testing"
	"time"

	providercontract "github.com/wunderous/host-agents/contracts/provider"
)

func TestGenerationActivationAndSessionAffinity(t *testing.T) {
	m := NewProviderLifecycleManager(DrainPolicy{Timeout: 100 * time.Millisecond})
	ref := providercontract.ProviderRef{ID: "com.opute.example", Version: "1.0.0"}
	first, err := m.CreateCandidate(ref, "sha256:first", "http://127.0.0.1:1/mcp", "catalog-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.MarkReady(first.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.Activate(first.ID); err != nil {
		t.Fatal(err)
	}
	session, err := m.OpenSession(ref.ID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := m.CreateCandidate(ref, "sha256:second", "http://127.0.0.1:2/mcp", "catalog-2")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.MarkReady(second.ID); err != nil {
		t.Fatal(err)
	}
	previous, active, err := m.Activate(second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if previous == nil || previous.ID != session.GenerationID() || active.ID != second.ID {
		t.Fatalf("generation switch previous=%+v active=%+v session=%s", previous, active, session.GenerationID())
	}
	if err := m.Drain(context.Background(), first.ID); err == nil {
		t.Fatal("drain succeeded while old session was active")
	}
	session.Close()
	if err := m.Drain(context.Background(), first.ID); err != nil {
		t.Fatal(err)
	}
	if got, ok := m.Active(ref.ID); !ok || got.ID != second.ID {
		t.Fatalf("active generation = %+v/%v", got, ok)
	}
}

func TestGenerationFailedCandidateCannotActivate(t *testing.T) {
	m := NewProviderLifecycleManager(DrainPolicy{})
	candidate, err := m.CreateCandidate(providercontract.ProviderRef{ID: "com.opute.example", Version: "1.0.0"}, "sha256:one", "http://127.0.0.1:1/mcp", "catalog")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Fail(candidate.ID, "readiness failed"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.Activate(candidate.ID); err == nil {
		t.Fatal("failed candidate was activated")
	}
}

func TestGenerationExplicitSessionCanValidateCandidate(t *testing.T) {
	m := NewProviderLifecycleManager(DrainPolicy{})
	ref := providercontract.ProviderRef{ID: "com.opute.example", Version: "1.0.0"}
	candidate, err := m.CreateCandidate(ref, "sha256:one", "http://127.0.0.1:1/mcp", "catalog")
	if err != nil {
		t.Fatal(err)
	}
	session, err := m.OpenSessionForGeneration(ref.ID, candidate.ID)
	if err != nil {
		t.Fatal(err)
	}
	if session.GenerationID() != candidate.ID {
		t.Fatalf("session generation = %q, want %q", session.GenerationID(), candidate.ID)
	}
	session.Close()
	if got, ok := m.Get(candidate.ID); !ok || got.Sessions != 0 {
		t.Fatalf("candidate session count = %+v/%v", got, ok)
	}
}

func TestGenerationActivationRollbackRestoresPrevious(t *testing.T) {
	m := NewProviderLifecycleManager(DrainPolicy{})
	ref := providercontract.ProviderRef{ID: "com.opute.example", Version: "1.0.0"}
	first, err := m.CreateCandidate(ref, "sha256:first", "http://127.0.0.1:1/mcp", "catalog-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.MarkReady(first.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.Activate(first.ID); err != nil {
		t.Fatal(err)
	}
	second, err := m.CreateCandidate(ref, "sha256:second", "http://127.0.0.1:2/mcp", "catalog-2")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.MarkReady(second.ID); err != nil {
		t.Fatal(err)
	}
	previous, activated, err := m.Activate(second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if previous == nil {
		t.Fatal("activation did not return previous generation")
	}
	if err := m.RollbackActivation(activated.ID, previous.ID); err != nil {
		t.Fatal(err)
	}
	if got, ok := m.Active(ref.ID); !ok || got.ID != first.ID {
		t.Fatalf("active generation after rollback = %+v/%v", got, ok)
	}
	if got, ok := m.Get(second.ID); !ok || got.State != GenerationFailed {
		t.Fatalf("candidate generation after rollback = %+v/%v", got, ok)
	}
}
