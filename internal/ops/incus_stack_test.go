package ops

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wunderous/host-agents/internal/exec"
	"github.com/wunderous/host-agents/internal/hostruntime"
)

func validResetService() *HostOperationsService {
	return &HostOperationsService{
		shared: hostruntime.Shared{
			InstanceID:              "agent-a",
			OwnershipMode:           "enforce",
			SharedHostOwnerInstance: "agent-a",
		},
	}
}

func TestResetIncusStackFailsClosedBeforeInventory(t *testing.T) {
	service := validResetService()
	_, err := service.ResetIncusStack(nil, ResetIncusStackArgs{
		Confirm:                     true,
		Reinstall:                   true,
		InstancePrefix:              "opute-",
		DisposableHostFingerprint:   "host-a",
		ExpectedHostFingerprint:     "host-b",
		DisposableHostAuthorization: "dispose:host-a",
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "matching disposable host fingerprint") {
		t.Fatalf("expected fingerprint gate, got %v", err)
	}
}

func TestResetIncusStackRequiresExplicitTarget(t *testing.T) {
	service := validResetService()
	_, err := service.ResetIncusStack(nil, ResetIncusStackArgs{
		Confirm:                     true,
		Reinstall:                   true,
		DisposableHostFingerprint:   "host-a",
		ExpectedHostFingerprint:     "host-a",
		DisposableHostAuthorization: "dispose:host-a",
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "explicit instanceNames or instancePrefix") {
		t.Fatalf("expected explicit target gate, got %v", err)
	}
}

func TestResetIncusCheckpointRoundTripsAndClears(t *testing.T) {
	service := validResetService()
	service.resetCheckpointPath = filepath.Join(t.TempDir(), "reset", "checkpoint.json")
	original := resetIncusCheckpoint{
		Targets: []ResetIncusInventoryItem{{Name: "opute-disposable", Owner: "agent-a"}},
		Deleted: []string{"opute-disposable"},
		Phase:   "interrupted",
	}
	if err := service.writeResetCheckpoint(original); err != nil {
		t.Fatal(err)
	}
	loaded, err := service.readResetCheckpoint()
	if err != nil {
		t.Fatal(err)
	}
	if loaded == nil || loaded.Phase != "interrupted" || len(loaded.Deleted) != 1 {
		t.Fatalf("unexpected checkpoint: %#v", loaded)
	}
	if err := service.clearResetCheckpoint(); err != nil {
		t.Fatal(err)
	}
	loaded, err = service.readResetCheckpoint()
	if err != nil {
		t.Fatal(err)
	}
	if loaded != nil {
		t.Fatalf("expected checkpoint to be removed, got %#v", loaded)
	}
}

func TestResetInventoryOwnershipRefusesForeignAndUnowned(t *testing.T) {
	if err := validateResetInventoryOwnership([]ResetIncusInventoryItem{
		{Name: "opute-owned", Owner: "agent-a"},
	}, "agent-a"); err != nil {
		t.Fatalf("owned instances must pass: %v", err)
	}
	for _, owner := range []string{"agent-b", ""} {
		err := validateResetInventoryOwnership([]ResetIncusInventoryItem{
			{Name: "opute-foreign", Owner: owner},
		}, "agent-a")
		if err == nil || !strings.Contains(err.Error(), "refuses unowned instance") {
			t.Fatalf("expected foreign/unowned refusal for owner %q, got %v", owner, err)
		}
	}
}

func TestVerifyResetIncusStackProbesPoolBridgeProfile(t *testing.T) {
	service := validResetService()
	service.shared.CommandRunnerFn = func(args []string, onData func(string), timeout time.Duration) (exec.Result, error) {
		switch args[0] {
		case "storage":
			return exec.Result{Stdout: `[{"name":"default","driver":"dir"}]`}, nil
		case "network":
			return exec.Result{Stdout: `[{"name":"incusbr0","type":"bridge"}]`}, nil
		case "profile":
			return exec.Result{Stdout: "root:\n  path: /\n  pool: default\n"}, nil
		default:
			return exec.Result{}, errors.New("unexpected command")
		}
	}
	verified, err := service.verifyResetIncusStack()
	if err != nil {
		t.Fatal(err)
	}
	if verified["poolReady"] != true || verified["bridgeReady"] != true || verified["profileReady"] != true || verified["verified"] != true {
		t.Fatalf("expected all Incus invariants verified: %#v", verified)
	}
}

func TestVerifyResetIncusStackFailsClosedOnMissingInvariant(t *testing.T) {
	service := validResetService()
	service.shared.CommandRunnerFn = func(args []string, onData func(string), timeout time.Duration) (exec.Result, error) {
		switch args[0] {
		case "storage":
			return exec.Result{Stdout: `[{"name":"other","driver":"dir"}]`}, nil
		case "network":
			return exec.Result{Stdout: `[{"name":"incusbr0","type":"bridge"}]`}, nil
		case "profile":
			return exec.Result{Stdout: "root:\n  path: /\n  pool: default\n"}, nil
		default:
			return exec.Result{}, errors.New("unexpected command")
		}
	}
	if _, err := service.verifyResetIncusStack(); err == nil {
		t.Fatal("expected verification failure when the default storage pool is missing")
	}
}
