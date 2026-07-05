//go:build !windows

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
