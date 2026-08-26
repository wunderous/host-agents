package state

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/wunderous/host-agents/internal/resourceid"

	_ "modernc.org/sqlite"
)

func TestOpenMigratesLegacyActiveRuntimeColumns(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE active_runtimes (
        capability TEXT PRIMARY KEY,
        serving_contract TEXT NOT NULL,
        runtime TEXT NOT NULL
    ); INSERT INTO active_runtimes(capability, serving_contract, runtime)
       VALUES ('llm-serving', 'openai-chat.v1', 'ollama')`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := Open(dir)
	if err != nil {
		t.Fatalf("legacy state should migrate: %v", err)
	}
	defer store.Close()

	active, found, err := store.GetActiveCapability("llm-serving")
	if err != nil {
		t.Fatal(err)
	}
	if !found || active.Provider != "ollama" {
		t.Fatalf("expected migrated active capability, found=%v record=%+v", found, active)
	}
}

func TestResourceRegistryRoundTrip(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	record := resourceid.Record{
		URI: "model:local:hf.co/LiquidAI/LFM2-2.6B-GGUF:Q4_K_M", Coordinates: map[string]any{"runtime": "ollama"},
	}
	if err := store.UpsertResource(record); err != nil {
		t.Fatal(err)
	}
	got, found, err := store.GetResource(record.URI)
	if err != nil || !found {
		t.Fatalf("get found=%v err=%v", found, err)
	}
	if got.ResourceType != "model" || got.TenantID != "local" || got.Coordinates["runtime"] != "ollama" {
		t.Fatalf("record = %+v", got)
	}
	listed, err := store.ListResources("model", "local")
	if err != nil || len(listed) != 1 {
		t.Fatalf("listed=%+v err=%v", listed, err)
	}
	if err := store.DeleteResource(record.URI); err != nil {
		t.Fatal(err)
	}
	if _, found, err := store.GetResource(record.URI); err != nil || found {
		t.Fatalf("deleted found=%v err=%v", found, err)
	}
}

func TestResourceRegistryRejectsIdentityPollution(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	err = store.UpsertResource(resourceid.Record{
		URI: "vm:local:worker-01", ResourceType: "pod", TenantID: "local", ResourceID: "worker-01",
	})
	if err == nil {
		t.Fatal("expected URI/type mismatch to be rejected")
	}
}

func TestCapabilityInvocationIsDurableAndOpaque(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.RecordCapabilityInvocation(CapabilityInvocationRecord{
		InvocationID: "invocation-1", OperationID: "list_vms", CapabilityVersion: 1,
		CatalogRevision: "sha256:catalog", GenerationID: "host-1", Authorization: "admitted",
		ArgumentsJSON: `{"tenant":"local"}`, ResultJSON: `{"isError":false}`,
		ObservationJSON: `{"status":"success"}`, TerminalStatus: "success",
	}); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM capability_invocations WHERE invocation_id = 'invocation-1'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("durable invocation count = %d", count)
	}
}

func TestTaskSnapshotSurvivesStoreRestart(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveTaskSnapshot("task-1", "run_host_command", "running", map[string]any{
		"taskId": "task-1", "toolName": "run_host_command", "status": "completed",
		"result": map[string]any{"content": []map[string]any{{"type": "text", "text": "done"}}},
	}); err != nil {
		store.Close()
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	snapshots, err := reopened.ListTaskSnapshots()
	if err != nil || len(snapshots) != 1 {
		t.Fatalf("snapshots=%v err=%v", snapshots, err)
	}
	if snapshots[0]["taskId"] != "task-1" || snapshots[0]["status"] != "completed" {
		t.Fatalf("snapshot=%v", snapshots[0])
	}
}
