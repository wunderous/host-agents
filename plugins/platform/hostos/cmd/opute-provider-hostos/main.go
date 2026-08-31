// Command opute-provider-hostos is the Opute host-platform provider. It is a
// read-only provider generation: it observes the operating system and CPU of
// the machine the Host Agent runs on and never mutates the host, so it
// declares no recipes and no teardown plan.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	providercontract "github.com/wunderous/host-agents/contracts/provider"
	"github.com/wunderous/host-agents/pkg/hostplatform"
)

const (
	providerID      = "com.opute.hostos"
	providerVersion = "1.0.0"
	capabilityID    = "opute.capability.host-platform.v1"
	serviceID       = "opute.capability.host-platform"
	detectOperation = "opute.capability.host-platform.detect"
	validateOp      = "opute.capability.host-platform.validate"
)

func main() {
	port := os.Getenv("OPUTE_PROVIDER_PORT")
	if port == "" {
		port = "4321"
	}
	server := newServer()
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, PropagateRequestCancellation: true})
	log.Printf("Opute host platform provider listening on :%s/mcp", port)
	if err := http.ListenAndServe(":"+port, handler); err != nil {
		log.Fatal(err)
	}
}

func newServer() *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "opute-provider-hostos", Version: providerVersion}, &mcp.ServerOptions{Capabilities: &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{ListChanged: true}}})
	addManifestTool(server)
	addDetectTool(server)
	addValidateTool(server)
	return server
}

func installManifest() providercontract.InstallManifest {
	return providercontract.InstallManifest{
		Schema:   providercontract.InstallManifestVersion,
		Provider: providercontract.ProviderRef{ID: providerID, Version: providerVersion},
		Provides: []providercontract.CapabilityRef{{ID: capabilityID, Version: 1}},
		Services: []providercontract.ServiceDefinition{{
			ID:           serviceID,
			CapabilityID: capabilityID,
			Version:      1,
			Operations: []providercontract.Operation{{
				ID:                detectOperation,
				Version:           1,
				Description:       "Detect the operating system and CPU identity of the host running the agent.",
				InputSchema:       map[string]any{"type": "object", "properties": map[string]any{}},
				OutputSchema:      platformSchema(),
				Effect:            "read",
				Idempotent:        true,
				SupportsReadiness: true,
				TaskSupport:       "sync_only",
			}, {
				ID:                validateOp,
				Version:           1,
				Description:       "Validate a caller-declared expected host kind or CPU family against the observed host.",
				InputSchema:       validateInputSchema(),
				OutputSchema:      map[string]any{"type": "object", "required": []string{"contractVersion", "ready", "platform"}},
				Effect:            "read",
				Idempotent:        true,
				SupportsReadiness: true,
				TaskSupport:       "sync_only",
			}},
		}},
		Validation: providercontract.ValidationRef{Capability: capabilityID, Operation: validateOp},
	}
}

func platformSchema() map[string]any {
	return map[string]any{"type": "object", "required": []string{"contractVersion", "os", "kind", "cpu"}, "properties": map[string]any{
		"contractVersion": map[string]any{"type": "string", "const": hostplatform.ContractVersion},
		"os":              map[string]any{"type": "string", "enum": []string{"linux", "macos", "windows"}},
		"kind":            map[string]any{"type": "string", "enum": []string{"linux", "macos", "windows-native", "wsl1", "wsl2"}},
		"cpu": map[string]any{"type": "object", "required": []string{"architecture", "family"}, "properties": map[string]any{
			"architecture": map[string]any{"type": "string"},
			"family":       map[string]any{"type": "string", "enum": []string{"x86-64", "x86", "arm64", "arm", "apple-silicon", "unknown"}},
			"vendor":       map[string]any{"type": "string"},
			"model":        map[string]any{"type": "string"},
			"series":       map[string]any{"type": "string"},
			"variant":      map[string]any{"type": "string"},
			"logicalCores": map[string]any{"type": "integer"},
		}},
	}}
}

func validateInputSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"expectedKind":      map[string]any{"type": "string", "enum": []string{"linux", "macos", "windows-native", "wsl1", "wsl2"}},
		"expectedCPUFamily": map[string]any{"type": "string", "enum": []string{"x86-64", "x86", "arm64", "arm", "apple-silicon", "unknown"}},
	}}
}

func addManifestTool(server *mcp.Server) {
	manifest := installManifest()
	server.AddTool(&mcp.Tool{Name: "opute.provider.get_install_manifest", Description: "Read the host platform provider installation manifest", InputSchema: map[string]any{"type": "object"}, OutputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return structured(manifest)
	})
}

func addDetectTool(server *mcp.Server) {
	server.AddTool(&mcp.Tool{Name: detectOperation, Description: "Detect the operating system and CPU identity of the host running the agent, distinguishing native Windows, macOS, WSL1/WSL2, and native Linux.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{}}, OutputSchema: platformSchema()}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return structured(hostplatform.Detect())
	})
}

func addValidateTool(server *mcp.Server) {
	server.AddTool(&mcp.Tool{Name: validateOp, Description: "Validate a caller-declared expected host kind or CPU family against the observed host.", InputSchema: validateInputSchema(), OutputSchema: map[string]any{"type": "object"}}, func(_ context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var args validateArgs
		if request != nil && request.Params != nil && len(request.Params.Arguments) > 0 {
			if err := json.Unmarshal(request.Params.Arguments, &args); err != nil {
				return nil, err
			}
		}
		return structured(validateHost(args, hostplatform.Detect()))
	})
}

type validateArgs struct {
	ExpectedKind      string `json:"expectedKind"`
	ExpectedCPUFamily string `json:"expectedCPUFamily"`
}

// validateHost fails closed: an expectation that does not match the observed
// host is reported as not ready with the exact mismatch, never coerced into a
// nearby host kind.
func validateHost(args validateArgs, platform hostplatform.Platform) map[string]any {
	mismatches := []string{}
	if args.ExpectedKind != "" && args.ExpectedKind != platform.Kind {
		mismatches = append(mismatches, fmt.Sprintf("expected host kind %q, observed %q", args.ExpectedKind, platform.Kind))
	}
	if args.ExpectedCPUFamily != "" && args.ExpectedCPUFamily != platform.CPU.Family {
		mismatches = append(mismatches, fmt.Sprintf("expected CPU family %q, observed %q", args.ExpectedCPUFamily, platform.CPU.Family))
	}
	return map[string]any{
		"contractVersion": hostplatform.ContractVersion,
		"capability":      capabilityID,
		"ready":           len(mismatches) == 0,
		"mismatches":      mismatches,
		"platform":        platform,
	}
}

func structured(value any) (*mcp.CallToolResult, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(encoded)}}, StructuredContent: value}, nil
}
