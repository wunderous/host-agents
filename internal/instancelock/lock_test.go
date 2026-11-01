package instancelock

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
)

const holderEnv = "OPUTE_TEST_INSTANCE_LOCK_HOLDER"

// TestLockHolderHelper is re-run as a separate process that takes the lock,
// reports it and waits to be killed.
func TestLockHolderHelper(t *testing.T) {
	root := os.Getenv(holderEnv)
	if root == "" {
		t.Skip("helper process")
	}
	if _, err := Acquire(root); err != nil {
		os.Stdout.WriteString("error: " + err.Error() + "\n")
		os.Exit(1)
	}
	os.Stdout.WriteString("held\n")
	select {}
}

func TestAcquireRefusesSecondHolderUntilReleased(t *testing.T) {
	root := filepath.Join(t.TempDir(), "instance")
	first, err := Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Acquire(root); err == nil || err.Error() != "host agent instance is already in use: "+
		filepath.Join(root, FileName)+" is locked by another process" {
		t.Fatalf("second Acquire = %v, want the in-use refusal", err)
	}
	first.Release()
	first.Release()
	second, err := Acquire(root)
	if err != nil {
		t.Fatalf("Acquire after Release = %v", err)
	}
	second.Release()
	if info, err := os.Stat(filepath.Join(root, FileName)); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("lock file = %v, %v; want it kept with mode 0600", info, err)
	}
}

func TestAcquireReleasesEarlierRootsWhenALaterOneIsHeld(t *testing.T) {
	base := t.TempDir()
	a, b := filepath.Join(base, "a"), filepath.Join(base, "b")
	held, err := Acquire(b)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Release()
	if _, err := Acquire(a, b); err == nil || !strings.Contains(err.Error(), filepath.Join(b, FileName)) {
		t.Fatalf("Acquire(a, b) = %v, want refusal naming b", err)
	}
	again, err := Acquire(a)
	if err != nil {
		t.Fatalf("a stayed locked after the refusal: %v", err)
	}
	again.Release()
}

func TestAcquireLocksARepeatedRootOnce(t *testing.T) {
	root := t.TempDir()
	lock, err := Acquire(root, filepath.Join(root, "."), root+string(filepath.Separator))
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	if len(lock.files) != 1 {
		t.Fatalf("held %d lock files, want 1", len(lock.files))
	}
}

func TestWithin(t *testing.T) {
	root := filepath.Join(t.TempDir(), "instance")
	for path, want := range map[string]bool{
		root:                                  true,
		filepath.Join(root, "state"):          true,
		filepath.Join(root, "a", "..", "b"):   true,
		filepath.Join(root, "..", "state"):    false,
		root + "-other":                       false,
		filepath.Join(root, "..", "..", "db"): false,
	} {
		if got := Within(root, path); got != want {
			t.Errorf("Within(%q, %q) = %v, want %v", root, path, got, want)
		}
	}
}

func TestKilledHolderLeavesNoStaleLock(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("SIGKILL is Unix")
	}
	root := filepath.Join(t.TempDir(), "instance")
	cmd := exec.Command(os.Args[0], "-test.run=^TestLockHolderHelper$")
	cmd.Env = append(os.Environ(), holderEnv+"="+root)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || line != "held\n" {
		_ = cmd.Process.Kill()
		t.Fatalf("holder said %q, %v", line, err)
	}
	if _, err := Acquire(root); err == nil {
		t.Fatal("lock was free while the holder process ran")
	}
	if err := cmd.Process.Signal(syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	lock, err := Acquire(root)
	if err != nil {
		t.Fatalf("Acquire after SIGKILL = %v, want the lock free", err)
	}
	lock.Release()
}
