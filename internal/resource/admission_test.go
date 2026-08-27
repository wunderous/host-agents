package resource

import (
	"context"
	"runtime"
	"testing"
	"time"
)

func TestClassifyTool(t *testing.T) {
	for _, test := range []struct {
		tool  string
		class Class
	}{
		{"host_agent_heartbeat", ClassControl},
		{"get_host_info", ClassControl},
		{"list_vms", ClassControl},
		{"configure_oci_storage", ClassHeavy},
		{"build_and_push_oci_image", ClassHeavy},
		// Serving reconciliation writes an explicit endpoint unit and waits for
		// its bounded readiness contract. It is a normal host operation, not an
		// exclusive resource-heavy workload, so unrelated normal work cannot
		// starve public serving convergence.
		{"run_host_command", ClassNormal},
		{"apply_manifest", ClassNormal},
	} {
		if got := ClassifyTool(test.tool); got != test.class {
			t.Fatalf("ClassifyTool(%q) = %q, want %q", test.tool, got, test.class)
		}
	}
}

func TestHeavyAdmissionIsSerialized(t *testing.T) {
	c, err := NewCoordinator(Config{LockDir: t.TempDir(), MaxNormal: 2, MaxHeavy: 1, MaxQueued: 2, DiskPaths: []string{t.TempDir()}})
	if err != nil {
		t.Fatal(err)
	}
	first, err := c.Acquire(context.Background(), "build_and_push_oci_image")
	if err != nil {
		t.Fatal(err)
	}
	deferred := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		_, acquireErr := c.Acquire(ctx, "prepare_host_agent_artifacts")
		deferred <- acquireErr
	}()
	if err := <-deferred; err == nil {
		t.Fatal("expected second heavy operation to remain blocked")
	}
	first()
}

func TestServingReconciliationCanCoexistWithNormalWork(t *testing.T) {
	c, err := NewCoordinator(Config{LockDir: t.TempDir(), MaxNormal: 2, MaxHeavy: 1, MaxQueued: 2, DiskPaths: []string{t.TempDir()}})
	if err != nil {
		t.Fatal(err)
	}

	first, err := c.Acquire(context.Background(), "run_host_command")
	if err != nil {
		t.Fatal(err)
	}
	defer first()

	second, err := c.Acquire(context.Background(), "run_host_command")
	if err != nil {
		t.Fatalf("serving reconciliation should not wait behind one normal operation: %v", err)
	}
	second()
}

func TestCoResidentCoordinatorsShareHeavyLock(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows host agents do not share the WSL Incus provider lock")
	}
	lockDir := t.TempDir()
	first, err := NewCoordinator(Config{LockDir: lockDir, DiskPaths: []string{t.TempDir()}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewCoordinator(Config{LockDir: lockDir, DiskPaths: []string{t.TempDir()}})
	if err != nil {
		t.Fatal(err)
	}
	release, err := first.Acquire(context.Background(), "build_and_push_oci_image")
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := second.Acquire(ctx, "prepare_host_agent_artifacts"); err == nil {
		t.Fatal("expected co-resident coordinator to share the heavy lock")
	}
}

func TestCriticalPressureRejectsHeavyWork(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows host pressure probes are not the WSL/Incus admission contract")
	}
	c, err := NewCoordinator(Config{LockDir: t.TempDir(), MinAvailableMemoryBytes: 1 << 62, DiskPaths: []string{t.TempDir()}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Acquire(context.Background(), "build_and_push_oci_image")
	if err == nil {
		t.Fatal("expected resource pressure rejection")
	}
	if admissionErr, ok := err.(*AdmissionError); !ok || admissionErr.Code != "host_resource_pressure" {
		t.Fatalf("unexpected error: %T %v", err, err)
	}
}
