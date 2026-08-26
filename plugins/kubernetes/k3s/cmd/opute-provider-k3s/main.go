package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	capabilitycontract "github.com/wunderous/host-agents/contracts/capability"
	providercontract "github.com/wunderous/host-agents/contracts/provider"
)

const (
	kubernetesCapability = capabilitycontract.Kubernetes
	maxManifestBytes     = 4 * 1024 * 1024
)

func main() {
	port := firstNonEmpty(os.Getenv("OPUTE_PROVIDER_PORT"), "4320")
	manifest := k3sManifest()
	server := mcp.NewServer(&mcp.Implementation{Name: "opute-provider-k3s", Version: "1.0.0"}, &mcp.ServerOptions{Capabilities: &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{ListChanged: true}}})
	addManifestTool(server, manifest)
	addOperations(server)
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, PropagateRequestCancellation: true})
	log.Printf("Opute K3s provider listening on :%s/mcp", port)
	if err := http.ListenAndServe(":"+port, handler); err != nil {
		log.Fatal(err)
	}
}

func k3sManifest() providercontract.InstallManifest {
	return providercontract.InstallManifest{
		Schema:     providercontract.InstallManifestVersion,
		Provider:   providercontract.ProviderRef{ID: "com.opute.k3s", Version: "1.0.1"},
		Provides:   []providercontract.CapabilityRef{{ID: kubernetesCapability, Version: 1}},
		Recipes:    []providercontract.RecipeRef{{ID: "com.opute.k3s.managed", Source: providercontract.RecipeSource{URI: "recipes/kubernetes.yaml", Revision: "working-tree", SHA256: "sha256:058d01adece826a08598c200df440ffa2406b0bf7a98a50543446fd7420c24d1"}, Mode: "kubernetes"}},
		Services:   []providercontract.ServiceDefinition{{ID: "opute.capability.kubernetes", CapabilityID: kubernetesCapability, Version: 1, Operations: operations()}},
		Validation: providercontract.ValidationRef{Capability: kubernetesCapability, Operation: capabilitycontract.KubernetesValidateOperation},
	}
}

