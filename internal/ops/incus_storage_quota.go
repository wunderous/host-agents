package ops

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Incus accepts a root disk `size` on any storage pool, but only some drivers
// turn it into a real allocation. On a non-enforcing pool the quota is
// silently discarded: the instance still reports the requested limit while the
// guest can consume the entire host filesystem. Provisioning therefore
// resolves enforcement before it promises a bound, and never records a limit
// it did not actually obtain.
//
// Enforcement is a property of the pool's driver, and for the dir driver also
// of the backing filesystem, so it must be read from the host rather than
// assumed from the request.

// procMountsPath is overridden by tests; production always reads the kernel
// mount table.
var procMountsPath = "/proc/mounts"

type incusStoragePool struct {
	Name   string
	Driver string
	Source string
}

// rootDiskQuota is the outcome of admission. Size is empty when no enforceable
// bound could be applied, so callers omit the device size rather than writing
// an unenforced limit into instance config.
type rootDiskQuota struct {
	Size     string
	Pool     string
	Driver   string
	Enforced bool
	Reason   string
}

// storageDriverEnforcesQuota lists drivers whose root `size` is always a real
// allocation. The dir driver is deliberately absent: it enforces quotas only
// when the backing filesystem has project quotas enabled, which is resolved
// separately. An unrecognized driver is treated as non-enforcing so a new or
// misreported driver fails closed instead of silently dropping the bound.
func storageDriverEnforcesQuota(driver string) bool {
	switch strings.ToLower(strings.TrimSpace(driver)) {
	case "btrfs", "zfs", "lvm", "lvmcluster", "ceph", "cephfs":
		return true
	default:
		return false
	}
}

type mountEntry struct {
	MountPoint string
	FSType     string
	Options    string
}

// unescapeMountField decodes the octal escapes the kernel writes into
// /proc/mounts for characters that would otherwise break field splitting.
func unescapeMountField(field string) string {
	replacer := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)
	return replacer.Replace(field)
}

func parseProcMounts(data []byte) []mountEntry {
	entries := make([]mountEntry, 0, 32)
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 4 {
			continue
		}
		entries = append(entries, mountEntry{
			MountPoint: unescapeMountField(fields[1]),
			FSType:     fields[2],
			Options:    fields[3],
		})
	}
	return entries
}

// resolveMountForPath returns the most specific mount whose mount point is a
// path prefix of path. Later entries win ties because the kernel appends
// mounts in order, so the last matching mount is the one in effect.
func resolveMountForPath(entries []mountEntry, path string) (mountEntry, bool) {
	path = strings.TrimSpace(path)
	if path == "" {
		return mountEntry{}, false
	}
	best := mountEntry{}
	bestLen := -1
	for _, entry := range entries {
		mount := entry.MountPoint
		if mount == "" {
			continue
		}
		if path != mount && !strings.HasPrefix(path, strings.TrimSuffix(mount, "/")+"/") {
			continue
		}
		if len(mount) >= bestLen {
			best = entry
			bestLen = len(mount)
		}
	}
	if bestLen < 0 {
		return mountEntry{}, false
	}
	return best, true
}

// filesystemEnforcesProjectQuota reports whether a dir-driver pool backed by
// this mount can enforce a size. ext4 and XFS support it only when mounted
// with project quotas active.
func filesystemEnforcesProjectQuota(fsType, options string) bool {
	switch strings.ToLower(strings.TrimSpace(fsType)) {
	case "ext4", "xfs":
	default:
		return false
	}
	for _, option := range strings.Split(options, ",") {
		switch strings.ToLower(strings.TrimSpace(option)) {
		case "prjquota", "pquota", "project":
			return true
		}
	}
	return false
}

