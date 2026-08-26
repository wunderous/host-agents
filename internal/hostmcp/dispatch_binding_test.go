package hostmcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	_ "modernc.org/sqlite"

	hostcapability "github.com/wunderous/host-agents/internal/capability"
	capabilitycatalog "github.com/wunderous/host-agents/internal/catalog"
	"github.com/wunderous/host-agents/internal/ops"
	"github.com/wunderous/host-agents/internal/provider"
	"github.com/wunderous/host-agents/internal/resource"
	"github.com/wunderous/host-agents/internal/tools"
)

// capturingCapability records exactly what crossed the capability boundary so
// tests can prove raw arguments and typed bindings remain separate.
type capturingCapability struct {
	descriptor      tools.CapabilityDescriptor
	receivedArgs    map[string]any
	receivedBinding tools.ExecutionBinding
	validationErr   error
}

func (c *capturingCapability) Definition() tools.CapabilityDescriptor { return c.descriptor }

func (c *capturingCapability) Invoke(_ context.Context, args hostcapability.RawArguments, binding tools.ExecutionBinding, _ hostcapability.ExecutionSink) (*mcp.CallToolResult, error) {
	c.receivedArgs = map[string]any(args)
	c.receivedBinding = binding
	return structuredResult(map[string]any{"ok": true}, ""), nil
}

func (c *capturingCapability) ValidateResult(_ context.Context, result *mcp.CallToolResult) (hostcapability.CapabilityObservation, error) {
	if c.validationErr != nil {
		return hostcapability.CapabilityObservation{}, c.validationErr
	}
	return hostcapability.PassThroughObservation(c.descriptor, result)
}

func newBindingTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	svc := ops.NewHostOperationsService(ops.Options{
		ProviderID: provider.IDIncus,
		ToolsForProvider: func(providerID string) []string {
			names, err := tools.HostToolNamesForProvider(providerID)
			if err != nil {
				return nil
			}
			return names
		},
	})
	stateDir := t.TempDir()
	server, err := NewServer(Options{
		ProviderID:     "incus",
		Ops:            svc,
		Standalone:     true,
		AllowMutations: true,
		StateDir:       stateDir,
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	t.Cleanup(func() { _ = server.Close() })
	return server, stateDir
}

func bindingTestDescriptor(name string, requires ...tools.ResourceBinding) tools.CapabilityDescriptor {
	return tools.CapabilityDescriptor{
		OperationID:    name,
		Version:        1,
		Name:           name,
		Description:    "Test-only binding boundary probe",
		InputSchema:    map[string]any{"type": "object", "properties": map[string]any{"uri": map[string]any{"type": "string"}}},
		OutputSchema:   map[string]any{"type": "object", "required": []string{"ok"}},
		Effect:         "read",
		Provider:       "incus",
		Implementation: "host-agent:incus",
		ResourceKinds:  []string{"vm"},
		Requires:       requires,
	}
}

func TestProviderDispatchDoesNotHoldAdmissionWhileCallingProviderCallback(t *testing.T) {
	server, _ := newBindingTestServer(t)
	config := resource.DefaultConfig(t.TempDir())
	config.MaxNormal = 1
	admission, err := resource.NewCoordinator(config)
	if err != nil {
		t.Fatal(err)
	}
	server.admission = admission

	descriptor := bindingTestDescriptor("opute.capability.fake.callback")
	descriptor.Provider = "incus"
	descriptor.Implementation = "provider:incus"
	capability := &capturingCapability{descriptor: descriptor}
	if err := server.RegisterCapabilityModule(capability, descriptor.Provider, descriptor.Implementation); err != nil {
		t.Fatal(err)
	}

	release, err := admission.Acquire(context.Background(), "provider-admission-regression")
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	result, err := server.DispatchTool(ctx, descriptor.OperationID, map[string]any{"raw": true}, nil)
	if err != nil || result == nil || result.IsError {
		t.Fatalf("provider callback dispatch acquired a nested admission permit: result=%#v err=%v", result, err)
	}
	if capability.receivedArgs["raw"] != true {
		t.Fatalf("provider callback did not receive raw arguments: %#v", capability.receivedArgs)
	}
}

// TestDispatchPreservesRawArgumentsAndRecordsSeparateBinding is the HOST-003
// contract: the exact decoded client object reaches the capability unchanged,
// resolved provider coordinates travel only in the typed execution binding,
// and durable evidence records both separately.
func TestDispatchPreservesRawArgumentsAndRecordsSeparateBinding(t *testing.T) {
	server, stateDir := newBindingTestServer(t)
	if err := server.ops.RegisterResource("vm:local:worker-01", map[string]any{
		"providerInstanceName": "worker-01",
		"displayName":          "worker-01",
		"instanceType":         "vm",
	}); err != nil {
		t.Fatalf("register resource: %v", err)
	}
	capabilityValue := &capturingCapability{descriptor: bindingTestDescriptor("probe.binding.capture", tools.ResourceBinding{Argument: "uri", ResourceType: "vm", Required: true})}
	if err := server.RegisterCapabilityModule(capabilityValue, "incus", "host-agent:incus"); err != nil {
		t.Fatalf("register capability module: %v", err)
	}

	submitted := map[string]any{"uri": "vm:local:worker-01", "opaqueShape": map[string]any{"nested": []any{1, "two"}}}
	result, err := server.DispatchTool(context.Background(), "probe.binding.capture", submitted, nil)
	if err != nil || result == nil || result.IsError {
		t.Fatalf("dispatch = %#v err=%v", result, err)
	}

	if got := capabilityValue.receivedArgs; !structuralEqual(got, submitted) {
		t.Fatalf("capability received rewritten arguments: %#v", got)
	}
	for key := range capabilityValue.receivedArgs {
		if strings.HasPrefix(key, "__resolved") {
			t.Fatalf("synthetic resolved field leaked into raw arguments: %s", key)
		}
	}
	binding := capabilityValue.receivedBinding
	if binding.SchemaVersion != tools.ExecutionBindingSchemaVersion {
		t.Fatalf("binding schema version = %q", binding.SchemaVersion)
	}
	if binding.TenantID != "local" {
		t.Fatalf("binding tenant = %q", binding.TenantID)
	}
	if len(binding.Resources) != 1 {
		t.Fatalf("binding resources = %#v", binding.Resources)
	}
	resource := binding.Resources[0]
	if resource.URI != "vm:local:worker-01" || resource.ResourceType != "vm" || resource.Coordinates["providerInstanceName"] != "worker-01" {
		t.Fatalf("bound resource = %#v", resource)
	}
	if binding.ProviderInstanceName() != "worker-01" {
		t.Fatalf("provider instance name lookup = %q", binding.ProviderInstanceName())
	}

	db, err := sql.Open("sqlite", filepath.Join(stateDir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var argumentsJSON, bindingJSON string
	if err := db.QueryRow(`SELECT arguments_json, binding_json FROM capability_invocations
		WHERE operation_id = ? ORDER BY created_at DESC LIMIT 1`, "probe.binding.capture").Scan(&argumentsJSON, &bindingJSON); err != nil {
		t.Fatalf("read durable invocation evidence: %v", err)
	}
	var recordedArgs map[string]any
	if err := json.Unmarshal([]byte(argumentsJSON), &recordedArgs); err != nil {
		t.Fatal(err)
	}
	if !structuralEqual(recordedArgs, submitted) {
		t.Fatalf("durable arguments differ from submitted raw arguments: %s", argumentsJSON)
	}
	var recordedBinding tools.ExecutionBinding
	if err := json.Unmarshal([]byte(bindingJSON), &recordedBinding); err != nil {
		t.Fatal(err)
	}
	if recordedBinding.ProviderInstanceName() != "worker-01" {
		t.Fatalf("durable binding missing provider coordinates: %s", bindingJSON)
	}
	if strings.Contains(argumentsJSON, "__resolved") || strings.Contains(argumentsJSON, "providerInstanceName") {
		t.Fatalf("provider coordinates leaked into durable raw arguments: %s", argumentsJSON)
	}
}

// TestDispatchFailsClosedOnForeignTenantWrongKindAndUnknownURIs proves tenant
// and canonical-resource admission at the single dispatch boundary.
func TestDispatchFailsClosedOnForeignTenantWrongKindAndUnknownURIs(t *testing.T) {
	server, _ := newBindingTestServer(t)
	if err := server.ops.RegisterResource("vm:local:worker-01", map[string]any{"providerInstanceName": "worker-01"}); err != nil {
		t.Fatalf("register resource: %v", err)
	}
	descriptor := bindingTestDescriptor("probe.binding.admission",
		tools.ResourceBinding{Argument: "uri", ResourceType: "vm", Required: true})
	capabilityValue := &capturingCapability{descriptor: descriptor}
	if err := server.RegisterCapabilityModule(capabilityValue, "incus", "host-agent:incus"); err != nil {
		t.Fatalf("register capability module: %v", err)
	}

	cases := []struct {
		name string
		uri  string
		want string
	}{
		{"foreign tenant", "vm:tenant-b:worker-01", "different tenant"},
		{"wrong resource kind", "container:local:worker-01", "expected"},
		{"unregistered resource", "vm:local:missing-vm", "resource not found"},
	}
	for _, testCase := range cases {
		result, err := server.DispatchTool(context.Background(), descriptor.OperationID, map[string]any{"uri": testCase.uri}, nil)
		if err != nil {
			t.Fatalf("%s: protocol error: %v", testCase.name, err)
		}
		if result == nil || !result.IsError {
			t.Fatalf("%s: admitted unexpectedly: %#v", testCase.name, result)
		}
		text := result.Content[0].(*mcp.TextContent).Text
		if !strings.Contains(text, testCase.want) {
			t.Fatalf("%s: unexpected error text %q", testCase.name, text)
		}
		structured, _ := result.StructuredContent.(map[string]any)
		if structured["owner"] != "admission" || structured["code"] != "resource_binding" {
			t.Fatalf("%s: admission error not typed: %#v", testCase.name, structured)
		}
		if capabilityValue.receivedArgs != nil {
			t.Fatalf("%s: capability was invoked despite failed admission", testCase.name)
		}
	}
}

// TestCapabilityOwnedInvalidArgumentsSurfaceTypedOwnerError proves invalid
// input is owned by the capability and rendered as a typed owner error rather
// than a generic arguments-invalid transport failure.
func TestCapabilityOwnedInvalidArgumentsSurfaceTypedOwnerError(t *testing.T) {
	server, _ := newBindingTestServer(t)
	descriptor := bindingTestDescriptor("probe.binding.invalid-input")
	descriptor.InputSchema = map[string]any{
		"type":       "object",
		"properties": map[string]any{"name": map[string]any{"type": "string"}},
		"required":   []string{"name"},
	}
	invoked := false
	if err := server.RegisterCapability(capabilitycatalog.Registration{
		Descriptor: descriptor, ProviderID: "incus", Implementation: "host-agent:incus",
	}, func(_ context.Context, _ map[string]any) (*mcp.CallToolResult, error) {
		invoked = true
		return structuredResult(map[string]any{"ok": true}, ""), nil
	}); err != nil {
		t.Fatalf("register capability: %v", err)
	}

	result, err := server.DispatchTool(context.Background(), descriptor.OperationID, map[string]any{"unexpected": true}, nil)
	if err != nil {
		t.Fatalf("protocol error: %v", err)
	}
	if result == nil || !result.IsError {
		t.Fatalf("invalid input accepted: %#v", result)
	}
	structured, _ := result.StructuredContent.(map[string]any)
	if structured["owner"] != "capability" || structured["code"] != "invalid_arguments" {
		t.Fatalf("typed owner error missing: %#v", structured)
	}
	if invoked {
		t.Fatal("capability body ran despite schema-owned input rejection")
	}
}

func TestRequiredBindingUsesDeclaredArgumentAndRejectsMissingValues(t *testing.T) {
	server, _ := newBindingTestServer(t)
	if err := server.ops.RegisterResource("database:local:db-1", map[string]any{"providerInstanceName": "db-1"}); err != nil {
		t.Fatal(err)
	}
	descriptor := bindingTestDescriptor("probe.binding.database", tools.ResourceBinding{Argument: "databaseUri", ResourceType: "database", Required: true})
	descriptor.InputSchema = map[string]any{
		"type":       "object",
		"properties": map[string]any{"databaseUri": map[string]any{"type": "string"}},
		"required":   []string{"databaseUri"},
	}
	capabilityValue := &capturingCapability{descriptor: descriptor}
	if err := server.RegisterCapabilityModule(capabilityValue, "incus", "host-agent:incus"); err != nil {
		t.Fatal(err)
	}
	missing, err := server.DispatchTool(context.Background(), descriptor.OperationID, map[string]any{"uri": "database:local:db-1"}, nil)
	if err != nil || missing == nil || !missing.IsError {
		t.Fatalf("missing declared binding was admitted: %#v err=%v", missing, err)
	}
	if structured, _ := missing.StructuredContent.(map[string]any); structured["code"] != "resource_binding" {
		t.Fatalf("missing binding error was not typed: %#v", missing.StructuredContent)
	}
	if capabilityValue.receivedArgs != nil {
		t.Fatal("capability ran without its required declared binding")
	}
}

func TestDurableInvocationPreservesTypedValidationErrors(t *testing.T) {
	server, stateDir := newBindingTestServer(t)
	descriptor := bindingTestDescriptor("probe.binding.invalid-result")
	capabilityValue := &capturingCapability{descriptor: descriptor, validationErr: errors.New("provider payload did not match output schema")}
	if err := server.RegisterCapabilityModule(capabilityValue, "incus", "host-agent:incus"); err != nil {
		t.Fatal(err)
	}
	result, err := server.DispatchTool(context.Background(), descriptor.OperationID, map[string]any{}, nil)
	if err != nil || result == nil || !result.IsError {
		t.Fatalf("invalid result did not fail as a typed tool error: %#v err=%v", result, err)
	}
	db, err := sql.Open("sqlite", filepath.Join(stateDir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var resultJSON string
	if err := db.QueryRow(`SELECT result_json FROM capability_invocations WHERE operation_id = ? ORDER BY created_at DESC LIMIT 1`, descriptor.OperationID).Scan(&resultJSON); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resultJSON, `"isError":true`) || !strings.Contains(resultJSON, `"code":"invalid_result"`) || !strings.Contains(resultJSON, `"owner":"capability"`) {
		t.Fatalf("durable typed error evidence incomplete: %s", resultJSON)
	}
}

// structuralEqual compares decoded JSON values; Go marshals map keys in sorted
// order, so equal structures always produce equal canonical JSON.
func structuralEqual(left, right any) bool {
	leftJSON, errLeft := json.Marshal(left)
	rightJSON, errRight := json.Marshal(right)
	return errLeft == nil && errRight == nil && string(leftJSON) == string(rightJSON)
}
