package tui_test

import (
	"bytes"
	"context"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/wunderous/host-agents/internal/hostmcp"
	"github.com/wunderous/host-agents/internal/ops"
	"github.com/wunderous/host-agents/internal/provider"
	"github.com/wunderous/host-agents/internal/tools"
	"github.com/wunderous/host-agents/internal/transport"
)

func TestPackagedTUIProvesDeterministicNoLLMVerticalSlice(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the isolated Incus fixture uses a POSIX executable")
	}

	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	repoRoot := filepath.Clean(filepath.Join(root, "..", ".."))

	incusDir := t.TempDir()
	incusPath := filepath.Join(incusDir, "incus")
	if err := os.WriteFile(incusPath, []byte(`#!/bin/sh
if [ "$1" = "list" ]; then
  printf '%s\n' '[{"name":"worker-01","status":"Running","type":"container","state":{"network":{"eth0":{"addresses":[{"address":"192.0.2.11","family":"inet","scope":"global"}]}}}},{"name":"worker-02","status":"Stopped","type":"container","state":{"network":{"eth0":{"addresses":[{"address":"192.0.2.12","family":"inet","scope":"global"}]}}}}]'
  exit 0
fi
printf '%s\n' 'unsupported isolated Incus fixture command' >&2
exit 1
`), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", incusDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("OPUTE_INCUS_BINARY_PATH", incusPath)

	service := ops.NewHostOperationsService(ops.Options{
		ProviderID: provider.IDIncus,
		ToolsForProvider: func(providerID string) []string {
			names, listErr := tools.HostToolNamesForProvider(providerID)
			if listErr != nil {
				return nil
			}
			return names
		},
	})
	host, err := hostmcp.NewServer(hostmcp.Options{
		ProviderID: "incus",
		Ops:        service,
		Standalone: true,
		StateDir:   t.TempDir(),
		// The plan itself is read-only. Mutation admission is enabled only so
		// this test can prove the durable plan runner reaches completion.
		AllowMutations: true,
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	t.Cleanup(func() { _ = host.Close() })
	httpServer := transport.NewHTTPServer(transport.HTTPOptions{
		HostServer: host,
		BindHost:   "127.0.0.1",
		Port:       0,
	})
	server := httptest.NewServer(httpServer.Handler())
	t.Cleanup(server.Close)

	planPath := filepath.Join(t.TempDir(), "read-only.plan.json")
	plan := `{"contractVersion":"host-plan.v1","planId":"packaged-read-only","generation":1,"idempotencyKey":"packaged-read-only-1","nodes":[{"id":"host-info","action":{"tool":"get_host_info","args":{}}}]}`
	if err := os.WriteFile(planPath, []byte(plan), 0o600); err != nil {
		t.Fatal(err)
	}
	planDir := t.TempDir()

	binary := filepath.Join(t.TempDir(), "opute-host-agent")
	build := exec.Command("go", "build", "-o", binary, "./cmd/opute-host-agent")
	build.Dir = repoRoot
	if output, buildErr := build.CombinedOutput(); buildErr != nil {
		t.Fatalf("build packaged TUI: %v\n%s", buildErr, output)
	}

	commands := strings.Join([]string{
		"/context",
		"get_vm_info vmName=@worker-01 fast=true",
		"get_vm_info vmName=@worker fast=true",
		"setup graph " + planPath,
		"setup validate " + planPath,
		"setup apply " + planPath,
		"/exit",
		"",
	}, "\n")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary,
		"--url", server.URL+"/mcp",
		"--plan-dir", planDir,
		"--auto-approve",
		"--no-prompt",
	)
	command.Dir = repoRoot
	command.Stdin = strings.NewReader(commands)
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Run(); err != nil {
		t.Fatalf("packaged TUI: %v\n%s", err, output.String())
	}
	text := output.String()
	for _, expected := range []string{
		"deterministic mode is ready",
		"catalog revision:",
		"worker-01",
		"entity selection required for @worker",
		"level 1: host-info",
		`"valid": true`,
		"operation ",
		": completed",
		"node host-info: applied",
		"bye",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("packaged TUI output missing %q:\n%s", expected, text)
		}
	}
	entries, err := os.ReadDir(planDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || filepath.Ext(entries[0].Name()) != ".plan" {
		t.Fatalf("packaged durable plan copy = %#v", entries)
	}
}
