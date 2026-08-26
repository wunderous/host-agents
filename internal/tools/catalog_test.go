package tools

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestStandaloneCatalogAdvertisesOllamaDefaultAndLlamaAlternate(t *testing.T) {
	localNames := map[string]bool{
		"check_local_llm_prerequisites": true,
		"list_local_llm_models":         true,
		"probe_local_llm":               true,
		"install_local_llm_model":       true,
		"configure_local_llm_model":     true,
		"start_local_llm_runtime":       true,
		"configure_local_llm_runtime":   true,
		"stop_local_llm_runtime":        true,
		"remove_local_llm_model":        true,
	}
	for _, definition := range StandaloneToolDefinitions() {
		if !localNames[definition.Name] {
			continue
		}
		encoded, err := json.Marshal(definition)
		if err != nil {
			t.Fatal(err)
		}
		text := strings.ToLower(string(encoded))
		if strings.Contains(text, "transformers") {
			t.Fatalf("retired runtime advertised for %s: %s", definition.Name, text)
		}
		if !strings.Contains(text, "ollama") && !strings.Contains(text, "llama-cpp") && definition.Name != "check_local_llm_prerequisites" && definition.Name != "configure_local_llm_model" && definition.Name != "configure_local_llm_runtime" && definition.Name != "remove_local_llm_model" {
			t.Fatalf("runtime contract missing from %s", definition.Name)
		}
	}
}

func TestCanonicalizeToolDefinitionsDoesNotInventResourceIdentities(t *testing.T) {
	defs := []ToolDefinition{
		{
			Name: "create_vm",
			InputSchema: map[string]any{
				"type": "object", "required": []string{"vmName"},
				"properties": map[string]any{"vmName": map[string]any{"type": "string"}},
			},
		},
		{
			Name: "start_vm",
			InputSchema: map[string]any{
				"type": "object", "required": []string{"uri"},
				"properties": map[string]any{"uri": map[string]any{"type": "string"}, "force": map[string]any{"type": "boolean"}},
			},
			OutputSchema: map[string]any{
				"type": "object", "properties": map[string]any{
					"status": map[string]any{"type": "string"},
					"uri":    map[string]any{"type": "string"},
				},
			},
			Meta: map[string]any{
				"requires": []any{map[string]any{"argument": "uri", "resourceType": "vm", "required": true}},
				"produces": []any{map[string]any{"sourcePath": "uri", "resourceType": "vm"}},
			},
		},
	}
	got := CanonicalizeToolDefinitions(defs)
	createProps := got[0].InputSchema["properties"].(map[string]any)
	if _, ok := createProps["vmName"]; !ok {
		t.Fatalf("creation name was rewritten: %#v", createProps)
	}
	startProps := got[1].InputSchema["properties"].(map[string]any)
	if _, ok := startProps["uri"]; !ok {
		t.Fatalf("explicit canonical uri was removed: %#v", startProps)
	}
	if required := got[1].InputSchema["required"].([]string); len(required) != 1 || required[0] != "uri" {
		t.Fatalf("unexpected lifecycle required fields: %#v", got[1].InputSchema["required"])
	}
	if bindings := got[1].Meta["requires"].([]map[string]any); len(bindings) != 1 || bindings[0]["argument"] != "uri" || bindings[0]["resourceType"] != "vm" {
		t.Fatalf("canonical lifecycle binding missing: %#v", got[1].Meta["requires"])
	}
	outputRequired := got[1].OutputSchema["properties"].(map[string]any)
	if _, ok := outputRequired["uri"]; !ok {
		t.Fatalf("explicit output uri was removed: %#v", outputRequired)
	}
}

func TestCapabilityCatalogDoesNotInferResourceBindings(t *testing.T) {
	defs := []ToolDefinition{
		{
			Name:         "list_vms",
			InputSchema:  map[string]any{"type": "object"},
			OutputSchema: map[string]any{"type": "object", "properties": map[string]any{"uri": map[string]any{"type": "string"}}},
		},
		{
			Name:         "get_vm_info",
			InputSchema:  map[string]any{"type": "object", "required": []any{"uri"}, "properties": map[string]any{"uri": map[string]any{"type": "string"}}},
			OutputSchema: map[string]any{"type": "object"},
		},
	}
	snapshot := BuildCapabilityCatalog("incus", defs)
	for _, descriptor := range snapshot.Tools {
		if len(descriptor.ArgumentProducers) != 0 || len(descriptor.Requires) != 0 || len(descriptor.Produces) != 0 {
			t.Fatalf("catalog inferred a binding: %+v", descriptor)
		}
	}
}

