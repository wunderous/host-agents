package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	providercontract "github.com/wunderous/host-agents/contracts/provider"
)

func main() {
	port := os.Getenv("OPUTE_PROVIDER_PORT")
	if port == "" {
		port = "4319"
	}
	manifest := providercontract.InstallManifest{
		Schema:   providercontract.InstallManifestVersion,
		Provider: providercontract.ProviderRef{ID: "com.opute.cloudflare", Version: "1.0.0"},
		Provides: []providercontract.CapabilityRef{{ID: "opute.capability.tunneling.v1", Version: 1}},
		Recipes:  []providercontract.RecipeRef{{ID: "com.opute.cloudflare.tunneling", Source: providercontract.RecipeSource{URI: "recipes/tunneling.yaml", Revision: "working-tree", SHA256: "sha256:2f404972cbe5c463b8fe501973894c241341b2621e5941fad06af1434a958bc7"}, Mode: "tunnel"}, {ID: "com.opute.cloudflare.tunneling.managed", Source: providercontract.RecipeSource{URI: "recipes/tunneling-managed.yaml", Revision: "working-tree", SHA256: "sha256:092810c1e394beee437a672e7e0ed1db9cf0786a3a050c5077536912eeb4e367"}, Mode: "managed"}},
		Services: []providercontract.ServiceDefinition{{
			ID:      "opute.capability.tunneling",
			Version: 1,
			Operations: []providercontract.Operation{{
				ID:                "opute.capability.tunneling.validate",
				InputSchema:       map[string]any{"type": "object", "required": []string{"bindings"}, "properties": map[string]any{"bindings": map[string]any{"type": "array"}}},
				OutputSchema:      map[string]any{"type": "object"},
				Effect:            "read",
				Idempotent:        true,
				SupportsReadiness: true,
			}},
		}},
		Teardown: &providercontract.Operation{
			ID:                "opute.provider.teardown",
			InputSchema:       map[string]any{"type": "object", "required": []string{"inputs"}, "properties": map[string]any{"inputs": map[string]any{"type": "object"}}},
			OutputSchema:      map[string]any{"type": "object", "required": []string{"contractVersion", "plan"}},
			Effect:            "destructive",
			ResourceKinds:     []string{"service", "tunnel"},
			Idempotent:        true,
			SupportsReadiness: true,
		},
		Validation: providercontract.ValidationRef{Capability: "opute.capability.tunneling.v1", Operation: "opute.capability.tunneling.validate"},
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "opute-provider-cloudflare", Version: "1.0.0"}, &mcp.ServerOptions{Capabilities: &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{ListChanged: true}}})
	server.AddTool(&mcp.Tool{Name: "opute.provider.get_install_manifest", Description: "Read the Cloudflare provider installation manifest", InputSchema: map[string]any{"type": "object"}, OutputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return structured(manifest)
	})
	server.AddTool(&mcp.Tool{Name: "opute.capability.tunneling.validate", Description: "Validate declared tunnel bindings", InputSchema: map[string]any{"type": "object", "required": []string{"bindings"}, "properties": map[string]any{"bindings": map[string]any{"type": "array"}}}, OutputSchema: map[string]any{"type": "object"}}, func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var input struct {
			Bindings []any `json:"bindings"`
		}
		if req.Params != nil {
			if err := json.Unmarshal(req.Params.Arguments, &input); err != nil {
				return nil, err
			}
		}
		return structured(map[string]any{
			"contractVersion": "opute.capability.tunneling.v1",
			"ready":           len(input.Bindings) > 0,
			"bindings":        input.Bindings,
		})
	})
	addTeardownTool(server)
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	log.Printf("Opute Cloudflare provider listening on :%s/mcp", port)
	if err := http.ListenAndServe(":"+port, handler); err != nil {
		log.Fatal(err)
	}
}

func addTeardownTool(server *mcp.Server) {
	server.AddTool(&mcp.Tool{Name: "opute.provider.teardown", Description: "Delete declared Cloudflare resources and return a generic plan that stops and removes the provider-owned tunnel service unit.", InputSchema: map[string]any{"type": "object", "required": []string{"inputs"}, "properties": map[string]any{"inputs": map[string]any{"type": "object"}}}, OutputSchema: map[string]any{"type": "object"}}, func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var input struct {
			Inputs map[string]any `json:"inputs"`
		}
		if request != nil && request.Params != nil {
			if err := json.Unmarshal(request.Params.Arguments, &input); err != nil {
				return nil, err
			}
		}
		phase := stringInput(input.Inputs, "phase", "")
		if request != nil && request.Params != nil {
			var envelope map[string]any
			if err := json.Unmarshal(request.Params.Arguments, &envelope); err == nil {
				if value, ok := envelope["phase"].(string); ok {
					phase = value
				}
			}
		}
		if phase == "finalize" {
			if err := cleanupExternalResources(ctx, input.Inputs); err != nil {
				return nil, err
			}
			return structured(map[string]any{"completed": true})
		}
		serviceName := stringInput(input.Inputs, "serviceName", "opute-cloudflare-tunnel.service")
		serviceFile := stringInput(input.Inputs, "serviceFile", "~/.config/systemd/user/opute-cloudflare-tunnel.service")
		cleanupKey := stringInput(input.Inputs, "tunnelId", "") + "-" + strings.Join(stringSliceInput(input.Inputs, "dnsRecordIds"), ",")
		return structured(map[string]any{"contractVersion": "host-plan.v1", "plan": teardownPlan("com.opute.cloudflare.teardown", serviceName, serviceFile, cleanupKey)})
	})
}

var cloudflareIdentifierPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func cleanupExternalResources(ctx context.Context, inputs map[string]any) error {
	tunnelID := stringInput(inputs, "tunnelId", "")
	recordIDs := stringSliceInput(inputs, "dnsRecordIds")
	if tunnelID == "" && len(recordIDs) == 0 {
		return nil
	}
	accountID := strings.TrimSpace(os.Getenv("CLOUDFLARE_ACCOUNT_ID"))
	zoneID := strings.TrimSpace(os.Getenv("CLOUDFLARE_ZONE_ID"))
	apiToken := strings.TrimSpace(os.Getenv("CLOUDFLARE_API_TOKEN"))
	if accountID == "" || zoneID == "" || apiToken == "" {
		return fmt.Errorf("external Cloudflare cleanup requires CLOUDFLARE_ACCOUNT_ID, CLOUDFLARE_ZONE_ID, and CLOUDFLARE_API_TOKEN")
	}
	if tunnelID != "" {
		if !cloudflareIdentifierPattern.MatchString(tunnelID) {
			return fmt.Errorf("invalid Cloudflare tunnel id")
		}
		if err := cloudflareDelete(ctx, apiToken, fmt.Sprintf("https://api.cloudflare.com/client/v4/accounts/%s/cfd_tunnel/%s", accountID, tunnelID)); err != nil {
			return fmt.Errorf("delete Cloudflare tunnel: %w", err)
		}
	}
	for _, recordID := range recordIDs {
		if !cloudflareIdentifierPattern.MatchString(recordID) {
			return fmt.Errorf("invalid Cloudflare DNS record id")
		}
		if err := cloudflareDelete(ctx, apiToken, fmt.Sprintf("https://api.cloudflare.com/client/v4/zones/%s/dns_records/%s", zoneID, recordID)); err != nil {
			return fmt.Errorf("delete Cloudflare DNS record: %w", err)
		}
	}
	return nil
}

func stringSliceInput(inputs map[string]any, name string) []string {
	values, _ := inputs[name].([]any)
	result := make([]string, 0, len(values))
	for _, value := range values {
		if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
			result = append(result, strings.TrimSpace(text))
		}
	}
	return result
}

func cloudflareDelete(ctx context.Context, token, endpoint string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := (&http.Client{}).Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 64*1024))
		return fmt.Errorf("HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func stringInput(inputs map[string]any, name, fallback string) string {
	if value, ok := inputs[name].(string); ok && value != "" {
		return value
	}
	return fallback
}

func teardownPlan(planID, serviceName, serviceFile, cleanupKey string) map[string]any {
	return map[string]any{
		"contractVersion": "host-plan.v1", "planId": planID, "generation": 1,
		"idempotencyKey": planID + "-" + serviceName + "-" + serviceFile + "-" + cleanupKey,
		"nodes": []any{
			map[string]any{"id": "stop", "action": map[string]any{"tool": "set_host_service_state", "args": map[string]any{"serviceName": serviceName, "state": "stop", "scope": "user"}}, "validate": map[string]any{"tool": "inspect_host_service", "args": map[string]any{"serviceName": serviceName, "scope": "user"}, "assert": []any{map[string]any{"path": "/active", "op": "eq", "value": false}}}},
			map[string]any{"id": "disable", "dependsOn": []string{"stop"}, "action": map[string]any{"tool": "set_host_service_state", "args": map[string]any{"serviceName": serviceName, "state": "disable", "scope": "user"}}, "validate": map[string]any{"tool": "inspect_host_service", "args": map[string]any{"serviceName": serviceName, "scope": "user"}, "assert": []any{map[string]any{"path": "/enabled", "op": "eq", "value": false}}}},
			map[string]any{"id": "remove-service-file", "dependsOn": []string{"disable"}, "action": map[string]any{"tool": "remove_host_file", "args": map[string]any{"path": serviceFile, "confirm": true}}, "validate": map[string]any{"tool": "inspect_host_file", "args": map[string]any{"path": serviceFile}, "assert": []any{map[string]any{"path": "/exists", "op": "eq", "value": false}}}},
		},
	}
}

func structured(value any) (*mcp.CallToolResult, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(encoded)}}, StructuredContent: value}, nil
}
