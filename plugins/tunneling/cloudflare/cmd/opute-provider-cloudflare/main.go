package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	providercontract "github.com/wunderous/host-agents/contracts/provider"
	"github.com/wunderous/host-agents/internal/resourceid"
	"github.com/wunderous/host-agents/pkg/hostagentclient"
)

const tunnelingCapability = "opute.capability.tunneling.v1"

func main() {
	port := firstNonEmpty(os.Getenv("OPUTE_PROVIDER_PORT"), "4319")
	manifest := cloudflareManifest()
	server := mcp.NewServer(&mcp.Implementation{Name: "opute-provider-cloudflare", Version: "1.0.0"}, &mcp.ServerOptions{Capabilities: &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{ListChanged: true}}})
	addManifestTool(server, manifest)
	addCloudflareOperations(server)
	addTeardownTool(server)
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, PropagateRequestCancellation: true})
	log.Printf("Opute Cloudflare provider listening on :%s/mcp", port)
	if err := http.ListenAndServe(":"+port, handler); err != nil {
		log.Fatal(err)
	}
}

func cloudflareManifest() providercontract.InstallManifest {
	return providercontract.InstallManifest{
		Schema: providercontract.InstallManifestVersion, Provider: providercontract.ProviderRef{ID: "com.opute.cloudflare", Version: "1.0.0"},
		Provides: []providercontract.CapabilityRef{{ID: tunnelingCapability, Version: 1}},
		Recipes: []providercontract.RecipeRef{
			{ID: "com.opute.cloudflare.tunneling", Source: providercontract.RecipeSource{URI: "recipes/tunneling.yaml", Revision: "working-tree", SHA256: "sha256:2f404972cbe5c463b8fe501973894c241341b2621e5941fad06af1434a958bc7"}, Mode: "tunnel"},
			{ID: "com.opute.cloudflare.tunneling.managed", Source: providercontract.RecipeSource{URI: "recipes/tunneling-managed.yaml", Revision: "working-tree", SHA256: "sha256:b665b9e50ebb64389dcca167d4b95b02f867fbb33d806d6254e6047fb8e9d9b3"}, Mode: "managed"},
		},
		Services:   []providercontract.ServiceDefinition{{ID: "opute.capability.tunneling", CapabilityID: tunnelingCapability, Version: 1, Operations: cloudflareOperations()}},
		Teardown:   &providercontract.Operation{ID: "opute.provider.teardown", Version: 1, InputSchema: teardownSchema(), OutputSchema: map[string]any{"type": "object", "required": []string{"contractVersion", "plan"}}, Effect: "destructive", ResourceKinds: []string{"service", "tunnel"}, Idempotent: true, SupportsReadiness: true, TaskSupport: "sync_only"},
		Validation: providercontract.ValidationRef{Capability: tunnelingCapability, Operation: "opute.capability.tunneling.validate"},
	}
}

