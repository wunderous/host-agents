package hostmcp

import (
	"regexp"
	"testing"
)

var hex8 = regexp.MustCompile(`^[0-9a-f]{8}$`)

func TestToolNamePrefixIsStableAndAgentScoped(t *testing.T) {
	const agentA = "host-zephyrus-ef47fbbf"
	const agentB = "host-workstation-e5059700"

	first := ToolNamePrefix(agentA)
	second := ToolNamePrefix(agentA)
	other := ToolNamePrefix(agentB)

	if !hex8.MatchString(first) {
		t.Fatalf("prefix %q is not 8 lowercase hex", first)
	}
	if first != second {
		t.Fatalf("prefix drifted for the same agent id: %q vs %q", first, second)
	}
	if first == other {
		t.Fatalf("distinct agent ids produced the same prefix %q", first)
	}
	if ToolNamePrefix("") != "" {
		t.Fatal("empty agent id must not mint a prefix")
	}
}

func TestToolNamePrefixTakesOnlyAgentID(t *testing.T) {
	var _ func(string) string = ToolNamePrefix
	left := ToolNamePrefix("host-zephyrus-ef47fbbf")
	right := ToolNamePrefix("host-zephyrus-aaaaaaaa")
	if left == right {
		t.Fatal("prefix must follow the canonical agent ID, not a shared host slug")
	}
}

func TestToolNamePrefixGoldenVector(t *testing.T) {
	// Shared with opute-harness packages/plugin-mcp-opute/src/tool-prefix.js.
	const agentID = "host-zephyrus-ef47fbbf"
	got := ToolNamePrefix(agentID)
	if got != "e9e864e9" {
		t.Fatalf("golden prefix = %q, want e9e864e9 (namespace %s)", got, ToolNamePrefixNamespace)
	}
	if ToolNamePrefixNamespace.String() != "4e03155a-251e-592a-a89b-98a92a34631a" {
		t.Fatalf("namespace = %s", ToolNamePrefixNamespace)
	}
}

func TestWireToolNameRoundTrip(t *testing.T) {
	prefix := ToolNamePrefix("host-zephyrus-ef47fbbf")
	wire := WireToolName(prefix, "provision_vm")
	if wire != prefix+"_provision_vm" {
		t.Fatalf("wire = %q", wire)
	}
	if CatalogNameFromWire(prefix, wire) != "provision_vm" {
		t.Fatalf("catalog round-trip = %q", CatalogNameFromWire(prefix, wire))
	}
	if CatalogNameFromWire(prefix, "provision_vm") != "provision_vm" {
		t.Fatal("unprefixed catalog names must pass through")
	}
	if WireToolName("", "provision_vm") != "provision_vm" {
		t.Fatal("empty prefix must not rewrite catalog names")
	}
	if WireToolName(prefix, "opute.provider.install") != prefix+"_opute.provider.install" {
		t.Fatalf("dotted catalog wire = %q", WireToolName(prefix, "opute.provider.install"))
	}
}

func TestImplementationNameForPrefix(t *testing.T) {
	if got := implementationNameForPrefix(""); got != "host-agent" {
		t.Fatalf("default implementation = %q", got)
	}
	if got := implementationNameForPrefix("9f3c1b6a"); got != "host-agent-9f3c1b6a" {
		t.Fatalf("prefixed implementation = %q", got)
	}
}
