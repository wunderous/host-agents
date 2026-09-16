package hostmcp

import (
	"context"
	"strings"
	"testing"
	"time"

	providercontract "github.com/wunderous/host-agents/contracts/provider"
	"github.com/wunderous/host-agents/internal/cordis"
	hostexec "github.com/wunderous/host-agents/internal/exec"
	"github.com/wunderous/host-agents/internal/hostagent"
	"github.com/wunderous/host-agents/internal/hostruntime"
	"github.com/wunderous/host-agents/internal/tools"
)

// systemdHostFake answers the read-only systemd queries the resolver makes.
// listUnits is the `list-units` body; execStarts maps a unit name to the
// executable `systemctl show` reports for it.
type systemdHostFake struct {
	listUnits  string
	execStarts map[string]string
	fragments  map[string]string
	commands   []string
}

func (f *systemdHostFake) run(command []string, _ func(string), _ time.Duration) (hostexec.Result, error) {
	joined := strings.Join(command, " ")
	f.commands = append(f.commands, joined)
	switch {
	case strings.Contains(joined, "list-units"):
		return hostexec.Result{ExitCode: 0, Stdout: f.listUnits}, nil
	case strings.Contains(joined, " show "):
		unit := ""
		for index, argument := range command {
			if argument == "show" && index+1 < len(command) {
				unit = command[index+1]
				break
			}
		}
		return hostexec.Result{ExitCode: 0, Stdout: "ExecStart={ path=" + f.execStarts[unit] + " ; argv[]=" + f.execStarts[unit] + " serve ; ignore_errors=no }\nFragmentPath=" + f.fragments[unit] + "\n"}, nil
	case strings.Contains(joined, "is-active"):
		return hostexec.Result{ExitCode: 0, Stdout: "active\n"}, nil
	case strings.Contains(joined, "is-enabled"):
		return hostexec.Result{ExitCode: 0, Stdout: "enabled\n"}, nil
	default:
		return hostexec.Result{ExitCode: 0}, nil
	}
}

func newSystemdFakeServer(t *testing.T, fake *systemdHostFake) *Server {
	t.Helper()
	svc := hostagent.New(hostagent.Options{
		ProviderID: hostruntime.IDIncus,
		ToolsForProvider: func(providerID string) []string {
			names, err := tools.HostToolNamesForProvider(providerID)
			if err != nil {
				return nil
			}
			return names
		},
		HostCommandRunnerFn: fake.run,
	})
	server, err := NewServer(Options{ProviderID: "incus", Ops: svc, Standalone: true, AllowMutations: true, StateDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { server.Close() })
	return server
}

// The host must be able to name a provider's own unit without the caller
// remembering it from install time, and it must claim only the unit the
// provider was actually installed into. Both units below look like k3s; only
// one lives under the provider id.
func TestResolveProviderHostServiceClaimsOnlyTheUnitInstalledUnderTheProviderID(t *testing.T) {
	fake := &systemdHostFake{
		listUnits: strings.Join([]string{
			"opute-provider-k3s.service loaded active running Opute Kubernetes provider MCP",
			"opute-provider-k3s-p15-mtzntn1h.service loaded active running Opute K3s provider MCP",
			"opute-provider-cloudflare.service loaded active running Opute Cloudflare provider MCP",
			"cron.service loaded active running Regular background program processing daemon",
		}, "\n"),
		execStarts: map[string]string{
			// A unit left by an older instance layout: the provider id is not in
			// its path, so nothing proves it belongs to this generation.
			"opute-provider-k3s":              "/home/u/.local/share/opute/instances/host-a/opute-provider-k3s",
			"opute-provider-k3s-p15-mtzntn1h": "/home/u/.local/share/opute/providers/com.opute.k3s/bin/opute-provider-k3s",
			"opute-provider-cloudflare":       "/home/u/.local/share/opute/providers/com.opute.cloudflare/bin/opute-provider-cloudflare",
		},
		fragments: map[string]string{
			"opute-provider-k3s-p15-mtzntn1h": "/home/u/.config/systemd/user/opute-provider-k3s-p15-mtzntn1h.service",
		},
	}
	server := newSystemdFakeServer(t, fake)

	resolved := server.resolveProviderHostService("com.opute.k3s")
	if resolved == nil {
		t.Fatal("resolver found no host service for an installed provider")
	}
	if name, _ := resolved["serviceName"].(string); name != "opute-provider-k3s-p15-mtzntn1h" {
		t.Fatalf("serviceName = %q, want the unit installed under com.opute.k3s", name)
	}
	if file, _ := resolved["serviceFile"].(string); !strings.HasSuffix(file, "opute-provider-k3s-p15-mtzntn1h.service") {
		t.Fatalf("serviceFile = %q, want the resolved unit's own fragment path", file)
	}

	// A provider that is not installed on this host has no unit to claim, and
	// the resolver must say so rather than reach for the nearest similar name.
	if resolved := server.resolveProviderHostService("com.opute.absent"); resolved != nil {
		t.Fatalf("resolver claimed %v for a provider that is not installed", resolved)
	}
}

// The defect: teardown retired the generation, host cleanup silently did
// nothing because no serviceName was supplied, and the run reported completed
// with the provider still listening. Nothing may be retired by a call that
// cannot name what it would reclaim.
func TestProviderTeardownRefusesWhenItCannotNameTheProvidersHostService(t *testing.T) {
	const providerID = "com.opute.unreclaimable"
	fake := &systemdHostFake{
		listUnits:  "cron.service loaded active running Regular background program processing daemon\n",
		execStarts: map[string]string{},
		fragments:  map[string]string{},
	}
	server := newSystemdFakeServer(t, fake)

	manager := cordis.NewProviderLifecycleManager(cordis.DrainPolicy{})
	server.providerLifecycle = manager
	generation, err := manager.CreateCandidate(providercontract.ProviderRef{ID: providerID, Version: "1.0.0"}, "manifest-hash", "http://127.0.0.1:45999/mcp", "catalog")
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.MarkReady(generation.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := manager.Activate(generation.ID); err != nil {
		t.Fatal(err)
	}

	result, err := server.handleProviderTeardownContext(context.Background(), map[string]any{
		"provider": providerID,
		"confirm":  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || !result.IsError {
		t.Fatalf("teardown result = %#v, want a refusal", result)
	}
	if text := resultText(result); !strings.Contains(text, "provider_host_service_unknown") {
		t.Fatalf("refusal = %q, want a typed provider_host_service_unknown answer", text)
	}
	if _, active := manager.Active(providerID); !active {
		t.Fatal("the generation was retired by a teardown that could not reclaim it")
	}

	// hostService=none is how a provider with nothing to reclaim says so, and
	// it must get past the refusal rather than be blocked by it.
	inputs := map[string]any{"hostService": "none"}
	if err := requireProviderHostServiceTarget(providerID, inputs); err != nil {
		t.Fatalf("hostService=none was refused: %v", err)
	}
}