func TestCapabilityCatalogCarriesOnlyExplicitResourceBindings(t *testing.T) {
	snapshot := BuildCapabilityCatalog("incus", []ToolDefinition{{
		Name:        "list_vms",
		InputSchema: map[string]any{"type": "object"},
		OutputSchema: map[string]any{"type": "object", "properties": map[string]any{
			"items": map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{"uri": map[string]any{"type": "string"}}}},
		}},
		Meta: map[string]any{"produces": []any{map[string]any{"resourceType": "vm", "sourcePath": "items[].uri"}}},
	}})
	if len(snapshot.Tools) != 1 || len(snapshot.Tools[0].Produces) != 1 || snapshot.Tools[0].Produces[0].SourcePath != "items[].uri" {
		t.Fatalf("explicit binding missing: %+v", snapshot.Tools)
	}
}

func TestCapabilityCatalogDerivesBidirectionalCapabilityEdges(t *testing.T) {
	snapshot := BuildCapabilityCatalog("incus", []ToolDefinition{
		{
			Name:         "get_vm_info",
			InputSchema:  map[string]any{"type": "object", "required": []string{"uri"}, "properties": map[string]any{"uri": map[string]any{"type": "string"}}},
			OutputSchema: map[string]any{"type": "object"},
			Meta:         map[string]any{"requires": []any{map[string]any{"argument": "uri", "resourceType": "vm", "required": true}}},
		},
		{
			Name:         "list_vms",
			InputSchema:  map[string]any{"type": "object"},
			OutputSchema: map[string]any{"type": "object", "properties": map[string]any{"vms": map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{"uri": map[string]any{"type": "string"}}}}}},
			Meta:         map[string]any{"produces": []any{map[string]any{"resourceType": "vm", "sourcePath": "vms[].uri"}}},
		},
	})
	if len(snapshot.Edges) != 1 {
		t.Fatalf("edges = %#v want one edge", snapshot.Edges)
	}
	edge := snapshot.Edges[0]
	if edge.SourceTool != "list_vms" || edge.SourcePath != "vms[].uri" || edge.TargetTool != "get_vm_info" || edge.TargetArgument != "uri" || edge.ResourceType != "vm" {
		t.Fatalf("edge = %#v", edge)
	}
	var source, target CapabilityDescriptor
	for _, descriptor := range snapshot.Tools {
		switch descriptor.Name {
		case "list_vms":
			source = descriptor
		case "get_vm_info":
			target = descriptor
		}
	}
	if len(source.OutputEdges) != 1 || len(target.InputEdges) != 1 {
		t.Fatalf("edge projections source=%#v target=%#v", source.OutputEdges, target.InputEdges)
	}
	if got := target.ArgumentProducers["uri"]; len(got) != 1 || got[0] != "list_vms" {
		t.Fatalf("argument producers = %#v", target.ArgumentProducers)
	}
}

func TestCapabilityCatalogRebuildsEdgesAndDropsLegacyProducerMetadata(t *testing.T) {
	snapshot := BuildCapabilityCatalogFromDescriptors("incus", []CapabilityDescriptor{
		{
			Name: "plugin_list_hosts", OperationID: "plugin_list_hosts", Version: 1,
			InputSchema: map[string]any{"type": "object"},
			OutputSchema: map[string]any{"type": "object", "properties": map[string]any{
				"hosts": map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{"uri": map[string]any{"type": "string"}}}},
			}},
			Effect: "read", Provider: "incus", Implementation: "plugin",
			Produces: []ResourceBinding{{ResourceType: "host", SourcePath: "hosts[].uri"}},
		},
		{
			Name: "inspect_host", OperationID: "inspect_host", Version: 1,
			InputSchema:  map[string]any{"type": "object", "required": []string{"uri"}, "properties": map[string]any{"uri": map[string]any{"type": "string"}}},
			OutputSchema: map[string]any{"type": "object"},
			Effect:       "read", Provider: "incus", Implementation: "host",
			Requires:          []ResourceBinding{{Argument: "uri", ResourceType: "host", Required: true}},
			ArgumentProducers: map[string][]string{"uri": {"untrusted_tool_name"}},
		},
	})
	if len(snapshot.Edges) != 1 || snapshot.Edges[0].SourceTool != "plugin_list_hosts" || snapshot.Edges[0].ResourceType != "host" {
		t.Fatalf("typed plugin edge = %#v", snapshot.Edges)
	}
	for _, descriptor := range snapshot.Tools {
		if descriptor.Name == "inspect_host" && len(descriptor.ArgumentProducers["uri"]) != 1 || descriptor.Name == "inspect_host" && descriptor.ArgumentProducers["uri"][0] != "plugin_list_hosts" {
			t.Fatalf("legacy producer metadata survived: %#v", descriptor.ArgumentProducers)
		}
	}
}