func cloudflareOperations() []providercontract.Operation {
	read := func(id string, input map[string]any, output map[string]any, resources []string) providercontract.Operation {
		return providerOperation(id, "read", input, output, resources, nil)
	}
	mutation := func(id string, input map[string]any, output map[string]any, resources []string) providercontract.Operation {
		return providerOperation(id, "mutation", input, output, resources, nil)
	}
	destructive := func(id string, input map[string]any, output map[string]any, resources []string) providercontract.Operation {
		return providerOperation(id, "destructive", input, output, resources, nil)
	}
	ops := []providercontract.Operation{
		read("opute.capability.tunneling.validate", validationSchema(), map[string]any{"type": "object"}, nil),
		mutation("opute.capability.tunneling.ensure-host-tunnel", tunnelSchema(), tunnelOutputSchema(), []string{"host", "tunnel"}),
		read("opute.capability.tunneling.probe-host-tunnel", tunnelSchema(), tunnelOutputSchema(), []string{"host", "tunnel"}),
		destructive("opute.capability.tunneling.remove-host-tunnel", tunnelSchema(), tunnelOutputSchema(), []string{"host", "tunnel"}),
		providerOperation("opute.capability.tunneling.install-kubernetes-connector", "mutation", connectorTargetSchema(), connectorOutputSchema(), []string{"cluster", "tunnel"}, []providercontract.ResourceBinding{{Argument: "targetUri", ResourceType: "cluster", Required: true}}),
		providerOperation("opute.capability.tunneling.delete-kubernetes-connector", "destructive", connectorDeleteSchema(), connectorOutputSchema(), []string{"cluster", "tunnel"}, []providercontract.ResourceBinding{{Argument: "targetUri", ResourceType: "cluster", Required: true}}),
	}
	for _, id := range []string{"ensure_cloudflared_tunnel", "create_cloudflare_tunnel"} {
		ops = append(ops, mutation(id, tunnelSchema(), tunnelOutputSchema(), []string{"host", "tunnel"}))
	}
	for _, id := range []string{"probe_host_exposure", "get_cloudflare_tunnel_status"} {
		ops = append(ops, read(id, tunnelSchema(), tunnelOutputSchema(), []string{"host", "tunnel"}))
	}
	for _, id := range []string{"remove_local_llm_cloudflared_tunnel", "remove_host_exposure", "delete_cloudflare_tunnel"} {
		ops = append(ops, destructive(id, tunnelSchema(), tunnelOutputSchema(), []string{"host", "tunnel"}))
	}
	ops = append(ops, mutation("install_cloudflared_connector", connectorSchema(), connectorOutputSchema(), []string{"cluster", "tunnel"}), destructive("delete_cloudflared_connector", connectorDeleteLegacySchema(), connectorOutputSchema(), []string{"cluster", "tunnel"}))
	return ops
}

func providerOperation(id, effect string, input, output map[string]any, resources []string, requires []providercontract.ResourceBinding) providercontract.Operation {
	return providercontract.Operation{ID: id, Version: 1, InputSchema: input, OutputSchema: output, Effect: effect, ResourceKinds: resources, Requires: requires, Idempotent: true, SupportsReadiness: effect != "read", TaskSupport: "sync_only"}
}

func validationSchema() map[string]any {
	return map[string]any{"type": "object", "required": []string{"bindings"}, "properties": map[string]any{"bindings": map[string]any{"type": "array"}, "placement": map[string]any{"type": "string", "enum": []string{"host", "kubernetes", "container"}}}}
}
func tunnelSchema() map[string]any {
	return map[string]any{"type": "object", "required": []string{"bindingId", "localTarget"}, "properties": map[string]any{"bindingId": map[string]any{"type": "string", "minLength": 1}, "hostname": map[string]any{"type": "string"}, "localTarget": map[string]any{"type": "string"}, "runToken": map[string]any{"type": "string"}, "connector": map[string]any{"type": "string"}, "placement": map[string]any{"type": "string", "enum": []string{"host", "container", "kubernetes"}}, "targetUri": map[string]any{"type": "string"}, "artifactUri": map[string]any{"type": "string"}, "artifactSha256": map[string]any{"type": "string"}, "artifactPath": map[string]any{"type": "string"}, "serviceName": map[string]any{"type": "string"}, "serviceFile": map[string]any{"type": "string"}}}
}
func connectorSchema() map[string]any {
	return map[string]any{"type": "object", "required": []string{"token", "namespace"}, "properties": map[string]any{"token": map[string]any{"type": "string", "minLength": 1}, "namespace": map[string]any{"type": "string", "minLength": 1}, "name": map[string]any{"type": "string"}, "image": map[string]any{"type": "string"}, "replicas": map[string]any{"type": "integer", "minimum": 1}, "targetUri": map[string]any{"type": "string"}, "placement": map[string]any{"type": "string", "enum": []string{"kubernetes", "container"}}, "artifactUri": map[string]any{"type": "string"}, "artifactSha256": map[string]any{"type": "string"}, "localTargets": map[string]any{"type": "array"}}}
}

func connectorTargetSchema() map[string]any {
	schema := connectorSchema()
	schema["required"] = []string{"token", "namespace", "targetUri"}
	return schema
}

func connectorDeleteSchema() map[string]any {
	schema := connectorSchema()
	schema["required"] = []string{"namespace", "targetUri"}
	return schema
}

