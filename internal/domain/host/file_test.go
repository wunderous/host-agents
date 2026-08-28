package host

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/wunderous/host-agents/internal/hostruntime"
)

func TestEnsureAndInspectHostFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	service := testService(hostruntime.Shared{})
	path := filepath.Join(home, ".config", "systemd", "user", "example.service")
	first, err := service.EnsureHostFile(EnsureHostFileArgs{Path: path, Content: "[Service]\nExecStart=/bin/true\n", Mode: 0o644})
	if err != nil {
		t.Fatal(err)
	}
	if first["changed"] != true {
		t.Fatalf("first write should change file: %#v", first)
	}
	second, err := service.EnsureHostFile(EnsureHostFileArgs{Path: path, Content: "[Service]\nExecStart=/bin/true\n", Mode: 0o644})
	if err != nil {
		t.Fatal(err)
	}
	if second["changed"] != false {
		t.Fatalf("second write should be satisfied: %#v", second)
	}
	observed, err := service.InspectHostFile(InspectHostFileArgs{Path: path, ExpectedSHA256: first["contentSha256"].(string)})
	if err != nil {
		t.Fatal(err)
	}
	if observed["exists"] != true || observed["regular"] != true || observed["matches"] != true {
		t.Fatalf("unexpected inspection: %#v", observed)
	}
	if _, err := service.InspectHostFile(InspectHostFileArgs{Path: "/tmp/outside-home"}); err == nil {
		t.Fatal("outside-home path should be rejected")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
}

func TestInspectHostFileMatchesExpectedContentWithoutReturningIt(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	service := testService(hostruntime.Shared{})
	path := filepath.Join(home, "managed.conf")
	if _, err := service.EnsureHostFile(EnsureHostFileArgs{Path: path, Content: "secret=runtime\n", Mode: 0o600}); err != nil {
		t.Fatal(err)
	}
	result, err := service.InspectHostFile(InspectHostFileArgs{Path: path, ExpectedContent: "secret=runtime\n"})
	if err != nil {
		t.Fatal(err)
	}
	if result["matches"] != true {
		t.Fatalf("expected content to match: %#v", result)
	}
	if _, ok := result["content"]; ok {
		t.Fatalf("inspect result returned file content: %#v", result)
	}
	result, err = service.InspectHostFile(InspectHostFileArgs{Path: path, ExpectedContent: "secret=other\n"})
	if err != nil {
		t.Fatal(err)
	}
	if result["matches"] != false {
		t.Fatalf("expected content mismatch: %#v", result)
	}
}

func TestRemoveHostFileRequiresConfirmationAndHash(t *testing.T) {
	service := testService(hostruntime.Shared{OwnershipMode: "disabled"})
	path := ".config/opute/test-remove-host-file"
	created, err := service.EnsureHostFile(EnsureHostFileArgs{Path: path, Content: "owned\n", Mode: 0o600})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.RemoveHostFile(RemoveHostFileArgs{Path: path}); err == nil {
		t.Fatal("remove without confirmation succeeded")
	}
	if _, err := service.RemoveHostFile(RemoveHostFileArgs{Path: path, Confirm: true, ExpectedSHA256: "sha256:" + "00"}); err == nil {
		t.Fatal("remove with a mismatched hash succeeded")
	}
	removed, err := service.RemoveHostFile(RemoveHostFileArgs{Path: path, Confirm: true, ExpectedSHA256: created["contentSha256"].(string)})
	if err != nil {
		t.Fatal(err)
	}
	if removed["removed"] != true {
		t.Fatalf("remove result = %#v", removed)
	}
	missing, err := service.RemoveHostFile(RemoveHostFileArgs{Path: path, Confirm: true})
	if err != nil {
		t.Fatal(err)
	}
	if missing["exists"] != false {
		t.Fatalf("missing result = %#v", missing)
	}
}
