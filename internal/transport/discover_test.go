package transport

import "testing"

// The legacy bypass and the 2026-07-28-only surface must stay disjoint. An
// entry that is both would let a pre-2026 client reach a method that did not
// exist for it, without the contract validation that method assumes -- which is
// the bypass ADR 0011 narrowed. The relationship used to live in a comment.
func TestLegacyBypassNeverCoversModernOnlyMethods(t *testing.T) {
	for method := range legacyCompatibleMethods {
		if isModernOnlyMethod(method) {
			t.Errorf("%q is on the legacy bypass and is a 2026-07-28-only method; a pre-2026 client cannot need it", method)
		}
	}
}

// The extension router and the bypass reasoning are one predicate now. If the
// router grows a family, this fails until the set names it.
func TestModernOnlyMethodsCoverTheExtensionSurface(t *testing.T) {
	for _, method := range []string{"server/discover", "tasks/get", "tasks/list", "tasks/cancel", "resources/list"} {
		if !isModernOnlyMethod(method) {
			t.Errorf("%q is served as an extension method but is not modern-only", method)
		}
	}
	for _, method := range []string{"tools/list", "tools/call", "ping", "prompts/get"} {
		if isModernOnlyMethod(method) {
			t.Errorf("%q predates 2026-07-28 and must not be modern-only", method)
		}
	}
}
