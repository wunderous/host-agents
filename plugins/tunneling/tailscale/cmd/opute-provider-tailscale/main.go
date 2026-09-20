package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	capabilitycontract "github.com/wunderous/host-agents/contracts/capability"
	providercontract "github.com/wunderous/host-agents/contracts/provider"
	"github.com/wunderous/host-agents/internal/mcphttp"
	"github.com/wunderous/host-agents/internal/resourceid"
)

const (
	tunnelingCapability = "opute.capability.tunneling.v1"
	providerGeneration  = "com.opute.tailscale@1.0.0"
	defaultProviderPort = "4321"
	providerPluginID    = "com.opute.tailscale"
	providerPluginVer   = "1.0.0"
)

func main() {
	port := firstNonEmpty(os.Getenv("OPUTE_PROVIDER_PORT"), defaultProviderPort)
	manifest := tailscaleManifest()
	server := mcp.NewServer(&mcp.Implementation{Name: "opute-provider-tailscale", Version: providerPluginVer}, &mcp.ServerOptions{Capabilities: &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{ListChanged: true}}})
	addManifestTool(server, manifest)
	addTailscaleOperations(server)
	addTeardownTool(server)
	handler := mcphttp.WrapProviderHandler(
		mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, PropagateRequestCancellation: true}),
		map[string]any{"name": "opute-provider-tailscale", "version": providerPluginVer},
	)
	log.Printf("Opute Tailscale provider listening on :%s/mcp", port)
	if err := http.ListenAndServe(":"+port, handler); err != nil {
		log.Fatal(err)
	}
}

func tailscaleManifest() providercontract.InstallManifest {
	return providercontract.InstallManifest{
		Schema:   providercontract.InstallManifestVersion,
		Provider: providercontract.ProviderRef{ID: providerPluginID, Version: providerPluginVer},
		Provides: []providercontract.CapabilityRef{
			{ID: tunnelingCapability, Version: 1},
			{ID: capabilitycontract.NetworkOverlay, Version: 1},
		},
		Recipes: []providercontract.RecipeRef{
			{ID: "com.opute.tailscale.overlay-mesh", Source: providercontract.RecipeSource{URI: "recipes/overlay-mesh.yaml", Revision: "working-tree", SHA256: "sha256:d69e7afff15d7e9bf9990fd0bb8d85686a8efb73d00b21048a01f95a6ffa8c36"}, Mode: "private-mesh"},
			{ID: "com.opute.tailscale.public-ingress", Source: providercontract.RecipeSource{URI: "recipes/public-ingress.yaml", Revision: "working-tree", SHA256: "sha256:f6245b0462e2faf31a037531215b39969cbe2b8bd277a7a5ffc2bff30326544c"}, Mode: "public-ingress"},
			{ID: "com.opute.tailscale.install", Source: providercontract.RecipeSource{URI: "recipes/install.yaml", Revision: "working-tree", SHA256: "sha256:c6e955fb74212182746cee4038554d91091746704b3bd0dc7ad9fcd03c3602e6"}, Mode: "install"},
		},
		Services: []providercontract.ServiceDefinition{
			{ID: "opute.capability.tunneling", CapabilityID: tunnelingCapability, Version: 1, Operations: tunnelingOperations()},
			{ID: "opute.capability.network-overlay", CapabilityID: capabilitycontract.NetworkOverlay, Version: 1, Operations: networkOverlayOperations()},
		},
		Teardown: &providercontract.Operation{
			ID: "opute.provider.teardown", Version: 1, InputSchema: teardownSchema(), OutputSchema: map[string]any{"type": "object", "required": []string{"contractVersion", "plan"}},
			Effect: "destructive", ResourceKinds: []string{"service", "network"}, Idempotent: true, SupportsReadiness: true, TaskSupport: "sync_only",
			ResourceCost: &providercontract.ResourceCost{Class: "control"},
		},
		Validation: providercontract.ValidationRef{Capability: capabilitycontract.NetworkOverlay, Operation: capabilitycontract.NetworkOverlayValidateOperation},
	}
}