func operations() []providercontract.Operation {
	read := func(id string, input map[string]any) providercontract.Operation {
		return providerOperation(id, "read", input)
	}
	mutation := func(id string, input map[string]any) providercontract.Operation {
		return providerOperation(id, "mutation", input)
	}
	destructive := func(id string, input map[string]any) providercontract.Operation {
		return providerOperation(id, "destructive", input)
	}
	targetRead := func(id string, input map[string]any) providercontract.Operation {
		operation := read(id, input)
		operation.Requires = []providercontract.ResourceBinding{clusterTargetBinding()}
		return operation
	}
	targetMutation := func(id string, input map[string]any) providercontract.Operation {
		operation := mutation(id, input)
		operation.Requires = []providercontract.ResourceBinding{clusterTargetBinding()}
		return operation
	}
	targetDestructive := func(id string, input map[string]any) providercontract.Operation {
		operation := destructive(id, input)
		operation.Requires = []providercontract.ResourceBinding{clusterTargetBinding()}
		return operation
	}
	return []providercontract.Operation{
		read(capabilitycontract.KubernetesValidateOperation, map[string]any{"type": "object"}),
		targetMutation(capabilitycontract.KubernetesProvisionOperation, targetSchema(map[string]any{
			"version": map[string]any{"type": "string", "minLength": 1, "maxLength": 64},
		}, "version")),
		targetRead(capabilitycontract.KubernetesStatusOperation, targetSchema(map[string]any{})),
		targetMutation(capabilitycontract.KubernetesConfigureRegistryOperation, targetSchema(map[string]any{
			"endpoint": map[string]any{"type": "string", "minLength": 1, "maxLength": 512},
			"registry": map[string]any{"type": "string", "minLength": 1, "maxLength": 256},
			"insecure": map[string]any{"type": "boolean"},
		}, "endpoint")),
		targetMutation(capabilitycontract.KubernetesRestartOperation, targetSchema(map[string]any{})),
		targetDestructive(capabilitycontract.KubernetesRemoveOperation, targetSchema(map[string]any{})),
		targetMutation(capabilitycontract.KubernetesApplyManifestOperation, targetSchema(map[string]any{"manifest": map[string]any{"type": "string", "minLength": 1, "maxLength": maxManifestBytes}}, "manifest")),
		targetMutation(capabilitycontract.KubernetesPutSecretOperation, targetSchema(map[string]any{"namespace": map[string]any{"type": "string"}, "name": map[string]any{"type": "string"}, "data": map[string]any{"type": "object", "writeOnly": true, "additionalProperties": map[string]any{"type": "string"}}}, "namespace", "name", "data")),
		targetRead(capabilitycontract.KubernetesGetResourceOperation, targetSchema(resourceFields(), "kind", "resourceName")),
		targetDestructive(capabilitycontract.KubernetesDeleteResourceOperation, targetSchema(resourceFields(), "kind", "resourceName")),
		targetRead(capabilitycontract.KubernetesGetResourceStatusOperation, targetSchema(resourceFields(), "resourceKind", "resourceName")),
		targetRead(capabilitycontract.KubernetesListEventsOperation, targetSchema(map[string]any{"namespace": map[string]any{"type": "string"}, "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 200}})),
		read(capabilitycontract.KubernetesListClustersOperation, map[string]any{"type": "object", "properties": map[string]any{"source": map[string]any{"type": "string"}}}),
		targetRead(capabilitycontract.KubernetesGetClusterInfoOperation, targetSchema(map[string]any{})),
		targetRead(capabilitycontract.KubernetesExecCommandOperation, targetSchema(map[string]any{
			"kubectlArgs": map[string]any{"type": "array", "minItems": 1, "items": map[string]any{"type": "string", "minLength": 1}},
			"stdin":       map[string]any{"type": "string", "writeOnly": true},
		}, "kubectlArgs")),
	}
}

func clusterTargetBinding() providercontract.ResourceBinding {
	return providercontract.ResourceBinding{Argument: "targetUri", ResourceType: "cluster", Required: true}
}

func providerOperation(id, effect string, input map[string]any) providercontract.Operation {
	return providercontract.Operation{ID: id, Version: 1, InputSchema: input, OutputSchema: map[string]any{"type": "object"}, Effect: effect, Idempotent: true, SupportsReadiness: effect != "read", TaskSupport: "sync_only"}
}

func targetSchema(properties map[string]any, operationRequired ...string) map[string]any {
	properties = cloneMap(properties)
	properties["targetUri"] = map[string]any{"type": "string", "pattern": "^cluster:[a-z][a-z0-9-]{0,31}:.+$"}
	properties["providerInstanceName"] = map[string]any{"type": "string", "minLength": 1, "pattern": "^[A-Za-z0-9][A-Za-z0-9_.-]*$"}
	properties["instanceType"] = map[string]any{"type": "string", "enum": []string{"vm", "container"}}
	required := []string{"targetUri", "providerInstanceName"}
	required = append(required, operationRequired...)
	return map[string]any{"type": "object", "required": required, "properties": properties}
}

func resourceFields() map[string]any {
	return map[string]any{
		"kind": map[string]any{"type": "string"}, "resourceKind": map[string]any{"type": "string"},
		"resourceName": map[string]any{"type": "string"}, "namespace": map[string]any{"type": "string"},
	}
}

func addManifestTool(server *mcp.Server, manifest providercontract.InstallManifest) {
	server.AddTool(&mcp.Tool{Name: "opute.provider.get_install_manifest", Description: "Read the K3s provider installation manifest", InputSchema: map[string]any{"type": "object"}, OutputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return structured(manifest)
	})
}

func addOperations(server *mcp.Server) {
	for _, operation := range operations() {
		operation := operation
		server.AddTool(&mcp.Tool{Name: operation.ID, Description: "K3s Kubernetes provider operation", InputSchema: operation.InputSchema, OutputSchema: operation.OutputSchema}, func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args := map[string]any{}
			if request != nil && request.Params != nil && len(request.Params.Arguments) > 0 {
				if err := json.Unmarshal(request.Params.Arguments, &args); err != nil {
					return nil, err
				}
			}
			return dispatch(ctx, operation.ID, args)
		})
	}
}

func dispatch(ctx context.Context, operation string, args map[string]any) (*mcp.CallToolResult, error) {
	switch operation {
	case capabilitycontract.KubernetesValidateOperation:
		return structured(map[string]any{"contractVersion": kubernetesCapability, "ready": true})
	case capabilitycontract.KubernetesProvisionOperation:
		if err := requireTarget(args); err != nil {
			return nil, err
		}
		return provision(ctx, args)
	case capabilitycontract.KubernetesStatusOperation:
		if err := requireTarget(args); err != nil {
			return nil, err
		}
		return getClusterInfo(ctx, args)
	case capabilitycontract.KubernetesConfigureRegistryOperation:
		if err := requireTarget(args); err != nil {
			return nil, err
		}
		return configureRegistry(ctx, args)
	case capabilitycontract.KubernetesRestartOperation:
		if err := requireTarget(args); err != nil {
			return nil, err
		}
		return restart(ctx, args)
	case capabilitycontract.KubernetesRemoveOperation:
		if err := requireTarget(args); err != nil {
			return nil, err
		}
		return remove(ctx, args)
	case capabilitycontract.KubernetesApplyManifestOperation:
		if err := requireTarget(args); err != nil {
			return nil, err
		}
		manifest := stringInput(args, "manifest")
		if manifest == "" {
			return nil, fmt.Errorf("manifest is required")
		}
		if len(manifest) > maxManifestBytes {
			return nil, fmt.Errorf("manifest exceeds the 4 MiB limit")
		}
		if _, err := runKubectl(ctx, args, []string{"apply", "-f", "-"}, []byte(strings.ReplaceAll(manifest, "__OPUTE_DOLLAR__", "$"))); err != nil {
			return nil, err
		}
		return structured(map[string]any{"targetUri": stringInput(args, "targetUri"), "applied": true})
	case capabilitycontract.KubernetesPutSecretOperation:
		if err := requireTarget(args); err != nil {
			return nil, err
		}
		return putSecret(ctx, args)
	case capabilitycontract.KubernetesGetResourceOperation:
		if err := requireTarget(args); err != nil {
			return nil, err
		}
		return getResource(ctx, args)
	case capabilitycontract.KubernetesDeleteResourceOperation:
		if err := requireTarget(args); err != nil {
			return nil, err
		}
		kind, name, namespace, err := resourceArguments(args)
		if err != nil {
			return nil, err
		}
		command := []string{"delete", kind, name}
		if namespace != "" {
			command = append(command, "-n", namespace)
		}
		command = append(command, "--ignore-not-found=true")
		if _, err := runKubectl(ctx, args, command, nil); err != nil {
			return nil, err
		}
		return structured(map[string]any{"targetUri": stringInput(args, "targetUri"), "kind": kind, "resourceName": name, "namespace": namespace, "deleted": true})
	case capabilitycontract.KubernetesGetResourceStatusOperation:
		if err := requireTarget(args); err != nil {
			return nil, err
		}
		return getStatus(ctx, args)
	case capabilitycontract.KubernetesListEventsOperation:
		if err := requireTarget(args); err != nil {
			return nil, err
		}
		return listEvents(ctx, args)
	case capabilitycontract.KubernetesListClustersOperation:
		return listClusters(ctx, args)
	case capabilitycontract.KubernetesGetClusterInfoOperation:
		if err := requireTarget(args); err != nil {
			return nil, err
		}
		return getClusterInfo(ctx, args)
	case capabilitycontract.KubernetesExecCommandOperation:
		if err := requireTarget(args); err != nil {
			return nil, err
		}
		kubectlArgs, err := stringSliceArgument(args, "kubectlArgs")
		if err != nil {
			return nil, err
		}
		stdout, err := runKubectl(ctx, args, kubectlArgs, []byte(stringInput(args, "stdin")))
		if err != nil {
			return nil, err
		}
		return structured(map[string]any{"targetUri": stringInput(args, "targetUri"), "stdout": string(stdout), "exitCode": 0})
	default:
		return nil, fmt.Errorf("unknown K3s provider operation %q", operation)
	}
}

func provision(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	instance := stringInput(args, "providerInstanceName")
	version := stringInput(args, "version")
	if version == "" {
		version = defaultK3sVersion
	}
	if strings.ContainsAny(version, "\x00\r\n ';&|`$()") {
		return nil, fmt.Errorf("version contains unsafe characters")
	}
	if kind := stringInput(args, "instanceType"); kind == "container" {
		if _, err := runCommand(ctx, []string{"config", "set", instance, "security.nesting=true"}, nil); err != nil {
			return nil, fmt.Errorf("enable container nesting: %w", err)
		}
	}
	if _, err := runCommand(ctx, []string{"start", instance}, nil); err != nil && !strings.Contains(strings.ToLower(err.Error()), "already running") {
		return nil, fmt.Errorf("start Kubernetes target: %w", err)
	}
	install := fmt.Sprintf(
		"%s && env INSTALL_K3S_VERSION=%s sh -c %s",
		ensureCurlCommand,
		shellQuote(version),
		shellQuote("curl -sfL https://get.k3s.io | sh -"),
	)
	if _, err := runCommand(ctx, []string{"exec", instance, "--", "bash", "-lc", install}, nil); err != nil {
		return nil, fmt.Errorf("provision Kubernetes runtime: %w", err)
	}
	result, err := getClusterInfo(ctx, args)
	if err != nil {
		return nil, fmt.Errorf("verify Kubernetes runtime: %w", err)
	}
	object, _ := result.StructuredContent.(map[string]any)
	object["provisioned"] = true
	object["version"] = version
	return structured(object)
}

func configureRegistry(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	instance := stringInput(args, "providerInstanceName")
	endpoint := strings.TrimRight(stringInput(args, "endpoint"), "/")
	parsed, err := url.Parse(endpoint)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || strings.ContainsAny(endpoint, "\x00\r\n") {
		return nil, fmt.Errorf("endpoint must be an absolute http or https URL")
	}
	registry := stringInput(args, "registry")
	if registry == "" {
		registry = parsed.Host
	}
	if strings.ContainsAny(registry, "\x00\r\n ';&|`$()") {
		return nil, fmt.Errorf("registry contains unsafe characters")
	}
	protocol := "https"
	if insecure, _ := args["insecure"].(bool); insecure {
		protocol = "http"
	}
	config := fmt.Sprintf("mirrors:\n  %s:\n    endpoint:\n      - %s\nconfigs: {}\n", registry, protocol+"://"+parsed.Host)
	encoded := base64.StdEncoding.EncodeToString([]byte(config))
	write := fmt.Sprintf("mkdir -p /etc/rancher/k3s; printf '%%s' %s | base64 -d > /etc/rancher/k3s/registries.yaml", shellQuote(encoded))
	if _, err := runCommand(ctx, []string{"exec", instance, "--", "bash", "-lc", write}, nil); err != nil {
		return nil, fmt.Errorf("configure Kubernetes registry: %w", err)
	}
	if _, err := runCommand(ctx, []string{"exec", instance, "--", "systemctl", "restart", "k3s"}, nil); err != nil {
		return nil, fmt.Errorf("restart Kubernetes runtime after registry configuration: %w", err)
	}
	return structured(map[string]any{
		"targetUri":  stringInput(args, "targetUri"),
		"registry":   registry,
		"endpoint":   protocol + "://" + parsed.Host,
		"configured": true,
	})
}

func restart(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	instance := stringInput(args, "providerInstanceName")
	if _, err := runCommand(ctx, []string{"exec", instance, "--", "systemctl", "restart", "k3s"}, nil); err != nil {
		return nil, fmt.Errorf("restart Kubernetes runtime: %w", err)
	}
	result, err := getClusterInfo(ctx, args)
	if err != nil {
		return nil, err
	}
	object, _ := result.StructuredContent.(map[string]any)
	object["restarted"] = true
	return structured(object)
}

func remove(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	instance := stringInput(args, "providerInstanceName")
	if _, err := runCommand(ctx, []string{"exec", instance, "--", "bash", "-lc", "/usr/local/bin/k3s-uninstall.sh"}, nil); err != nil {
		return nil, fmt.Errorf("remove Kubernetes runtime: %w", err)
	}
	return structured(map[string]any{"targetUri": stringInput(args, "targetUri"), "removed": true})
}

const (
	defaultK3sVersion = "v1.31.8+k3s1"
	ensureCurlCommand = "if ! command -v curl >/dev/null 2>&1; then apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y curl; fi"
)

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\\"'\\\"'") + "'"
}

