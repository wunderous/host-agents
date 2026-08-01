//go:build windows

package heartbeat

type HostDiskStats struct {
	Mount          string
	TotalBytes     int64
	AvailableBytes int64
	Pressure       string
}

func defaultDiskPaths() []string { return nil }

func readDiskStats(_ []string) []HostDiskStats { return nil }
