package tui

import (
	"bytes"
	"strings"
	"testing"
)

func testEntities() *EntityIndex {
	return NewEntityIndex([]Entity{
		{Kind: "vm", CanonicalField: "name", CanonicalValue: "worker-01", DisplayName: "Worker 01", Aliases: []string{"worker"}, Provider: "incus", Source: "inventory", CatalogRevision: "catalog-1"},
		{Kind: "vm", CanonicalField: "name", CanonicalValue: "worker-02", DisplayName: "Worker 02", Provider: "incus", Source: "inventory", CatalogRevision: "catalog-1"},
	})
}

func TestEntityExactCanonicalIsTheOnlyAutomaticSelection(t *testing.T) {
	index := testEntities()
	ref, _, err := index.Resolve("@vm:worker-01", -1)
	if err != nil {
		t.Fatalf("exact canonical resolve: %v", err)
	}
	if ref.CanonicalValue != "worker-01" || ref.Selection != "exact_canonical" {
		t.Fatalf("reference = %#v", ref)
	}
	if _, matches, err := index.Resolve("@worker", -1); err == nil || len(matches) != 2 {
		t.Fatalf("prefix/alias should require selection: matches=%d err=%v", len(matches), err)
	}
}

func TestEntitySearchNormalizesButNeverFabricatesIdentity(t *testing.T) {
	index := testEntities()
	matches := index.Search("@Worker 01")
	if len(matches) != 1 || (matches[0].Method != "normalized" && matches[0].Method != "prefix") {
		t.Fatalf("normalized matches = %#v", matches)
	}
	if _, _, err := index.Resolve("@missing", -1); err == nil {
		t.Fatal("missing entity unexpectedly resolved")
	}
}

func TestEntitySelectionUsesInteractiveChoiceAndRecordsExplicitSelection(t *testing.T) {
	var output bytes.Buffer
	input, err := newLineReader(strings.NewReader("2\n"), &output, false)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	app := &App{config: Config{Out: &output}, input: input, entities: testEntities()}
	resolved, refs, err := app.resolveEntityArguments(map[string]any{"vmName": "@worker"})
	if err != nil {
		t.Fatalf("resolve selected entity: %v", err)
	}
	if resolved["vmName"] != "worker-02" {
		t.Fatalf("resolved arguments = %#v", resolved)
	}
	if len(refs) != 1 || refs[0].Selection != "explicit" || refs[0].CanonicalValue != "worker-02" {
		t.Fatalf("references = %#v", refs)
	}
	if !strings.Contains(output.String(), "1) vm:worker-01") || !strings.Contains(output.String(), "2) vm:worker-02") {
		t.Fatalf("selection palette missing: %s", output.String())
	}
}

func TestEntitySelectionFailsClosedWithoutPrompt(t *testing.T) {
	app := &App{config: Config{NoPrompt: true, Out: &bytes.Buffer{}}, entities: testEntities()}
	_, _, err := app.resolveEntityArguments(map[string]any{"vmName": "@worker"})
	if err == nil || !strings.Contains(err.Error(), "selection") {
		t.Fatalf("ambiguous entity should fail closed: %v", err)
	}
}