func tunnelingOperations() []providercontract.Operation {
	return []providercontract.Operation{
		providerOperation("opute.capability.tunneling.validate", "read", map[string]any{
			"type": "object", "required": []string{"bindings"},
			"properties": map[string]any{"bindings": map[string]any{"type": "array"}, "placement": map[string]any{"type": "string", "enum": []string{"host", "kubernetes", "container"}}},
		}, map[string]any{"type": "object"}, nil, nil),
	}
}

func networkOverlayOperations() []providercontract.Operation {
	read := func(id string, input map[string]any) providercontract.Operation {
		return providerOperation(id, "read", input, map[string]any{"type": "object"}, []string{"host", "network"}, overlayTargetBinding())
	}
	mutation := func(id string, input map[string]any) providercontract.Operation {
		return providerOperation(id, "mutation", input, map[string]any{"type": "object"}, []string{"host", "network"}, overlayTargetBinding())
	}
	destructive := func(id string, input map[string]any) providercontract.Operation {
		return providerOperation(id, "destructive", input, map[string]any{"type": "object"}, []string{"host", "network"}, overlayTargetBinding())
	}
	credProps := map[string]any{
		"credentialKind": map[string]any{"type": "string", "enum": []string{"api-key", "auth-key"}},
		"credential":     map[string]any{"type": "string", "writeOnly": true},
		"apiKey":         map[string]any{"type": "string", "writeOnly": true},
		"authKey":        map[string]any{"type": "string", "writeOnly": true},
	}
	return []providercontract.Operation{
		read(capabilitycontract.NetworkOverlayValidateOperation, map[string]any{
			"type": "object", "required": []string{"targetUri"},
			"properties": mergeProps(map[string]any{"targetUri": targetURISchema()}, credProps),
		}),
		mutation(capabilitycontract.NetworkOverlayPrepareMembershipOperation, map[string]any{
			"type": "object", "required": []string{"targetUri", "name"},
			"properties": mergeProps(map[string]any{
				"targetUri": targetURISchema(),
				"name":      map[string]any{"type": "string", "pattern": "^[A-Za-z0-9][A-Za-z0-9_.-]{0,62}$"},
			}, credProps),
		}),
		mutation(capabilitycontract.NetworkOverlayAttachTargetOperation, map[string]any{
			"type": "object", "required": []string{"membershipRef", "targetUri"},
			"properties": mergeProps(map[string]any{
				"membershipRef": map[string]any{"type": "string", "minLength": 1, "writeOnly": true},
				"targetUri":     targetURISchema(),
			}, credProps),
		}),
		mutation(capabilitycontract.NetworkOverlayEnrollOperation, map[string]any{
			"type": "object", "required": []string{"targetUri", "name"},
			"properties": mergeProps(map[string]any{
				"targetUri":     targetURISchema(),
				"name":          map[string]any{"type": "string", "pattern": "^[A-Za-z0-9][A-Za-z0-9_.-]{0,62}$"},
				"membershipRef": map[string]any{"type": "string", "minLength": 1, "writeOnly": true},
			}, credProps),
		}),
		read(capabilitycontract.NetworkOverlayProbeReachabilityOperation, map[string]any{
			"type": "object", "required": []string{"targetUri", "peerMeshIp"},
			"properties": map[string]any{
				"targetUri":  targetURISchema(),
				"peerMeshIp": map[string]any{"type": "string", "format": "ipv4"},
				"pathClass":  map[string]any{"type": "string", "enum": []string{"private-mesh", "public-ingress"}},
			},
		}),
		read(capabilitycontract.NetworkOverlayProbeOperation, map[string]any{
			"type": "object", "required": []string{"targetUri", "pathClass"},
			"properties": map[string]any{
				"targetUri":  targetURISchema(),
				"peerMeshIp": map[string]any{"type": "string", "format": "ipv4"},
				"pathClass":  map[string]any{"type": "string", "enum": []string{"private-mesh", "public-ingress"}},
			},
		}),
		read(capabilitycontract.NetworkOverlayReportTwoNodeReadinessOperation, map[string]any{
			"type":     "object",
			"required": []string{"targetUri", "peerTargetUri", "hostAgentId", "datastoreMode"},
			"properties": map[string]any{
				"targetUri":         targetURISchema(),
				"peerTargetUri":     targetURISchema(),
				"hostAgentId":       map[string]any{"type": "string", "minLength": 1},
				"datastoreMode":     map[string]any{"type": "string", "enum": []string{"external-datastore", "embedded-etcd-two-node"}},
				"datastoreEvidence": map[string]any{"type": "boolean"},
			},
		}),
		mutation(capabilitycontract.NetworkOverlayEnsurePrivateMeshOperation, map[string]any{
			"type": "object", "required": []string{"targetUri", "peerTargetUri", "peerMeshIp"},
			"properties": map[string]any{
				"targetUri":     targetURISchema(),
				"peerTargetUri": targetURISchema(),
				"peerMeshIp":    map[string]any{"type": "string", "format": "ipv4"},
			},
		}),
		mutation(capabilitycontract.NetworkOverlayEnsurePrivateServiceOperation, map[string]any{
			"type": "object", "required": []string{"targetUri", "localTarget"},
			"properties": map[string]any{
				"targetUri":     targetURISchema(),
				"localTarget":   map[string]any{"type": "string", "minLength": 1},
				"membershipRef": map[string]any{"type": "string", "minLength": 1, "writeOnly": true},
			},
		}),
		mutation(capabilitycontract.NetworkOverlayEnsurePublicIngressOperation, map[string]any{
			"type": "object", "required": []string{"targetUri", "localTarget"},
			"properties": map[string]any{
				"targetUri":     targetURISchema(),
				"localTarget":   map[string]any{"type": "string", "minLength": 1},
				"membershipRef": map[string]any{"type": "string", "minLength": 1, "writeOnly": true},
				"hostname":      map[string]any{"type": "string"},
				"targetKind":    map[string]any{"type": "string"},
				"operatorMode":  map[string]any{"type": "boolean"},
			},
		}),
		mutation(capabilitycontract.NetworkOverlayEnsureHAEndpointOperation, map[string]any{
			"type": "object", "required": []string{"targetUri", "localTarget"},
			"properties": map[string]any{
				"targetUri":      targetURISchema(),
				"peerTargetUris": map[string]any{"type": "array", "items": targetURISchema()},
				"localTarget":    map[string]any{"type": "string", "minLength": 1},
				"hostname":       map[string]any{"type": "string"},
				"tunnelName":     map[string]any{"type": "string"},
				"membershipRef":  map[string]any{"type": "string", "minLength": 1, "writeOnly": true},
				"targetKind":     map[string]any{"type": "string"},
			},
		}),
		mutation(capabilitycontract.NetworkOverlayPromotePublicIngressOperation, map[string]any{
			"type": "object", "required": []string{"endpointRef", "targetUri"},
			"properties": map[string]any{
				"endpointRef":  map[string]any{"type": "string", "minLength": 1, "writeOnly": true},
				"targetUri":    targetURISchema(),
				"operatorMode": map[string]any{"type": "boolean"},
			},
		}),
		providerOperation(capabilitycontract.NetworkOverlayRemoveHAEndpointOperation, "destructive", map[string]any{
			"type": "object", "required": []string{"endpointRef"},
			"properties": map[string]any{"endpointRef": map[string]any{"type": "string", "minLength": 1, "writeOnly": true}},
		}, map[string]any{"type": "object"}, []string{"host", "network"}, nil),
		destructive(capabilitycontract.NetworkOverlayRemoveMembershipOperation, map[string]any{
			"type": "object", "required": []string{"membershipRef", "targetUri"},
			"properties": map[string]any{
				"membershipRef": map[string]any{"type": "string", "minLength": 1, "writeOnly": true},
				"targetUri":     targetURISchema(),
			},
		}),
	}
}

