package incus

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wunderous/host-agents/internal/hostruntime"

	"github.com/wunderous/host-agents/internal/exec"
)

func TestStorageDriverEnforcesQuota(t *testing.T) {
	for _, driver := range []string{"btrfs", "zfs", "lvm", "ceph", "ZFS"} {
		if !storageDriverEnforcesQuota(driver) {
			t.Fatalf("driver %q must be treated as quota-enforcing", driver)
		}
	}
	// dir is resolved against the backing filesystem, and an unknown driver
	// must fail closed rather than silently dropping the bound.
	for _, driver := range []string{"dir", "", "future-driver"} {
		if storageDriverEnforcesQuota(driver) {
			t.Fatalf("driver %q must not be assumed quota-enforcing", driver)
		}
	}
}

func TestFilesystemEnforcesProjectQuota(t *testing.T) {
	cases := []struct {
		fsType  string
		options string
		want    bool
	}{
		{"ext4", "rw,relatime,prjquota", true},
		{"xfs", "rw,pquota", true},
		{"ext4", "rw,relatime,discard,errors=remount-ro,data=ordered", false},
		{"overlay", "rw,prjquota", false},
		{"tmpfs", "rw", false},
	}
	for _, tc := range cases {
		if got := filesystemEnforcesProjectQuota(tc.fsType, tc.options); got != tc.want {
			t.Fatalf("%s [%s]: got %v want %v", tc.fsType, tc.options, got, tc.want)
		}
	}
}

func TestResolveMountForPathPicksMostSpecific(t *testing.T) {
	entries := parseProcMounts([]byte(
		"/dev/sda1 / ext4 rw,relatime 0 0\n" +
			"/dev/sdb1 /var/lib/incus ext4 rw,prjquota 0 0\n" +
			"tmpfs /run tmpfs rw 0 0\n"))
	mount, ok := resolveMountForPath(entries, "/var/lib/incus/storage-pools/default")
	if !ok || mount.MountPoint != "/var/lib/incus" {
		t.Fatalf("expected the /var/lib/incus mount, got %+v (ok=%v)", mount, ok)
	}
	if root, ok := resolveMountForPath(entries, "/srv/data"); !ok || root.MountPoint != "/" {
		t.Fatalf("expected fallback to /, got %+v (ok=%v)", root, ok)
	}
	// A path that merely shares a prefix string is not inside the mount.
	if _, ok := resolveMountForPath(entries, "/var/lib/incus-backup"); ok {
		if mount, _ := resolveMountForPath(entries, "/var/lib/incus-backup"); mount.MountPoint == "/var/lib/incus" {
			t.Fatal("prefix string match must not be treated as a mount containment")
		}
	}
}

func TestPoolEnforcesQuota(t *testing.T) {
	mounts := parseProcMounts([]byte(
		"/dev/sda1 / ext4 rw,relatime 0 0\n" +
			"/dev/sdb1 /quota ext4 rw,prjquota 0 0\n"))

	if ok, _ := poolEnforcesQuota(incusStoragePool{Name: "p", Driver: "zfs"}, mounts); !ok {
		t.Fatal("zfs pool must enforce quotas")
	}
	// The defect this guards: a dir pool on a filesystem without project
	// quotas accepts a size and never enforces it.
	ok, reason := poolEnforcesQuota(incusStoragePool{Name: "default", Driver: "dir", Source: "/var/lib/incus/storage-pools/default"}, mounts)
	if ok {
		t.Fatal("dir pool without project quotas must not be reported as enforcing")
	}
	if reason == "" {
		t.Fatal("a non-enforcing pool must explain why")
	}
	if ok, _ := poolEnforcesQuota(incusStoragePool{Name: "q", Driver: "dir", Source: "/quota/pools/default"}, mounts); !ok {
		t.Fatal("dir pool on a prjquota filesystem must enforce quotas")
	}
	// Source unknown, or no mount table available: fail closed.
	if ok, _ := poolEnforcesQuota(incusStoragePool{Name: "u", Driver: "dir"}, mounts); ok {
		t.Fatal("dir pool with unknown source must fail closed")
	}
	if ok, _ := poolEnforcesQuota(incusStoragePool{Name: "n", Driver: "dir", Source: "/quota/x"}, nil); ok {
		t.Fatal("missing mount table must fail closed")
	}
}

