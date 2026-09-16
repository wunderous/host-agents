package host

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// AgentRuntime is the composition root's own configuration, narrowed to the
// parts that describe where this Host Agent is installed. The host domain
// derives the rest -- home, scope, unit directory, provider root -- because
// those are observations of the machine rather than configuration, and the
// domain is where the same derivations already live.
type AgentRuntime struct {
	InstanceID   string
	InstanceRoot string
	MCPPort      int
}

// AgentInstallation is how a Host Agent answers "where are you, so I can put
// something next to you?".
//
// Placing a provider module beside the agent needs four facts that nothing on
// the wire reported: which directory systemd units belong in for this agent's
// scope, which environment file carries the agent's own credentials so the
// provider can call back, which endpoint to call back on, and which home the
// unit should run with. Every caller that has installed a provider so far has
// had to be told all four -- scripts/p15-two-node-public-mesh.ts carries them
// per host, as literals -- which is exactly the privileged caller knowledge a
// host-agent-only bootstrap cannot have. The agent is the only party that knows
// them for certain, so it says them.
//
// It is a description, not a capability: every field is read from this process's
// own configuration or from a stat of its own filesystem, and nothing here
// changes anything.
type AgentInstallation struct {
	AgentID         string `json:"agentId,omitempty"`
	InstanceID      string `json:"instanceId,omitempty"`
	InstanceRoot    string `json:"instanceRoot,omitempty"`
	EnvironmentFile string `json:"environmentFile,omitempty"`
	HomeDir         string `json:"homeDir,omitempty"`
	ServiceScope    string `json:"serviceScope,omitempty"`
	ServiceUnitDir  string `json:"serviceUnitDir,omitempty"`
	ServiceWantedBy string `json:"serviceWantedBy,omitempty"`
	MCPEndpoint     string `json:"mcpEndpoint,omitempty"`
	ProviderRoot    string `json:"providerRoot,omitempty"`
}

// agentEnvironmentFileName is the file the agent's own unit reads its
// credentials from. It sits in the instance root by construction in
// internal/config, and is reported only when it is actually present -- an
// absent path in a unit's EnvironmentFile= is a startup failure, so reporting
// one we have not seen would hand the caller a broken unit.
const agentEnvironmentFileName = "host-agent.env"

func describeAgentInstallation(agentID string, runtime AgentRuntime) *AgentInstallation {
	installation := &AgentInstallation{
		AgentID:      strings.TrimSpace(agentID),
		InstanceID:   strings.TrimSpace(runtime.InstanceID),
		InstanceRoot: strings.TrimSpace(runtime.InstanceRoot),
	}
	if home, err := hostHomeDir(); err == nil {
		installation.HomeDir = home
		installation.ProviderRoot = filepath.Join(home, ".local", "share", "opute", "providers")
	}
	installation.ServiceScope = agentServiceScope()
	switch installation.ServiceScope {
	case "system":
		installation.ServiceUnitDir = "/etc/systemd/system"
		installation.ServiceWantedBy = "multi-user.target"
	default:
		if installation.HomeDir != "" {
			installation.ServiceUnitDir = filepath.Join(installation.HomeDir, ".config", "systemd", "user")
		}
		// The user manager has no multi-user.target. Reporting the scope without
		// the target it implies would leave a recipe author to rediscover the
		// pairing, and getting it wrong yields a unit that installs into nothing.
		installation.ServiceWantedBy = "default.target"
	}
	if installation.InstanceRoot != "" {
		candidate := filepath.Join(installation.InstanceRoot, agentEnvironmentFileName)
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
			installation.EnvironmentFile = candidate
		}
	}
	if runtime.MCPPort > 0 {
		// Loopback deliberately: this endpoint is what a provider running on
		// this same host dials back on. The agent's public or bridge address is
		// a different question with a different answer.
		installation.MCPEndpoint = fmt.Sprintf("http://127.0.0.1:%d/mcp", runtime.MCPPort)
	}
	return installation
}

// agentServiceScope reports which systemd manager owns units this agent
// installs. Root under the system manager owns /etc/systemd/system; anyone else
// owns their own user manager and cannot write there -- which host A proves,
// having been unable to remove a unit it had installed under an earlier,
// privileged arrangement.
func agentServiceScope() string {
	if os.Geteuid() == 0 {
		return "system"
	}
	return "user"
}
