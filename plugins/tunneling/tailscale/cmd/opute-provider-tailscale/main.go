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
			{ID: capabilitycontract.MeshRuntime, Version: 1},
			{ID: capabilitycontract.MeshMembership, Version: 1},
			{ID: capabilitycontract.PrivateMesh, Version: 1},
			{ID: capabilitycontract.PublicIngress, Version: 1},
		},
		Recipes: []providercontract.RecipeRef{
			{ID: "com.opute.tailscale.activate", Source: providercontract.RecipeSource{URI: "recipes/activate.yaml", Revision: "working-tree", SHA256: "sha256:f878248c0cd8c9035a2ab3d2e7f92ca80aeef33b82747d96b7f4053249722204"}, Mode: "activate"},
			{ID: "com.opute.tailscale.overlay-mesh", Source: providercontract.RecipeSource{URI: "recipes/overlay-mesh.yaml", Revision: "working-tree", SHA256: "sha256:3c783ab82f5e1cafdc657e29ab569fef7c906d61a351554efbc814ebfbc2a27c"}, Mode: "private-mesh"},
			{ID: "com.opute.tailscale.public-ingress", Source: providercontract.RecipeSource{URI: "recipes/public-ingress.yaml", Revision: "working-tree", SHA256: "sha256:97528bf47b0f350ca4f57cf5ed957e99ba2c5f3027d1573d1e38e0fd5d9b2da6"}, Mode: "public-ingress"},
			{ID: "com.opute.tailscale.ha-network-bundle", Source: providercontract.RecipeSource{URI: "recipes/ha-network-bundle.yaml", Revision: "working-tree", SHA256: "sha256:7ede770cca2ee6ed3fa350b768cc2a75a9f9a63b78f2a99f5a7939aba238d6cc"}, Mode: "vendor-bundle"},
			{ID: "com.opute.tailscale.install", Source: providercontract.RecipeSource{URI: "recipes/install.yaml", Revision: "working-tree", SHA256: "sha256:b7c3560f037fe7f493f483c70944c6f97a52485b49d189b05134b115e3129dba"}, Mode: "install"},
		},
		Services: []providercontract.ServiceDefinition{
			{ID: "opute.capability.mesh-runtime", CapabilityID: capabilitycontract.MeshRuntime, Version: 1, Operations: meshRuntimeOperations()},
			{ID: "opute.capability.mesh-membership", CapabilityID: capabilitycontract.MeshMembership, Version: 1, Operations: meshMembershipOperations()},
			{ID: "opute.capability.private-mesh", CapabilityID: capabilitycontract.PrivateMesh, Version: 1, Operations: privateMeshOperations()},
			{ID: "opute.capability.public-ingress", CapabilityID: capabilitycontract.PublicIngress, Version: 1, Operations: publicIngressOperations()},
		},
		Teardown: &providercontract.Operation{
			ID: "opute.provider.teardown", Version: 1, InputSchema: teardownSchema(), OutputSchema: map[string]any{"type": "object", "required": []string{"contractVersion", "plan"}},
			Effect: "destructive", ResourceKinds: []string{"service", "network"}, Idempotent: true, SupportsReadiness: true, TaskSupport: "sync_only",
			ResourceCost: &providercontract.ResourceCost{Class: "control"},
		},
		Validation: providercontract.ValidationRef{Capability: capabilitycontract.PublicIngress, Operation: capabilitycontract.PublicIngressProbeOperation},
	}
}

func meshRuntimeOperations() []providercontract.Operation {
	mutation := func(id string, input map[string]any) providercontract.Operation {
		return providerOperation(id, "mutation", input, meshRuntimeOutputSchema(), []string{"host", "network"}, overlayTargetBinding())
	}
	read := func(id string, input map[string]any) providercontract.Operation {
		return providerOperation(id, "read", input, meshRuntimeOutputSchema(), []string{"host", "network"}, overlayTargetBinding())
	}
	credProps := overlayCredentialProps()
	return []providercontract.Operation{
		read(capabilitycontract.MeshRuntimeValidateOperation, map[string]any{
			"type": "object", "required": []string{"targetUri"},
			"properties": mergeProps(map[string]any{"targetUri": targetURISchema()}, credProps),
		}),
		mutation(capabilitycontract.MeshRuntimeEnsureAgentOperation, map[string]any{
			"type": "object", "required": []string{"targetUri"},
			"properties": mergeProps(map[string]any{
				"targetUri": targetURISchema(),
				"agentMode": map[string]any{"type": "string", "enum": []string{"node-agent", "host-agent"}},
			}, credProps),
		}),
		mutation(capabilitycontract.MeshRuntimeEnsureControlPlaneOperation, map[string]any{
			"type": "object", "required": []string{"targetUri"},
			"properties": mergeProps(map[string]any{
				"targetUri":        targetURISchema(),
				"operatorMode":     map[string]any{"type": "boolean"},
				"ingressClassName": map[string]any{"type": "string"},
				"operatorEvidence": map[string]any{"type": "string"},
				"controlPlaneRef":  map[string]any{"type": "string"},
			}, credProps),
		}),
		read(capabilitycontract.MeshRuntimeStatusOperation, map[string]any{
			"type": "object", "required": []string{"targetUri"},
			"properties": mergeProps(map[string]any{"targetUri": targetURISchema()}, credProps),
		}),
	}
}

func meshRuntimeOutputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"contractVersion":   map[string]any{"type": "string"},
			"ready":             map[string]any{"type": "boolean"},
			"provider":          map[string]any{"type": "string"},
			"targetUri":         map[string]any{"type": "string"},
			"agentReady":        map[string]any{"type": "boolean"},
			"controlPlaneReady": map[string]any{"type": "boolean"},
			"agentVersion":      map[string]any{"type": "string"},
			"controlPlaneRef":   map[string]any{"type": "string"},
			"ingressClassName":  map[string]any{"type": "string"},
			"probe":             map[string]any{"type": "object"},
			"error":             map[string]any{"type": "string"},
			"generation":        map[string]any{"type": "string"},
		},
	}
}

func meshMembershipOperations() []providercontract.Operation {
	mutation := func(id string, input map[string]any) providercontract.Operation {
		return providerOperation(id, "mutation", input, map[string]any{"type": "object"}, []string{"host", "network"}, overlayTargetBinding())
	}
	read := func(id string, input map[string]any) providercontract.Operation {
		return providerOperation(id, "read", input, map[string]any{"type": "object"}, []string{"host", "network"}, overlayTargetBinding())
	}
	credProps := overlayCredentialProps()
	return []providercontract.Operation{
		mutation(capabilitycontract.MeshMembershipEnrollOperation, map[string]any{
			"type": "object", "required": []string{"targetUri", "name"},
			"properties": mergeProps(map[string]any{
				"targetUri":     targetURISchema(),
				"name":          map[string]any{"type": "string", "pattern": "^[A-Za-z0-9][A-Za-z0-9_.-]{0,62}$"},
				"membershipRef": map[string]any{"type": "string", "minLength": 1, "writeOnly": true},
			}, credProps),
		}),
		read(capabilitycontract.MeshMembershipStatusOperation, map[string]any{
			"type": "object", "required": []string{"targetUri"},
			"properties": mergeProps(map[string]any{"targetUri": targetURISchema()}, credProps),
		}),
		mutation(capabilitycontract.MeshMembershipLeaveOperation, map[string]any{
			"type": "object", "required": []string{"targetUri", "membershipRef"},
			"properties": mergeProps(map[string]any{
				"targetUri":     targetURISchema(),
				"membershipRef": map[string]any{"type": "string", "minLength": 1, "writeOnly": true},
			}, credProps),
		}),
	}
}

func privateMeshOperations() []providercontract.Operation {
	mutation := func(id string, input map[string]any) providercontract.Operation {
		return providerOperation(id, "mutation", input, map[string]any{"type": "object"}, []string{"host", "network"}, overlayTargetBinding())
	}
	read := func(id string, input map[string]any) providercontract.Operation {
		return providerOperation(id, "read", input, map[string]any{"type": "object"}, []string{"host", "network"}, overlayTargetBinding())
	}
	credProps := overlayCredentialProps()
	return []providercontract.Operation{
		mutation(capabilitycontract.PrivateMeshEnsureOperation, map[string]any{
			"type": "object", "required": []string{"targetUri", "membershipRef", "peerTargetUri"},
			"properties": mergeProps(map[string]any{
				"targetUri":     targetURISchema(),
				"membershipRef": map[string]any{"type": "string", "minLength": 1, "writeOnly": true},
				"peerTargetUri": targetURISchema(),
				"peerMeshIp":    map[string]any{"type": "string", "format": "ipv4"},
			}, credProps),
		}),
		mutation(capabilitycontract.PrivateMeshEnsureServiceOperation, map[string]any{
			"type": "object", "required": []string{"targetUri", "membershipRef", "serviceName", "port"},
			"properties": mergeProps(map[string]any{
				"targetUri":     targetURISchema(),
				"membershipRef": map[string]any{"type": "string", "minLength": 1, "writeOnly": true},
				"serviceName":   map[string]any{"type": "string", "minLength": 1},
				"port":          map[string]any{"type": "integer", "minimum": 1, "maximum": 65535},
			}, credProps),
		}),
		read(capabilitycontract.PrivateMeshProbeOperation, map[string]any{
			"type": "object", "required": []string{"targetUri"},
			"properties": mergeProps(map[string]any{
				"targetUri":  targetURISchema(),
				"peerMeshIp": map[string]any{"type": "string", "format": "ipv4"},
			}, credProps),
		}),
	}
}