// stubQuotaService answers the two Incus queries admission performs: the
// default profile (for the root pool) and the pool record itself.
func stubQuotaService(t *testing.T, driver, source string) *Service {
	t.Helper()
	service := &Service{shared: &hostruntime.Shared{}}
	service.shared.CommandRunnerFn = func(args []string, _ func(string), _ time.Duration) (exec.Result, error) {
		joined := strings.Join(args, " ")
		switch {
		case strings.Contains(joined, "/1.0/profiles/default"):
			return exec.Result{ExitCode: 0, Stdout: `{"devices":{}}`}, nil
		case strings.HasSuffix(joined, "/1.0/storage-pools"):
			return exec.Result{ExitCode: 0, Stdout: `["/1.0/storage-pools/default"]`}, nil
		case strings.Contains(joined, "/1.0/storage-pools/default"):
			body := fmt.Sprintf(`{"name":"default","driver":%q,"config":{"source":%q}}`, driver, source)
			return exec.Result{ExitCode: 0, Stdout: body}, nil
		}
		return exec.Result{ExitCode: 0}, nil
	}
	return service
}

func withProcMounts(t *testing.T, content string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mounts")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write mounts: %v", err)
	}
	previous := procMountsPath
	procMountsPath = path
	t.Cleanup(func() { procMountsPath = previous })
}

func TestAdmitRootDiskQuotaFailsClosedOnUnenforceablePool(t *testing.T) {
	withProcMounts(t, "/dev/sda1 / ext4 rw,relatime 0 0\n")
	service := stubQuotaService(t, "dir", "/var/lib/incus/storage-pools/default")

	// An explicitly requested bound must never be silently discarded.
	if _, err := service.admitRootDiskQuota("10GiB", true); err == nil {
		t.Fatal("explicit quota on a non-enforcing pool must fail closed")
	} else if !strings.Contains(err.Error(), "cannot be enforced") {
		t.Fatalf("error must explain the refusal, got %v", err)
	}

	// An implicit default is dropped instead, so provisioning does not record
	// a limit the guest does not actually have.
	quota, err := service.admitRootDiskQuota("10GiB", false)
	if err != nil {
		t.Fatalf("implicit default must not fail: %v", err)
	}
	if quota.Size != "" {
		t.Fatalf("unenforceable implicit quota must be dropped, got %q", quota.Size)
	}
	if quota.Enforced || quota.Reason == "" {
		t.Fatalf("dropped quota must report why: %+v", quota)
	}
}

func TestAdmitRootDiskQuotaAppliesOnEnforcingPool(t *testing.T) {
	withProcMounts(t, "/dev/sda1 / ext4 rw,relatime 0 0\n")
	service := stubQuotaService(t, "zfs", "")

	quota, err := service.admitRootDiskQuota("10GiB", true)
	if err != nil {
		t.Fatalf("enforcing pool must admit the quota: %v", err)
	}
	if quota.Size != "10GiB" || !quota.Enforced {
		t.Fatalf("quota was not applied: %+v", quota)
	}
}

func TestAdmitRootDiskQuotaIgnoresEmptyRequest(t *testing.T) {
	service := stubQuotaService(t, "dir", "/var/lib/incus/storage-pools/default")
	quota, err := service.admitRootDiskQuota("  ", true)
	if err != nil || quota.Size != "" {
		t.Fatalf("empty request must be a no-op, got %+v err=%v", quota, err)
	}
}