func connectorDeleteLegacySchema() map[string]any {
	schema := connectorSchema()
	schema["required"] = []string{"namespace"}
	return schema
}
func teardownSchema() map[string]any {
	return map[string]any{
		"type":     "object",
		"required": []string{"inputs"},
		"properties": map[string]any{
			"inputs": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"phase":        map[string]any{"type": "string", "enum": []string{"prepare", "finalize"}},
					"serviceName":  map[string]any{"type": "string"},
					"serviceFile":  map[string]any{"type": "string"},
					"tunnelId":     map[string]any{"type": "string", "pattern": "^[A-Za-z0-9_-]+$"},
					"dnsRecordIds": map[string]any{"type": "array", "items": map[string]any{"type": "string", "pattern": "^[A-Za-z0-9_-]+$"}},
				},
			},
		},
	}
}
func tunnelOutputSchema() map[string]any {
	return map[string]any{"type": "object", "required": []string{"contractVersion", "ready", "bindingId", "placement"}}
}
func connectorOutputSchema() map[string]any {
	return map[string]any{"type": "object", "required": []string{"contractVersion", "ready", "placement"}}
}

func addManifestTool(server *mcp.Server, manifest providercontract.InstallManifest) {
	server.AddTool(&mcp.Tool{Name: "opute.provider.get_install_manifest", Description: "Read the Cloudflare provider installation manifest", InputSchema: map[string]any{"type": "object"}, OutputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) { return structured(manifest) })
}

func addCloudflareOperations(server *mcp.Server) {
	declared := make(map[string]providercontract.Operation)
	for _, operation := range cloudflareOperations() {
		declared[operation.ID] = operation
	}
	for _, operation := range []string{"opute.capability.tunneling.validate", "opute.capability.tunneling.ensure-host-tunnel", "opute.capability.tunneling.probe-host-tunnel", "opute.capability.tunneling.remove-host-tunnel", "opute.capability.tunneling.install-kubernetes-connector", "opute.capability.tunneling.delete-kubernetes-connector", "ensure_cloudflared_tunnel", "remove_local_llm_cloudflared_tunnel", "probe_host_exposure", "remove_host_exposure", "create_cloudflare_tunnel", "get_cloudflare_tunnel_status", "delete_cloudflare_tunnel", "install_cloudflared_connector", "delete_cloudflared_connector"} {
		operation := operation
		schema := declared[operation]
		server.AddTool(&mcp.Tool{Name: operation, Description: "Cloudflare tunneling provider operation", InputSchema: schema.InputSchema, OutputSchema: schema.OutputSchema}, func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args, err := requestArguments(request)
			if err != nil {
				return nil, err
			}
			return dispatchCloudflareOperation(ctx, operation, args)
		})
	}
}

func dispatchCloudflareOperation(ctx context.Context, operation string, args map[string]any) (*mcp.CallToolResult, error) {
	switch operation {
	case "opute.capability.tunneling.validate":
		bindings, _ := args["bindings"].([]any)
		if len(bindings) == 0 {
			return nil, fmt.Errorf("bindings must contain at least one declared binding")
		}
		placement, err := placementInput(args)
		if err != nil {
			return nil, err
		}
		return structured(map[string]any{"contractVersion": tunnelingCapability, "ready": true, "bindings": bindings, "placement": placement})
	case "opute.capability.tunneling.ensure-host-tunnel", "ensure_cloudflared_tunnel", "create_cloudflare_tunnel":
		return ensureTunnel(ctx, args)
	case "opute.capability.tunneling.probe-host-tunnel", "probe_host_exposure", "get_cloudflare_tunnel_status":
		return probeTunnel(ctx, args)
	case "opute.capability.tunneling.remove-host-tunnel", "remove_local_llm_cloudflared_tunnel", "remove_host_exposure", "delete_cloudflare_tunnel":
		return removeTunnel(ctx, args)
	case "opute.capability.tunneling.install-kubernetes-connector", "install_cloudflared_connector":
		return installConnector(ctx, args)
	case "opute.capability.tunneling.delete-kubernetes-connector", "delete_cloudflared_connector":
		return deleteConnector(ctx, args)
	default:
		return nil, fmt.Errorf("unknown Cloudflare provider operation %q", operation)
	}
}

