package fingerprint

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// Version changes whenever the meaning of a physical identity changes. v2
// deliberately distinguishes WSL's Windows installation identity from the
// distro-local execution context.
const Version = "v2"

type Source string

const (
	SourceLinuxMachineID           Source = "linux-machine-id"
	SourceWindowsMachineGUID       Source = "windows-machine-guid"
	SourceWindowsMachineGUIDViaWSL Source = "windows-machine-guid-via-wsl"
	SourceMacOSPlatformUUID        Source = "macos-io-platform-uuid"
)

type ExecutionContextKind string

const (
	ExecutionContextNativeWindows ExecutionContextKind = "native-windows"
	ExecutionContextWSL           ExecutionContextKind = "wsl"
	ExecutionContextNativeLinux   ExecutionContextKind = "native-linux"
	ExecutionContextMacOS         ExecutionContextKind = "macos"
)

type ExecutionContext struct {
	ID          string               `json:"id"`
	Kind        ExecutionContextKind `json:"kind"`
	DisplayName string               `json:"displayName,omitempty"`
}

type Identity struct {
	Fingerprint        string           `json:"fingerprint"`
	FingerprintVersion string           `json:"fingerprintVersion"`
	FingerprintSource  Source           `json:"fingerprintSource"`
	ExecutionContext   ExecutionContext `json:"executionContext"`
}

func ReadIdentity() (Identity, error) {
	switch runtime.GOOS {
	case "linux":
		context, err := readLinuxExecutionContext()
		if err != nil {
			return Identity{}, err
		}
		if context.Kind == ExecutionContextWSL {
			guid, err := readWindowsMachineGUID()
			if err != nil {
				// Interop is a per-session capability and a systemd user unit
				// never holds a live WSL_INTEROP socket, so a permanent
				// identity must not be lost with the session that first read
				// it. A GUID acquired earlier under this same version and
				// source is reused; with no such record the original
				// fail-closed error still stands.
				cached, ok := readCachedMachineGUID(SourceWindowsMachineGUIDViaWSL)
				if !ok {
					return Identity{}, fmt.Errorf("read Windows MachineGuid through WSL interop: %w", err)
				}
				guid = cached
			} else {
				writeCachedMachineGUID(SourceWindowsMachineGUIDViaWSL, guid)
			}
			identity := format(SourceWindowsMachineGUIDViaWSL, guid)
			identity.ExecutionContext = context
			return identity, nil
		}
		raw, err := os.ReadFile("/etc/machine-id")
		if err != nil {
			return Identity{}, err
		}
		v := strings.TrimSpace(string(raw))
		if v == "" {
			return Identity{}, fmt.Errorf("empty /etc/machine-id")
		}
		identity := format(SourceLinuxMachineID, v)
		identity.ExecutionContext = context
		return identity, nil
	case "windows":
		guid, err := readWindowsMachineGUID()
		if err != nil {
			return Identity{}, err
		}
		identity := format(SourceWindowsMachineGUID, guid)
		identity.ExecutionContext = ExecutionContext{ID: string(ExecutionContextNativeWindows), Kind: ExecutionContextNativeWindows, DisplayName: "Windows"}
		return identity, nil
	case "darwin":
		uuid, err := readMacPlatformUUID()
		if err != nil {
			return Identity{}, err
		}
		identity := format(SourceMacOSPlatformUUID, uuid)
		identity.ExecutionContext = ExecutionContext{ID: "native-macos", Kind: ExecutionContextMacOS, DisplayName: "macOS"}
		return identity, nil
	default:
		return Identity{}, fmt.Errorf("unsupported platform %s", runtime.GOOS)
	}
}

func readLinuxExecutionContext() (ExecutionContext, error) {
	raw, err := os.ReadFile("/etc/machine-id")
	if err != nil {
		return ExecutionContext{}, err
	}
	machineID := strings.TrimSpace(string(raw))
	if machineID == "" {
		return ExecutionContext{}, fmt.Errorf("empty /etc/machine-id for execution context")
	}
	if isWSL() {
		digest := sha256.Sum256([]byte("execution-context:" + Version + ":wsl:" + strings.ToLower(machineID)))
		return ExecutionContext{
			ID:          "wsl:" + hex.EncodeToString(digest[:]),
			Kind:        ExecutionContextWSL,
			DisplayName: strings.TrimSpace(os.Getenv("WSL_DISTRO_NAME")),
		}, nil
	}
	return ExecutionContext{ID: "native-linux", Kind: ExecutionContextNativeLinux, DisplayName: "Linux"}, nil
}

