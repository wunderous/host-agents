package host

import (
	"strings"
	"testing"
	"time"

	hostexec "github.com/wunderous/host-agents/internal/exec"
	"github.com/wunderous/host-agents/internal/hostruntime"
)

func TestUninstallIncusStackRequiresConfirmation(t *testing.T) {
	svc := testService(hostruntime.Shared{})
	_, err := svc.UninstallIncusStack(UninstallIncusStackArgs{}, nil)
	if err == nil || !strings.Contains(err.Error(), "confirm=true") {
		t.Fatalf("expected confirmation gate, got %v", err)
	}
}

// Deleting guests belongs to delete_vm. A caller who asks to purge the
// virtualization stack while instances still exist is asking for something
// other than what they said, so the refusal names them.
func TestUninstallIncusStackRefusesWhileInstancesRemain(t *testing.T) {
	restore := stubIncusInstalled(true)
	defer restore()
	svc := testService(hostruntime.Shared{})
	svc.shared.CommandRunnerFn = func(args []string, _ func(string), _ time.Duration) (hostexec.Result, error) {
		if len(args) < 1 || args[0] != "list" {
			t.Fatalf("expected an instance list, got %v", args)
		}
		return hostexec.Result{ExitCode: 0, Stdout: `[{"name":"keeper"},{"name":"other"}]`}, nil
	}
	_, err := svc.UninstallIncusStack(UninstallIncusStackArgs{Confirm: true}, nil)
	if err == nil || !strings.Contains(err.Error(), "keeper") || !strings.Contains(err.Error(), "delete them first") {
		t.Fatalf("expected a refusal naming the remaining instances, got %v", err)
	}
}

func TestUninstallIncusStackReportsAnAbsentStack(t *testing.T) {
	restore := stubIncusInstalled(false)
	defer restore()
	svc := testService(hostruntime.Shared{})
	out, err := svc.UninstallIncusStack(UninstallIncusStackArgs{Confirm: true}, nil)
	if err != nil {
		t.Fatalf("an absent stack is not an error: %v", err)
	}
	if out["alreadyAbsent"] != true {
		t.Fatalf("expected alreadyAbsent, got %v", out)
	}
}

func stubIncusInstalled(present bool) func() {
	previous := incusInstalled
	incusInstalled = func() bool { return present }
	return func() { incusInstalled = previous }
}
