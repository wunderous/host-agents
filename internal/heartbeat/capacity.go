package heartbeat

import (
	"bufio"
	"math"
	"os"
	"path/filepath"
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
	MemoryEvents         map[string]int64
	PressureStalls       map[string]PressureStall
	CgroupControllers    []string
	CgroupEnforcement    string
	TasksCurrent         int64
	TasksLimit           int64
	DiskTotalBytes       int64
	DiskAvailableBytes   int64
	DiskPressure         string
	DiskMount            string
	DiskFilesystems      []HostDiskStats
}

// PressureStall is the kernel PSI reading for one resource and stall class.
// A missing field is represented by zero because PSI counters are monotonic
// and the averages are the values callers use for admission decisions.
type PressureStall struct {
	SomeAvg10     float64 `json:"someAvg10,omitempty"`
	SomeAvg60     float64 `json:"someAvg60,omitempty"`
	SomeAvg300    float64 `json:"someAvg300,omitempty"`
	SomeTotalUsec int64   `json:"someTotalUsec,omitempty"`
	FullAvg10     float64 `json:"fullAvg10,omitempty"`
	FullAvg60     float64 `json:"fullAvg60,omitempty"`
	FullAvg300    float64 `json:"fullAvg300,omitempty"`
	FullTotalUsec int64   `json:"fullTotalUsec,omitempty"`
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
	stats.MemoryLimitBytes = readCgroupMemoryLimit()
	stats.MemoryUsageBytes = readCgroupMemoryUsage()
	stats.MemoryEvents = readMemoryEvents()
	stats.PressureStalls = readPressureStalls()
	stats.CgroupControllers, stats.CgroupEnforcement = readCgroupEnforcement()
	stats.TasksCurrent, stats.TasksLimit = readCgroupTasks()
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
	var effectiveQuota float64
	for _, directory := range currentCgroupDirectories() {
		if contents, err := os.ReadFile(filepath.Join(directory, "cpu.max")); err == nil {
			fields := strings.Fields(string(contents))
			if len(fields) >= 2 && fields[0] != "max" {
				quota, quotaErr := strconv.ParseFloat(fields[0], 64)
				period, periodErr := strconv.ParseFloat(fields[1], 64)
				if quotaErr == nil && periodErr == nil && quota > 0 && period > 0 {
					cores := quota / period
					if effectiveQuota == 0 || cores < effectiveQuota {
						effectiveQuota = cores
					}
				}
			}
		}
	}
	if effectiveQuota > 0 {
		return effectiveQuota
	}
	quota := readCgroupFile("cpu.cfs_quota_us")
	period := readCgroupFile("cpu.cfs_period_us")
	if quota <= 0 || period <= 0 {
		return 0
	}
	return quota / period
}

func readCgroupMemoryLimit() int64 {
	var effectiveLimit int64
	for _, directory := range currentCgroupDirectories() {
		for _, name := range []string{"memory.max", "memory.limit_in_bytes"} {
			contents, err := os.ReadFile(filepath.Join(directory, name))
			if err != nil {
				continue
			}
			value := strings.TrimSpace(string(contents))
			if value == "" || value == "max" {
				continue
			}
			parsed, err := strconv.ParseInt(value, 10, 64)
			if err == nil && parsed > 0 {
				if effectiveLimit == 0 || parsed < effectiveLimit {
					effectiveLimit = parsed
				}
			}
		}
	}
	return effectiveLimit
}

func readCgroupMemoryUsage() int64 {
	for _, directory := range currentCgroupDirectories() {
		for _, name := range []string{"memory.current", "memory.usage_in_bytes"} {
			contents, err := os.ReadFile(filepath.Join(directory, name))
			if err != nil {
				continue
			}
			parsed, err := strconv.ParseInt(strings.TrimSpace(string(contents)), 10, 64)
			if err == nil && parsed >= 0 {
				return parsed
			}
		}
	}
	return 0
}

