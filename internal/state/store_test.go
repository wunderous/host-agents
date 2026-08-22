package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPlanRunsAreIdempotentAndBecomeUnknownAfterRestart(t *testing.T) {
	dir := t.TempDir()
	first, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	record := PlanRecord{
		RunID:           "run-1",
		PlanID:          "plan-1",
		Generation:      1,
		IdempotencyKey:  "key-1",
		DocumentHash:    "sha256:doc",
		CatalogRevision: "sha256:catalog",
		Status:          "running",
		PlanJSON:        `{"planId":"plan-1"}`,
		StateJSON:       `{"status":"running"}`,
	}
	created, duplicate, err := first.CreatePlan(record)
	if err != nil || !duplicate || created.RunID != record.RunID {
		t.Fatalf("first CreatePlan = %#v duplicate=%v err=%v", created, duplicate, err)
	}
	created, duplicate, err = first.CreatePlan(PlanRecord{
		RunID:           "run-2",
		PlanID:          record.PlanID,
		Generation:      record.Generation,
		IdempotencyKey:  record.IdempotencyKey,
		DocumentHash:    record.DocumentHash,
		CatalogRevision: record.CatalogRevision,
		Status:          "running",
		PlanJSON:        record.PlanJSON,
		StateJSON:       record.StateJSON,
	})
	if err != nil || duplicate || created.RunID != record.RunID {
		t.Fatalf("duplicate CreatePlan = %#v duplicate=%v err=%v", created, duplicate, err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	got, found, err := second.GetPlan(record.RunID)
	if err != nil || !found {
		t.Fatalf("GetPlan after restart: found=%v err=%v", found, err)
	}
	if got.Status != "unknown" {
		t.Fatalf("plan status after restart = %q, want unknown", got.Status)
	}
}

func TestWorkingOperationsBecomeUnknownAfterRestart(t *testing.T) {
	dir := t.TempDir()
	first, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Create("op-1", "create_vm", "create disposable VM"); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	got, found, err := second.Get("op-1")
	if err != nil || !found {
		t.Fatalf("Get after restart: found=%v err=%v", found, err)
	}
	if got["status"] != "unknown" {
		t.Fatalf("status after restart = %v, want unknown", got["status"])
	}
}

func TestStoreCloseIsIdempotent(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestOpenRejectsStateDirectoryPathThatIsAFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state-path")
	if err := os.WriteFile(path, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err == nil {
		t.Fatal("expected state path pointing at a file to fail")
	}
}
