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
