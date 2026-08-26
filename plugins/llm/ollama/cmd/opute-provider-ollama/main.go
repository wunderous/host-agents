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
)

func main() {
	port := os.Getenv("OPUTE_PROVIDER_PORT")
	if port == "" {
		port = "4318"
	}
	manifest := providercontract.InstallManifest{
		Schema:   providercontract.InstallManifestVersion,
		Provider: providercontract.ProviderRef{ID: "com.opute.ollama", Version: "1.0.0"},
		Provides: []providercontract.CapabilityRef{{ID: "opute.capability.llm-serving.v1", Version: 1}},
		Recipes: []providercontract.RecipeRef{{
			ID:     "com.opute.ollama.managed-linux",
			Source: providercontract.RecipeSource{URI: "recipes/ollama.yaml", Revision: "working-tree", SHA256: "sha256:5ab8852a0ea89cf00b8592f557d8e9aa80cdc0a6a28cd929b41b8b863d86166c"},
			Mode:   "managed",
		}, {
			ID:     "com.opute.ollama.external",
			Source: providercontract.RecipeSource{URI: "recipes/ollama-external.yaml", Revision: "working-tree", SHA256: "sha256:54104fcd78f20500f2b12907c123c419673e24c5b78ca6a709601aa5b41bc255"},
			Mode:   "external",
		}},
		Services: []providercontract.ServiceDefinition{{
			ID:           "opute.capability.llm-serving",
			CapabilityID: "opute.capability.llm-serving.v1",
			Version:      1,
			Operations: []providercontract.Operation{{
				ID:                "opute.capability.llm-serving.validate",
				InputSchema:       map[string]any{"type": "object", "required": []string{"endpoint"}, "properties": map[string]any{"endpoint": map[string]any{"type": "string"}, "model": map[string]any{"type": "string"}}},
				OutputSchema:      map[string]any{"type": "object"},
				Effect:            "read",
				Idempotent:        true,
				SupportsReadiness: true,
				TaskSupport:       "sync_only",
			}, {
				ID:           "opute.capability.llm-serving.get-context-size",
				InputSchema:  map[string]any{"type": "object", "properties": map[string]any{}},
				OutputSchema: map[string]any{"type": "object", "required": []string{"contractVersion", "capability", "setting", "contextSize", "persisted"}},
				Effect:       "read",
				Idempotent:   true,
				TaskSupport:  "sync_only",
			}, {
				ID:                "opute.capability.llm-serving.set-context-size",
				InputSchema:       map[string]any{"type": "object", "required": []string{"contextSize"}, "properties": map[string]any{"contextSize": map[string]any{"type": "integer", "minimum": ollamaContextMinimum, "maximum": ollamaContextMaximum}}},
				OutputSchema:      map[string]any{"type": "object", "required": []string{"contractVersion", "capability", "setting", "contextSize", "persisted", "applied"}},
				Effect:            "mutation",
				Idempotent:        true,
				SupportsReadiness: true,
				TaskSupport:       "sync_only",
			}},
		}},
		Teardown: &providercontract.Operation{
			ID:                "opute.provider.teardown",
			InputSchema:       map[string]any{"type": "object", "required": []string{"inputs"}, "properties": map[string]any{"inputs": map[string]any{"type": "object"}}},
			OutputSchema:      map[string]any{"type": "object", "required": []string{"contractVersion", "plan"}},
			Effect:            "destructive",
			ResourceKinds:     []string{"service"},
			Idempotent:        true,
			SupportsReadiness: true,
			TaskSupport:       "sync_only",
		},
		Validation: providercontract.ValidationRef{Capability: "opute.capability.llm-serving.v1", Operation: "opute.capability.llm-serving.validate"},
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "opute-provider-ollama", Version: "1.0.0"}, &mcp.ServerOptions{Capabilities: &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{ListChanged: true}}})
	addManifestTool(server, manifest)
	addValidationTool(server)
	addContextTools(server)
	addTeardownTool(server)
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, PropagateRequestCancellation: true})
	log.Printf("Opute Ollama provider listening on :%s/mcp", port)
	if err := http.ListenAndServe(":"+port, handler); err != nil {
		log.Fatal(err)
	}
}