func ensureTunnel(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	placement, err := placementInput(args)
	if err != nil {
		return nil, err
	}
	// The legacy host alias predates the neutral placement field and its
	// callers only provide the binding, target, and run token.  Keep that
	// compatibility operation provider-owned by deriving its one safe
	// connector value here; callers never need to know this implementation
	// detail and non-host placements still require their explicit target URI.
	if placement == "host" && stringInput(args, "connector", "") == "" {
		args["connector"] = "host"
	}
	if stringInput(args, "bindingId", "") == "" || stringInput(args, "localTarget", "") == "" {
		return nil, fmt.Errorf("bindingId and localTarget are required")
	}
	if err := validateLocalTarget(stringInput(args, "localTarget", ""), stringInput(args, "connector", "")); err != nil {
		return nil, err
	}
	client, err := connectHostAgent(ctx)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	switch placement {
	case "host":
		err = reconcileHostTunnel(ctx, client, args)
	case "container":
		err = reconcileContainerTunnel(ctx, client, args)
	case "kubernetes":
		err = installKubernetesConnector(ctx, client, args)
	}
	if err != nil {
		return nil, err
	}
	return structured(map[string]any{"contractVersion": tunnelingCapability, "ready": true, "bindingId": stringInput(args, "bindingId", ""), "placement": placement, "hostname": stringInput(args, "hostname", "")})
}

func reconcileHostTunnel(ctx context.Context, client *hostagentclient.Client, args map[string]any) error {
	serviceName := firstNonEmpty(stringInput(args, "serviceName", ""), "opute-cloudflare-tunnel.service")
	serviceFile := firstNonEmpty(stringInput(args, "serviceFile", ""), "~/.config/systemd/user/"+serviceName)
	// Host exposure reconciliation is invoked from the neutral exposure
	// lifecycle, which only carries the run token and target. Keep the
	// cloudflared artifact choice provider-owned instead of requiring callers
	// to know provider implementation details.
	artifactPath := firstNonEmpty(stringInput(args, "artifactPath", ""), "~/.local/share/opute/providers/com.opute.cloudflare/bin/cloudflared")
	artifactURI := firstNonEmpty(stringInput(args, "artifactUri", ""), "https://github.com/cloudflare/cloudflared/releases/download/2026.8.2/cloudflared-linux-amd64")
	artifactSHA := firstNonEmpty(stringInput(args, "artifactSha256", ""), "fcfb02b575a52ca1af2e3267af4e1517bcdeb30ac48c834c69abaed3c0576ad2")
	if _, err := callHost(ctx, client, "ensure_host_artifact", map[string]any{"uri": artifactURI, "destination": artifactPath, "sha256": artifactSHA, "executable": true}); err != nil {
		return err
	}
	if stringInput(args, "runToken", "") == "" {
		return fmt.Errorf("runToken is required for host tunnel placement")
	}
	executable := firstNonEmpty(artifactPath, "cloudflared")
	if strings.HasPrefix(executable, "~") {
		executable = "%h" + strings.TrimPrefix(executable, "~")
	}
	unit := fmt.Sprintf("[Unit]\nDescription=Opute Cloudflare tunnel\nAfter=network-online.target\n\n[Service]\nExecStart=%s tunnel --no-autoupdate run --token %s\nRestart=on-failure\n\n[Install]\nWantedBy=default.target\n", executable, shellQuote(stringInput(args, "runToken", "")))
	if _, err := callHost(ctx, client, "ensure_host_file", map[string]any{"path": serviceFile, "content": unit, "mode": 0600}); err != nil {
		return err
	}
	if _, err := callHost(ctx, client, "ensure_host_service_supervisor", map[string]any{"scope": "user"}); err != nil {
		return err
	}
	if _, err := callHost(ctx, client, "set_host_service_state", map[string]any{
		"uri":         hostServiceURI(serviceName),
		"serviceName": serviceName,
		"state":       "start",
		"scope":       "user",
	}); err != nil {
		return err
	}
	if target := stringInput(args, "localTarget", ""); target != "" {
		_, err := callHost(ctx, client, "probe_http_endpoint", map[string]any{"endpoint": target})
		return err
	}
	return nil
}

