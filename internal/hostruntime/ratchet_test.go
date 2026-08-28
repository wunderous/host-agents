package hostruntime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// budgetLines is the ceiling for this package, in non-test lines.
//
// §9.2's three membership rules bound what may ENTER hostruntime. This bounds
// what accumulates: a slow drift of members that each individually pass the
// rules is exactly how the old internal/ops grew, and no per-member rule catches it.
//
// A ratchet, not a cliff. Raising this is allowed and sometimes correct -- the
// W7 partition will raise it once when the shared members land -- but it is a
// deliberate edit with a reason in the commit message, which is the whole point.
//
// Raised 260 -> 320 by the W7 shared partition: shared.go adds `Shared` (the
// ten identity/config/execution-handle fields), the seven helpers that cleared
// all three rules, `ResourceRegistry`, and `SharedHostOwnershipError`. Note
// what did NOT come with them -- `runVMExec` performs an incus ownership check,
// so rule 3 makes it an operation and it stays in the incus domain.
//
// Raised 320 -> 420 for the registry bookkeeping (RegisterResource,
// DeregisterResource, ResourceURIForProviderName, Coordinates). Same rule-3
// line: those parse a URI and touch the registry, while ResolveResource asks
// incus whether an instance exists and therefore stayed behind.
//
// Raised 420 -> 470 for the in-memory ResourceRegistry, moved here from
// the old internal/ops so a domain test can build a real Shared without importing the
// package this work is dismantling.
//
// Raised 470 -> 540 for EnvOr and the registry half of ResolveResource. The
// split is the point: parsing, the tenant check, and the registry lookup are
// hostruntime's, while OBSERVING that a VM or a systemd unit exists is a domain
// operation and is passed in as an Adopter. Rule 3 held; it just needed a seam.
const budgetLines = 540

func TestHostruntimeStaysWithinBudget(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	perFile := map[string]int{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(filepath.Clean(name))
		if err != nil {
			t.Fatal(err)
		}
		lines := len(strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n"))
		perFile[name] = lines
		total += lines
	}
	if total == 0 {
		t.Fatal("measured no hostruntime source; the ratchet cannot pass on an empty scope")
	}
	if total > budgetLines {
		t.Errorf("hostruntime is %d non-test lines, over its %d-line budget (%v).\n"+
			"Before raising budgetLines, check the addition against §9.2: does it name a domain type, "+
			"does it have two or more domain consumers, and is it identity/config/an execution handle "+
			"rather than an operation? If any answer is no, it belongs in a domain package.",
			total, budgetLines, perFile)
	}
}
