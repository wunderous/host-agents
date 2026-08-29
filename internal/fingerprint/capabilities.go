package fingerprint

import (
	"os/exec"
	"runtime"
)

// Capabilities are tested observations, not authorization grants. The
// platform must still authorize each lifecycle operation and bind it to the
// exact HostAgentId.
type Capabilities struct {
	CanInvokeWindowsInterop     bool `json:"canInvokeWindowsInterop"`
	CanManageWSL                bool `json:"canManageWsl"`
	CanTerminateWSLDistribution bool `json:"canTerminateWslDistribution"`
	CanShutdownWSL              bool `json:"canShutdownWsl"`
}

func DetectCapabilities() Capabilities {
	interop := false
	if runtime.GOOS == "windows" {
		interop = true
	} else if runtime.GOOS == "linux" && isWSL() {
		_, powershellAvailable := WindowsInteropCommand("powershell.exe")
		_, regAvailable := WindowsInteropCommand("reg.exe")
		interop = powershellAvailable || regAvailable
	}
	_, wslAvailable := WindowsInteropCommand("wsl.exe")
	manageWSL := (runtime.GOOS == "windows" || (runtime.GOOS == "linux" && isWSL())) && wslAvailable
	return Capabilities{
		CanInvokeWindowsInterop:     interop,
		CanManageWSL:                manageWSL,
		CanTerminateWSLDistribution: manageWSL,
		CanShutdownWSL:              manageWSL,
	}
}

func commandAvailable(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// WindowsInteropCommand resolves the executable used for Windows interop.
// WSL systemd services commonly have a Linux-only PATH, so PATH lookup alone
// is insufficient even when the mounted Windows interop binaries exist.
func WindowsInteropCommand(name string) (string, bool) {
	candidates := []string{name}
	switch name {
	case "powershell.exe":
		candidates = append([]string{
			"/mnt/c/Windows/System32/WindowsPowerShell/v1.0/powershell.exe",
		}, candidates...)
	case "reg.exe", "wsl.exe":
		candidates = append([]string{
			"/mnt/c/Windows/System32/" + name,
			"/mnt/c/Windows/system32/" + name,
		}, candidates...)
	}
	for _, candidate := range candidates {
		if commandAvailable(candidate) {
			return candidate, true
		}
	}
	return "", false
}
