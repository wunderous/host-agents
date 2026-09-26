package hostmcp

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wunderous/host-agents/internal/mcphttp"
	"github.com/wunderous/host-agents/internal/resource"
)

const (
	resourceDelegationVersion = "host-resource-delegation.v1"
	resourceDelegationTTL     = time.Hour
)

type resourceDelegationClaims struct {
	Version            string
	ID                 string
	ReservationID      string
	AgentID            string
	TaskID             string
	TaskOperation      string
	ProviderID         string
	ProviderGeneration string
	ProviderOperation  string
	ExpiresAt          int64
}

func newResourceDelegationKey() ([]byte, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	return key, nil
}

// withProviderResourceDelegation signs a short-lived context for one active
// provider operation. It carries only an existing reservation and task owner;
// callback calls still use normal Host Agent authentication, authorization,
// schema validation, canonical resource resolution, and dispatch.
func (s *Server) withProviderResourceDelegation(ctx context.Context, providerID, generationID, operationID string) (context.Context, func(), error) {
	reservation, ok := resource.ReservationFromContext(ctx)
	if !ok || reservation.ID == "" || reservation.ID == "control" || reservation.ID == "unmanaged" {
		return ctx, nil, nil
	}
	taskOperation, taskID := resource.OperationIdentityFromContext(ctx)
	if taskID == "" || reservation.Request.TaskID != taskID {
		return nil, nil, fmt.Errorf("host_resource_delegation_owner_invalid: provider callback reservation is not bound to the active durable task")
	}
	agentID := strings.TrimSpace(s.agent.AgentID())
	if agentID == "" || reservation.Request.AgentID != agentID {
		return nil, nil, fmt.Errorf("host_resource_delegation_owner_invalid: provider callback reservation does not belong to this Host Agent")
	}
	if !s.tasks.IsWorking(taskID) {
		return nil, nil, fmt.Errorf("host_resource_delegation_task_inactive: the provider callback task is not active")
	}
	if strings.TrimSpace(providerID) == "" || strings.TrimSpace(generationID) == "" || strings.TrimSpace(operationID) == "" || strings.TrimSpace(taskOperation) == "" {
		return nil, nil, fmt.Errorf("host_resource_delegation_context_invalid: provider and task identities are required")
	}

	claims := resourceDelegationClaims{
		Version:            resourceDelegationVersion,
		ID:                 uuid.NewString(),
		ReservationID:      reservation.ID,
		AgentID:            agentID,
		TaskID:             taskID,
		TaskOperation:      taskOperation,
		ProviderID:         providerID,
		ProviderGeneration: generationID,
		ProviderOperation:  operationID,
		ExpiresAt:          time.Now().Add(resourceDelegationTTL).Unix(),
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return nil, nil, fmt.Errorf("encode provider callback delegation: %w", err)
	}
	encodedPayload := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, s.resourceDelegationKey)
	_, _ = mac.Write([]byte(encodedPayload))
	token := encodedPayload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	s.resourceDelegationMu.Lock()
	s.resourceDelegations[claims.ID] = token
	s.resourceDelegationMu.Unlock()
	delegatedCtx := mcphttp.WithResourceDelegation(ctx, token)
	release := func() {
		s.resourceDelegationMu.Lock()
		delete(s.resourceDelegations, claims.ID)
		s.resourceDelegationMu.Unlock()
	}
	return delegatedCtx, release, nil
}

// contextForProviderCallback verifies that request metadata was issued by this
// Host Agent for an active provider call and attaches the live reservation to
// the ordinary dispatch context. It never authorizes a tool call itself.
func (s *Server) contextForProviderCallback(ctx context.Context, request *mcp.CallToolRequest, toolName string) (context.Context, error) {
	if request == nil || request.Params == nil {
		return ctx, nil
	}
	token := mcphttp.ResourceDelegationFromMeta(request.Params.Meta)
	if token == "" {
		return ctx, nil
	}
	claims, err := s.verifyResourceDelegation(token)
	if err != nil {
		return nil, err
	}
	if claims.AgentID != strings.TrimSpace(s.agent.AgentID()) || isLifecycleTool(toolName) || s.isProviderCapability(toolName) || !s.isHostAgentTool(toolName) {
		return nil, fmt.Errorf("host_resource_delegation_scope_invalid: callback is outside the signed Host Agent execution scope")
	}
	if !s.tasks.IsWorking(claims.TaskID) {
		return nil, fmt.Errorf("host_resource_delegation_task_inactive: the provider callback task is not active")
	}
	parent := &resource.Reservation{
		ID: claims.ReservationID,
		Request: resource.AdmissionRequest{
			AgentID: claims.AgentID, TaskID: claims.TaskID, OperationID: claims.TaskOperation,
		},
		CreatedAt: time.Now().UTC(),
		ExpiresAt: time.Unix(claims.ExpiresAt, 0).UTC(),
	}
	ctx = resource.WithReservation(ctx, parent)
	return resource.WithOperationIdentity(ctx, claims.TaskOperation, claims.TaskID), nil
}

func (s *Server) verifyResourceDelegation(token string) (resourceDelegationClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 || len(s.resourceDelegationKey) == 0 {
		return resourceDelegationClaims{}, fmt.Errorf("host_resource_delegation_invalid: provider callback delegation is malformed")
	}
	mac := hmac.New(sha256.New, s.resourceDelegationKey)
	_, _ = mac.Write([]byte(parts[0]))
	suppliedMAC, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || !hmac.Equal(suppliedMAC, mac.Sum(nil)) {
		return resourceDelegationClaims{}, fmt.Errorf("host_resource_delegation_invalid: provider callback delegation signature is invalid")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return resourceDelegationClaims{}, fmt.Errorf("host_resource_delegation_invalid: provider callback delegation is malformed")
	}
	var claims resourceDelegationClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return resourceDelegationClaims{}, fmt.Errorf("host_resource_delegation_invalid: provider callback delegation is malformed")
	}
	if claims.Version != resourceDelegationVersion || claims.ID == "" || claims.ReservationID == "" || claims.AgentID == "" || claims.TaskID == "" ||
		claims.TaskOperation == "" || claims.ProviderID == "" || claims.ProviderGeneration == "" || claims.ProviderOperation == "" ||
		time.Now().Unix() >= claims.ExpiresAt {
		return resourceDelegationClaims{}, fmt.Errorf("host_resource_delegation_expired: provider callback delegation is expired or incomplete")
	}
	s.resourceDelegationMu.Lock()
	activeToken, active := s.resourceDelegations[claims.ID]
	s.resourceDelegationMu.Unlock()
	if !active || activeToken != token {
		return resourceDelegationClaims{}, fmt.Errorf("host_resource_delegation_inactive: provider callback delegation is no longer active")
	}
	return claims, nil
}

func (s *Server) isHostAgentTool(name string) bool {
	for _, definition := range s.toolDefs {
		if definition.Name == name {
			return true
		}
	}
	return false
}
