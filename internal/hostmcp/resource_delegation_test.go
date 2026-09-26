package hostmcp

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	hostcapability "github.com/wunderous/host-agents/internal/capability"
	"github.com/wunderous/host-agents/internal/hostagent"
	"github.com/wunderous/host-agents/internal/mcphttp"
	"github.com/wunderous/host-agents/internal/resource"
	"github.com/wunderous/host-agents/internal/resourceid"
	"github.com/wunderous/host-agents/internal/tasks"
	"github.com/wunderous/host-agents/internal/tools"
)

func TestProviderResourceDelegationIsScopedToLiveTaskAndRevocable(t *testing.T) {
	registry := tasks.NewRegistry()
	task := registry.Create("run_host_local_recipe", nil, time.Hour, "running", nil)
	server := &Server{
		agent:                 hostagent.New(hostagent.Options{AgentID: "agent-a"}),
		tasks:                 registry,
		toolDefs:              []tools.ToolDefinition{{Name: "get_host_info"}},
		resourceDelegationKey: []byte("test-resource-delegation-key"),
		resourceDelegations:   make(map[string]string),
	}
	parent := &resource.Reservation{
		ID: "reservation-a",
		Request: resource.AdmissionRequest{
			AgentID: "agent-a", TaskID: task.TaskID, OperationID: "run_host_local_recipe",
		},
	}
	ctx := resource.WithReservation(context.Background(), parent)
	ctx = resource.WithOperationIdentity(ctx, "run_host_local_recipe", task.TaskID)
	mismatchedOperation := resource.WithOperationIdentity(
		resource.WithReservation(context.Background(), parent), "other-parent-operation", task.TaskID,
	)
	if _, _, err := server.withProviderResourceDelegation(mismatchedOperation, "provider.example", "generation-a", "tunnel.ensure"); err == nil {
		t.Fatal("reservation bound to another operation was signed into a callback delegation")
	}

	delegated, release, err := server.withProviderResourceDelegation(ctx, "provider.example", "generation-a", "tunnel.ensure")
	if err != nil {
		t.Fatal(err)
	}
	token := mcphttp.ResourceDelegationFromContext(delegated)
	if token == "" {
		t.Fatal("provider operation did not receive a delegation")
	}
	claims, err := server.verifyResourceDelegation(token)
	if err != nil {
		t.Fatalf("verify issued delegation: %v", err)
	}
	if claims.ProviderID != "provider.example" || claims.ProviderGeneration != "generation-a" || claims.ProviderOperation != "tunnel.ensure" {
		t.Fatalf("delegation provider scope = %#v", claims)
	}

	request := callbackRequest(token)
	callbackCtx, err := server.contextForProviderCallback(context.Background(), request, "get_host_info")
	if err != nil {
		t.Fatalf("verify callback request: %v", err)
	}
	gotParent, ok := resource.ReservationFromContext(callbackCtx)
	if !ok || gotParent.ID != parent.ID {
		t.Fatalf("callback reservation = %#v, want %q", gotParent, parent.ID)
	}
	operation, taskID := resource.OperationIdentityFromContext(callbackCtx)
	if operation != "run_host_local_recipe" || taskID != task.TaskID {
		t.Fatalf("callback task identity = (%q, %q)", operation, taskID)
	}

	if _, err := server.contextForProviderCallback(context.Background(), callbackRequest(token+"x"), "get_host_info"); err == nil {
		t.Fatal("tampered delegation was accepted")
	}
	if _, err := server.contextForProviderCallback(context.Background(), request, "run_host_plan"); err == nil {
		t.Fatal("lifecycle callback target was accepted")
	}
	if _, err := server.contextForProviderCallback(context.Background(), request, "unregistered_tool"); err == nil {
		t.Fatal("unregistered callback target was accepted")
	}

	release()
	if _, err := server.contextForProviderCallback(context.Background(), request, "get_host_info"); err == nil {
		t.Fatal("revoked delegation was accepted")
	}
}

