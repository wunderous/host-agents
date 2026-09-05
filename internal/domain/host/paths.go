package host

import (
	"errors"
	"os"
	osuser "os/user"
	"strings"
)

// hostHomeDir resolves the service owner's home even when a systemd service
// deliberately has no HOME environment variable. os.UserHomeDir only consults
// that variable on Unix; the account database is the authoritative fallback
// for system-scoped Host Agents.
func hostHomeDir() (string, error) {
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		return home, nil
	}
	current, err := osuser.Current()
	if err != nil || strings.TrimSpace(current.HomeDir) == "" {
		if err != nil {
			return "", err
		}
		return "", errors.New("account home directory is empty")
	}
	return current.HomeDir, nil
}
