package heartbeat

import (
	"bufio"
	"math"
	"os"
	"runtime"
	"strconv"
	"strings"
)

// HostSystemStats describes static host CPU and memory capacity.
type HostSystemStats struct {
	CPUCount             int
	CPUQuotaCores        float64
	CPULoad1m            float64
	CPULoad5m            float64
	CPULoad15m           float64
	MemoryTotalBytes     int64
	MemoryFreeBytes      int64 // Backward-compatible alias for MemAvailable.
	MemoryAvailableBytes int64
	MemoryUsedBytes      int64
	MemoryLimitBytes     int64
	MemoryUsageBytes     int64
	MemoryPressure       string
	DiskTotalBytes       int64
	DiskAvailableBytes   int64
	DiskPressure         string
	DiskMount            string
	DiskFilesystems      []HostDiskStats
}

// HostVMStats describes Incus VM inventory totals for heartbeat metrics.
type HostVMStats struct {
	RunningVMCount            int
	TotalVMCount              int
	RunningVMCPULimitCores    int
	TotalVMCPULimitCores      int
	RunningVMMemoryLimitBytes int64
	TotalVMMemoryLimitBytes   int64
	RunningVMDiskLimitBytes   int64
	TotalVMDiskLimitBytes     int64
	RunningQEMUCount          int
	TotalQEMUCount            int
	RunningContainerCount     int
	TotalContainerCount       int
}

// CollectVMStats returns running/total VM counts (best-effort).
type CollectVMStats func() (HostVMStats, error)

// ReadHostSystemStats reads CPU count and memory totals from the host OS.
func ReadHostSystemStats() HostSystemStats {
	return ReadHostSystemStatsForPaths(defaultDiskPaths())
}

// ReadHostSystemStatsForPaths reads host resource telemetry while allowing
// callers such as the admission coordinator to include configured mounts.
func ReadHostSystemStatsForPaths(paths []string) HostSystemStats {
	stats := HostSystemStats{
		CPUCount: runtime.NumCPU(),
	}
	if memTotal, memFree, ok := readLinuxMemInfo(); ok {
		stats.MemoryTotalBytes = memTotal
		stats.MemoryFreeBytes = memFree
		stats.MemoryAvailableBytes = memFree
		stats.MemoryUsedBytes = memTotal - memFree
		if stats.MemoryUsedBytes < 0 {
			stats.MemoryUsedBytes = 0
		}
		stats.MemoryPressure = memoryPressure(memFree, memTotal)
	}
	stats.CPUQuotaCores = readCPUQuotaCores()
	stats.CPULoad1m, stats.CPULoad5m, stats.CPULoad15m = readLoadAverage()
	stats.MemoryLimitBytes = readCgroupMemoryValue("memory.max", "memory.limit_in_bytes")
	stats.MemoryUsageBytes = readCgroupMemoryValue("memory.current", "memory.usage_in_bytes")
	stats.DiskFilesystems = readDiskStats(paths)
	for _, disk := range stats.DiskFilesystems {
		if stats.DiskMount == "" || disk.AvailableBytes < stats.DiskAvailableBytes {
			stats.DiskMount = disk.Mount
			stats.DiskTotalBytes = disk.TotalBytes
			stats.DiskAvailableBytes = disk.AvailableBytes
			stats.DiskPressure = disk.Pressure
		}
	}
	return stats
}

// ReadHostSystemMetadata returns the JSON-safe resource snapshot used by
// direct host MCP responses. Heartbeats use the same shape.
func ReadHostSystemMetadata() map[string]any {
	return systemMetadata(ReadHostSystemStats())
}

func memoryPressure(availableBytes, totalBytes int64) string {
	if totalBytes <= 0 {
		return "unknown"
	}
	ratio := float64(availableBytes) / float64(totalBytes)
	switch {
	case ratio < 0.10:
		return "critical"
	case ratio < 0.20:
		return "warning"
	default:
		return "normal"
	}
}

func diskPressure(availableBytes, totalBytes int64) string {
	if totalBytes <= 0 {
		return "unknown"
	}
	ratio := float64(availableBytes) / float64(totalBytes)
	switch {
	case ratio < 0.05:
		return "critical"
	case ratio < 0.10:
		return "warning"
	default:
		return "normal"
	}
}

func readLoadAverage() (float64, float64, float64) {
	contents, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0, 0, 0
	}
	fields := strings.Fields(string(contents))
	if len(fields) < 3 {
		return 0, 0, 0
	}
	values := [3]float64{}
	for i := range values {
		parsed, err := strconv.ParseFloat(fields[i], 64)
		if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) || parsed < 0 {
			return 0, 0, 0
		}
		values[i] = parsed
	}
	return values[0], values[1], values[2]
}

func readCPUQuotaCores() float64 {
	if contents, err := os.ReadFile("/sys/fs/cgroup/cpu.max"); err == nil {
		fields := strings.Fields(string(contents))
		if len(fields) >= 2 && fields[0] != "max" {
			quota, quotaErr := strconv.ParseFloat(fields[0], 64)
			period, periodErr := strconv.ParseFloat(fields[1], 64)
			if quotaErr == nil && periodErr == nil && quota > 0 && period > 0 {
				return quota / period
			}
		}
	}
	quota := readCgroupFile("cpu.cfs_quota_us")
	period := readCgroupFile("cpu.cfs_period_us")
	if quota <= 0 || period <= 0 {
		return 0
	}
	return quota / period
}

