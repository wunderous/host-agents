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
// rules is exactly how internal/ops grew, and no per-member rule catches it.
//
// A ratchet, not a cliff. Raising this is allowed and sometimes correct -- the
// W7 partition will raise it once when the shared members land -- but it is a
// deliberate edit with a reason in the commit message, which is the whole point.
const budgetLines = 260

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
