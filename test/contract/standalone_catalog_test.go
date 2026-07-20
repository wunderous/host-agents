package contract_test

import (
	"testing"

	"github.com/wunderous/host-agents/internal/tools"
)

func TestStandaloneCatalogMatchesVersionedContract(t *testing.T) {
	if err := tools.ValidateStandaloneToolContract(); err != nil {
		t.Fatal(err)
	}
	contract, err := tools.LoadStandaloneToolContract()
	if err != nil {
		t.Fatal(err)
	}
	if len(contract.Tools) != len(tools.StandaloneToolNames) {
		t.Fatalf("contract has %d tools, allowlist has %d", len(contract.Tools), len(tools.StandaloneToolNames))
	}
	for _, entry := range contract.Tools {
		if entry.Support != "stable" && entry.Support != "experimental" {
			t.Fatalf("tool %q has invalid support level %q", entry.Name, entry.Support)
		}
	}
}