func putSecret(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	namespace := stringInput(args, "namespace")
	name := stringInput(args, "name")
	if namespace == "" || name == "" {
		return nil, fmt.Errorf("namespace and name are required")
	}
	data, ok := args["data"].(map[string]any)
	if !ok || len(data) == 0 {
		return nil, fmt.Errorf("data is required")
	}
	encoded := map[string]string{}
	for key, raw := range data {
		value, ok := raw.(string)
		if !ok || strings.TrimSpace(key) == "" {
			return nil, fmt.Errorf("secret data must contain string values")
		}
		encoded[key] = base64.StdEncoding.EncodeToString([]byte(value))
	}
	manifest := map[string]any{"apiVersion": "v1", "kind": "Secret", "metadata": map[string]any{"name": name, "namespace": namespace}, "type": "Opaque", "data": encoded}
	body, err := json.Marshal(manifest)
	if err != nil {
		return nil, err
	}
	if _, err := runKubectl(ctx, args, []string{"apply", "-f", "-"}, body); err != nil {
		return nil, err
	}
	return structured(map[string]any{"targetUri": stringInput(args, "targetUri"), "namespace": namespace, "name": name, "configured": true})
}

func getResource(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	kind, name, namespace, err := resourceArguments(args)
	if err != nil {
		return nil, err
	}
	command := []string{"get", kind, name, "-o", "json"}
	if namespace != "" {
		command = append(command, "-n", namespace)
	}
	raw, err := runKubectl(ctx, args, command, nil)
	if err != nil {
		return nil, err
	}
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil, fmt.Errorf("invalid Kubernetes resource response: %w", err)
	}
	return structured(map[string]any{"targetUri": stringInput(args, "targetUri"), "kind": kind, "resourceName": name, "namespace": namespace, "resource": object, "json": string(raw), "yaml": string(raw)})
}

