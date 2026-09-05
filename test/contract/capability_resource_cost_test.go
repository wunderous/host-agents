package contract

import (
	"strings"
	"testing"

	"github.com/wunderous/host-agents/internal/tools"
)

// TestMutatingCapabilitiesDeclareResourceCost mirrors the admission guard in
// hostmcp.admitInvocationWithDescriptor: a capability that changes host state
// is admitted only when its cost is declared, either at a dispatch
// registration or as typed resourceCost metadata on the descriptor. A name
// that satisfies neither fails closed at call time with
// "resource_declaration_required", which is invisible until something calls
// it -- exec_kubernetes_command reached production in exactly that state, and
// the operator health probe reported the resulting error as "operator is not
// installed".
//
// Asserting the precondition over the whole published catalog turns that
// latent per-name failure into a build failure.
func TestMutatingCapabilitiesDeclareResourceCost(t *testing.T) {
	for _, standalone := range []bool{false, true} {
		server := newContractTestServer(t, standalone)
		for _, descriptor := range server.CatalogSnapshot().Tools {
			if descriptor.Effect == string(tools.EffectRead) {
				continue
			}
			if strings.HasPrefix(strings.TrimSpace(descriptor.Implementation), "provider:") {
				// Provider workloads carry their cost in the plugin manifest,
				// which this in-process catalog does not load.
				continue
			}
			if _, declared := tools.RegisteredAdmissionClass(descriptor.Name); declared {
				continue
			}
			if descriptor.ResourceCost == nil {
				t.Errorf(
					"standalone=%v capability %q has effect %q with no dispatch registration and no typed resourceCost: every call fails closed",
					standalone, descriptor.Name, descriptor.Effect,
				)
			}
		}
	}
}
