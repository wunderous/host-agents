package ops

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestBuildAndPushOciImageValidatesArgs(t *testing.T) {
	svc := &HostOperationsService{}
	_, err := svc.BuildAndPushOciImage(context.Background(), BuildAndPushOciImageArgs{}, nil)
	if runtime.GOOS != "linux" {
		if err == nil || !strings.Contains(err.Error(), "unsupported") {
			t.Fatalf("expected unsupported error, got %v", err)
		}
		return
	}
	if err == nil || !strings.Contains(err.Error(), "contextDir") {
		t.Fatalf("expected contextDir error, got %v", err)
	}

	dir := t.TempDir()
	_, err = svc.BuildAndPushOciImage(context.Background(), BuildAndPushOciImageArgs{
		ContextDir: dir,
		Image:      "bad image",
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "invalid characters") {
		t.Fatalf("expected invalid image error, got %v", err)
	}

	_, err = svc.BuildAndPushOciImage(context.Background(), BuildAndPushOciImageArgs{
		ContextDir: dir,
		Image:      "registry.example/app:test",
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "dockerfile not found") {
		t.Fatalf("expected missing dockerfile error, got %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Do not actually build in unit tests; missing builder path still validates dockerfile presence first.
}