func TestProviderResourceDelegationRejectsExpiredAndWrongAgent(t *testing.T) {
	registry := tasks.NewRegistry()
	task := registry.Create("run_host_local_recipe", nil, time.Hour, "running", nil)
	server := &Server{
		agent:                 hostagent.New(hostagent.Options{AgentID: "agent-a"}),
		tasks:                 registry,
		toolDefs:              []tools.ToolDefinition{{Name: "get_host_info"}},
		resourceDelegationKey: []byte("test-resource-delegation-key"),
		resourceDelegations:   make(map[string]string),
	}
	claims := resourceDelegationClaims{
		Version: resourceDelegationVersion, ID: "expired-delegation",
		ReservationID: "reservation-a", AgentID: "agent-a", TaskID: task.TaskID,
		TaskOperation: "run_host_local_recipe", ProviderID: "provider.example",
		ProviderGeneration: "generation-a", ProviderOperation: "tunnel.ensure",
		ExpiresAt: time.Now().Add(-time.Minute).Unix(),
	}
	expired := signResourceDelegationForTest(t, server, claims)
	if _, err := server.verifyResourceDelegation(expired); err == nil {
		t.Fatal("expired delegation was accepted")
	}

	claims.ID = "wrong-agent-delegation"
	claims.AgentID = "agent-b"
	claims.ExpiresAt = time.Now().Add(time.Minute).Unix()
	wrongAgent := signResourceDelegationForTest(t, server, claims)
	if _, err := server.contextForProviderCallback(context.Background(), callbackRequest(wrongAgent), "get_host_info"); err == nil {
		t.Fatal("delegation issued for another agent was accepted")
	}
}

func TestProviderCallbackTaskBridgePreservesParentAdmissionOwner(t *testing.T) {
	root := t.TempDir()
	config := resource.Config{
		LockDir: filepath.Join(root, "locks"), MaxNormal: 1, MaxHeavy: 1, MaxQueued: 2,
		DiskPaths: []string{root}, PolicyRevision: resource.HostResourcePolicyRevision,
		EnforcementMode: resource.EnforcementEnforced, CPUCapacityCores: 64,
		MemoryCapacityBytes: 1 << 50, TaskCapacity: 1 << 20, ReservationTTL: time.Hour,
		TenantID: "local", ReconcilePolicy: func(_ context.Context, target resourceid.URI) error {
			if target.ResourceType != resourceid.TypeHostService {
				t.Fatalf("unexpected reconciliation target %q", target.String())
			}
			return nil
		},
	}
	coordinator, err := resource.NewCoordinator(config)
	if err != nil {
		t.Fatalf("NewCoordinator: %v", err)
	}

	registry := tasks.NewRegistry()
	parentTask := registry.Create("run_host_local_recipe", nil, time.Hour, "running", nil)
	server := &Server{
		agent:                 hostagent.New(hostagent.Options{AgentID: "agent-a"}),
		tasks:                 registry,
		admission:             coordinator,
		toolDefs:              []tools.ToolDefinition{{Name: "apply_manifest"}},
		resourceDelegationKey: []byte("test-resource-delegation-key"),
		resourceDelegations:   make(map[string]string),
	}
	parent, err := coordinator.Admit(context.Background(), resource.AdmissionRequest{
		Class: resource.ClassHeavy, CPUCores: 2, MemoryBytes: 2 << 30, Tasks: 8,
		Operation: "run_host_local_recipe", OperationID: "run_host_local_recipe",
		AgentID: "agent-a", TaskID: parentTask.TaskID,
	})
	if err != nil {
		t.Fatalf("admit parent reservation: %v", err)
	}
	t.Cleanup(func() { _ = coordinator.Release(parent) })

	parentCtx := resource.WithReservation(context.Background(), parent)
	parentCtx = resource.WithOperationIdentity(parentCtx, "run_host_local_recipe", parentTask.TaskID)
	delegatedCtx, release, err := server.withProviderResourceDelegation(parentCtx, "provider.example", "generation-a", "tunnel.ensure")
	if err != nil {
		t.Fatalf("issue callback delegation: %v", err)
	}
	defer release()
	callbackCtx, err := server.contextForProviderCallback(context.Background(), callbackRequest(mcphttp.ResourceDelegationFromContext(delegatedCtx)), "apply_manifest")
	if err != nil {
		t.Fatalf("verify callback delegation: %v", err)
	}
	owner, err := server.providerCallbackTaskOwnerFromContext(callbackCtx)
	if err != nil {
		t.Fatalf("validate callback task owner: %v", err)
	}
	if owner == nil || owner.reservation.ID != parent.ID || owner.operation != "run_host_local_recipe" || owner.taskID != parentTask.TaskID {
		t.Fatalf("callback owner = %#v, want reservation %q and task %q", owner, parent.ID, parentTask.TaskID)
	}

	childTaskID := "bridged-child-task"
	type untrustedContextKey struct{}
	untrustedCallbackCtx := context.WithValue(callbackCtx, untrustedContextKey{}, "do not copy")
	if untrustedCallbackCtx.Value(untrustedContextKey{}) != "do not copy" {
		t.Fatal("test setup did not attach its untrusted callback context value")
	}
	taskCtx := asyncTaskExecutionContext(context.Background(), "apply_manifest", childTaskID, owner)
	if taskCtx.Value(untrustedContextKey{}) != nil {
		t.Fatal("async task copied an arbitrary provider callback context value")
	}
	if got, ok := resource.ReservationFromContext(taskCtx); !ok || got.ID != parent.ID {
		t.Fatalf("async task reservation = %#v, want parent %q", got, parent.ID)
	}
	operation, taskID := resource.OperationIdentityFromContext(taskCtx)
	if operation != "run_host_local_recipe" || taskID != parentTask.TaskID || taskID == childTaskID {
		t.Fatalf("async task resource owner = (%q, %q), want parent owner (%q, %q)", operation, taskID, "run_host_local_recipe", parentTask.TaskID)
	}

	descriptor := tools.CapabilityDescriptor{Name: "apply_manifest", ResourceCost: &tools.ResourceCost{
		Class: string(resource.ClassNormal), CPUCores: 0.25, MemoryBytes: 256 << 20, Tasks: 1,
	}}
	inherited, err := server.admitInvocationWithDescriptor(taskCtx, "apply_manifest", map[string]any{}, tools.ExecutionBinding{}, descriptor, true, false)
	if err != nil {
		t.Fatalf("normal child should reuse the heavy parent reservation: %v", err)
	}
	if inherited.ID != parent.ID || inherited.Request.ParentReservationID != parent.ID || inherited.Request.TaskID != parentTask.TaskID {
		t.Fatalf("nested admission = %#v, want inherited parent reservation %q and owner task %q", inherited, parent.ID, parentTask.TaskID)
	}

	ordinaryTaskCtx := asyncTaskExecutionContext(context.Background(), "apply_manifest", childTaskID, nil)
	ordinaryOperation, ordinaryTaskID := resource.OperationIdentityFromContext(ordinaryTaskCtx)
	if ordinaryOperation != "apply_manifest" || ordinaryTaskID != childTaskID {
		t.Fatalf("ordinary task owner = (%q, %q), want its own identity", ordinaryOperation, ordinaryTaskID)
	}
	if _, err := server.admitInvocationWithDescriptor(ordinaryTaskCtx, "apply_manifest", map[string]any{}, tools.ExecutionBinding{}, descriptor, true, false); err == nil {
		t.Fatal("ordinary child without a verified parent unexpectedly bypassed saturated capacity")
	} else {
		var admissionErr *resource.AdmissionError
		if !errors.As(err, &admissionErr) || admissionErr.Code != "host_capacity_saturated" {
			t.Fatalf("ordinary child error = %T %v, want host_capacity_saturated", err, err)
		}
	}
}