func readCgroupMemoryValue(v2Name, v1Name string) int64 {
	for _, path := range []string{"/sys/fs/cgroup/" + v2Name, "/sys/fs/cgroup/memory/" + v1Name} {
		contents, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		value := strings.TrimSpace(string(contents))
		if value == "" || value == "max" {
			return 0
		}
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err == nil && parsed > 0 {
			return parsed
		}
	}
	return 0
}

func readCgroupFile(name string) float64 {
	for _, path := range []string{"/sys/fs/cgroup/" + name, "/sys/fs/cgroup/cpu/" + name} {
		contents, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		parsed, err := strconv.ParseFloat(strings.TrimSpace(string(contents)), 64)
		if err == nil {
			return parsed
		}
	}
	return 0
}

func readLinuxMemInfo() (totalBytes int64, freeBytes int64, ok bool) {
	file, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0, false
	}
	defer file.Close()

	var memTotalKB, memAvailableKB int64
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "MemTotal:"):
			memTotalKB = parseMeminfoKB(line)
		case strings.HasPrefix(line, "MemAvailable:"):
			memAvailableKB = parseMeminfoKB(line)
		}
	}
	if memTotalKB <= 0 {
		return 0, 0, false
	}
	if memAvailableKB <= 0 {
		memAvailableKB = 0
	}
	return memTotalKB * 1024, memAvailableKB * 1024, true
}

func parseMeminfoKB(line string) int64 {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return 0
	}
	value, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil {
		return 0
	}
	return value
}

func systemMetadata(stats HostSystemStats) map[string]any {
	metadata := map[string]any{}
	if stats.CPUCount > 0 {
		metadata["cpuCount"] = stats.CPUCount
	}
	if stats.MemoryTotalBytes > 0 {
		metadata["memoryTotalBytes"] = stats.MemoryTotalBytes
	}
	if stats.MemoryFreeBytes > 0 {
		metadata["memoryFreeBytes"] = stats.MemoryFreeBytes
	}
	if stats.MemoryAvailableBytes > 0 {
		metadata["memoryAvailableBytes"] = stats.MemoryAvailableBytes
	}
	if stats.CPUQuotaCores > 0 {
		metadata["cpuQuotaCores"] = stats.CPUQuotaCores
	}
	if stats.CPULoad1m > 0 {
		metadata["cpuLoad1m"] = stats.CPULoad1m
	}
	if stats.CPULoad5m > 0 {
		metadata["cpuLoad5m"] = stats.CPULoad5m
	}
	if stats.CPULoad15m > 0 {
		metadata["cpuLoad15m"] = stats.CPULoad15m
	}
	if stats.MemoryUsedBytes > 0 {
		metadata["memoryUsedBytes"] = stats.MemoryUsedBytes
	}
	if stats.MemoryLimitBytes > 0 {
		metadata["memoryLimitBytes"] = stats.MemoryLimitBytes
	}
	if stats.MemoryUsageBytes > 0 {
		metadata["memoryUsageBytes"] = stats.MemoryUsageBytes
	}
	if stats.MemoryPressure != "" {
		metadata["memoryPressure"] = stats.MemoryPressure
	}
	if stats.DiskTotalBytes > 0 {
		metadata["diskTotalBytes"] = stats.DiskTotalBytes
	}
	if stats.DiskAvailableBytes > 0 {
		metadata["diskAvailableBytes"] = stats.DiskAvailableBytes
	}
	if stats.DiskPressure != "" {
		metadata["diskPressure"] = stats.DiskPressure
	}
	if stats.DiskMount != "" {
		metadata["diskMount"] = stats.DiskMount
	}
	if len(stats.DiskFilesystems) > 0 {
		filesystems := make([]map[string]any, 0, len(stats.DiskFilesystems))
		for _, disk := range stats.DiskFilesystems {
			filesystems = append(filesystems, map[string]any{
				"mount":          disk.Mount,
				"totalBytes":     disk.TotalBytes,
				"availableBytes": disk.AvailableBytes,
				"pressure":       disk.Pressure,
			})
		}
		metadata["diskFilesystems"] = filesystems
	}
	if len(metadata) == 0 {
		return nil
	}
	return metadata
}

func vmMetrics(stats HostVMStats) map[string]any {
	metrics := map[string]any{}
	if stats.TotalVMCount >= 0 {
		metrics["totalVmCount"] = stats.TotalVMCount
	}
	if stats.RunningVMCount >= 0 {
		metrics["runningVmCount"] = stats.RunningVMCount
	}
	metrics["runningVmCpuLimitCores"] = stats.RunningVMCPULimitCores
	metrics["totalVmCpuLimitCores"] = stats.TotalVMCPULimitCores
	metrics["runningVmMemoryLimitBytes"] = stats.RunningVMMemoryLimitBytes
	metrics["totalVmMemoryLimitBytes"] = stats.TotalVMMemoryLimitBytes
	metrics["runningVmDiskLimitBytes"] = stats.RunningVMDiskLimitBytes
	metrics["totalVmDiskLimitBytes"] = stats.TotalVMDiskLimitBytes
	metrics["runningQemuCount"] = stats.RunningQEMUCount
	metrics["totalQemuCount"] = stats.TotalQEMUCount
	metrics["runningContainerCount"] = stats.RunningContainerCount
	metrics["totalContainerCount"] = stats.TotalContainerCount
	if len(metrics) == 0 {
		return nil
	}
	return metrics
}
