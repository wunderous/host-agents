// Package vminfo holds the VM inventory wire shapes.
//
// A contract package, not a domain: no behaviour, no internal imports. The
// incus domain measures capacity and the host domain reports it as part of the
// host description, so neither can own the struct without the other importing
// it.
package vminfo

type VMInventoryCapacity struct {
	RunningVMCount            int   `json:"runningVmCount"`
	TotalVMCount              int   `json:"totalVmCount"`
	RunningVMCPULimitCores    int   `json:"runningVmCpuLimitCores"`
	TotalVMCPULimitCores      int   `json:"totalVmCpuLimitCores"`
	RunningVMMemoryLimitBytes int64 `json:"runningVmMemoryLimitBytes"`
	TotalVMMemoryLimitBytes   int64 `json:"totalVmMemoryLimitBytes"`
	RunningVMDiskLimitBytes   int64 `json:"runningVmDiskLimitBytes"`
	TotalVMDiskLimitBytes     int64 `json:"totalVmDiskLimitBytes"`
	RunningQEMUCount          int   `json:"runningQemuCount"`
	TotalQEMUCount            int   `json:"totalQemuCount"`
	RunningContainerCount     int   `json:"runningContainerCount"`
	TotalContainerCount       int   `json:"totalContainerCount"`
}
