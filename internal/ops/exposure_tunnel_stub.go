//go:build !windows && !linux

package ops

func ensureWindowsCloudflaredTunnel(args EnsureCloudflaredTunnelArgs) (*EnsureCloudflaredTunnelResult, error) {
	return nil, nil
}

func stopWindowsCloudflaredTunnel(bindingID string) error {
	return nil
}

func isWindowsTunnelConnected(_ string) bool {
	return false
}

func ensureNativeLinuxCloudflared(_ *HostOperationsService, _ EnsureCloudflaredTunnelArgs) (*EnsureCloudflaredTunnelResult, error) {
	return nil, nil
}

func isNativeLinuxCloudflaredRunning(_ string) bool { return false }

func stopNativeLinuxCloudflaredTunnel(_ string) error { return nil }