func TestProviderCallbackTaskOwnerRejectsMismatchedAndInactiveParents(t *testing.T) {
	registry := tasks.NewRegistry()
	active := registry.Create("run_host_local_recipe", nil, time.Hour, "running", nil)
	otherActive := registry.Create("run_host_local_recipe", nil, time.Hour, "running", nil)
	inactive := registry.Create("run_host_local_recipe", nil, time.Hour, "running", nil)
	registry.Complete(inactive.TaskID, tasks.ToolResult{})
	server := &Server{agent: hostagent.New(hostagent.Options{AgentID: "agent-a"}), tasks: registry}

	newParent := func(taskID string, expiresAt time.Time) *resource.Reservation {
		return &resource.Reservation{ID: "reservation-parent", Request: resource.AdmissionRequest{
			AgentID: "agent-a", OperationID: "run_host_local_recipe", TaskID: taskID,
		}, ExpiresAt: expiresAt}
	}
	validContext := func(reservation *resource.Reservation, operation, taskID string) context.Context {
		return resource.WithOperationIdentity(resource.WithReservation(context.Background(), reservation), operation, taskID)
	}

	if _, err := server.providerCallbackTaskOwnerFromContext(validContext(newParent(active.TaskID, time.Now().Add(time.Hour)), "other-operation", active.TaskID)); err == nil {
		t.Fatal("mismatched operation owner was accepted")
	}
	if _, err := server.providerCallbackTaskOwnerFromContext(validContext(newParent(active.TaskID, time.Now().Add(time.Hour)), "run_host_local_recipe", otherActive.TaskID)); err == nil {
		t.Fatal("reservation and context with different active task owners were accepted")
	}
	if _, err := server.providerCallbackTaskOwnerFromContext(validContext(newParent(inactive.TaskID, time.Now().Add(time.Hour)), "run_host_local_recipe", inactive.TaskID)); err == nil {
		t.Fatal("inactive parent task was accepted")
	}
	if _, err := server.providerCallbackTaskOwnerFromContext(validContext(newParent(active.TaskID, time.Now().Add(-time.Minute)), "run_host_local_recipe", active.TaskID)); err == nil {
		t.Fatal("expired parent reservation was accepted")
	}
}