func reconcileContainerTunnel(ctx context.Context, client *hostagentclient.Client, args map[string]any) error {
	targetURI := stringInput(args, "targetUri", "")
	target, err := typedTargetURI(targetURI, resourceid.TypeContainer)
	if err != nil {
		return err
	}
	if _, err := callHost(ctx, client, "provision_container", map[string]any{"containerName": target.ResourceID, "image": stringInput(args, "image", ""), "nesting": true}); err != nil {
		return err
	}
	if stringInput(args, "runToken", "") == "" || stringInput(args, "artifactUri", "") == "" || stringInput(args, "artifactSha256", "") == "" {
		return fmt.Errorf("container placement requires runToken, artifactUri, and artifactSha256")
	}
	script := "set -eu; command -v cloudflared >/dev/null 2>&1 || { curl -fsSL --proto =https --tlsv1.2 \"$1\" -o /tmp/cloudflared; echo \"$2  /tmp/cloudflared\" | sha256sum -c -; install -m 0755 /tmp/cloudflared /usr/local/bin/cloudflared; }; mkdir -p /etc/systemd/system; printf '%s\\n' '[Unit]' 'After=network-online.target' '' '[Service]' 'ExecStart=/usr/local/bin/cloudflared tunnel --no-autoupdate run --token " + shellQuote(stringInput(args, "runToken", "")) + "' 'Restart=on-failure' > /etc/systemd/system/opute-cloudflare-tunnel.service; systemctl daemon-reload; systemctl enable --now opute-cloudflare-tunnel.service"
	_, err = callHost(ctx, client, "run_instance_command", map[string]any{"uri": target.String(), "command": "sh", "args": []any{"-lc", script, "cloudflared", stringInput(args, "artifactUri", ""), strings.TrimPrefix(stringInput(args, "artifactSha256", ""), "sha256:")}, "timeoutMs": 120000})
	return err
}

func installConnector(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	placement := firstNonEmpty(stringInput(args, "placement", ""), "kubernetes")
	if placement == "container" {
		client, err := connectHostAgent(ctx)
		if err != nil {
			return nil, err
		}
		defer client.Close()
		containerArgs := cloneMap(args)
		if stringInput(containerArgs, "runToken", "") == "" {
			containerArgs["runToken"] = stringInput(containerArgs, "token", "")
		}
		if err := reconcileContainerTunnel(ctx, client, containerArgs); err != nil {
			return nil, err
		}
		return structured(map[string]any{"contractVersion": tunnelingCapability, "ready": true, "placement": placement})
	}
	if placement != "kubernetes" {
		return nil, fmt.Errorf("connector placement must be kubernetes or container")
	}
	client, err := connectHostAgent(ctx)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	if err := installKubernetesConnector(ctx, client, args); err != nil {
		return nil, err
	}
	return structured(map[string]any{"contractVersion": tunnelingCapability, "ready": true, "placement": placement})
}

func installKubernetesConnector(ctx context.Context, client *hostagentclient.Client, args map[string]any) error {
	target, err := typedTargetURI(stringInput(args, "targetUri", ""), resourceid.TypeCluster)
	if err != nil {
		return err
	}
	namespace, name, image := firstNonEmpty(stringInput(args, "namespace", ""), "cloudflare-connector"), firstNonEmpty(stringInput(args, "name", ""), "cloudflared"), firstNonEmpty(stringInput(args, "image", ""), "cloudflare/cloudflared:2026.7.2")
	_, err = callHost(ctx, client, "apply_manifest", map[string]any{"uri": target.String(), "manifest": cloudflaredManifest(namespace, name, image, intInput(args, "replicas", 1), stringInput(args, "token", ""), args["localTargets"])})
	return err
}

func deleteConnector(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	client, err := connectHostAgent(ctx)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	targetURI := stringInput(args, "targetUri", "")
	target, err := typedTargetURI(targetURI, resourceid.TypeCluster)
	if err != nil {
		return nil, err
	}
	if _, err := callHost(ctx, client, "delete_k8s_resource", map[string]any{"uri": target.String(), "kind": "namespace", "resourceName": firstNonEmpty(stringInput(args, "namespace", ""), "cloudflare-connector")}); err != nil {
		return nil, err
	}
	return structured(map[string]any{"contractVersion": tunnelingCapability, "ready": true, "deleted": true, "placement": "kubernetes"})
}