func providerOperation(id, effect string, input, output map[string]any, resources []string, requires []providercontract.ResourceBinding) providercontract.Operation {
	taskSupport := "sync_only"
	if effect != "read" {
		taskSupport = "bridged"
	}
	op := providercontract.Operation{
		ID: id, Version: 1, InputSchema: input, OutputSchema: output, Effect: effect,
		ResourceKinds: resources, Requires: requires, Idempotent: true, SupportsReadiness: effect != "read", TaskSupport: taskSupport,
	}
	if effect != "read" {
		op.ResourceCost = &providercontract.ResourceCost{Class: "control"}
	}
	return op
}

func overlayTargetBinding() []providercontract.ResourceBinding {
	return []providercontract.ResourceBinding{
		{Argument: "targetUri", ResourceType: resourceid.TypeVM, Required: true},
		{Argument: "targetUri", ResourceType: resourceid.TypeContainer, Required: true},
	}
}

func targetURISchema() map[string]any {
	return map[string]any{"type": "string", "pattern": "^(vm|container):[a-z][a-z0-9-]{0,31}:.+$"}
}

func teardownSchema() map[string]any {
	return map[string]any{
		"type": "object", "required": []string{"inputs"},
		"properties": map[string]any{
			"inputs": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"phase":       map[string]any{"type": "string", "enum": []string{"prepare", "finalize"}},
					"serviceName": map[string]any{"type": "string"},
					"serviceFile": map[string]any{"type": "string"},
					"scope":       map[string]any{"type": "string", "enum": []string{"user", "system"}},
					"generation":  map[string]any{"type": "string"},
				},
			},
		},
	}
}

