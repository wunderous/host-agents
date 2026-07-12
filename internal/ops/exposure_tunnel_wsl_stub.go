//go:build !linux

package ops

import "fmt"

func isRunningInWSL() bool {
	return false
}

func ensureWindowsCloudflaredViaWSL(_ *HostOperationsService, _ EnsureCloudflaredTunnelArgs) (*EnsureCloudflaredTunnelResult, error) {
	return nil, fmt.Errorf("WSL cloudflared delegation is only available on Linux")
}

func isWSLWindowsCloudflaredRunning(_ string) bool {
	return false
}

func stopWSLWindowsCloudflaredTunnel(_ string) error {
	return nil
}
