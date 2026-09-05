package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wunderous/host-agents/internal/domain/host"
	"github.com/wunderous/host-agents/internal/domain/incus"
	"github.com/wunderous/host-agents/internal/domain/kubernetes"
	"github.com/wunderous/host-agents/internal/domain/postgres"
	"github.com/wunderous/host-agents/internal/domain/serving"
	"github.com/wunderous/host-agents/internal/hostagent"
	"github.com/wunderous/host-agents/internal/resource"
)

// DispatchTool executes a host MCP tool via HostOperationsService and returns
// an MCP CallToolResult. The argument map is the unchanged client/model input;
// resolved provider coordinates arrive only through the typed execution
// binding, never as synthetic argument fields.
func DispatchTool(ctx context.Context, svc *hostagent.Service, name string, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("tool name is required")
	}
	if err := ctx.Err(); err != nil {
		return ErrorResult(err), nil
	}

	result, err := runTool(ctx, svc, name, args, binding, onData)
	if err != nil {
		return ErrorResult(err), nil
	}
	return result, nil
}

func listClustersFastArg(args map[string]any) bool {
	// Cluster inventory is a control-plane list operation. Keep the default
	// bounded and provider-independent; callers that need live node and
	// version probes can request the slower detail path explicitly.
	if requested, ok := args["fast"].(bool); ok {
		return requested
	}
	return true
}

func runTool(ctx context.Context, svc *hostagent.Service, name string, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
	handler, ok := LookupTool(name)
	if !ok {
		if IsOmittedToolName(name) {
			return nil, fmt.Errorf("tool '%s' is not available in the Go host agent (bridge-backed capability omitted)", name)
		}
		return nil, fmt.Errorf("tool not found: %s", name)
	}
	return handler(ctx, svc, args, binding, onData)
}

func structuredResult(structured any, text string) *mcp.CallToolResult {
	if text == "" {
		b, _ := json.Marshal(structured)
		text = string(b)
	}
	return &mcp.CallToolResult{
		Content:           []mcp.Content{&mcp.TextContent{Text: text}},
		StructuredContent: structured,
	}
}

// CapabilityError is the typed owner boundary for a capability invocation
// failure. Owner records which layer failed — "capability" for capability-
// owned validation/execution, "admission" for tenant/resource binding, or
// "lifecycle" for generation state — so clients render capability-owned
// invalid-input errors distinctly from envelope or transport problems.
type CapabilityError struct {
	Owner   string
	Code    string
	Message string
	Err     error
}

func (e *CapabilityError) Error() string {
	message := e.Message
	if message == "" && e.Err != nil {
		message = e.Err.Error()
	}
	if message == "" {
		message = "capability error"
	}
	return message
}

func (e *CapabilityError) Unwrap() error { return e.Err }

// NewCapabilityError wraps err in the typed owner boundary.
func NewCapabilityError(owner, code string, err error) *CapabilityError {
	return &CapabilityError{Owner: owner, Code: code, Err: err}
}

// ErrorResult builds an MCP error tool result.
func ErrorResult(err error) *mcp.CallToolResult {
	var admissionErr *resource.AdmissionError
	if errors.As(err, &admissionErr) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "Error: " + err.Error()}},
			StructuredContent: map[string]any{
				"code":         admissionErr.Code,
				"class":        admissionErr.Class,
				"pressure":     admissionErr.Pressure,
				"reason":       admissionErr.Reason,
				"retryAfterMs": admissionErr.RetryAfterMs,
				"owner":        "admission",
			},
			IsError: true,
		}
	}
	var requestErr *resource.RequestError
	if errors.As(err, &requestErr) {
		return &mcp.CallToolResult{
			Content:           []mcp.Content{&mcp.TextContent{Text: "Error: " + err.Error()}},
			StructuredContent: map[string]any{"code": requestErr.Code, "field": requestErr.Field, "reason": requestErr.Reason, "owner": "admission"},
			IsError:           true,
		}
	}
	var reconcileErr *resource.ReconcileError
	if errors.As(err, &reconcileErr) {
		return &mcp.CallToolResult{
			Content:           []mcp.Content{&mcp.TextContent{Text: "Error: " + err.Error()}},
			StructuredContent: map[string]any{"code": reconcileErr.Code, "reason": reconcileErr.Reason, "owner": "admission"},
			IsError:           true,
		}
	}
	if capabilityErr, ok := err.(*CapabilityError); ok {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "Error: " + err.Error()}},
			StructuredContent: map[string]any{
				"code":    capabilityErr.Code,
				"owner":   capabilityErr.Owner,
				"message": capabilityErr.Error(),
			},
			IsError: true,
		}
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: "Error: " + err.Error()}},
		IsError: true,
	}
}

