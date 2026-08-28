package postgres

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSQLiteDatabaseLifecycleIsolatesConsumers(t *testing.T) {
	root := t.TempDir()
	service := &Service{sqliteDatabaseRoot: root}

	first, err := service.EnsureSQLiteDatabase(context.Background(), SQLiteDatabaseArgs{ConsumerID: "service-a", DatabaseName: "platform"})
	if err != nil {
		t.Fatal(err)
	}
	if !first.Created || !first.Exists || first.Provider != "sqlite" {
		t.Fatalf("first result = %#v", first)
	}
	second, err := service.EnsureSQLiteDatabase(context.Background(), SQLiteDatabaseArgs{ConsumerID: "service-b", DatabaseName: "platform"})
	if err != nil {
		t.Fatal(err)
	}
	if first.Path == second.Path {
		t.Fatalf("consumers share SQLite path: %q", first.Path)
	}
	if filepath.Dir(first.Path) == filepath.Dir(second.Path) {
		t.Fatalf("consumers share SQLite directory: %q", filepath.Dir(first.Path))
	}

	repeated, err := service.EnsureSQLiteDatabase(context.Background(), SQLiteDatabaseArgs{ConsumerID: "service-a", DatabaseName: "platform"})
	if err != nil {
		t.Fatal(err)
	}
	if repeated.Created {
		t.Fatal("repeated ensure reported a new database")
	}

	status, err := service.GetSQLiteDatabaseStatus(context.Background(), SQLiteDatabaseArgs{ConsumerID: "service-a", DatabaseName: "platform"})
	if err != nil {
		t.Fatal(err)
	}
	if !status.Exists {
		t.Fatalf("status = %#v", status)
	}

	removed, err := service.RemoveSQLiteDatabase(context.Background(), SQLiteDatabaseArgs{ConsumerID: "service-a", DatabaseName: "platform"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if removed.Exists {
		t.Fatalf("removed result = %#v", removed)
	}
	if _, err := os.Stat(first.Path); !os.IsNotExist(err) {
		t.Fatalf("database still exists, stat error = %v", err)
	}
}

func TestSQLiteDatabaseRejectsPathTraversalAndUnconfirmedRemoval(t *testing.T) {
	service := &Service{sqliteDatabaseRoot: t.TempDir()}
	for _, args := range []SQLiteDatabaseArgs{
		{ConsumerID: "../escape", DatabaseName: "db"},
		{ConsumerID: "service", DatabaseName: "../escape"},
	} {
		if _, err := service.EnsureSQLiteDatabase(context.Background(), args); err == nil {
			t.Fatalf("expected invalid args to fail: %#v", args)
		}
	}
	if _, err := service.RemoveSQLiteDatabase(context.Background(), SQLiteDatabaseArgs{ConsumerID: "service", DatabaseName: "db"}, false); err == nil {
		t.Fatal("expected removal without confirmation to fail")
	}
}
