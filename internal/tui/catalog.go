package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wunderous/host-agents/internal/plan"
	"github.com/wunderous/host-agents/internal/tools"
)

type Catalog struct {
	Snapshot tools.CapabilityCatalogSnapshot
	byName   map[string]tools.CapabilityDescriptor
	names    []string
}

func NewCatalog(snapshot tools.CapabilityCatalogSnapshot) *Catalog {
	byName := make(map[string]tools.CapabilityDescriptor, len(snapshot.Tools))
	for _, descriptor := range snapshot.Tools {
		byName[descriptor.Name] = descriptor
	}
	return &Catalog{Snapshot: snapshot, byName: byName, names: sortedNames(byName)}
}

func (c *Catalog) Refresh(ctx context.Context, client *Client) error {
	snapshot, err := client.CapabilityCatalog(ctx)
	if err != nil {
		return err
	}
	c.Snapshot = snapshot
	c.byName = make(map[string]tools.CapabilityDescriptor, len(snapshot.Tools))
	for _, descriptor := range snapshot.Tools {
		c.byName[descriptor.Name] = descriptor
	}
	c.names = sortedNames(c.byName)
	return nil
}

func (c *Catalog) Get(name string) (tools.CapabilityDescriptor, bool) {
	if c == nil {
		return tools.CapabilityDescriptor{}, false
	}
	descriptor, ok := c.byName[name]
	return descriptor, ok
}

func (c *Catalog) Names(prefix string) []string {
	if c == nil {
		return nil
	}
	names := make([]string, 0, len(c.names))
	for _, name := range c.names {
		if strings.HasPrefix(name, prefix) {
			names = append(names, name)
		}
	}
	return names
}

func sortedNames(byName map[string]tools.CapabilityDescriptor) []string {
	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (c *Catalog) ValidateCall(name string, args map[string]any) error {
	descriptor, ok := c.Get(name)
	if !ok {
		return fmt.Errorf("unknown capability %q; refresh the catalog", name)
	}
	return plan.ValidateJSON(descriptor.InputSchema, args)
}

func snapshotFromTools(result *mcp.ListToolsResult) tools.CapabilityCatalogSnapshot {
	definitions := make([]tools.ToolDefinition, 0, len(result.Tools))
	for _, tool := range result.Tools {
		if tool == nil {
			continue
		}
		definitions = append(definitions, tools.ToolDefinition{Name: tool.Name, Title: tool.Title, Description: tool.Description, InputSchema: schemaMap(tool.InputSchema), OutputSchema: schemaMap(tool.OutputSchema), Meta: map[string]any(tool.Meta)})
	}
	return tools.BuildCapabilityCatalog("incus", definitions)
}

func schemaMap(value any) map[string]any {
	if value == nil {
		return nil
	}
	if schema, ok := value.(map[string]any); ok {
		return schema
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var schema map[string]any
	if json.Unmarshal(encoded, &schema) != nil {
		return nil
	}
	return schema
}

func decodeSnapshot(value any) (tools.CapabilityCatalogSnapshot, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return tools.CapabilityCatalogSnapshot{}, err
	}
	var snapshot tools.CapabilityCatalogSnapshot
	if err := json.Unmarshal(encoded, &snapshot); err != nil {
		return tools.CapabilityCatalogSnapshot{}, err
	}
	if snapshot.Revision == "" || len(snapshot.Tools) == 0 {
		return tools.CapabilityCatalogSnapshot{}, fmt.Errorf("server returned an empty capability catalog")
	}
	return snapshot, nil
}