func probeTunnel(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	client, err := connectHostAgent(ctx)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	endpoint := stringInput(args, "localTarget", "")
	if endpoint == "" {
		return nil, fmt.Errorf("localTarget is required")
	}
	result, err := callHost(ctx, client, "probe_http_endpoint", map[string]any{"endpoint": endpoint})
	if err != nil {
		return nil, err
	}
	return structured(map[string]any{"contractVersion": tunnelingCapability, "ready": !result.IsError, "bindingId": stringInput(args, "bindingId", ""), "placement": firstNonEmpty(stringInput(args, "placement", ""), "host"), "probe": result.StructuredContent})
}

func removeTunnel(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	client, err := connectHostAgent(ctx)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	serviceName := firstNonEmpty(stringInput(args, "serviceName", ""), "opute-cloudflare-tunnel.service")
	serviceFile := firstNonEmpty(stringInput(args, "serviceFile", ""), "~/.config/systemd/user/"+serviceName)
	for _, call := range []struct {
		name string
		args map[string]any
	}{{"set_host_service_state", map[string]any{"uri": hostServiceURI(serviceName), "serviceName": serviceName, "state": "stop", "scope": "user"}}, {"set_host_service_state", map[string]any{"uri": hostServiceURI(serviceName), "serviceName": serviceName, "state": "disable", "scope": "user"}}, {"remove_host_file", map[string]any{"path": serviceFile, "confirm": true}}} {
		if _, err := callHost(ctx, client, call.name, call.args); err != nil {
			return nil, err
		}
	}
	return structured(map[string]any{"contractVersion": tunnelingCapability, "ready": true, "deleted": true, "bindingId": stringInput(args, "bindingId", ""), "placement": firstNonEmpty(stringInput(args, "placement", ""), "host")})
}

func hostServiceURI(serviceName string) string {
	return "host-service:local:user/" + strings.TrimSpace(serviceName)
}

func cloudflaredManifest(namespace, name, image string, replicas int, token string, localTargets any) string {
	if replicas < 1 {
		replicas = 1
	}
	manifest := `apiVersion: v1
kind: Namespace
metadata:
  name: %s
---
apiVersion: v1
kind: Secret
metadata:
  name: %s-token
  namespace: %s
stringData:
  token: %s
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: %s
  namespace: %s
spec:
  replicas: %d
  selector:
    matchLabels:
      app: %s
  template:
    metadata:
      labels:
        app: %s
    spec:
      containers:
      - name: cloudflared
        image: %s
        args: [tunnel, --no-autoupdate, run, --token, $(TOKEN)]
        env:
        - name: TOKEN
          valueFrom:
            secretKeyRef:
              name: %s-token
              key: token
        readinessProbe:
          exec:
            command: [cloudflared, --version]
          initialDelaySeconds: 5
          periodSeconds: 10
# localTargets are provider-owned routing metadata: %s
`
	return fmt.Sprintf(manifest, namespace, name, namespace, yamlQuote(token), name, namespace, replicas, name, name, image, name, yamlQuote(fmt.Sprint(localTargets)))
}