// poolEnforcesQuota resolves whether pool can enforce a root disk size, and
// returns the reason it cannot when it cannot.
func poolEnforcesQuota(pool incusStoragePool, mounts []mountEntry) (bool, string) {
	if storageDriverEnforcesQuota(pool.Driver) {
		return true, ""
	}
	if strings.EqualFold(strings.TrimSpace(pool.Driver), "dir") {
		source := strings.TrimSpace(pool.Source)
		if source == "" {
			return false, fmt.Sprintf("storage pool %q uses the dir driver and its source path is unknown, so project-quota support cannot be verified", pool.Name)
		}
		mount, ok := resolveMountForPath(mounts, source)
		if !ok {
			return false, fmt.Sprintf("storage pool %q uses the dir driver and no mount was found for %q, so project-quota support cannot be verified", pool.Name, source)
		}
		if filesystemEnforcesProjectQuota(mount.FSType, mount.Options) {
			return true, ""
		}
		return false, fmt.Sprintf("storage pool %q uses the dir driver on %s mounted at %q without project quotas (prjquota), so Incus accepts a root disk size but does not enforce it", pool.Name, mount.FSType, mount.MountPoint)
	}
	return false, fmt.Sprintf("storage pool %q uses the %q driver, which does not enforce root disk quotas", pool.Name, pool.Driver)
}

func (s *HostOperationsService) readIncusStoragePool(name string) (incusStoragePool, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return incusStoragePool{}, fmt.Errorf("storage pool name is required")
	}
	res, err := s.commandRunner([]string{"query", "/1.0/storage-pools/" + urlPathEscape(name)}, nil, defaultDiscoveryTimeout)
	if err != nil {
		return incusStoragePool{}, err
	}
	if res.ExitCode != 0 {
		return incusStoragePool{}, fmt.Errorf("%s", firstNonEmpty(res.Stderr, res.Stdout, "storage pool query failed"))
	}
	var payload struct {
		Name   string            `json:"name"`
		Driver string            `json:"driver"`
		Config map[string]string `json:"config"`
	}
	if err := json.Unmarshal([]byte(res.Stdout), &payload); err != nil {
		return incusStoragePool{}, err
	}
	pool := incusStoragePool{Name: firstNonEmpty(payload.Name, name), Driver: payload.Driver}
	if payload.Config != nil {
		pool.Source = payload.Config["source"]
	}
	return pool, nil
}

// resolveRootDiskPool mirrors the pool selection performed by the launch paths
// so admission inspects the pool the instance will actually use.
func (s *HostOperationsService) resolveRootDiskPool() (string, error) {
	if devices, err := s.readDefaultProfileDevices(); err == nil {
		if root := devices["root"]; root.Type == "disk" && strings.TrimSpace(root.Pool) != "" {
			return strings.TrimSpace(root.Pool), nil
		}
	}
	return s.resolveDefaultStoragePool()
}

// admitRootDiskQuota decides whether requested can be applied as a real bound.
//
// An explicitly requested quota that cannot be enforced fails closed: silently
// accepting it would report a limit the guest does not have. An implicit
// default is dropped instead of failing, because the caller asked for no bound
// and must not be told it received one.
func (s *HostOperationsService) admitRootDiskQuota(requested string, explicit bool) (rootDiskQuota, error) {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		return rootDiskQuota{}, nil
	}
	poolName, err := s.resolveRootDiskPool()
	if err != nil {
		return rootDiskQuota{}, err
	}
	pool, err := s.readIncusStoragePool(poolName)
	if err != nil {
		return rootDiskQuota{}, err
	}
	var mounts []mountEntry
	if data, readErr := os.ReadFile(procMountsPath); readErr == nil {
		mounts = parseProcMounts(data)
	}
	enforced, reason := poolEnforcesQuota(pool, mounts)
	quota := rootDiskQuota{Pool: pool.Name, Driver: pool.Driver, Enforced: enforced, Reason: reason}
	if enforced {
		quota.Size = requested
		return quota, nil
	}
	if explicit {
		return rootDiskQuota{}, fmt.Errorf("root disk quota %q cannot be enforced: %s; provision on a pool with a quota-capable driver (btrfs, zfs, lvm, ceph) or enable project quotas on the dir pool's filesystem", requested, reason)
	}
	return quota, nil
}