func addTeardownTool(server *mcp.Server) {
	server.AddTool(&mcp.Tool{Name: "opute.provider.teardown", Description: "Return a generic plan that stops and removes the provider-owned Ollama service unit.", InputSchema: map[string]any{"type": "object", "required": []string{"inputs"}, "properties": map[string]any{"inputs": map[string]any{"type": "object"}}}, OutputSchema: map[string]any{"type": "object"}}, func(_ context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var input struct {
			Inputs map[string]any `json:"inputs"`
		}
		if request != nil && request.Params != nil {
			if err := json.Unmarshal(request.Params.Arguments, &input); err != nil {
				return nil, err
			}
		}
		serviceName := stringInput(input.Inputs, "serviceName", "ollama.service")
		serviceFile := stringInput(input.Inputs, "serviceFile", "~/.config/systemd/user/ollama.service")
		return structured(map[string]any{
			"contractVersion": "host-plan.v1",
			"plan":            teardownPlan("com.opute.ollama.teardown", serviceName, serviceFile),
		})
	})
}

func stringInput(inputs map[string]any, name, fallback string) string {
	if value, ok := inputs[name].(string); ok && value != "" {
		return value
	}
	return fallback
}

func teardownPlan(planID, serviceName, serviceFile string) map[string]any {
	return map[string]any{
		"contractVersion": "host-plan.v1",
		"planId":          planID,
		"generation":      1,
		"idempotencyKey":  planID + "-" + serviceName + "-" + serviceFile,
		"nodes": []any{
			map[string]any{
				"id":       "stop",
				"action":   map[string]any{"tool": "set_host_service_state", "args": map[string]any{"serviceName": serviceName, "state": "stop", "scope": "user"}},
				"validate": map[string]any{"tool": "inspect_host_service", "args": map[string]any{"serviceName": serviceName, "scope": "user"}, "assert": []any{map[string]any{"path": "/active", "op": "eq", "value": false}}},
			},
			map[string]any{
				"id":        "disable",
				"dependsOn": []string{"stop"},
				"action":    map[string]any{"tool": "set_host_service_state", "args": map[string]any{"serviceName": serviceName, "state": "disable", "scope": "user"}},
				"validate":  map[string]any{"tool": "inspect_host_service", "args": map[string]any{"serviceName": serviceName, "scope": "user"}, "assert": []any{map[string]any{"path": "/enabled", "op": "eq", "value": false}}},
			},
			map[string]any{
				"id":        "remove-service-file",
				"dependsOn": []string{"disable"},
				"action":    map[string]any{"tool": "remove_host_file", "args": map[string]any{"path": serviceFile, "confirm": true}},
				"validate":  map[string]any{"tool": "inspect_host_file", "args": map[string]any{"path": serviceFile}, "assert": []any{map[string]any{"path": "/exists", "op": "eq", "value": false}}},
			},
		},
	}
}

func addManifestTool(server *mcp.Server, manifest providercontract.InstallManifest) {
	server.AddTool(&mcp.Tool{Name: "opute.provider.get_install_manifest", Description: "Read the Ollama provider installation manifest", InputSchema: map[string]any{"type": "object"}, OutputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return structured(manifest)
	})
}

func addValidationTool(server *mcp.Server) {
	server.AddTool(&mcp.Tool{Name: "opute.capability.llm-serving.validate", Description: "Validate the declared LLM serving endpoint", InputSchema: map[string]any{"type": "object", "required": []string{"endpoint"}, "properties": map[string]any{"endpoint": map[string]any{"type": "string"}, "model": map[string]any{"type": "string"}}}, OutputSchema: map[string]any{"type": "object"}}, func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var args struct {
			Endpoint string `json:"endpoint"`
			Model    string `json:"model"`
		}
		if request != nil && request.Params != nil {
			encoded, _ := json.Marshal(request.Params.Arguments)
			if err := json.Unmarshal(encoded, &args); err != nil {
				return nil, err
			}
		}
		if args.Endpoint == "" {
			return nil, fmt.Errorf("endpoint is required")
		}
		// The host-owned neutral probe remains authoritative. This provider
		// operation is intentionally a thin contract adapter and does not
		// install, start, or mutate Ollama.
		return structured(map[string]any{"contractVersion": "opute.capability.llm-serving.v1", "endpoint": args.Endpoint, "requestedModel": args.Model, "ready": false, "provider": map[string]any{"validationDelegated": true}})
	})
}

func structured(value any) (*mcp.CallToolResult, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(encoded)}}, StructuredContent: value}, nil
}
