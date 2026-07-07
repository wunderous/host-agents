package heartbeat

import "testing"

func TestSystemMetadata(t *testing.T) {
	meta := systemMetadata(HostSystemStats{
		CPUCount:         8,
		MemoryTotalBytes: 16_000_000_000,
		MemoryFreeBytes:  8_000_000_000,
	})
	if meta["cpuCount"] != 8 {
		t.Fatalf("cpuCount = %#v", meta["cpuCount"])
	}
	if meta["memoryTotalBytes"] != int64(16_000_000_000) {
		t.Fatalf("memoryTotalBytes = %#v", meta["memoryTotalBytes"])
	}
}

func TestVMMetrics(t *testing.T) {
	metrics := vmMetrics(HostVMStats{RunningVMCount: 2, TotalVMCount: 5})
	if metrics["runningVmCount"] != 2 || metrics["totalVmCount"] != 5 {
		t.Fatalf("unexpected metrics: %#v", metrics)
	}
}

func TestReadHostSystemStats(t *testing.T) {
	stats := ReadHostSystemStats()
	if stats.CPUCount <= 0 {
		t.Fatalf("expected cpu count > 0, got %d", stats.CPUCount)
	}
}
