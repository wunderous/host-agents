package incus

const (
	defaultIncusVMCPUs   = 2
	defaultIncusVMMemory = "2GiB"

	// Root disk defaults are requests, not guarantees. They are applied only
	// when the resolved storage pool can enforce a quota; see
	// admitRootDiskQuota.
	defaultIncusVMRootDisk        = "10GiB"
	defaultIncusContainerRootDisk = "20GiB"
)
