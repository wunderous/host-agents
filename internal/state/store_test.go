package state

import (
	"os"
	"path/filepath"
	"testing"
)

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