func addTeardownTool(server *mcp.Server) {
	server.AddTool(&mcp.Tool{Name: "opute.provider.teardown", Description: "Return a generic cleanup plan and finalize Cloudflare API resources", InputSchema: teardownSchema(), OutputSchema: map[string]any{"type": "object"}}, func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var input struct {
			Phase  string         `json:"phase"`
			Inputs map[string]any `json:"inputs"`
		}
		if request != nil && request.Params != nil {
			if err := json.Unmarshal(request.Params.Arguments, &input); err != nil {
				return nil, err
			}
		}
		if firstNonEmpty(input.Phase, stringInput(input.Inputs, "phase", "")) == "finalize" {
			if err := cleanupExternalResources(ctx, input.Inputs); err != nil {
				return nil, err
			}
			return structured(map[string]any{"completed": true})
		}
		serviceName := firstNonEmpty(stringInput(input.Inputs, "serviceName", ""), "opute-cloudflare-tunnel.service")
		serviceFile := firstNonEmpty(stringInput(input.Inputs, "serviceFile", ""), "~/.config/systemd/user/"+serviceName)
		serviceURI := firstNonEmpty(stringInput(input.Inputs, "serviceUri", ""), hostServiceURI(serviceName))
		cleanupKey := stringInput(input.Inputs, "tunnelId", "") + "-" + strings.Join(stringSliceInput(input.Inputs, "dnsRecordIds"), ",")
		return structured(map[string]any{"contractVersion": "host-plan.v1", "plan": teardownPlan("com.opute.cloudflare.teardown", serviceName, serviceFile, serviceURI, cleanupKey)})
	})
}

var cloudflareIdentifierPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func cleanupExternalResources(ctx context.Context, inputs map[string]any) error {
	tunnelID, recordIDs := stringInput(inputs, "tunnelId", ""), stringSliceInput(inputs, "dnsRecordIds")
	if tunnelID == "" && len(recordIDs) == 0 {
		return nil
	}
	accountID, zoneID, apiToken := strings.TrimSpace(os.Getenv("CLOUDFLARE_ACCOUNT_ID")), strings.TrimSpace(os.Getenv("CLOUDFLARE_ZONE_ID")), strings.TrimSpace(os.Getenv("CLOUDFLARE_API_TOKEN"))
	if accountID == "" || zoneID == "" || apiToken == "" {
		return fmt.Errorf("external Cloudflare cleanup requires CLOUDFLARE_ACCOUNT_ID, CLOUDFLARE_ZONE_ID, and CLOUDFLARE_API_TOKEN")
	}
	var cleanupErrors []error
	if tunnelID != "" {
		if !cloudflareIdentifierPattern.MatchString(tunnelID) {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("invalid Cloudflare tunnel id"))
		} else if err := cloudflareDelete(ctx, apiToken, fmt.Sprintf("https://api.cloudflare.com/client/v4/accounts/%s/cfd_tunnel/%s?force=true", accountID, tunnelID)); err != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("delete Cloudflare tunnel: %w", err))
		}
	}
	for _, recordID := range recordIDs {
		if !cloudflareIdentifierPattern.MatchString(recordID) {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("invalid Cloudflare DNS record id"))
			continue
		}
		if err := cloudflareDelete(ctx, apiToken, fmt.Sprintf("https://api.cloudflare.com/client/v4/zones/%s/dns_records/%s", zoneID, recordID)); err != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("delete Cloudflare DNS record: %w", err))
		}
	}
	return errors.Join(cleanupErrors...)
}

func connectHostAgent(ctx context.Context) (*hostagentclient.Client, error) {
	endpoint := strings.TrimSpace(os.Getenv("OPUTE_HOST_AGENT_ENDPOINT"))
	if endpoint == "" {
		return nil, errors.New("OPUTE_HOST_AGENT_ENDPOINT is required for Cloudflare provider callbacks")
	}
	return hostagentclient.Connect(ctx, endpoint, os.Getenv("OPUTE_HOST_AGENT_BEARER_TOKEN"))
}

func typedTargetURI(raw, expectedType string) (resourceid.URI, error) {
	target, err := resourceid.Parse(strings.TrimSpace(raw))
	if err != nil {
		return resourceid.URI{}, fmt.Errorf("targetUri must be a canonical %s resource URI: %w", expectedType, err)
	}
	if target.ResourceType != expectedType {
		return resourceid.URI{}, fmt.Errorf("targetUri requires resource type %q, got %q", expectedType, target.ResourceType)
	}
	return target, nil
}

