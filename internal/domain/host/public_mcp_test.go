package host

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolvePublicMCPPathsUsesOwnedUserLocations(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	paths, endpoint, localTarget, err := resolvePublicMCPPaths(EnsurePublicMcpTunnelArgs{
		BindingID:   "binding-42",
		Endpoint:    "https://public.example/mcp",
		LocalTarget: "http://127.0.0.1:3004/mcp",
	})
	if err != nil {
		t.Fatal(err)
	}
	if endpoint != "https://public.example/mcp" || localTarget != "http://127.0.0.1:3004/mcp" {
		t.Fatalf("normalized endpoints = %q, %q", endpoint, localTarget)
	}
	for _, path := range []string{paths.artifactPath, paths.tokenFile, paths.serviceFile} {
		if !strings.HasPrefix(path, home+string(filepath.Separator)) {
			t.Fatalf("path escaped home: %q", path)
		}
	}
	if paths.serviceName != "opute-cloudflared-binding-42.service" {
		t.Fatalf("service name = %q", paths.serviceName)
	}
	if paths.unitArtifact != "%h/.local/share/opute/tunnels/binding-42/cloudflared" {
		t.Fatalf("unit artifact = %q", paths.unitArtifact)
	}
}

func TestResolvePublicMCPPathsSupportsProviderBindingIdentity(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	args := EnsurePublicMcpTunnelArgs{
		BindingID:   "host-linux-1:host.example.com",
		Endpoint:    "https://host.example.com/mcp",
		LocalTarget: "http://127.0.0.1:3004/mcp",
	}
	paths, _, _, err := resolvePublicMCPPaths(args)
	if err != nil {
		t.Fatal(err)
	}
	if paths.bindingID != args.BindingID {
		t.Fatalf("binding id = %q, want exact provider identity", paths.bindingID)
	}
	if strings.Contains(paths.serviceName, ":") || strings.Contains(paths.tokenFile, ":") {
		t.Fatalf("provider punctuation leaked into owned path: service=%q token=%q", paths.serviceName, paths.tokenFile)
	}
	if paths.serviceName != "opute-cloudflared-"+normalizePublicMCPInstanceID(args.BindingID)+".service" {
		t.Fatalf("service name = %q", paths.serviceName)
	}
}

func TestNormalizePublicMCPInstanceIDIsInjectiveForPunctuation(t *testing.T) {
	if normalizePublicMCPInstanceID("host-a:foo.example.com") == normalizePublicMCPInstanceID("host-a-foo-example-com") {
		t.Fatal("provider binding identities collided after normalization")
	}
}

func TestResolvePublicMCPPathsRejectsUnownedInputs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	base := EnsurePublicMcpTunnelArgs{
		BindingID:   "binding-42",
		Endpoint:    "https://public.example/mcp",
		LocalTarget: "http://127.0.0.1:3004/mcp",
	}
	cases := []struct {
		name string
		edit func(*EnsurePublicMcpTunnelArgs)
	}{
		{name: "non https", edit: func(args *EnsurePublicMcpTunnelArgs) { args.Endpoint = "http://public.example/mcp" }},
		{name: "non loopback origin", edit: func(args *EnsurePublicMcpTunnelArgs) { args.LocalTarget = "http://10.0.0.2:3004/mcp" }},
		{name: "unsafe binding", edit: func(args *EnsurePublicMcpTunnelArgs) { args.BindingID = "binding/42" }},
		{name: "foreign service", edit: func(args *EnsurePublicMcpTunnelArgs) { args.ServiceName = "ssh.service" }},
		{name: "foreign token file", edit: func(args *EnsurePublicMcpTunnelArgs) { args.TokenFile = "/tmp/token.env" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := base
			tc.edit(&args)
			if _, _, _, err := resolvePublicMCPPaths(args); err == nil {
				t.Fatal("unowned or invalid input was accepted")
			}
		})
	}
}

func TestResolvePublicMCPPathsAllowsExplicitRemoteOrigin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	args := EnsurePublicMcpTunnelArgs{
		BindingID:    "binding-remote",
		Endpoint:     "https://public.example/mcp",
		LocalTarget:  "http://10.0.100.1:3005/mcp",
		OriginHostID: "host-opute-ha-b-b9234af4",
	}
	_, _, localTarget, err := resolvePublicMCPPaths(args)
	if err != nil {
		t.Fatalf("explicit origin host should authorize a reachable remote target: %v", err)
	}
	if localTarget != args.LocalTarget {
		t.Fatalf("local target = %q, want %q", localTarget, args.LocalTarget)
	}
}

func TestRenderPublicMCPUnitKeepsTunnelTokenOutOfUnit(t *testing.T) {
	unit := renderPublicMCPUnit(publicMCPPaths{
		scope:         "user",
		serviceName:   "opute-cloudflared-binding-42.service",
		unitArtifact:  "%h/.local/share/opute/tunnels/binding-42/cloudflared",
		unitTokenFile: "%h/.config/opute/tunnels/binding-42.env",
	})
	if strings.Contains(unit, "OPUTE_CLOUDFLARED_TUNNEL_TOKEN=") || strings.Contains(unit, "secret-token") {
		t.Fatalf("unit embeds tunnel credential: %q", unit)
	}
	if !strings.Contains(unit, "EnvironmentFile=%h/.config/opute/tunnels/binding-42.env") || !strings.Contains(unit, "--token") {
		t.Fatalf("unit does not reference the owned environment file: %q", unit)
	}
}

func TestRequireOwnedManagedFileRefusesForeignContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "foreign.env")
	if err := os.WriteFile(path, []byte("OTHER=value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := requireOwnedManagedFile(path, publicMCPManagedFileMarker); err == nil {
		t.Fatal("foreign managed file was accepted")
	}
}

func TestRequireOwnedManagedFileRefusesSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "owned.env")
	path := filepath.Join(dir, "link.env")
	if err := os.WriteFile(target, []byte(publicMCPManagedFileMarker), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if err := requireOwnedManagedFile(path, publicMCPManagedFileMarker); err == nil {
		t.Fatal("symlinked managed file was accepted")
	}
}
