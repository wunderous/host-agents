//go:build !windows

package heartbeat

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// HostDiskStats describes free space on a host filesystem relevant to builds
// and WSL/Incus operation.
type HostDiskStats struct {
	Mount          string
	TotalBytes     int64
	AvailableBytes int64
	Pressure       string
}

func defaultDiskPaths() []string {
	paths := []string{"/"}
	if _, err := os.Stat("/mnt/c"); err == nil {
		paths = append(paths, "/mnt/c")
	}
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		paths = append(paths, filepath.Clean(home))
	}
	return paths
}

func readDiskStats(paths []string) []HostDiskStats {
	seen := make(map[string]bool)
	out := make([]HostDiskStats, 0, len(paths))
	for _, path := range paths {
		path = filepath.Clean(strings.TrimSpace(path))
		if path == "." || path == "" || seen[path] {
			continue
		}
		seen[path] = true
		var stat unix.Statfs_t
		if err := unix.Statfs(path, &stat); err != nil || stat.Blocks == 0 {
			continue
		}
		total := int64(stat.Blocks) * int64(stat.Bsize)
		available := int64(stat.Bavail) * int64(stat.Bsize)
		if total <= 0 || available < 0 {
			continue
		}
		out = append(out, HostDiskStats{
			Mount:          path,
			TotalBytes:     total,
			AvailableBytes: available,
			Pressure:       diskPressure(available, total),
		})
	}
	return out
}