func mergeProps(base, extra map[string]any) map[string]any {
	out := make(map[string]any, len(base)+len(extra))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

func addManifestTool(server *mcp.Server, manifest providercontract.InstallManifest) {
	server.AddTool(&mcp.Tool{Name: "opute.provider.get_install_manifest", Description: "Read the Tailscale provider installation manifest", InputSchema: map[string]any{"type": "object"}, OutputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return structured(manifest)
	})
}

func addTailscaleOperations(server *mcp.Server) {
	operations := append(tunnelingOperations(), networkOverlayOperations()...)
	for _, schema := range operations {
		operation := schema
		server.AddTool(&mcp.Tool{Name: operation.ID, Description: "Network overlay provider operation", InputSchema: operation.InputSchema, OutputSchema: operation.OutputSchema}, func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args, err := requestArguments(request)
			if err != nil {
				return nil, err
			}
			return dispatchTailscaleOperation(ctx, operation.ID, args)
		})
	}
}

func dispatchTailscaleOperation(ctx context.Context, operation string, args map[string]any) (*mcp.CallToolResult, error) {
	switch operation {
	case "opute.capability.tunneling.validate":
		bindings, _ := args["bindings"].([]any)
		if len(bindings) == 0 {
			return nil, fmt.Errorf("bindings must contain at least one declared binding")
		}
		placement := firstNonEmpty(stringInput(args, "placement", ""), "host")
		switch placement {
		case "host", "container", "kubernetes":
		default:
			return nil, fmt.Errorf("unsupported placement %q", placement)
		}
		return structured(map[string]any{"contractVersion": tunnelingCapability, "ready": true, "bindings": bindings, "placement": placement})
	case capabilitycontract.NetworkOverlayValidateOperation,
		capabilitycontract.NetworkOverlayPrepareMembershipOperation,
		capabilitycontract.NetworkOverlayAttachTargetOperation,
		capabilitycontract.NetworkOverlayEnrollOperation,
		capabilitycontract.NetworkOverlayProbeReachabilityOperation,
		capabilitycontract.NetworkOverlayProbeOperation,
		capabilitycontract.NetworkOverlayEnsurePrivateMeshOperation,
		capabilitycontract.NetworkOverlayEnsurePrivateServiceOperation,
		capabilitycontract.NetworkOverlayEnsurePublicIngressOperation,
		capabilitycontract.NetworkOverlayEnsureHAEndpointOperation,
		capabilitycontract.NetworkOverlayPromotePublicIngressOperation,
		capabilitycontract.NetworkOverlayRemoveHAEndpointOperation,
		capabilitycontract.NetworkOverlayRemoveMembershipOperation:
		return dispatchOverlayOperation(ctx, operation, args)
	default:
		return nil, fmt.Errorf("unknown provider operation %q", operation)
	}
}

var serviceNamePattern = regexp.MustCompile(`^[A-Za-z0-9_.@:-]+\.service$`)
var systemServiceFilePattern = regexp.MustCompile(`^/etc/systemd/system/[A-Za-z0-9_.@:-]+\.service$`)