func getStatus(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	result, err := getResource(ctx, args)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "not found") {
			return structured(map[string]any{"targetUri": stringInput(args, "targetUri"), "status": "missing", "message": err.Error()})
		}
		return nil, err
	}
	object, _ := result.StructuredContent.(map[string]any)
	resourceObject, _ := object["resource"].(map[string]any)
	status := "pending"
	switch strings.ToLower(firstNonEmpty(stringInput(args, "kind"), stringInput(args, "resourceKind"))) {
	case "service":
		status = "ready"
	case "pod":
		phase, _ := nestedString(resourceObject, "status", "phase")
		if phase == "Running" || phase == "Succeeded" {
			status = "ready"
		}
	case "deployment":
		desired := nestedNumber(resourceObject, "spec", "replicas")
		ready := nestedNumber(resourceObject, "status", "readyReplicas")
		if desired == 0 {
			desired = 1
		}
		if ready >= desired {
			status = "ready"
		}
	case "persistentvolumeclaim", "pvc":
		phase, _ := nestedString(resourceObject, "status", "phase")
		if phase == "Bound" {
			status = "ready"
		}
	}
	object["status"] = status
	return structured(object)
}

func listEvents(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	limit := 50
	if raw, ok := args["limit"].(float64); ok && int(raw) > 0 {
		limit = int(raw)
	}
	if limit > 200 {
		limit = 200
	}
	namespace := stringInput(args, "namespace")
	command := []string{"get", "events", "-o", "json"}
	if namespace != "" {
		command = append(command, "-n", namespace)
	}
	raw, err := runKubectl(ctx, args, command, nil)
	if err != nil {
		return nil, err
	}
	var list struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("invalid Kubernetes events response: %w", err)
	}
	sort.SliceStable(list.Items, func(i, j int) bool { return eventTimestamp(list.Items[i]) > eventTimestamp(list.Items[j]) })
	if len(list.Items) > limit {
		list.Items = list.Items[:limit]
	}
	events := make([]map[string]any, 0, len(list.Items))
	for _, item := range list.Items {
		metadata, _ := item["metadata"].(map[string]any)
		events = append(events, map[string]any{"type": item["type"], "reason": item["reason"], "message": item["message"], "count": item["count"], "firstTimestamp": item["firstTimestamp"], "lastTimestamp": item["lastTimestamp"], "eventTime": item["eventTime"], "source": item["source"], "involvedObject": item["involvedObject"], "name": metadata["name"], "namespace": metadata["namespace"]})
	}
	return structured(map[string]any{"targetUri": stringInput(args, "targetUri"), "namespace": namespace, "limit": limit, "events": events})
}