func isWSL() bool {
	if strings.TrimSpace(os.Getenv("WSL_INTEROP")) != "" || strings.TrimSpace(os.Getenv("WSL_DISTRO_NAME")) != "" {
		return true
	}
	raw, err := os.ReadFile("/proc/version")
	if err != nil {
		return false
	}
	value := strings.ToLower(string(raw))
	return strings.Contains(value, "microsoft") || strings.Contains(value, "wsl")
}

func format(source Source, value string) Identity {
	normalized := strings.ToLower(strings.TrimSpace(value))
	physicalSource := source
	if source == SourceWindowsMachineGUIDViaWSL {
		// Native Windows and WSL interop read the same Windows installation
		// MachineGuid. Preserve the acquisition source in evidence, but use one
		// canonical physical digest for parent grouping.
		physicalSource = SourceWindowsMachineGUID
	}
	digest := sha256.Sum256([]byte(fmt.Sprintf("%s:%s:%s", Version, physicalSource, normalized)))
	return Identity{
		Fingerprint:        fmt.Sprintf("host:%s:%s", Version, hex.EncodeToString(digest[:])),
		FingerprintVersion: Version,
		FingerprintSource:  source,
	}
}

func readWindowsMachineGUID() (string, error) {
	commands := windowsMachineGUIDCommands()
	var lastErr error
	for _, argv := range commands {
		cmd := exec.Command(argv[0], argv[1:]...)
		out, err := cmd.Output()
		if err != nil {
			lastErr = err
			continue
		}
		if len(argv) > 1 && argv[1] == "-NoProfile" {
			if value := strings.TrimSpace(string(out)); value != "" {
				return value, nil
			}
		}
		for _, line := range strings.Split(string(out), "\n") {
			parts := strings.Fields(strings.TrimSpace(line))
			if len(parts) >= 3 && strings.EqualFold(parts[0], "MachineGuid") {
				return parts[len(parts)-1], nil
			}
		}
	}
	if lastErr != nil {
		return "", fmt.Errorf("MachineGuid not found: %w", lastErr)
	}
	return "", fmt.Errorf("MachineGuid not found")
}

func windowsMachineGUIDCommands() [][]string {
	powershellArgs := []string{
		"-NoProfile",
		"-NonInteractive",
		"-Command",
		"(Get-ItemProperty -Path 'HKLM:\\SOFTWARE\\Microsoft\\Cryptography' -Name MachineGuid).MachineGuid",
	}
	regArgs := []string{"query", `HKLM\SOFTWARE\Microsoft\Cryptography`, "/v", "MachineGuid"}

	// systemd services in WSL do not inherit the interactive Windows interop
	// directories in PATH. Prefer their stable mounted locations, then retain
	// PATH lookup for interactive shells and alternate WSL configurations.
	return [][]string{
		append([]string{"/mnt/c/Windows/System32/WindowsPowerShell/v1.0/powershell.exe"}, powershellArgs...),
		append([]string{"/mnt/c/Windows/System32/reg.exe"}, regArgs...),
		append([]string{"/mnt/c/Windows/system32/reg.exe"}, regArgs...),
		append([]string{"powershell.exe"}, powershellArgs...),
		append([]string{"reg.exe"}, regArgs...),
		append([]string{"reg"}, regArgs...),
	}
}

func readMacPlatformUUID() (string, error) {
	cmd := exec.Command("/usr/sbin/ioreg", "-rd1", "-c", "IOPlatformExpertDevice")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	const key = `"IOPlatformUUID" = "`
	for _, line := range strings.Split(string(out), "\n") {
		if idx := strings.Index(line, key); idx >= 0 {
			rest := line[idx+len(key):]
			if end := strings.Index(rest, `"`); end >= 0 {
				return rest[:end], nil
			}
		}
	}
	return "", fmt.Errorf("IOPlatformUUID not found")
}