type callbackTaskObservation struct {
	reservationID string
	operation     string
	taskID        string
}

type callbackTaskCaptureCapability struct {
	descriptor tools.CapabilityDescriptor
	observed   chan callbackTaskObservation
}

func (c *callbackTaskCaptureCapability) Definition() tools.CapabilityDescriptor {
	return c.descriptor
}

func (c *callbackTaskCaptureCapability) Invoke(ctx context.Context, _ hostcapability.RawArguments, _ tools.ExecutionBinding, _ hostcapability.ExecutionSink) (*mcp.CallToolResult, error) {
	reservation, _ := resource.ReservationFromContext(ctx)
	operation, taskID := resource.OperationIdentityFromContext(ctx)
	observation := callbackTaskObservation{operation: operation, taskID: taskID}
	if reservation != nil {
		observation.reservationID = reservation.ID
	}
	c.observed <- observation
	return structuredResult(map[string]any{"ok": true}, ""), nil
}

func (c *callbackTaskCaptureCapability) ValidateResult(_ context.Context, result *mcp.CallToolResult) (hostcapability.CapabilityObservation, error) {
	return hostcapability.PassThroughObservation(c.descriptor, result)
}

func TestProviderCallbackTaskBridgePreservesOwnerThroughHandleToolCall(t *testing.T) {
	server, _ := newBindingTestServer(t)

	root := t.TempDir()
	config := resource.Config{
		LockDir: filepath.Join(root, "locks"), MaxNormal: 1, MaxHeavy: 1, MaxQueued: 2,
		DiskPaths: []string{root}, PolicyRevision: resource.HostResourcePolicyRevision,
		EnforcementMode: resource.EnforcementEnforced, CPUCapacityCores: 64,
		MemoryCapacityBytes: 1 << 50, TaskCapacity: 1 << 20, ReservationTTL: time.Hour,
		TenantID: "local", ReconcilePolicy: func(_ context.Context, target resourceid.URI) error {
			if target.ResourceType != resourceid.TypeHostService {
				t.Fatalf("unexpected reconciliation target %q", target.String())
			}
			return nil
		},
	}
	coordinator, err := resource.NewCoordinator(config)
	if err != nil {
		t.Fatalf("NewCoordinator: %v", err)
	}
	server.admission = coordinator

	parentTask := server.tasks.Create("run_host_local_recipe", nil, time.Hour, "running", nil)
	parent, err := coordinator.Admit(context.Background(), resource.AdmissionRequest{
		Class: resource.ClassHeavy, CPUCores: 2, MemoryBytes: 2 << 30, Tasks: 8,
		Operation: "run_host_local_recipe",
		AgentID:   server.agent.AgentID(),
	})
	if err != nil {
		t.Fatalf("admit parent reservation: %v", err)
	}
	if err := coordinator.BindReservationTask(parent, "run_host_local_recipe", parentTask.TaskID); err != nil {
		t.Fatalf("bind plan reservation to its operation and task: %v", err)
	}
	t.Cleanup(func() { _ = coordinator.Release(parent) })
	parentCtx := resource.WithReservation(context.Background(), parent)
	parentCtx = resource.WithOperationIdentity(parentCtx, "run_host_local_recipe", parentTask.TaskID)
	delegatedCtx, release, err := server.withProviderResourceDelegation(parentCtx, "provider.example", "generation-a", "tunnel.ensure")
	if err != nil {
		t.Fatalf("issue callback delegation: %v", err)
	}
	defer release()
	token := mcphttp.ResourceDelegationFromContext(delegatedCtx)

	descriptor := bindingTestDescriptor("probe.callback.task-owner")
	descriptor.TaskSupport = "bridged"
	descriptor.ResourceCost = &tools.ResourceCost{Class: string(resource.ClassNormal), CPUCores: 0.25, MemoryBytes: 256 << 20, Tasks: 1}
	server.toolDefs = append(server.toolDefs, tools.ToolDefinition{Name: descriptor.Name})
	capability := &callbackTaskCaptureCapability{descriptor: descriptor, observed: make(chan callbackTaskObservation, 1)}
	if err := server.registry.AuthorizeProvider(descriptor.Provider); err != nil {
		t.Fatalf("authorize test provider: %v", err)
	}
	if err := server.RegisterCapabilityModule(capability, descriptor.Provider, descriptor.Implementation); err != nil {
		t.Fatalf("register callback capability: %v", err)
	}

	request := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{
		Arguments: json.RawMessage(`{}`),
		Meta: mcp.Meta{
			mcphttp.ResourceDelegationMetaKey: token,
			"io.modelcontextprotocol/clientCapabilities": map[string]any{
				"extensions": map[string]any{"io.modelcontextprotocol/tasks": map[string]any{}},
			},
		},
	}}
	result, err := server.handleToolCall(context.Background(), request, descriptor.Name)
	if err != nil || result == nil || result.IsError {
		t.Fatalf("handleToolCall result = %#v err=%v", result, err)
	}
	envelope, ok := result.StructuredContent.(map[string]any)
	if !ok || envelope["resultType"] != "task" {
		t.Fatalf("callback did not return a child task envelope: %#v", result.StructuredContent)
	}
	childTaskID, _ := envelope["taskId"].(string)
	if childTaskID == "" || childTaskID == parentTask.TaskID {
		t.Fatalf("child task id = %q, parent task id = %q", childTaskID, parentTask.TaskID)
	}
	select {
	case observed := <-capability.observed:
		t.Fatalf("callback capability ran before the caller acknowledged task %q: %#v", childTaskID, observed)
	default:
	}
	getParams, err := json.Marshal(map[string]any{"taskId": childTaskID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.HandleExtensionMethod("tasks/get", getParams); err != nil {
		t.Fatalf("acknowledge callback task handle: %v", err)
	}

	select {
	case observed := <-capability.observed:
		if observed.reservationID != parent.ID || observed.operation != "run_host_local_recipe" || observed.taskID != parentTask.TaskID {
			t.Fatalf("capability saw owner %#v; want parent reservation %q and owner task %q", observed, parent.ID, parentTask.TaskID)
		}
	case <-time.After(2 * time.Second):
		record, _ := server.Tasks().Get(childTaskID)
		t.Fatalf("bridged callback did not reach the capability; child task: %#v", record)
	}

	deadline := time.Now().Add(2 * time.Second)
	completed := false
	for time.Now().Before(deadline) {
		record, found := server.Tasks().Get(childTaskID)
		if found && record.Status == tasks.StatusCompleted {
			if record.ToolResult == nil || record.ToolResult.IsError {
				t.Fatalf("bridged callback completed with error: %#v", record.ToolResult)
			}
			completed = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !completed {
		record, _ := server.Tasks().Get(childTaskID)
		t.Fatalf("bridged callback task did not complete: %#v", record)
	}

	unacknowledged, err := server.handleToolCall(context.Background(), request, descriptor.Name)
	if err != nil || unacknowledged == nil || unacknowledged.IsError {
		t.Fatalf("second callback task = %#v err=%v", unacknowledged, err)
	}
	unacknowledgedEnvelope, _ := unacknowledged.StructuredContent.(map[string]any)
	unacknowledgedID, _ := unacknowledgedEnvelope["taskId"].(string)
	if unacknowledgedID == "" || unacknowledgedID == childTaskID {
		t.Fatalf("second child task id = %q, first child task id = %q", unacknowledgedID, childTaskID)
	}
	cancelParams, err := json.Marshal(map[string]any{"taskId": unacknowledgedID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.HandleExtensionMethod("tasks/cancel", cancelParams); err != nil {
		t.Fatalf("cancel unacknowledged callback task: %v", err)
	}
	if cancelled, found := server.Tasks().Get(unacknowledgedID); !found || cancelled.Status != tasks.StatusCancelled {
		t.Fatalf("unacknowledged task after cancel = %#v, found=%t", cancelled, found)
	}
	select {
	case observed := <-capability.observed:
		t.Fatalf("unacknowledged callback task ran before cancellation: %#v", observed)
	default:
	}
}

func callbackRequest(token string) *mcp.CallToolRequest {
	return &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{
		Meta: mcp.Meta{mcphttp.ResourceDelegationMetaKey: token},
	}}
}

func signResourceDelegationForTest(t *testing.T, server *Server, claims resourceDelegationClaims) string {
	t.Helper()
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	encodedPayload := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, server.resourceDelegationKey)
	_, _ = mac.Write([]byte(encodedPayload))
	token := encodedPayload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	server.resourceDelegationMu.Lock()
	server.resourceDelegations[claims.ID] = token
	server.resourceDelegationMu.Unlock()
	return token
}