func listClusters(ctx context.Context, _ map[string]any) (*mcp.CallToolResult, error) {
	raw, err := runCommand(ctx, []string{"list", "--format", "json"}, nil)
	if err != nil {
		return nil, err
	}
	var instances []map[string]any
	if err := json.Unmarshal(raw, &instances); err != nil {
		return nil, fmt.Errorf("invalid Incus inventory response: %w", err)
	}
	clusters := make([]map[string]any, 0)
	for _, instance := range instances {
		name, _ := instance["name"].(string)
		if name == "" {
			continue
		}
		instanceType, _ := instance["type"].(string)
		if instanceType == "virtual-machine" {
			instanceType = "vm"
		}
		if instanceType != "vm" && instanceType != "container" {
			continue
		}
		versionOutput, err := runCommand(ctx, []string{"exec", name, "--", "k3s", "--version"}, nil)
		if err != nil {
			continue
		}
		cluster := map[string]any{"name": name, "status": instance["status"], "instanceType": instanceType, "provider": "k3s", "version": parseK3sVersion(string(versionOutput))}
		if nodesOutput, nodesErr := runCommand(ctx, []string{"exec", name, "--", "k3s", "kubectl", "get", "nodes", "-o", "custom-columns=NAME:.metadata.name,STATUS:.status.conditions[-1].type,VERSION:.status.nodeInfo.kubeletVersion", "--no-headers"}, nil); nodesErr == nil {
			nodes := parseClusterNodes(string(nodesOutput))
			cluster["nodes"] = nodes
			cluster["nodeCount"] = len(nodes)
		}
		clusters = append(clusters, cluster)
	}
	return structured(map[string]any{"clusters": clusters})
}

