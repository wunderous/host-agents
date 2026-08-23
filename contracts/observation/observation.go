// Package observation defines neutral capability observations. Provider
// native diagnostics belong in a namespaced provider payload, not here.
package observation

type Runtime struct {
	ContractVersion     string         `json:"contractVersion"`
	Endpoint            string         `json:"endpoint"`
	EndpointReady       bool           `json:"endpointReady"`
	ModelDiscoveryReady bool           `json:"modelDiscoveryReady"`
	RequestedModel      string         `json:"requestedModel,omitempty"`
	RequestedModelReady bool           `json:"requestedModelReady"`
	StreamingChatReady  bool           `json:"streamingChatReady"`
	Ready               bool           `json:"ready"`
	Error               string         `json:"error,omitempty"`
	Remediation         string         `json:"remediation,omitempty"`
	Provider            map[string]any `json:"provider,omitempty"`
}

type Tunnel struct {
	ContractVersion string          `json:"contractVersion"`
	Ready           bool            `json:"ready"`
	Bindings        []TunnelBinding `json:"bindings"`
	Error           string          `json:"error,omitempty"`
	Provider        map[string]any  `json:"provider,omitempty"`
}

type TunnelBinding struct {
	ID          string `json:"id"`
	Hostname    string `json:"hostname"`
	LocalTarget string `json:"localTarget"`
	Reachable   bool   `json:"reachable"`
	Ready       bool   `json:"ready"`
	Error       string `json:"error,omitempty"`
}