func callHost(ctx context.Context, client *hostagentclient.Client, name string, args map[string]any) (*mcp.CallToolResult, error) {
	result, err := client.Call(ctx, name, args)
	if err != nil {
		return nil, fmt.Errorf("host callback %s: %w", name, err)
	}
	if result == nil || result.IsError {
		return nil, fmt.Errorf("host callback %s failed", name)
	}
	return result, nil
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
func placementInput(args map[string]any) (string, error) {
	placement := firstNonEmpty(stringInput(args, "placement", ""), "host")
	switch placement {
	case "host", "container", "kubernetes":
		return placement, nil
	default:
		return "", fmt.Errorf("unsupported Cloudflare placement %q", placement)
	}
}

func validateLocalTarget(raw, connector string) error {
	target, err := url.ParseRequestURI(strings.TrimSpace(raw))
	if err != nil || target.Host == "" || (target.Scheme != "http" && target.Scheme != "https") || strings.ContainsAny(raw, "\r\n\x00") {
		return fmt.Errorf("localTarget must be an HTTP(S) URL")
	}
	if connector == "host" && (target.Hostname() != "127.0.0.1" && target.Hostname() != "localhost" || target.Port() == "") {
		return fmt.Errorf("host connector requires an explicit loopback localTarget port")
	}
	return nil
}
func intInput(args map[string]any, key string, fallback int) int {
	if value, ok := args[key].(float64); ok {
		return int(value)
	}
	if value, ok := args[key].(int); ok {
		return value
	}
	return fallback
}

func cloneMap(args map[string]any) map[string]any {
	clone := make(map[string]any, len(args))
	for key, value := range args {
		clone[key] = value
	}
	return clone
}
func stringInput(inputs map[string]any, name, fallback string) string {
	if value, ok := inputs[name].(string); ok && strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return fallback
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
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
func shellQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }
func yamlQuote(value string) string {
	return "\"" + strings.ReplaceAll(strings.ReplaceAll(value, "\\", "\\\\"), "\"", "\\\"") + "\""
}
func cloudflareDelete(ctx context.Context, token, endpoint string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := http.DefaultClient.Do(request)
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
	body, err := io.ReadAll(io.LimitReader(response.Body, 64*1024))
	if err != nil {
		return fmt.Errorf("read Cloudflare delete response: %w", err)
	}
	var envelope struct {
		Success bool `json:"success"`
		Errors  []struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return fmt.Errorf("decode Cloudflare delete response: %w", err)
	}
	if !envelope.Success {
		if len(envelope.Errors) > 0 {
			return fmt.Errorf("Cloudflare API error %d: %s", envelope.Errors[0].Code, envelope.Errors[0].Message)
		}
		return errors.New("Cloudflare API reported delete failure")
	}
	return nil
}
func teardownPlan(planID, serviceName, serviceFile, serviceURI, cleanupKey string) map[string]any {
	serviceArgs := map[string]any{"uri": serviceURI, "state": "stop", "scope": "user"}
	inspectArgs := map[string]any{"uri": serviceURI, "scope": "user"}
	return map[string]any{"contractVersion": "host-plan.v1", "planId": planID, "generation": 1, "idempotencyKey": planID + "-" + serviceName + "-" + serviceFile + "-" + cleanupKey, "nodes": []any{map[string]any{"id": "stop", "action": map[string]any{"tool": "set_host_service_state", "args": serviceArgs}, "validate": map[string]any{"tool": "inspect_host_service", "args": inspectArgs, "assert": []any{map[string]any{"path": "/active", "op": "eq", "value": false}}}}, map[string]any{"id": "disable", "dependsOn": []string{"stop"}, "action": map[string]any{"tool": "set_host_service_state", "args": map[string]any{"uri": serviceURI, "state": "disable", "scope": "user"}}, "validate": map[string]any{"tool": "inspect_host_service", "args": inspectArgs, "assert": []any{map[string]any{"path": "/enabled", "op": "eq", "value": false}}}}, map[string]any{"id": "remove-service-file", "dependsOn": []string{"disable"}, "action": map[string]any{"tool": "remove_host_file", "args": map[string]any{"path": serviceFile, "confirm": true}}, "validate": map[string]any{"tool": "inspect_host_file", "args": map[string]any{"path": serviceFile}, "assert": []any{map[string]any{"path": "/exists", "value": false, "op": "eq"}}}}}}
}
func structured(value any) (*mcp.CallToolResult, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(encoded)}}, StructuredContent: value}, nil
}
