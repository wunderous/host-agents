package host

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// listenerOwnedByPID checks the service process's socket descriptors against
// listening TCP sockets in its network namespace. Reachability alone cannot
// identify the service when another WSL distribution owns the same loopback.
func listenerOwnedByPID(pid, port int) (bool, error) {
	if pid <= 0 || port <= 0 || port > 65535 {
		return false, nil
	}
	procDir := fmt.Sprintf("/proc/%d", pid)
	inodes := make(map[string]struct{})
	for _, protocol := range []string{"tcp", "tcp6"} {
		content, err := os.ReadFile(filepath.Join(procDir, "net", protocol))
		if os.IsNotExist(err) && protocol == "tcp6" {
			continue
		}
		if err != nil {
			return false, err
		}
		for _, line := range strings.Split(string(content), "\n") {
			fields := strings.Fields(line)
			if len(fields) < 10 || fields[3] != "0A" {
				continue
			}
			_, hexPort, ok := strings.Cut(fields[1], ":")
			if !ok {
				continue
			}
			parsed, err := strconv.ParseUint(hexPort, 16, 16)
			if err == nil && int(parsed) == port {
				inodes[fields[9]] = struct{}{}
			}
		}
	}
	if len(inodes) == 0 {
		return false, nil
	}
	fds, err := os.ReadDir(filepath.Join(procDir, "fd"))
	if err != nil {
		return false, err
	}
	for _, fd := range fds {
		link, err := os.Readlink(filepath.Join(procDir, "fd", fd.Name()))
		if err != nil {
			continue
		}
		if strings.HasPrefix(link, "socket:[") && strings.HasSuffix(link, "]") {
			if _, ok := inodes[strings.TrimSuffix(strings.TrimPrefix(link, "socket:["), "]")]; ok {
				return true, nil
			}
		}
	}
	return false, nil
}
