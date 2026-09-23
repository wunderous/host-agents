package contract

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	capabilitycontract "github.com/wunderous/host-agents/contracts/capability"
)

func TestNetworkOverlayOperationIDsAreProviderNeutral(t *testing.T) {
	ids := []string{
		capabilitycontract.NetworkOverlayValidateOperation,
		capabilitycontract.NetworkOverlayPrepareMembershipOperation,
		capabilitycontract.NetworkOverlayJoinMembershipOperation,
		capabilitycontract.NetworkOverlayAttachTargetOperation,
		capabilitycontract.NetworkOverlayProbeReachabilityOperation,
		capabilitycontract.NetworkOverlayEnsureHAEndpointOperation,
		capabilitycontract.NetworkOverlayRemoveHAEndpointOperation,
		capabilitycontract.NetworkOverlayRemoveMembershipOperation,
		capabilitycontract.NetworkOverlayEnrollOperation,
		capabilitycontract.NetworkOverlayEnsurePrivateMeshOperation,
		capabilitycontract.NetworkOverlayEnsurePrivateServiceOperation,
		capabilitycontract.NetworkOverlayEnsurePublicIngressOperation,
		capabilitycontract.NetworkOverlayPromotePublicIngressOperation,
		capabilitycontract.NetworkOverlayProbeOperation,
		capabilitycontract.MeshMembershipEnrollOperation,
		capabilitycontract.MeshMembershipStatusOperation,
		capabilitycontract.MeshMembershipLeaveOperation,
		capabilitycontract.PrivateMeshEnsureOperation,
		capabilitycontract.PrivateMeshEnsureServiceOperation,
		capabilitycontract.PrivateMeshProbeOperation,
		capabilitycontract.PublicIngressEnsureOperation,
		capabilitycontract.PublicIngressPromoteOperation,
		capabilitycontract.PublicIngressProbeOperation,
	}
	forbidden := []string{"tailscale", "cloudflare", "cloudflared", "k3s", "kubectl", "funnel", "warp"}
	for _, id := range ids {
		lower := strings.ToLower(id)
		for _, token := range forbidden {
			if strings.Contains(lower, token) {
				t.Fatalf("capability operation ID %q contains lifecycle vocabulary %q", id, token)
			}
		}
		okPrefix := strings.HasPrefix(id, "opute.capability.network-overlay.") ||
			strings.HasPrefix(id, "opute.capability.mesh-membership.") ||
			strings.HasPrefix(id, "opute.capability.private-mesh.") ||
			strings.HasPrefix(id, "opute.capability.public-ingress.")
		if !okPrefix {
			t.Fatalf("unexpected overlay operation ID namespace: %q", id)
		}
	}

	_, current, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(current), "..", ".."))
	path := filepath.Join(root, "contracts", "capability", "capability.go")
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, spec := range gen.Specs {
			valueSpec, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, name := range valueSpec.Names {
				isNetworkingOperation := strings.HasPrefix(name.Name, "NetworkOverlay") ||
					strings.HasPrefix(name.Name, "MeshMembership") ||
					strings.HasPrefix(name.Name, "PrivateMesh") ||
					strings.HasPrefix(name.Name, "PublicIngress")
				if !isNetworkingOperation || !strings.HasSuffix(name.Name, "Operation") {
					continue
				}
				if i >= len(valueSpec.Values) {
					continue
				}
				lit, ok := valueSpec.Values[i].(*ast.BasicLit)
				if !ok {
					continue
				}
				id := strings.Trim(lit.Value, `"`)
				lower := strings.ToLower(id)
				for _, token := range forbidden {
					if strings.Contains(lower, token) {
						t.Fatalf("capability.go constant %s=%q contains %q", name.Name, id, token)
					}
				}
			}
		}
	}
}
