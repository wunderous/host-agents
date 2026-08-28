package oci

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/wunderous/host-agents/internal/hostruntime"
)

func TestStageBuildContextWritesAllowlistedDir(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux-only")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(home, ".opute", "build-contexts", "unit-test")
	_ = os.RemoveAll(dest)
	svc := &Service{shared: &hostruntime.Shared{}}
	out, err := svc.StageBuildContext(StageBuildContextArgs{
		DestDir: dest,
		Files: map[string]string{
			"Dockerfile": "FROM scratch\n",
			"app/hi.txt": "hello",
		},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out["fileCount"] != 2 {
		t.Fatalf("unexpected %#v", out)
	}
	body, err := os.ReadFile(filepath.Join(dest, "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "FROM scratch") {
		t.Fatalf("dockerfile content: %q", body)
	}
}

func TestStageBuildContextRejectsEscape(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux-only")
	}
	svc := &Service{shared: &hostruntime.Shared{}}
	_, err := svc.StageBuildContext(StageBuildContextArgs{
		DestDir: "/tmp/not-allowed",
		Files:   map[string]string{"Dockerfile": "FROM scratch\n"},
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "allowlisted") {
		t.Fatalf("expected allowlist error, got %v", err)
	}
}
