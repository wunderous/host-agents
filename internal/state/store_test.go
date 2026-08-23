package state

import (
	"database/sql"
	"path/filepath"
	"testing"

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
