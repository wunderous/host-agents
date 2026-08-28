// Package clusterinfo holds the cluster inventory wire shapes.
//
// It is a contract package, not a domain: it has no behaviour and imports
// nothing from internal. It exists because the kubernetes domain produces this
// inventory (from the provider plugin) and the cluster domain enriches and
// serves it -- so if either owned the type, the other would have to import it
// and S4.3 rule 1 would be broken by a struct definition.
package clusterinfo

type ClusterListResult struct {
	Clusters []ClusterDetail `json:"clusters"`
	Total    int             `json:"total"`
}

type ClusterNode struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Roles   string `json:"roles"`
	Age     string `json:"age"`
	Version string `json:"version"`
}

type ClusterDetail struct {
	URI                    string        `json:"uri"`
	ID                     string        `json:"id"`
	Name                   string        `json:"name"`
	Status                 string        `json:"status"`
	Provider               string        `json:"provider,omitempty"`
	InfraProvider          string        `json:"infraProvider,omitempty"`
	Version                string        `json:"version,omitempty"`
	NodeCount              int           `json:"nodeCount,omitempty"`
	APIEndpoint            string        `json:"apiEndpoint,omitempty"`
	IPv4                   []string      `json:"ipv4,omitempty"`
	CPU                    *int          `json:"cpu,omitempty"`
	Memory                 string        `json:"memory,omitempty"`
	Disk                   string        `json:"disk,omitempty"`
	AgentReady             *bool         `json:"agentReady,omitempty"`
	Nodes                  []ClusterNode `json:"nodes"`
	Logs                   []string      `json:"logs"`
	NodeInventoryAvailable *bool         `json:"nodeInventoryAvailable,omitempty"`
	// VMName is the backing Incus instance (cluster resource id).
	VMName string `json:"vmName,omitempty"`
	// HostId is the owning host agent identity (durable execution owner).
	HostId string `json:"hostId,omitempty"`
	// InstanceType is the provider-observed Incus target kind. It is retained
	// separately from the canonical cluster URI so a container cannot be
	// mistaken for a VM during later execution.
	InstanceType string `json:"instanceType,omitempty"`
}