func getClusterInfo(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	instance := stringInput(args, "providerInstanceName")
	versionOutput, err := runCommand(ctx, []string{"exec", instance, "--", "k3s", "--version"}, nil)
	if err != nil {
		return nil, err
	}
	nodesOutput, err := runCommand(ctx, []string{"exec", instance, "--", "k3s", "kubectl", "get", "nodes", "-o", "custom-columns=NAME:.metadata.name,STATUS:.status.conditions[-1].type,VERSION:.status.nodeInfo.kubeletVersion", "--no-headers"}, nil)
	if err != nil {
		return nil, err
	}
	nodes := parseClusterNodes(string(nodesOutput))
	return structured(map[string]any{
		"targetUri": stringInput(args, "targetUri"),
		"version":   parseK3sVersion(string(versionOutput)),
		"nodes":     nodes,
		"nodeCount": len(nodes),
		"ready":     len(nodes) > 0,
	})
}

func parseK3sVersion(output string) string {
	first := strings.TrimSpace(strings.SplitN(output, "\n", 2)[0])
	const prefix = "k3s version "
	if strings.HasPrefix(strings.ToLower(first), prefix) {
		return strings.TrimSpace(first[len(prefix):])
	}
	return first
}

func parseClusterNodes(output string) []map[string]any {
	lines := strings.Split(output, "\n")
	nodes := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		parts := strings.Fields(strings.TrimSpace(line))
		if len(parts) == 0 {
			continue
		}
		node := map[string]any{"name": parts[0], "status": "Unknown", "roles": "control-plane", "age": ""}
		if len(parts) > 1 {
			node["status"] = parts[1]
		}
		if len(parts) > 2 {
			node["version"] = parts[2]
		}
		nodes = append(nodes, node)
	}
	return nodes
}

