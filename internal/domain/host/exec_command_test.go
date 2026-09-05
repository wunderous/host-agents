package host

import (
	"context"
	"strings"
	"testing"
	"time"

	hostexec "github.com/wunderous/host-agents/internal/exec"
	"github.com/wunderous/host-agents/internal/hostruntime"
	"github.com/wunderous/host-agents/internal/resourceid"
)

func TestExecCommandRequiresName(t *testing.T) {
	svc := testService(hostruntime.Shared{})
	_, err := svc.ExecCommand(ExecCommandArgs{Command: "true"}, nil)
	if err == nil || !strings.Contains(err.Error(), "name is required") {
		t.Fatalf("expected name error, got %v", err)
	}
}

func TestExecCommandRequiresCommand(t *testing.T) {
	svc := testService(hostruntime.Shared{})
	_, err := svc.ExecCommand(ExecCommandArgs{VMName: "vm1"}, nil)
	if err == nil || !strings.Contains(err.Error(), "command is required") {
		t.Fatalf("expected command error, got %v", err)
	}
}

func TestRunInstanceCommandResolvesContainerURIWithoutVMFallback(t *testing.T) {
	registry := hostruntime.NewInMemoryResourceRegistry()
	if err := registry.UpsertResource(resourceid.Record{
		URI:          "container:tenant-a:connector",
		ResourceType: resourceid.TypeContainer,
		TenantID:     "tenant-a",
		ResourceID:   "connector",
		Coordinates:  map[string]any{"providerInstanceName": "connector", "instanceType": "container"},
		Status:       "active",
	}); err != nil {
		t.Fatal(err)
	}
	svc := testService(hostruntime.Shared{TenantID: "tenant-a", ResourceRegistry: registry})
	var got []string
	svc.shared.CommandRunnerFn = func(args []string, _ func(string), _ time.Duration) (hostexec.Result, error) {
		got = append([]string(nil), args...)
		return hostexec.Result{ExitCode: 0, Stdout: "ready\n"}, nil
	}
	out, err := svc.RunInstanceCommand(RunInstanceCommandArgs{URI: "container:tenant-a:connector", Command: "cloudflared", Args: []string{"--version"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out["uri"] != "container:tenant-a:connector" || out["instanceType"] != "container" {
		t.Fatalf("unexpected result: %#v", out)
	}
	if strings.Join(got, " ") != "exec connector -- cloudflared --version" {
		t.Fatalf("unexpected Incus argv: %#v", got)
	}
}

func TestRunInstanceCommandRejectsForeignTenantAndWrongType(t *testing.T) {
	svc := testService(hostruntime.Shared{TenantID: "tenant-a", ResourceRegistry: hostruntime.NewInMemoryResourceRegistry()})
	if _, err := svc.RunInstanceCommand(RunInstanceCommandArgs{URI: "container:tenant-b:connector", Command: "true"}, nil); err == nil || !strings.Contains(err.Error(), "different tenant") {
		t.Fatalf("expected foreign tenant rejection, got %v", err)
	}
	if _, err := svc.RunInstanceCommand(RunInstanceCommandArgs{URI: "cluster:tenant-a:cell", Command: "true"}, nil); err == nil || !strings.Contains(err.Error(), "resource not found") {
		t.Fatalf("expected no implicit cluster/VM fallback, got %v", err)
	}
}

func TestRunInstanceCommandContextPropagatesCancellationToProvider(t *testing.T) {
	registry := hostruntime.NewInMemoryResourceRegistry()
	if err := registry.UpsertResource(resourceid.Record{
		URI:          "container:tenant-a:connector",
		ResourceType: resourceid.TypeContainer,
		TenantID:     "tenant-a",
		ResourceID:   "connector",
		Coordinates:  map[string]any{"providerInstanceName": "connector", "instanceType": "container"},
		Status:       "active",
	}); err != nil {
		t.Fatal(err)
	}
	svc := testService(hostruntime.Shared{TenantID: "tenant-a", ResourceRegistry: registry})
	started := make(chan struct{})
	svc.deps.RunVMExecContext = func(ctx context.Context, _ string, _ []string, _ func(string), _ time.Duration) (hostexec.Result, error) {
		close(started)
		<-ctx.Done()
		return hostexec.Result{}, ctx.Err()
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		_, err := svc.RunInstanceCommandContext(ctx, RunInstanceCommandArgs{URI: "container:tenant-a:connector", Command: "cloudflared"}, nil)
		errCh <- err
	}()
	<-started
	cancel()
	select {
	case err := <-errCh:
		if err != context.Canceled {
			t.Fatalf("RunInstanceCommandContext error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RunInstanceCommandContext did not return after cancellation")
	}
}