func vmNameFromBinding(binding ExecutionBinding) string {
	return binding.ProviderInstanceName()
}

func resourceURIFromBinding(binding ExecutionBinding) string {
	for _, resource := range binding.Resources {
		if resource.ResourceType == "cluster" && strings.TrimSpace(resource.URI) != "" {
			return resource.URI
		}
	}
	return ""
}

func serviceNameFromBinding(args map[string]any, binding ExecutionBinding) string {
	if name := stringField(args, "serviceName"); name != "" {
		return name
	}
	return binding.StringCoordinate("serviceName")
}

func serviceScopeFromBinding(args map[string]any, binding ExecutionBinding) string {
	if scope := stringField(args, "scope"); scope != "" {
		return scope
	}
	return binding.StringCoordinate("scope")
}

func withBindingURI(out map[string]any, binding ExecutionBinding, allowedTypes ...string) map[string]any {
	if out == nil {
		out = map[string]any{}
	}
	if _, exists := out["uri"]; exists {
		return out
	}
	for _, resource := range binding.Resources {
		if len(allowedTypes) > 0 && !containsString(allowedTypes, resource.ResourceType) {
			continue
		}
		if strings.TrimSpace(resource.URI) != "" {
			out["uri"] = resource.URI
			break
		}
	}
	return out
}

func stringField(args map[string]any, key string) string {
	v, _ := args[key].(string)
	return strings.TrimSpace(v)
}

func rawStringField(args map[string]any, key string) string {
	v, _ := args[key].(string)
	return v
}

func postgresqlServiceRelayArgs(args map[string]any) *postgres.PostgreSQLServiceRelayArgs {
	raw, ok := args["localRelay"].(map[string]any)
	if !ok || raw == nil {
		return nil
	}
	return &postgres.PostgreSQLServiceRelayArgs{
		SessionID:       stringField(raw, "sessionId"),
		ListenHost:      stringField(raw, "listenHost"),
		ListenPort:      intField(raw, "listenPort"),
		TargetHost:      stringField(raw, "targetHost"),
		TargetPort:      intField(raw, "targetPort"),
		TTLSeconds:      intField(raw, "ttlSeconds"),
		RelayToken:      stringField(raw, "relayToken"),
		Persistent:      boolField(raw, "persistent"),
		ReplaceExisting: boolField(raw, "replaceExisting"),
	}
}

func postgresqlServiceArgs(args map[string]any, binding ExecutionBinding) postgres.PostgreSQLServiceArgs {
	return postgres.PostgreSQLServiceArgs{
		VMName: vmNameFromBinding(binding), ClusterName: stringField(args, "clusterName"), Namespace: stringField(args, "namespace"),
		Instances: intField(args, "instances"), StorageClass: stringField(args, "storageClass"), StorageSize: stringField(args, "storageSize"),
		RetentionPolicy: stringField(args, "retentionPolicy"), RestartConsumers: optionalBoolField(args, "restartConsumers"),
		Databases: uniqueStringSlice(stringSliceField(args, "databases")), ConsumerDatabaseKeys: stringMapField(args, "consumerDatabaseKeys"),
		ConsumerSecretName: stringField(args, "consumerSecretName"), ConsumerSecretLabel: stringField(args, "consumerSecretLabel"),
		ServiceOwner: stringField(args, "serviceOwner"), ServicePartOf: stringField(args, "servicePartOf"), RelayDeviceName: stringField(args, "relayDeviceName"),
		Relay: postgresqlServiceRelayArgs(args),
	}
}

