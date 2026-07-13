//go:build !linux

package ops

import "fmt"

func isRunningInWSL() bool {
	return false
}

func ensureWindowsCloudflaredViaWSL(_ *HostOperationsService, _ EnsureCloudflaredTunnelArgs) (*EnsureCloudflaredTunnelResult, error) {
	return nil, fmt.Errorf("WSL cloudflared delegation is only available on Linux")
}

func ensureQuickCloudflaredViaWSL(_ *HostOperationsService, _ EnsureCloudflaredTunnelArgs) (*EnsureCloudflaredTunnelResult, error) {
	return nil, fmt.Errorf("WSL cloudflared delegation is only available on Linux")
}

func useNativeWSLCloudflared() bool { return false }

func ensureNativeWSLCloudflared(_ *HostOperationsService, _ EnsureCloudflaredTunnelArgs) (*EnsureCloudflaredTunnelResult, error) {
	return nil, fmt.Errorf("native WSL cloudflared is only available on Linux")
}

func isNativeWSLCloudflaredRunning(_ string) bool { return false }

func stopNativeWSLCloudflaredTunnel(_ string) error { return nil }

func isWSLWindowsCloudflaredRunning(_ string) bool {
	return false
}

func stopWSLWindowsCloudflaredTunnel(_ string) error {
	return nil
}