func addTeardownTool(server *mcp.Server) {
	server.AddTool(&mcp.Tool{Name: "opute.provider.teardown", Description: "Return a cleanup plan and finalize owned overlay resources", InputSchema: teardownSchema(), OutputSchema: map[string]any{"type": "object"}}, func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var input struct {
			Phase  string         `json:"phase"`
			Inputs map[string]any `json:"inputs"`
		}
		if request != nil && request.Params != nil {
			if err := json.Unmarshal(request.Params.Arguments, &input); err != nil {
				return nil, err
			}
		}
		if input.Inputs == nil {
			input.Inputs = map[string]any{}
		}
		if firstNonEmpty(input.Phase, stringInput(input.Inputs, "phase", "")) == "finalize" {
			if err := finalizeOwnedTeardown(input.Inputs); err != nil {
				return nil, err
			}
			return structured(map[string]any{"completed": true})
		}
		serviceName := firstNonEmpty(stringInput(input.Inputs, "serviceName", ""), "opute-provider-tailscale.service")
		scope := firstNonEmpty(stringInput(input.Inputs, "scope", ""), "user")
		if scope != "user" && scope != "system" {
			return nil, fmt.Errorf("unsupported service scope %q", scope)
		}
		if !serviceNamePattern.MatchString(serviceName) {
			return nil, fmt.Errorf("serviceName must name a .service unit")
		}
		serviceFile := firstNonEmpty(stringInput(input.Inputs, "serviceFile", ""), defaultHostServiceFile(scope, serviceName))
		if scope == "system" && !systemServiceFilePattern.MatchString(serviceFile) {
			return nil, fmt.Errorf("system-scoped service file must be one .service unit beneath /etc/systemd/system")
		}
		serviceURI := firstNonEmpty(stringInput(input.Inputs, "serviceUri", ""), hostServiceURIForScope(scope, serviceName))
		generation := firstNonEmpty(stringInput(input.Inputs, "generation", ""), providerGeneration)
		return structured(map[string]any{
			"contractVersion": "host-plan.v1",
			"plan":            teardownPlan("com.opute.tailscale.teardown", serviceName, serviceFile, serviceURI, scope, generation),
		})
	})
}

func teardownPlan(planID, serviceName, serviceFile, serviceURI, scope, cleanupKey string) map[string]any {
	inspectArgs := map[string]any{"uri": serviceURI, "scope": scope}
	return map[string]any{
		"contractVersion": "host-plan.v1",
		"planId":          planID,
		"generation":      1,
		"idempotencyKey":  planID + "-" + serviceName + "-" + serviceFile + "-" + cleanupKey,
		"nodes": []any{map[string]any{
			"id":     "inspect-service",
			"action": map[string]any{"tool": "inspect_host_service", "args": inspectArgs},
		}},
	}
}

func defaultHostServiceFile(scope, serviceName string) string {
	if scope == "system" {
		return "/etc/systemd/system/" + serviceName
	}
	return "~/.config/systemd/user/" + serviceName
}

func hostServiceURIForScope(scope, serviceName string) string {
	return "host-service:local:" + scope + "/" + serviceName
}

func requestArguments(request *mcp.CallToolRequest) (map[string]any, error) {
	if request == nil || request.Params == nil || len(request.Params.Arguments) == 0 {
		return map[string]any{}, nil
	}
	var args map[string]any
	if err := json.Unmarshal(request.Params.Arguments, &args); err != nil {
		return nil, fmt.Errorf("decode provider arguments: %w", err)
	}
	return args, nil
}

func stringInput(inputs map[string]any, name, fallback string) string {
	if value, ok := inputs[name].(string); ok && strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return fallback
}

func boolInput(args map[string]any, key string, fallback bool) bool {
	if value, ok := args[key].(bool); ok {
		return value
	}
	return fallback
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func structured(value any) (*mcp.CallToolResult, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(encoded)}}, StructuredContent: value}, nil
}

func newProviderServer() *mcp.Server {
	manifest := tailscaleManifest()
	server := mcp.NewServer(&mcp.Implementation{Name: "opute-provider-tailscale", Version: providerPluginVer}, &mcp.ServerOptions{Capabilities: &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{ListChanged: true}}})
	addManifestTool(server, manifest)
	addTailscaleOperations(server)
	addTeardownTool(server)
	return server
}
