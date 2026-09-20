package host

import (
	"os"
	"strings"
	"testing"
)

func TestRejectLiveDistro(t *testing.T) {
	if err := rejectLiveDistro("Opute-HA-B", "Opute-HA-B"); err == nil || !strings.Contains(err.Error(), "live distro") {
		t.Fatalf("live distro: %v", err)
	}
	if err := rejectLiveDistro("Ubuntu-26.04", "Opute-HA-B"); err != nil {
		t.Fatal(err)
	}
}

func TestCompactClosedErrorCarriesReport(t *testing.T) {
	err := compactClosed(map[string]any{"distro": "Opute-HA-B", "locked": true}, wslVHDXLockedMessage)
	closed, ok := err.(*CompactClosedError)
	if !ok {
		t.Fatalf("type %T", err)
	}
	report := closed.StructuredReport()
	if report["code"] != "wsl_compact_closed" || report["locked"] != true || report["compacted"] != false || report["sparse"] != false {
		t.Fatalf("report = %#v", report)
	}
	if !strings.Contains(closed.Error(), "shutdown_wsl") {
		t.Fatalf("message = %q", closed.Error())
	}
}

func TestOptimizeVHDErrorMentionsElevation(t *testing.T) {
	err := optimizeVHDError(1, "Optimize-VHD : Access is denied")
	if err == nil || !strings.Contains(err.Error(), "Hyper-V Administrators") || !strings.Contains(err.Error(), "sparse") {
		t.Fatalf("got %v", err)
	}
}

func TestWSLDistroFromEnvironFile(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/environ"
	if err := os.WriteFile(path, []byte("HOME=/root\x00WSL_DISTRO_NAME=Ubuntu-26.04\x00PATH=/bin"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := wslDistroFromEnvironFile(path); got != "Ubuntu-26.04" {
		t.Fatalf("got %q", got)
	}
	if got := wslDistroFromEnvironFile(dir + "/missing"); got != "" {
		t.Fatalf("missing: %q", got)
	}
}

func TestCompactPreconditionsFailClosedWhenLockedOrRunning(t *testing.T) {
	if err := compactPreconditions("Running", false); err == nil || !strings.Contains(err.Error(), "shutdown_wsl") {
		t.Fatalf("running: %v", err)
	}
	if err := compactPreconditions("Stopped", true); err == nil || !strings.Contains(err.Error(), "sibling WSL VM") {
		t.Fatalf("locked: %v", err)
	}
	if err := compactPreconditions("Stopped", false); err != nil {
		t.Fatal(err)
	}
}

func TestParseWSLDistributionState(t *testing.T) {
	output := "  NAME            STATE           VERSION\n* Ubuntu-26.04    Running         2\n  Opute-HA-B      Stopped         2\n"
	state, ok := parseWSLDistributionState(output, "Opute-HA-B")
	if !ok || state != "Stopped" {
		t.Fatalf("state = %q ok=%v", state, ok)
	}
}

func TestParseWSLVHDXPath(t *testing.T) {
	output := "HKEY_CURRENT_USER\\Software\\Microsoft\\Windows\\CurrentVersion\\Lxss\\{abc}\n    DistributionName    REG_SZ    Opute-HA-B\n    BasePath            REG_SZ    D:\\opute-wsl\\Opute-HA-B\n"
	path, ok := parseWSLVHDXPath(output, "Opute-HA-B")
	if !ok || path != `D:\opute-wsl\Opute-HA-B\ext4.vhdx` {
		t.Fatalf("path = %q ok=%v", path, ok)
	}
}

func TestDecodeWindowsCLIUTF16(t *testing.T) {
	raw := string([]byte{'A', 0, 'B', 0, 'C', 0})
	if got := decodeWindowsCLI(raw); got != "ABC" {
		t.Fatalf("got %q", got)
	}
}
