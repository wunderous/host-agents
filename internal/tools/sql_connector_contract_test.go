package tools

import "testing"

// A capability cannot require the resource it creates. `ensure_sql_connector`
// declared `requires` on a `sql-connector` uri, and the resolver has no adopter
// for that type, so admission answered "resource not found" for every first
// call -- the connector could never come into existence. The declared input
// also had no `databaseId`, the one argument the handler and every caller
// actually use, while the description says the relay is "keyed by databaseId".
// These assertions pin the repaired shape: address by databaseId, and register
// the connector through `produces` from the uri the tool returns.
func TestSQLConnectorContractIsSatisfiable(t *testing.T) {
	defs, err := LoadCatalogExcludedDispatchToolDefinitions()
	if err != nil {
		t.Fatalf("load catalog-excluded definitions: %v", err)
	}

	byName := make(map[string]ToolDefinition, len(defs))
	for _, def := range defs {
		byName[def.Name] = def
	}

	catalog := BuildCapabilityCatalog("test-provider", defs)
	descriptors := make(map[string]CapabilityDescriptor, len(catalog.Tools))
	for _, descriptor := range catalog.Tools {
		descriptors[descriptor.Name] = descriptor
	}

	for _, name := range []string{"ensure_sql_connector", "get_sql_connector_status", "release_sql_connector"} {
		def, ok := byName[name]
		if !ok {
			t.Fatalf("%s is missing from the catalog-excluded definitions", name)
		}
		if !requiresInputProperty(def, "databaseId") {
			t.Errorf("%s must declare databaseId as a required input; the handler reads it and every caller sends it", name)
		}
		if hasInputProperty(def, "uri") {
			t.Errorf("%s must not take a uri argument; a connector is addressed by databaseId", name)
		}
		descriptor, ok := descriptors[name]
		if !ok {
			t.Fatalf("%s produced no capability descriptor", name)
		}
		for _, binding := range descriptor.Requires {
			if binding.ResourceType == "sql-connector" {
				t.Errorf("%s must not require a sql-connector resource: nothing adopts that type, so the binding can never resolve", name)
			}
		}
	}

	ensure := descriptors["ensure_sql_connector"]
	produced := false
	for _, binding := range ensure.Produces {
		if binding.ResourceType == "sql-connector" && binding.SourcePath == "uri" {
			produced = true
		}
	}
	if !produced {
		t.Error("ensure_sql_connector must produce the sql-connector resource from its returned uri")
	}
	if !requiresOutputProperty(byName["ensure_sql_connector"], "uri") {
		t.Error("ensure_sql_connector must return uri; the produces binding reads it")
	}
}

func inputSchemaSection(def ToolDefinition, section string) map[string]any {
	schema, ok := def.InputSchema[section]
	if !ok {
		return nil
	}
	value, _ := schema.(map[string]any)
	return value
}

func hasInputProperty(def ToolDefinition, name string) bool {
	_, ok := inputSchemaSection(def, "properties")[name]
	return ok
}

func requiresInputProperty(def ToolDefinition, name string) bool {
	if !hasInputProperty(def, name) {
		return false
	}
	return schemaListContains(def.InputSchema["required"], name)
}

func requiresOutputProperty(def ToolDefinition, name string) bool {
	return schemaListContains(def.OutputSchema["required"], name)
}

func schemaListContains(raw any, name string) bool {
	values, ok := raw.([]any)
	if !ok {
		return false
	}
	for _, value := range values {
		if text, ok := value.(string); ok && text == name {
			return true
		}
	}
	return false
}
