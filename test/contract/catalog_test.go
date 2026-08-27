package contract_test

import (
	"encoding/json"
	"testing"

	"github.com/wunderous/host-agents/internal/tools"
	"github.com/wunderous/host-agents/schemas"
)

func TestIncusCatalogMatchesExportMinusOmitted(t *testing.T) {
	got, err := tools.HostToolDefinitionsForProvider("incus")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := schemas.FS.ReadFile("incus-tools.json")
	if err != nil {
		t.Fatal(err)
	}
	var exported []tools.ToolDefinition
	if err := json.Unmarshal(raw, &exported); err != nil {
		t.Fatal(err)
	}
	want := make([]tools.ToolDefinition, 0, len(exported)+len(tools.IncusInventoryTools))
	localLLM := map[string]bool{
		"check_local_llm_prerequisites": true, "ensure_local_llm_server_binary": true, "list_local_llm_models": true,
		"probe_local_llm": true, "install_local_llm_model": true,
		"configure_local_llm_model": true, "start_local_llm_runtime": true,
		"configure_local_llm_runtime": true, "stop_local_llm_runtime": true,
		"remove_local_llm_model": true, "ensure_local_llm_relay": true,
		"remove_local_llm_relay": true, "ensure_local_llm_k3s_proxy": true,
		"remove_local_llm_k3s_proxy": true,
	}
	for _, tool := range exported {
		if localLLM[tool.Name] || tools.ProviderOwnedToolNames[tool.Name] || tools.IsOmittedToolName(tool.Name) || tools.IncusOmittedToolNames[tool.Name] || tools.CatalogExcludedToolNames[tool.Name] {
			continue
		}
		want = append(want, tool)
	}
	for _, name := range tools.IncusInventoryTools {
		found := false
		for _, tool := range want {
			if tool.Name == name {
				found = true
				break
			}
		}
		if !found {
			rawAll, err := schemas.FS.ReadFile("all-tools.json")
			if err != nil {
				t.Fatal(err)
			}
			var all []tools.ToolDefinition
			if err := json.Unmarshal(rawAll, &all); err != nil {
				t.Fatal(err)
			}
			for _, tool := range all {
				if tool.Name == name {
					want = append(want, tool)
					found = true
					break
				}
			}
		}
		if !found {
			t.Fatalf("missing incus inventory tool %q in export", name)
		}
	}
	gotNonLocal := make([]tools.ToolDefinition, 0, len(got))
	for _, tool := range got {
		if !localLLM[tool.Name] {
			gotNonLocal = append(gotNonLocal, tool)
		}
	}
	if len(gotNonLocal) != len(want) {
		t.Fatalf("non-local tool count mismatch: got %d want %d", len(gotNonLocal), len(want))
	}
	gotNames := make(map[string]bool, len(got))
	for _, tool := range got {
		gotNames[tool.Name] = true
	}
	for _, tool := range want {
		if !gotNames[tool.Name] {
			t.Fatalf("missing tool %q in incus catalog", tool.Name)
		}
	}
}

func TestExcludedToolsNotInCatalog(t *testing.T) {
	got, err := tools.HostToolDefinitionsForProvider("incus")
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range got {
		if tools.CatalogExcludedToolNames[tool.Name] {
			t.Fatalf("excluded tool %q in catalog", tool.Name)
		}
		if tools.IsOmittedToolName(tool.Name) {
			t.Fatalf("omitted tool %q in catalog", tool.Name)
		}
	}
}

func TestCatalogExcludedDispatchToolsLoadFromAllTools(t *testing.T) {
	internal, err := tools.LoadCatalogExcludedDispatchToolDefinitions()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"ensure_sql_connector",
		"get_sql_connector_status",
		"release_sql_connector",
		"install_sql_forward_sidecar",
		"agent_shell",
	}
	got := make(map[string]bool, len(internal))
	for _, tool := range internal {
		got[tool.Name] = true
	}
	for _, name := range want {
		if !got[name] {
			t.Fatalf("missing internal dispatch tool %q", name)
		}
	}
}

func TestIncusCatalogIncludesInventoryTools(t *testing.T) {
	got, err := tools.HostToolDefinitionsForProvider("incus")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range tools.IncusInventoryTools {
		found := false
		for _, tool := range got {
			if tool.Name == name {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("incus catalog missing %q", name)
		}
	}
}