func runKubectl(ctx context.Context, args map[string]any, kubectlArgs []string, input []byte) ([]byte, error) {
	if err := requireTarget(args); err != nil {
		return nil, err
	}
	command := append([]string{"exec", stringInput(args, "providerInstanceName"), "--", "k3s", "kubectl"}, kubectlArgs...)
	return runCommand(ctx, command, input)
}

func runCommand(ctx context.Context, args []string, input []byte) ([]byte, error) {
	instance := exec.CommandContext(ctx, "incus", args...)
	if input != nil {
		instance.Stdin = bytes.NewReader(input)
	}
	var stdout, stderr bytes.Buffer
	instance.Stdout = &stdout
	instance.Stderr = &stderr
	if err := instance.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = strings.TrimSpace(stdout.String())
		}
		return nil, fmt.Errorf("incus K3s command failed: %w: %s", err, message)
	}
	return stdout.Bytes(), nil
}

func requireTarget(args map[string]any) error {
	uri := stringInput(args, "targetUri")
	instance := stringInput(args, "providerInstanceName")
	if !strings.HasPrefix(uri, "cluster:") {
		return fmt.Errorf("targetUri must be a canonical cluster URI")
	}
	if instance == "" {
		return fmt.Errorf("providerInstanceName is required")
	}
	if kind := stringInput(args, "instanceType"); kind != "" && kind != "vm" && kind != "container" {
		return fmt.Errorf("unsupported instanceType %q", kind)
	}
	return nil
}

func resourceArguments(args map[string]any) (string, string, string, error) {
	kind := firstNonEmpty(stringInput(args, "kind"), stringInput(args, "resourceKind"))
	name := stringInput(args, "resourceName")
	namespace := stringInput(args, "namespace")
	if kind == "" || name == "" {
		return "", "", "", fmt.Errorf("kind and resourceName are required")
	}
	return kind, name, namespace, nil
}

func nestedString(object map[string]any, path ...string) (string, bool) {
	var current any = object
	for _, part := range path {
		value, ok := current.(map[string]any)
		if !ok {
			return "", false
		}
		current = value[part]
	}
	text, ok := current.(string)
	return text, ok
}

func nestedNumber(object map[string]any, path ...string) float64 {
	var current any = object
	for _, part := range path {
		value, ok := current.(map[string]any)
		if !ok {
			return 0
		}
		current = value[part]
	}
	switch value := current.(type) {
	case float64:
		return value
	case int:
		return float64(value)
	case json.Number:
		parsed, _ := strconv.ParseFloat(string(value), 64)
		return parsed
	default:
		return 0
	}
}

func eventTimestamp(item map[string]any) string {
	for _, key := range []string{"eventTime", "lastTimestamp", "firstTimestamp"} {
		if value, ok := item[key].(string); ok && value != "" {
			return value
		}
	}
	return ""
}

func structured(value any) (*mcp.CallToolResult, error) {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "provider result"}}, StructuredContent: value}, nil
}

func cloneMap(input map[string]any) map[string]any {
	output := make(map[string]any, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}

func stringInput(args map[string]any, key string) string {
	value, _ := args[key].(string)
	return strings.TrimSpace(value)
}

func stringSliceArgument(args map[string]any, key string) ([]string, error) {
	raw, ok := args[key].([]any)
	if !ok || len(raw) == 0 {
		return nil, fmt.Errorf("%s must contain at least one argument", key)
	}
	result := make([]string, 0, len(raw))
	for _, value := range raw {
		text, ok := value.(string)
		if !ok || strings.TrimSpace(text) == "" || strings.ContainsAny(text, "\x00\r\n") {
			return nil, fmt.Errorf("%s must contain non-empty safe strings", key)
		}
		result = append(result, text)
	}
	return result, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
