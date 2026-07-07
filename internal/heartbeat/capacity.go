package heartbeat

import (
	"bufio"
	"os"
	"runtime"
	"strconv"
	"strings"
)

// HostSystemStats describes static host CPU and memory capacity.
type HostSystemStats struct {
	CPUCount          int
	MemoryTotalBytes  int64
	MemoryFreeBytes   int64
}

// HostVMStats describes Incus VM inventory totals for heartbeat metrics.
type HostVMStats struct {
	RunningVMCount int
	TotalVMCount   int
}

// CollectVMStats returns running/total VM counts (best-effort).
type CollectVMStats func() (HostVMStats, error)

// ReadHostSystemStats reads CPU count and memory totals from the host OS.
func ReadHostSystemStats() HostSystemStats {
	stats := HostSystemStats{
		CPUCount: runtime.NumCPU(),
	}
	if memTotal, memFree, ok := readLinuxMemInfo(); ok {
		stats.MemoryTotalBytes = memTotal
		stats.MemoryFreeBytes = memFree
	}
	return stats
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
	if len(metrics) == 0 {
		return nil
	}
	return metrics
}