func resolveLocalLLMModelArg(args map[string]any) (string, error) {
	if modelRef, err := explicitLocalLLMModelRef(args); err != nil {
		return "", err
	} else if modelRef != "" {
		return modelRef, nil
	}
	switch stringField(args, "modelPreset") {
	case "":
		if localLLMRuntime(args) == "llama-cpp" {
			return "qwen3.5-0.8b/base-llama", nil
		}
		return hostagent.DefaultOllamaModel, nil
	case "lfm2-2.6b":
		if localLLMRuntime(args) == "llama-cpp" {
			return "", fmt.Errorf("model preset %q requires the ollama runtime", stringField(args, "modelPreset"))
		}
		return hostagent.DefaultOllamaModel, nil
	case "lfm2.5-thinking":
		if localLLMRuntime(args) == "llama-cpp" {
			return "", fmt.Errorf("model preset %q requires the ollama runtime", stringField(args, "modelPreset"))
		}
		return "lfm2.5-thinking:1.2b", nil
	case "qwen3.5":
		if localLLMRuntime(args) == "llama-cpp" {
			return "qwen3.5-0.8b/base-llama", nil
		}
		return "qwen3.5:2b", nil
	case "qwen3.5-0.8b":
		if localLLMRuntime(args) == "llama-cpp" {
			return "qwen3.5-0.8b/base-llama", nil
		}
		return "qwen3.5:0.8b", nil
	default:
		return "", fmt.Errorf("unsupported local LLM model preset %q", stringField(args, "modelPreset"))
	}
}

func explicitLocalLLMModelRef(args map[string]any) (string, error) {
	modelRef := stringField(args, "modelRef")
	model := stringField(args, "model")
	if modelRef != "" && model != "" && modelRef != model {
		return "", fmt.Errorf("model and modelRef must identify the same model when both are supplied")
	}
	if model != "" {
		return model, nil
	}
	return modelRef, nil
}

func localLLMModelRef(args map[string]any) string {
	model, err := explicitLocalLLMModelRef(args)
	if err != nil {
		return ""
	}
	return model
}

func localLLMModelRole(args map[string]any) (string, error) {
	role := stringField(args, "role")
	if role == "" {
		return "language", nil
	}
	if role != "language" && role != "embedding" {
		return "", fmt.Errorf("unsupported model role %q; expected language or embedding", role)
	}
	return role, nil
}

// localLLMRuntime defaults new payloads to the shared Ollama runtime. The
// llama-cpp path remains available only when explicitly selected.
func localLLMRuntime(args map[string]any) string {
	if runtime := stringField(args, "runtime"); runtime != "" {
		return runtime
	}
	return "ollama"
}

func stringSliceField(args map[string]any, key string) []string {
	values, ok := args[key].([]any)
	if !ok {
		if typed, typedOK := args[key].([]string); typedOK {
			return typed
		}
		return nil
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if text, ok := value.(string); ok {
			result = append(result, text)
		}
	}
	return result
}