func readCgroupFile(name string) float64 {
	for _, directory := range currentCgroupDirectories() {
		contents, err := os.ReadFile(filepath.Join(directory, name))
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

func readPressureStalls() map[string]PressureStall {
	result := make(map[string]PressureStall)
	for _, resource := range []string{"cpu", "memory", "io"} {
		contents, err := os.ReadFile("/proc/pressure/" + resource)
		if err != nil {
			continue
		}
		var stall PressureStall
		for _, line := range strings.Split(string(contents), "\n") {
			fields := strings.Fields(line)
			if len(fields) < 2 {
				continue
			}
			full := fields[0] == "full"
			if fields[0] != "some" && !full {
				continue
			}
			for _, field := range fields[1:] {
				parts := strings.SplitN(field, "=", 2)
				if len(parts) != 2 {
					continue
				}
				value, err := strconv.ParseFloat(parts[1], 64)
				if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
					continue
				}
				switch parts[0] {
				case "avg10":
					if full {
						stall.FullAvg10 = value
					} else {
						stall.SomeAvg10 = value
					}
				case "avg60":
					if full {
						stall.FullAvg60 = value
					} else {
						stall.SomeAvg60 = value
					}
				case "avg300":
					if full {
						stall.FullAvg300 = value
					} else {
						stall.SomeAvg300 = value
					}
				case "total":
					if full {
						stall.FullTotalUsec = int64(value)
					} else {
						stall.SomeTotalUsec = int64(value)
					}
				}
			}
		}
		if stall != (PressureStall{}) {
			result[resource] = stall
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func readMemoryEvents() map[string]int64 {
	for _, directory := range currentCgroupDirectories() {
		contents, err := os.ReadFile(filepath.Join(directory, "memory.events"))
		if err != nil {
			continue
		}
		result := make(map[string]int64)
		for _, line := range strings.Split(string(contents), "\n") {
			fields := strings.Fields(line)
			if len(fields) != 2 {
				continue
			}
			value, err := strconv.ParseInt(fields[1], 10, 64)
			if err == nil && value >= 0 {
				result[fields[0]] = value
			}
		}
		if len(result) > 0 {
			return result
		}
	}
	return nil
}

func readCgroupEnforcement() ([]string, string) {
	for _, directory := range currentCgroupDirectories() {
		controllersRaw, err := os.ReadFile(filepath.Join(directory, "cgroup.controllers"))
		if err == nil {
			controllers := strings.Fields(string(controllersRaw))
			if len(controllers) == 0 {
				return controllers, "unknown"
			}
			hasMemory := containsString(controllers, "memory")
			hasCPU := containsString(controllers, "cpu")
			_, memoryMaxErr := os.Stat(filepath.Join(directory, "memory.max"))
			_, memoryCurrentErr := os.Stat(filepath.Join(directory, "memory.current"))
			_, cpuMaxErr := os.Stat(filepath.Join(directory, "cpu.max"))
			if hasMemory && hasCPU && memoryMaxErr == nil && memoryCurrentErr == nil && cpuMaxErr == nil {
				return controllers, "enforced"
			}
			return controllers, "unknown"
		}

		// cgroup v1 has no cgroup.controllers file. Detect its two controls
		// from the controller-specific mount prepared by currentCgroupDirectories.
		if fileExists(filepath.Join(directory, "memory.limit_in_bytes")) &&
			fileExists(filepath.Join(directory, "cpu.cfs_quota_us")) {
			return []string{"cpu", "memory"}, "enforced"
		}
	}
	return nil, "unsupported"
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func readCgroupTasks() (int64, int64) {
	current := readCgroupInteger("pids.current")
	limit := readCgroupInteger("pids.max")
	return current, limit
}

func readCgroupInteger(name string) (value int64) {
	if name == "pids.max" {
		var effectiveLimit int64
		for _, directory := range currentCgroupDirectories() {
			contents, err := os.ReadFile(filepath.Join(directory, name))
			if err != nil || strings.TrimSpace(string(contents)) == "max" {
				continue
			}
			parsed, err := strconv.ParseInt(strings.TrimSpace(string(contents)), 10, 64)
			if err == nil && parsed > 0 && (effectiveLimit == 0 || parsed < effectiveLimit) {
				effectiveLimit = parsed
			}
		}
		return effectiveLimit
	}
	for _, directory := range currentCgroupDirectories() {
		contents, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil {
			continue
		}
		text := strings.TrimSpace(string(contents))
		if text == "max" {
			return 0
		}
		parsed, err := strconv.ParseInt(text, 10, 64)
		if err == nil && parsed >= 0 {
			return parsed
		}
	}
	return 0
}

const cgroupMountRoot = "/sys/fs/cgroup"

// currentCgroupDirectories resolves the cgroup directory containing this
// process before falling back to the mount root. WSL's cgroup v2 root can
// advertise controllers while omitting controller files at the root itself;
// reading only /sys/fs/cgroup would incorrectly report unsupported telemetry.
// The fallback also keeps older cgroup v1 hosts observable.
func currentCgroupDirectories() []string {
	contents, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return []string{cgroupMountRoot}
	}

	directories := make([]string, 0, 4)
	seen := make(map[string]struct{})
	add := func(root, relative string) {
		relative = strings.TrimSpace(relative)
		relative = strings.TrimPrefix(relative, "/")
		if relative == "" {
			relative = "."
		}
		candidate := filepath.Clean(filepath.Join(root, filepath.FromSlash(relative)))
		base := filepath.Clean(root)
		rel, relErr := filepath.Rel(base, candidate)
		if relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return
		}
		if _, ok := seen[candidate]; ok {
			return
		}
		seen[candidate] = struct{}{}
		directories = append(directories, candidate)
	}
	addHierarchy := func(root, relative string) {
		relative = strings.Trim(strings.TrimSpace(relative), "/")
		if relative == "" {
			add(root, ".")
			return
		}
		for {
			add(root, relative)
			parent := filepath.ToSlash(filepath.Dir(filepath.FromSlash(relative)))
			parent = strings.Trim(parent, "/")
			if parent == "" || parent == relative {
				add(root, ".")
				return
			}
			relative = parent
		}
	}

	for _, line := range strings.Split(string(contents), "\n") {
		parts := strings.SplitN(line, ":", 3)
		if len(parts) != 3 {
			continue
		}
		controllers := strings.TrimSpace(parts[1])
		relative := parts[2]
		if parts[0] == "0" {
			addHierarchy(cgroupMountRoot, relative)
			continue
		}
		for _, controller := range strings.Split(controllers, ",") {
			controller = strings.TrimSpace(controller)
			if controller == "" {
				continue
			}
			addHierarchy(filepath.Join(cgroupMountRoot, controller), relative)
		}
	}
	add(cgroupMountRoot, ".")
	return directories
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
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
	if stats.MemoryLimitBytes > 0 || stats.MemoryUsageBytes > 0 {
		metadata["cgroupMemory"] = map[string]any{
			"limitBytes": stats.MemoryLimitBytes,
			"usageBytes": stats.MemoryUsageBytes,
		}
	}
	if len(stats.MemoryEvents) > 0 {
		metadata["memoryEvents"] = stats.MemoryEvents
	}
	if len(stats.PressureStalls) > 0 {
		metadata["psi"] = stats.PressureStalls
	}
	if len(stats.CgroupControllers) > 0 {
		metadata["cgroupControllers"] = stats.CgroupControllers
	}
	if stats.CgroupEnforcement != "" {
		metadata["enforcement"] = stats.CgroupEnforcement
	}
	if stats.TasksCurrent > 0 || stats.TasksLimit > 0 {
		metadata["tasks"] = map[string]any{"current": stats.TasksCurrent, "limit": stats.TasksLimit}
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