func publicIngressOperations() []providercontract.Operation {
	mutation := func(id string, input map[string]any) providercontract.Operation {
		return providerOperation(id, "mutation", input, map[string]any{"type": "object"}, []string{"host", "network"}, overlayTargetBinding())
	}
	read := func(id string, input map[string]any) providercontract.Operation {
		return providerOperation(id, "read", input, map[string]any{"type": "object"}, []string{"host", "network"}, overlayTargetBinding())
	}
	credProps := overlayCredentialProps()
	return []providercontract.Operation{
		mutation(capabilitycontract.PublicIngressEnsureOperation, map[string]any{
			"type": "object", "required": []string{"targetUri", "membershipRef", "localTarget"},
			"properties": mergeProps(map[string]any{
				"targetUri":        targetURISchema(),
				"membershipRef":    map[string]any{"type": "string", "minLength": 1, "writeOnly": true},
				"localTarget":      map[string]any{"type": "string", "format": "uri"},
				"hostname":         map[string]any{"type": "string"},
				"endpoint":         map[string]any{"type": "string"},
				"operatorMode":     map[string]any{"type": "boolean"},
				"ingressClassName": map[string]any{"type": "string"},
				"operatorEvidence": map[string]any{"type": "string"},
			}, credProps),
		}),
		mutation(capabilitycontract.PublicIngressPromoteOperation, map[string]any{
			"type": "object", "required": []string{"targetUri", "membershipRef"},
			"properties": mergeProps(map[string]any{
				"targetUri":     targetURISchema(),
				"membershipRef": map[string]any{"type": "string", "minLength": 1, "writeOnly": true},
			}, credProps),
		}),
		read(capabilitycontract.PublicIngressProbeOperation, map[string]any{
			"type": "object", "required": []string{"targetUri"},
			"properties": mergeProps(map[string]any{"targetUri": targetURISchema()}, credProps),
		}),
	}
}

func overlayCredentialProps() map[string]any {
	return map[string]any{
		"hostAgentId":    map[string]any{"type": "string", "minLength": 1},
		"credentialKind": map[string]any{"type": "string", "enum": []string{"api-key", "auth-key"}},
		"credential":     map[string]any{"type": "string", "writeOnly": true},
		"apiKey":         map[string]any{"type": "string", "writeOnly": true},
		"authKey":        map[string]any{"type": "string", "writeOnly": true},
	}
}

func providerOperation(id, effect string, input, output map[string]any, resources []string, requires []providercontract.ResourceBinding) providercontract.Operation {
	taskSupport := "sync_only"
	// Overlay ops stay synchronous so host-local recipes can validate readiness
	// against the same in-process ownership store without bridged-task races.
	_ = effect
	if output == nil || (len(output) == 1 && fmt.Sprint(output["type"]) == "object") {
		output = map[string]any{
			"type": "object",
			"properties": map[string]any{
				"membershipRef":    map[string]any{"type": "string"},
				"ready":            map[string]any{"type": "boolean"},
				"meshIp":           map[string]any{"type": "string"},
				"overlayUri":       map[string]any{"type": "string"},
				"targetUri":        map[string]any{"type": "string"},
				"endpoint":         map[string]any{"type": "string"},
				"stable":           map[string]any{"type": "boolean"},
				"pathClass":        map[string]any{"type": "string"},
				"endpointRef":      map[string]any{"type": "string"},
				"operatorMode":     map[string]any{"type": "boolean"},
				"operatorEvidence": map[string]any{"type": "string"},
				"ingressClassName": map[string]any{"type": "string"},
				"probe":            map[string]any{"type": "object"},
				"contractVersion":  map[string]any{"type": "string"},
				"generation":       map[string]any{"type": "string"},
				"provider":         map[string]any{"type": "string"},
				"error":            map[string]any{"type": "string"},
			},
		}
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
	operations := append(append(append(meshRuntimeOperations(), meshMembershipOperations()...), privateMeshOperations()...), publicIngressOperations()...)
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
	case capabilitycontract.MeshRuntimeValidateOperation,
		capabilitycontract.MeshRuntimeEnsureAgentOperation,
		capabilitycontract.MeshRuntimeEnsureControlPlaneOperation,
		capabilitycontract.MeshRuntimeStatusOperation,
		capabilitycontract.MeshMembershipEnrollOperation,
		capabilitycontract.MeshMembershipStatusOperation,
		capabilitycontract.MeshMembershipLeaveOperation,
		capabilitycontract.PrivateMeshEnsureOperation,
		capabilitycontract.PrivateMeshEnsureServiceOperation,
		capabilitycontract.PrivateMeshProbeOperation,
		capabilitycontract.PublicIngressEnsureOperation,
		capabilitycontract.PublicIngressPromoteOperation,
		capabilitycontract.PublicIngressProbeOperation,
		// Deprecated network-overlay aliases during migration.
		capabilitycontract.NetworkOverlayValidateOperation,
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
