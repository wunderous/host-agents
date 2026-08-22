package tui

import (
	"bytes"
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wunderous/host-agents/internal/hostmcp"
	"github.com/wunderous/host-agents/internal/ops"
	"github.com/wunderous/host-agents/internal/provider"
	"github.com/wunderous/host-agents/internal/tools"
	"github.com/wunderous/host-agents/internal/transport"
)

func TestStreamableHTTPTUIDeterministicSession(t *testing.T) {
	svc := ops.NewHostOperationsService(ops.Options{
		ProviderID: provider.IDIncus,
		ToolsForProvider: func(providerID string) []string {
			names, err := tools.HostToolNamesForProvider(providerID)
			if err != nil {
				return nil
			}
			return names
		},
	})
	host, err := hostmcp.NewServer(hostmcp.Options{
		ProviderID: "incus", Ops: svc, Standalone: true, StateDir: t.TempDir(), AllowMutations: false,
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	t.Cleanup(func() { _ = host.Close() })
	httpServer := transport.NewHTTPServer(transport.HTTPOptions{HostServer: host, BindHost: "127.0.0.1", Port: 0})
	ts := httptest.NewServer(httpServer.Handler())
	t.Cleanup(ts.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var output bytes.Buffer
	app, err := New(ctx, Config{
		Endpoint: ts.URL + "/mcp", In: strings.NewReader("/help\nget_host_info\n/exit\n"), Out: &output, Err: &output, NoPrompt: true,
	})
	if err != nil {
		t.Fatalf("New TUI: %v", err)
	}
	t.Cleanup(func() { _ = app.client.Close() })
	if err := app.Loop(ctx); err != nil {
		t.Fatalf("TUI loop: %v", err)
	}
	text := output.String()
	for _, expected := range []string{"deterministic mode is ready", "/describe <capability>", "hostName", "bye"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("TUI output missing %q:\n%s", expected, text)
		}
	}
}

func TestTUISetupReadOnlyPlanGraphsValidatesPersistsAndPollsToCompletion(t *testing.T) {
	svc := ops.NewHostOperationsService(ops.Options{
		ProviderID: provider.IDIncus,
		ToolsForProvider: func(providerID string) []string {
			names, err := tools.HostToolNamesForProvider(providerID)
			if err != nil {
				return nil
			}
			return names
		},
	})
	host, err := hostmcp.NewServer(hostmcp.Options{
		ProviderID: "incus", Ops: svc, Standalone: true, StateDir: t.TempDir(), AllowMutations: true,
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	t.Cleanup(func() { _ = host.Close() })
	httpServer := transport.NewHTTPServer(transport.HTTPOptions{HostServer: host, BindHost: "127.0.0.1", Port: 0})
	ts := httptest.NewServer(httpServer.Handler())
	t.Cleanup(ts.Close)

	planPath := filepath.Join(t.TempDir(), "read-only-plan.json")
	planDocument := `{"contractVersion":"host-plan.v1","planId":"tui-read-only","generation":1,"idempotencyKey":"tui-read-only-1","nodes":[{"id":"host-info","action":{"tool":"get_host_info","args":{}}}]}`
	if err := os.WriteFile(planPath, []byte(planDocument), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var output bytes.Buffer
	app, err := New(ctx, Config{
		Endpoint: ts.URL + "/mcp", PlanDir: t.TempDir(), In: strings.NewReader(""), Out: &output, Err: &output, NoPrompt: true, AutoApprove: true,
	})
	if err != nil {
		t.Fatalf("New TUI: %v", err)
	}
	t.Cleanup(func() { _ = app.Close() })

	for _, command := range []string{"setup graph " + planPath, "setup validate " + planPath, "setup apply " + planPath} {
		if err := app.handle(ctx, command); err != nil {
			t.Fatalf("%s: %v\noutput:\n%s", command, err, output.String())
		}
	}
	text := output.String()
	for _, expected := range []string{"level 1: host-info", `"valid": true`, "operation ", ": completed", "node host-info: applied"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("setup output missing %q:\n%s", expected, text)
		}
	}
	entries, err := os.ReadDir(app.config.PlanDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || filepath.Ext(entries[0].Name()) != ".plan" {
		t.Fatalf("durable plan copy = %#v", entries)
	}
	if len(app.trace) == 0 {
		t.Fatal("setup execution emitted no trace")
	}
}
