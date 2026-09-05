package fingerprint

import (
	"os"
	"path/filepath"
	"testing"
)

// A WSL host must keep its identity across a WSL restart. Interop is a
// per-session capability, so the first start after a restart cannot read the
// MachineGuid; before the cache existed the agent exited with
// "MachineGuid not found" and crash-looped, taking every typed capability with
// it.
func TestCachedMachineGUIDSurvivesAnUnavailableInterop(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	writeCachedMachineGUID(SourceWindowsMachineGUIDViaWSL, "  4C4C4544-0043-3010-8046-C4C04F464331  ")

	value, ok := readCachedMachineGUID(SourceWindowsMachineGUIDViaWSL)
	if !ok {
		t.Fatal("a GUID written by a successful live read was not readable back")
	}
	if value != "4C4C4544-0043-3010-8046-C4C04F464331" {
		t.Fatalf("cached GUID was not stored verbatim after trimming: %q", value)
	}

	// The digest is over version+source+value, so a reused GUID must produce
	// exactly the fingerprint the live read produced -- a cache that changed
	// the fingerprint would silently re-enroll the host as a new one.
	live := format(SourceWindowsMachineGUIDViaWSL, "4C4C4544-0043-3010-8046-C4C04F464331")
	fromCache := format(SourceWindowsMachineGUIDViaWSL, value)
	if live.Fingerprint != fromCache.Fingerprint {
		t.Fatalf("cached identity changed the fingerprint: live %q vs cached %q", live.Fingerprint, fromCache.Fingerprint)
	}
}

// The cache is an availability aid, not a way around the fail-closed contract.
func TestCachedMachineGUIDIsIgnoredForAnotherSourceOrVersion(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)

	if _, ok := readCachedMachineGUID(SourceWindowsMachineGUIDViaWSL); ok {
		t.Fatal("an empty state directory reported a cached identity")
	}

	writeCachedMachineGUID(SourceWindowsMachineGUIDViaWSL, "guid-value")
	if _, ok := readCachedMachineGUID(SourceLinuxMachineID); ok {
		t.Fatal("a GUID cached for WSL interop was honoured for a different source")
	}

	path := filepath.Join(state, "opute", "host-fingerprint.json")
	if err := os.WriteFile(path, []byte(`{"fingerprintVersion":"v1","source":"windows-machine-guid-via-wsl","value":"stale"}`), 0o600); err != nil {
		t.Fatalf("write stale cache: %v", err)
	}
	if _, ok := readCachedMachineGUID(SourceWindowsMachineGUIDViaWSL); ok {
		t.Fatal("a cache from an older fingerprint version was honoured")
	}

	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write corrupt cache: %v", err)
	}
	if _, ok := readCachedMachineGUID(SourceWindowsMachineGUIDViaWSL); ok {
		t.Fatal("a corrupt cache file was honoured")
	}
}