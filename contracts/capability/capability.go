// Package capability contains stable, provider-neutral capability IDs and
// service contracts.
package capability

const (
	LLMServing = "opute.capability.llm-serving.v1"
	Tunneling  = "opute.capability.tunneling.v1"
)

type Validation struct {
	Capability string `json:"capability"`
	Contract   string `json:"contract"`
	Operation  string `json:"operation"`
}
