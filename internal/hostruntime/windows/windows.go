package windows

import "runtime"

// ProviderID is the Windows host agent provider identifier.
const ProviderID = "windows"

// IsWindows reports whether the current process runs on Windows.
func IsWindows() bool {
	return runtime.GOOS == "windows"
}

// MachineGUIDFingerprintSource documents the Windows fingerprint source string.
const MachineGUIDFingerprintSource = "windows-machine-guid"

// MachineGUIDViaWSLFingerprintSource is the explicit source used when a Linux
// agent obtains the Windows installation identity through powershell.exe or
// reg.exe interop. It must not be confused with the distro's machine-id.
const MachineGUIDViaWSLFingerprintSource = "windows-machine-guid-via-wsl"

const (
	NativeWindowsExecutionContext = "native-windows"
	WSLExecutionContext           = "wsl"
)
