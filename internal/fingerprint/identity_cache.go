package fingerprint

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// The Windows installation GUID is a permanent fact, but WSL interop is a
// per-session capability: the binfmt_misc handler needs the WSL_INTEROP socket
// of a live session, which a long-lived systemd user unit never holds. Reading
// the GUID live on every start therefore made a stable identity depend on a
// volatile artifact -- after a WSL restart the agent crash-looped on
// "MachineGuid not found" (restart counter 42), and the absolute-path
// candidates in windowsMachineGUIDCommands could not help, because the failure
// is in interop itself rather than in PATH resolution. Importing a shell's
// WSL_INTEROP into the user manager does not fix it either: that socket belongs
// to another session and answers "Invalid argument".
//
// The GUID is cached on first successful read and reused when interop is
// unavailable. This does not weaken the fail-closed contract: an identity that
// was never acquired still returns the original error, and a cached entry is
// honoured only when it records the same fingerprint version and source that a
// live read would have produced -- so it can never change a host's fingerprint,
// only avoid losing it.
type cachedIdentity struct {
	FingerprintVersion string `json:"fingerprintVersion"`
	Source             Source `json:"source"`
	Value              string `json:"value"`
}

func machineGUIDCachePath() (string, error) {
	base := strings.TrimSpace(os.Getenv("XDG_STATE_HOME"))
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, "opute", "host-fingerprint.json"), nil
}

// readCachedMachineGUID returns a previously acquired GUID for exactly this
// fingerprint version and source. Any mismatch is treated as no cache at all.
func readCachedMachineGUID(source Source) (string, bool) {
	path, err := machineGUIDCachePath()
	if err != nil {
		return "", false
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	var entry cachedIdentity
	if err := json.Unmarshal(raw, &entry); err != nil {
		return "", false
	}
	if entry.FingerprintVersion != Version || entry.Source != source {
		return "", false
	}
	value := strings.TrimSpace(entry.Value)
	if value == "" {
		return "", false
	}
	return value, true
}

// writeCachedMachineGUID records a live read. Failures are deliberately silent:
// the cache is an availability aid, and a host that just read its identity
// successfully must not be prevented from starting by an unwritable state dir.
func writeCachedMachineGUID(source Source, value string) {
	path, err := machineGUIDCachePath()
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	raw, err := json.Marshal(cachedIdentity{
		FingerprintVersion: Version,
		Source:             source,
		Value:              strings.TrimSpace(value),
	})
	if err != nil {
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
	}
}