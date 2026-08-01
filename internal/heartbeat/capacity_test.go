package heartbeat

import "testing"

func TestSystemMetadata(t *testing.T) {
	meta := systemMetadata(HostSystemStats{
		CPUCount:             8,
		CPUQuotaCores:        3.5,
		CPULoad1m:            1.25,
		MemoryTotalBytes:     16_000_000_000,
		MemoryFreeBytes:      8_000_000_000,
		MemoryAvailableBytes: 8_000_000_000,
		MemoryUsedBytes:      8_000_000_000,
		MemoryLimitBytes:     12_000_000_000,
		MemoryUsageBytes:     7_000_000_000,
		MemoryPressure:       "normal",
		DiskTotalBytes:       100_000_000_000,
		DiskAvailableBytes:   40_000_000_000,
		DiskPressure:         "normal",
		DiskMount:            "/",
		DiskFilesystems: []HostDiskStats{{
			Mount: "/", TotalBytes: 100_000_000_000, AvailableBytes: 40_000_000_000, Pressure: "normal",
		}},
	})
	if meta["cpuCount"] != 8 {
		t.Fatalf("cpuCount = %#v", meta["cpuCount"])
	}
	if meta["memoryTotalBytes"] != int64(16_000_000_000) {
		t.Fatalf("memoryTotalBytes = %#v", meta["memoryTotalBytes"])
	}
	if meta["cpuQuotaCores"] != 3.5 || meta["memoryPressure"] != "normal" {
		t.Fatalf("missing resource telemetry: %#v", meta)
	}
	if meta["diskPressure"] != "normal" || meta["diskMount"] != "/" {
		t.Fatalf("missing disk telemetry: %#v", meta)
	}
}

func TestVMMetrics(t *testing.T) {
	metrics := vmMetrics(HostVMStats{
		RunningVMCount:            2,
		TotalVMCount:              5,
		RunningVMCPULimitCores:    6,
		TotalVMCPULimitCores:      12,
		RunningVMMemoryLimitBytes: 8 << 30,
		TotalVMMemoryLimitBytes:   16 << 30,
		RunningQEMUCount:          1,
		TotalQEMUCount:            2,
		RunningContainerCount:     1,
		TotalContainerCount:       3,
	})
	if metrics["runningVmCount"] != 2 || metrics["totalVmCount"] != 5 {
		t.Fatalf("unexpected metrics: %#v", metrics)
	}
	if metrics["runningVmCpuLimitCores"] != 6 || metrics["totalVmMemoryLimitBytes"] != int64(16<<30) {
		t.Fatalf("missing allocation metrics: %#v", metrics)
	}
	if metrics["runningQemuCount"] != 1 || metrics["totalContainerCount"] != 3 {
		t.Fatalf("missing instance mix metrics: %#v", metrics)
	}
}

func TestMemoryPressure(t *testing.T) {
	if got := memoryPressure(1, 20); got != "critical" {
		t.Fatalf("critical pressure = %q", got)
	}
	if got := memoryPressure(3, 20); got != "warning" {
		t.Fatalf("warning pressure = %q", got)
	}
	if got := memoryPressure(10, 20); got != "normal" {
		t.Fatalf("normal pressure = %q", got)
	}
}

func TestDiskPressure(t *testing.T) {
	if got := diskPressure(1, 100); got != "critical" {
		t.Fatalf("critical pressure = %q", got)
	}
	if got := diskPressure(9, 100); got != "warning" {
		t.Fatalf("warning pressure = %q", got)
	}
	if got := diskPressure(20, 100); got != "normal" {
		t.Fatalf("normal pressure = %q", got)
	}
}

func TestReadHostSystemStats(t *testing.T) {
	stats := ReadHostSystemStats()
	if stats.CPUCount <= 0 {
		t.Fatalf("expected cpu count > 0, got %d", stats.CPUCount)
	}
}