func TestCapabilityCatalogDerivesEdgesAcrossSupportedResourceKinds(t *testing.T) {
	kinds := []string{"host", "vm", "container", "pod", "cluster", "database", "service"}
	descriptors := make([]CapabilityDescriptor, 0, len(kinds)*2)
	for _, kind := range kinds {
		producer := "produce_" + kind
		consumer := "consume_" + kind
		descriptors = append(descriptors,
			CapabilityDescriptor{Name: producer, OperationID: producer, Version: 1, InputSchema: map[string]any{"type": "object"}, OutputSchema: map[string]any{"type": "object", "properties": map[string]any{"uri": map[string]any{"type": "string"}}}, Effect: "read", Provider: "incus", Implementation: "test", Produces: []ResourceBinding{{ResourceType: kind, SourcePath: "uri"}}},
			CapabilityDescriptor{Name: consumer, OperationID: consumer, Version: 1, InputSchema: map[string]any{"type": "object", "properties": map[string]any{"uri": map[string]any{"type": "string"}}, "required": []string{"uri"}}, OutputSchema: map[string]any{"type": "object"}, Effect: "read", Provider: "incus", Implementation: "test", Requires: []ResourceBinding{{Argument: "uri", ResourceType: kind, Required: true}}},
		)
	}
	snapshot := BuildCapabilityCatalogFromDescriptors("incus", descriptors)
	if len(snapshot.Edges) != len(kinds) {
		t.Fatalf("universal typed edges = %d, want %d: %#v", len(snapshot.Edges), len(kinds), snapshot.Edges)
	}
}

func TestIncusCatalogPublishesVMInventoryContinuation(t *testing.T) {
	definitions, err := LoadAllToolDefinitions("incus")
	if err != nil {
		t.Fatal(err)
	}
	snapshot := BuildCapabilityCatalog("incus", definitions)
	for _, descriptor := range snapshot.Tools {
		if descriptor.OperationID != "list_vms" {
			continue
		}
		if descriptor.OutputType != "vm.uri" || len(descriptor.ResultTypes) != 1 || len(descriptor.ResultTypes[0].Selectors) != 1 || descriptor.ResultTypes[0].Selectors[0].ID != "uri" {
			t.Fatalf("list_vms result selector contract = %#v", descriptor)
		}
	}
	for _, edge := range snapshot.Edges {
		if edge.SourceTool == "list_vms" && edge.TargetTool == "get_vm_info" && edge.TargetArgument == "uri" && edge.SourcePath == "vms[].uri" {
			if edge.SelectorID != "uri" {
				t.Fatalf("VM inventory continuation selector = %#v", edge)
			}
			return
		}
	}
	t.Fatalf("VM inventory continuation missing from catalog edges: %#v", snapshot.Edges)
}

func TestCanonicalBuiltInTargetsDeclareExecutionBindings(t *testing.T) {
	definitions, err := LoadAllToolDefinitions("incus")
	if err != nil {
		t.Fatal(err)
	}
	wanted := map[string]string{
		"start_vm": "vm", "list_namespaces": "cluster", "list_pods": "cluster",
	}
	seen := map[string]bool{}
	for _, definition := range definitions {
		resourceType, ok := wanted[definition.Name]
		if !ok {
			continue
		}
		bindings := explicitResourceBindings(definition.Meta, "requires")
		if !hasRequiredBinding(bindings, resourceType) {
			t.Fatalf("%s binding = %#v", definition.Name, bindings)
		}
		seen[definition.Name] = true
	}
	for name := range wanted {
		if !seen[name] {
			t.Fatalf("expected built-in definition %q was not loaded", name)
		}
	}
}

func mustLoadDefinitions(t *testing.T, providerID string) []ToolDefinition {
	t.Helper()
	defs, err := HostToolDefinitionsForProvider(providerID)
	if err != nil {
		t.Fatal(err)
	}
	return defs
}

func hasRequiredBinding(bindings []ResourceBinding, resourceType string) bool {
	for _, binding := range bindings {
		if binding.Argument == "uri" && binding.ResourceType == resourceType && binding.Required {
			return true
		}
	}
	return false
}

func TestCanonicalKubernetesOperationsRequireClusterURIs(t *testing.T) {
	definitions, err := LoadAllToolDefinitions("incus")
	if err != nil {
		t.Fatal(err)
	}
	wanted := map[string]bool{
		"apply_manifest":          true,
		"put_k8s_secret":          true,
		"get_k8s_resource":        true,
		"delete_k8s_resource":     true,
		"get_k8s_resource_status": true,
		"list_k8s_events":         true,
	}
	seen := map[string]bool{}
	for _, definition := range definitions {
		if !wanted[definition.Name] {
			continue
		}
		bindings := explicitResourceBindings(definition.Meta, "requires")
		if len(bindings) != 1 || bindings[0].Argument != "uri" || bindings[0].ResourceType != "cluster" || !bindings[0].Required {
			t.Fatalf("%s binding = %#v", definition.Name, bindings)
		}
		seen[definition.Name] = true
	}
	for name := range wanted {
		if !seen[name] {
			t.Fatalf("expected Kubernetes definition %q was not loaded", name)
		}
	}
}
