package host

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/wunderous/host-agents/internal/contract/vminfo"
	"github.com/wunderous/host-agents/internal/hostruntime"
)

// Installing a provider module beside the agent needs the agent's own unit
// directory, environment file, callback endpoint and home. Until this existed
// the only thing that knew them was a driver script holding them as per-host
// literals, which is precisely the knowledge a host-agent-only bootstrap does
// not have.
func TestDescribeAgentInstallationAnswersWhereTheAgentIs(t *testing.T) {
	root := t.TempDir()
	envFile := filepath.Join(root, agentEnvironmentFileName)
	if err := os.WriteFile(envFile, []byte("MCP_AUTH_TOKEN=x\n"), 0o600); err != nil {
		t.Fatalf("write env file: %v", err)
	}

	installation := describeAgentInstallation("host-zephyrus-ef47fbbf", AgentRuntime{
		InstanceID:   "host-zephyrus-ef47fbbf",
		InstanceRoot: root,
		MCPPort:      3004,
	})
	if installation == nil {
		t.Fatal("no installation described")
	}
	if installation.AgentID != "host-zephyrus-ef47fbbf" || installation.InstanceRoot != root {
		t.Fatalf("identity not carried: %+v", installation)
	}
	if installation.EnvironmentFile != envFile {
		t.Fatalf("environment file = %q, want %q", installation.EnvironmentFile, envFile)
	}
	if installation.MCPEndpoint != "http://127.0.0.1:3004/mcp" {
		t.Fatalf("callback endpoint = %q", installation.MCPEndpoint)
	}
	if installation.ServiceScope != agentServiceScope() {
		t.Fatalf("scope = %q", installation.ServiceScope)
	}
	if installation.ServiceUnitDir == "" {
		t.Fatal("no unit directory reported; a caller cannot place a unit without one")
	}
	if installation.ServiceScope == "system" && installation.ServiceUnitDir != "/etc/systemd/system" {
		t.Fatalf("system scope unit dir = %q", installation.ServiceUnitDir)
	}
	if installation.ServiceScope == "user" && filepath.Base(installation.ServiceUnitDir) != "user" {
		t.Fatalf("user scope unit dir = %q", installation.ServiceUnitDir)
	}
	if installation.ProviderRoot == "" || filepath.Base(installation.ProviderRoot) != "providers" {
		t.Fatalf("provider root = %q", installation.ProviderRoot)
	}
	// The scope implies the target, and the user manager has no
	// multi-user.target. A unit installed into the wrong one installs into
	// nothing, so the pairing is reported rather than left to be rediscovered.
	wantedBy := map[string]string{"system": "multi-user.target", "user": "default.target"}
	if installation.ServiceWantedBy != wantedBy[installation.ServiceScope] {
		t.Fatalf("wantedBy = %q for scope %q", installation.ServiceWantedBy, installation.ServiceScope)
	}
}

// An EnvironmentFile= naming a path that is not there is a unit that fails to
// start. Reporting a path we have not seen would hand the caller exactly that,
// so an absent file is reported as absent rather than as a plausible default.
func TestDescribeAgentInstallationOmitsAnEnvironmentFileItCannotSee(t *testing.T) {
	installation := describeAgentInstallation("agent", AgentRuntime{
		InstanceID:   "agent",
		InstanceRoot: t.TempDir(),
		MCPPort:      3004,
	})
	if installation.EnvironmentFile != "" {
		t.Fatalf("reported an environment file that does not exist: %q", installation.EnvironmentFile)
	}
}

// A directory sitting where the environment file should be is not an
// environment file.
func TestDescribeAgentInstallationRejectsANonRegularEnvironmentFile(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, agentEnvironmentFileName), 0o755); err != nil {
		t.Fatalf("create directory: %v", err)
	}
	installation := describeAgentInstallation("agent", AgentRuntime{InstanceRoot: root, MCPPort: 3004})
	if installation.EnvironmentFile != "" {
		t.Fatalf("claimed a directory as an environment file: %q", installation.EnvironmentFile)
	}
}

// No port configured is not port zero; a callback endpoint of
// http://127.0.0.1:0/mcp dials nothing.
func TestDescribeAgentInstallationOmitsAnUnconfiguredEndpoint(t *testing.T) {
	installation := describeAgentInstallation("agent", AgentRuntime{InstanceRoot: t.TempDir()})
	if installation.MCPEndpoint != "" {
		t.Fatalf("endpoint = %q, want none", installation.MCPEndpoint)
	}
}

// The description is reached through get_host_info, so a Deps that does not
// supply the runtime must leave the field absent rather than emit an empty
// object a caller would interpolate blanks out of.
func TestDescribeHostOmitsTheAgentBlockWithoutARuntimeDep(t *testing.T) {
	shared := hostruntime.Shared{Runtime: hostruntime.NewRuntime(hostruntime.ResolveConfig("incus"))}
	service := New(&shared, Deps{
		VMInventoryCapacity: func() (vminfo.VMInventoryCapacity, error) {
			return vminfo.VMInventoryCapacity{}, errors.New("not available in this test")
		},
		SupportedTools: func(string) []string { return nil },
	})
	if info := service.DescribeHost(); info.Agent != nil {
		t.Fatalf("agent block present without a runtime dep: %+v", info.Agent)
	}
}