func uniqueStringSlice(values []string) []string {
	seen := make(map[string]bool, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}

func stringMapField(args map[string]any, key string) map[string]string {
	raw, ok := args[key].(map[string]any)
	if !ok || raw == nil {
		return nil
	}
	result := make(map[string]string, len(raw))
	for name, value := range raw {
		if text, ok := value.(string); ok {
			result[name] = text
		}
	}
	return result
}

func boolField(args map[string]any, key string) bool {
	v, _ := args[key].(bool)
	return v
}

func intField(args map[string]any, key string) int {
	switch v := args[key].(type) {
	case float64:
		return int(v)
	case float32:
		return int(v)
	case int:
		return v
	case int64:
		return int(v)
	case int32:
		return int(v)
	case json.Number:
		n, err := v.Int64()
		if err != nil {
			return 0
		}
		return int(n)
	default:
		return 0
	}
}

func intSliceField(args map[string]any, key string) []int {
	values, ok := args[key].([]any)
	if !ok {
		if typed, typedOK := args[key].([]int); typedOK {
			return typed
		}
		return nil
	}
	result := make([]int, 0, len(values))
	for _, value := range values {
		result = append(result, intValue(value))
	}
	return result
}

func intValue(value any) int {
	switch v := value.(type) {
	case float64:
		return int(v)
	case float32:
		return int(v)
	case int:
		return v
	case int64:
		return int(v)
	case int32:
		return int(v)
	case json.Number:
		n, err := v.Int64()
		if err == nil {
			return int(n)
		}
	}
	return 0
}

func int64Field(args map[string]any, key string) int64 {
	switch v := args[key].(type) {
	case float64:
		return int64(v)
	case float32:
		return int64(v)
	case int:
		return int64(v)
	case int64:
		return v
	case int32:
		return int64(v)
	case json.Number:
		n, err := v.Int64()
		if err != nil {
			return 0
		}
		return n
	default:
		return 0
	}
}

func optionalIntField(args map[string]any, key string) *int {
	if args == nil {
		return nil
	}
	if _, ok := args[key]; !ok {
		return nil
	}
	v := intField(args, key)
	return &v
}

func optionalInt64Field(args map[string]any, key string) *int64 {
	if args == nil {
		return nil
	}
	if _, ok := args[key]; !ok {
		return nil
	}
	v := int64Field(args, key)
	return &v
}

func optionalBoolField(args map[string]any, key string) *bool {
	if args == nil {
		return nil
	}
	if _, ok := args[key]; !ok {
		return nil
	}
	v := boolField(args, key)
	return &v
}

func provisionArgs(args map[string]any) incus.ProvisionVMArgs {
	vmName := stringField(args, "vmName")
	if vmName == "" {
		vmName = stringField(args, "name")
	}
	return incus.ProvisionVMArgs{
		VMName:       vmName,
		Image:        stringField(args, "image"),
		CPUs:         intField(args, "cpus"),
		Memory:       stringField(args, "memory"),
		Disk:         stringField(args, "disk"),
		InstanceType: stringField(args, "instanceType"),
	}
}

func servingAssignmentArgs(args map[string]any) serving.ServingAssignmentArgs {
	return serving.ServingAssignmentArgs{
		ContractVersion: stringField(args, "contractVersion"),
		AssignmentID:    stringField(args, "assignmentId"),
		Generation:      intField(args, "generation"),
		IdempotencyKey:  stringField(args, "idempotencyKey"),
		Service:         stringField(args, "service"),
		Mode:            stringField(args, "mode"),
		Runtime:         stringField(args, "runtime"),
		Target:          mapField(args, "target"),
		Artifact:        mapField(args, "artifact"),
		Endpoints:       anySliceField(args, "endpoints"),
		Readiness:       anySliceField(args, "readiness"),
		Exposure:        mapField(args, "exposure"),
		ServiceUnit:     stringField(args, "serviceUnit"),
		DesiredState:    stringField(args, "desiredState"),
		RestartPolicy:   stringField(args, "restartPolicy"),
	}
}

func mapField(args map[string]any, name string) map[string]any {
	if value, ok := args[name].(map[string]any); ok {
		return value
	}
	return nil
}

func anySliceField(args map[string]any, name string) []any {
	if value, ok := args[name].([]any); ok {
		return value
	}
	return nil
}

func execCommandArgs(args map[string]any, binding ExecutionBinding) host.ExecCommandArgs {
	var argv []string
	if raw, ok := args["args"].([]any); ok {
		for _, item := range raw {
			if s, ok := item.(string); ok {
				argv = append(argv, s)
			}
		}
	}
	return host.ExecCommandArgs{
		VMName:    vmNameFromBinding(binding),
		Command:   stringField(args, "command"),
		Args:      argv,
		TimeoutMs: intField(args, "timeout"),
	}
}

func installHelmChartArgs(args map[string]any, binding ExecutionBinding) kubernetes.InstallHelmChartArgs {
	releaseName := stringField(args, "releaseName")
	if releaseName == "" {
		releaseName = stringField(args, "chartName")
	}
	if releaseName == "" {
		releaseName = stringField(args, "name")
	}
	chartSource := stringField(args, "chartSource")
	if chartSource == "" {
		chartSource = stringField(args, "chart")
	}
	namespace := stringField(args, "namespace")
	if namespace == "" {
		namespace = "kube-system"
	}
	return kubernetes.InstallHelmChartArgs{
		VMName:      vmNameFromBinding(binding),
		ReleaseName: releaseName,
		ChartSource: chartSource,
		Namespace:   namespace,
		Repo:        stringField(args, "repo"),
		Values:      hostagent.HelmValuesYAML(args["values"]),
	}
}

func uninstallHelmChartArgs(args map[string]any, binding ExecutionBinding) kubernetes.UninstallHelmChartArgs {
	releaseName := stringField(args, "releaseName")
	if releaseName == "" {
		releaseName = stringField(args, "chartName")
	}
	if releaseName == "" {
		releaseName = stringField(args, "name")
	}
	namespace := stringField(args, "namespace")
	if namespace == "" {
		namespace = "kube-system"
	}
	return kubernetes.UninstallHelmChartArgs{
		VMName:      vmNameFromBinding(binding),
		ReleaseName: releaseName,
		Namespace:   namespace,
	}
}
