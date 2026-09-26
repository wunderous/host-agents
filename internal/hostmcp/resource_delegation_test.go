package hostmcp

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wunderous/host-agents/internal/hostagent"
	"github.com/wunderous/host-agents/internal/mcphttp"
	"github.com/wunderous/host-agents/internal/resource"
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
