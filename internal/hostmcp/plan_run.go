package hostmcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wunderous/host-agents/internal/plan"
	"github.com/wunderous/host-agents/internal/state"
	"github.com/wunderous/host-agents/internal/tasks"
	"github.com/wunderous/host-agents/internal/tools"
)

func (s *Server) planCapabilities() map[string]plan.Capability {
	snapshot := s.CatalogSnapshot()
	return planCapabilitiesFromSnapshot(snapshot)
}

func planCapabilitiesFromSnapshot(snapshot tools.CapabilityCatalogSnapshot) map[string]plan.Capability {
	capabilities := make(map[string]plan.Capability, len(snapshot.Tools))
	for _, descriptor := range snapshot.Tools {
		capabilities[descriptor.Name] = plan.Capability{
			Name:         descriptor.Name,
			InputSchema:  descriptor.InputSchema,
			OutputSchema: descriptor.OutputSchema,
			Effect:       descriptor.Effect,
			Idempotent:   descriptor.Idempotent,
		}
	}
	return capabilities
}

func decodePlanArgument(args map[string]any) (plan.Document, error) {
	raw, ok := args["plan"]
	if !ok || raw == nil {
		return plan.Document{}, fmt.Errorf("plan is required")
	}
	doc, err := plan.Decode(raw)
	if err != nil {
		return plan.Document{}, err
	}
	return doc, nil
}

func (s *Server) validateHostPlan(doc plan.Document) (map[string]any, error) {
	snapshot := s.CatalogSnapshot()
	return s.validateHostPlanWithSnapshot(doc, snapshot)
}

func (s *Server) validateHostPlanWithSnapshot(doc plan.Document, snapshot tools.CapabilityCatalogSnapshot) (map[string]any, error) {
	if err := plan.Validate(doc, planCapabilitiesFromSnapshot(snapshot), snapshot.Revision); err != nil {
		return nil, err
	}
	hash, _, err := plan.DocumentHash(doc)
	if err != nil {
		return nil, fmt.Errorf("hash plan: %w", err)
	}
	levels, err := plan.TopologicalLevels(doc)
	if err != nil {
		return nil, err
	}
	levelIDs := make([][]string, 0, len(levels))
	for _, level := range levels {
		ids := make([]string, 0, len(level))
		for _, node := range level {
			ids = append(ids, node.ID)
		}
		levelIDs = append(levelIDs, ids)
	}
	return map[string]any{
		"valid":           true,
		"contractVersion": doc.ContractVersion,
		"planId":          doc.PlanID,
		"generation":      doc.Generation,
		"idempotencyKey":  doc.IdempotencyKey,
		"documentHash":    hash,
		"catalogRevision": snapshot.Revision,
		"nodeCount":       len(doc.Nodes),
		"levels":          levelIDs,
	}, nil
}

func (s *Server) handleValidateHostPlan(args map[string]any) (*mcp.CallToolResult, error) {
	doc, err := decodePlanArgument(args)
	if err != nil {
		return tools.ErrorResult(err), nil
	}
	result, err := s.validateHostPlan(doc)
	if err != nil {
		return tools.ErrorResult(err), nil
	}
	return structuredResult(result, "host plan is valid"), nil
}

func (s *Server) handleRunHostPlan(args map[string]any) (*mcp.CallToolResult, error) {
	s.planMu.Lock()
	if s.closed || s.state == nil {
		s.planMu.Unlock()
		return tools.ErrorResult(fmt.Errorf("durable plan state is unavailable; configure a state directory")), nil
	}
	s.planWG.Add(1)
	s.planMu.Unlock()
	launched := false
	defer func() {
		if !launched {
			s.planWG.Done()
		}
	}()
	doc, err := decodePlanArgument(args)
	if err != nil {
		return tools.ErrorResult(err), nil
	}
	snapshot := s.CatalogSnapshot()
	if _, err := s.validateHostPlanWithSnapshot(doc, snapshot); err != nil {
		return tools.ErrorResult(err), nil
	}
	hash, encoded, err := plan.DocumentHash(doc)
	if err != nil {
		return tools.ErrorResult(err), nil
	}
	redactedPlan := redactPlanDocument(encoded)
	resume, _ := args["resume"].(bool)

	record, found, err := s.state.FindPlan(doc.PlanID, doc.Generation, doc.IdempotencyKey)
	if err != nil {
		return tools.ErrorResult(fmt.Errorf("find plan run: %w", err)), nil
	}
	if found {
		if record.DocumentHash != hash {
			return tools.ErrorResult(fmt.Errorf("idempotency key already belongs to a different plan document: %s", record.RunID)), nil
		}
		if record.Status == "working" || record.Status == "running" {
			return s.planRunResult(record), nil
		}
		if !resume {
			return s.planRunResult(record), nil
		}
		if record.CatalogRevision != "" && record.CatalogRevision != snapshot.Revision {
			return tools.ErrorResult(fmt.Errorf("cannot resume plan %s against catalog revision %s; recorded revision is %s", record.RunID, snapshot.Revision, record.CatalogRevision)), nil
		}
	} else {
		record = state.PlanRecord{
			RunID:           uuid.NewString(),
			PlanID:          doc.PlanID,
			Generation:      doc.Generation,
			IdempotencyKey:  doc.IdempotencyKey,
			DocumentHash:    hash,
			CatalogRevision: snapshot.Revision,
			Status:          "working",
			PlanJSON:        redactedPlan,
			StateJSON:       marshalPlanState(initialPlanState("", doc)),
		}
		var created bool
		record, created, err = s.state.CreatePlan(record)
		if err != nil {
			return tools.ErrorResult(fmt.Errorf("persist plan run: %w", err)), nil
		}
		if record.DocumentHash != hash {
			return tools.ErrorResult(fmt.Errorf("idempotency key already belongs to a different plan document: %s", record.RunID)), nil
		}
		if !created {
			if record.Status == "working" || record.Status == "running" || !resume {
				return s.planRunResult(record), nil
			}
			if record.CatalogRevision != "" && record.CatalogRevision != snapshot.Revision {
				return tools.ErrorResult(fmt.Errorf("cannot resume plan %s against catalog revision %s; recorded revision is %s", record.RunID, snapshot.Revision, record.CatalogRevision)), nil
			}
		}
	}

	if existing, ok := s.tasks.Get(record.RunID); ok && existing.Status == tasks.StatusWorking {
		return s.planRunResult(record), nil
	}
	stateValue := initialPlanState(record.RunID, doc)
	if strings.TrimSpace(record.StateJSON) != "" {
		if err := json.Unmarshal([]byte(record.StateJSON), &stateValue); err != nil {
			return tools.ErrorResult(fmt.Errorf("decode persisted plan state: %w", err)), nil
		}
	}
	stateValue.RunID = record.RunID
	stateValue.PlanID = doc.PlanID
	stateValue.Generation = doc.Generation
	stateValue.Status = "running"
	if err := s.state.UpdatePlan(record.RunID, "running", marshalPlanState(stateValue), ""); err != nil {
		return tools.ErrorResult(fmt.Errorf("start plan run: %w", err)), nil
	}

	taskCtx, cancel := context.WithCancel(context.Background())
	rec := s.tasks.CreateWithID(record.RunID, "run_host_plan", map[string]any{"plan": redactTaskValue(args["plan"]), "resume": resume}, time.Hour, "Executing host plan...", map[string]any{
		"planId":          doc.PlanID,
		"generation":      doc.Generation,
		"catalogRevision": snapshot.Revision,
	}, cancel)
	s.planMu.Lock()
	if s.closed {
		s.planMu.Unlock()
		cancel()
		s.tasks.Cancel(rec.TaskID)
		stateValue.Status = "unknown"
		stateValue.Error = "host plan server is closing"
		_ = s.state.UpdatePlan(record.RunID, "unknown", marshalPlanState(stateValue), stateValue.Error)
		return tools.ErrorResult(fmt.Errorf("host plan server is closing")), nil
	}
	s.planCancels[record.RunID] = cancel
	s.planMu.Unlock()
	launched = true
	go s.executeHostPlan(taskCtx, cancel, rec.TaskID, doc, stateValue, snapshot)
	return s.planRunResult(record), nil
}

func initialPlanState(runID string, doc plan.Document) plan.RunState {
	nodes := make(map[string]plan.NodeRunState, len(doc.Nodes))
	for _, node := range doc.Nodes {
		nodes[node.ID] = plan.NodeRunState{ID: node.ID, Status: plan.StatusPending}
	}
	return plan.RunState{RunID: runID, PlanID: doc.PlanID, Generation: doc.Generation, Status: "pending", Nodes: nodes, Outputs: map[string]any{}}
}

func (s *Server) executeHostPlan(ctx context.Context, cancel context.CancelFunc, runID string, doc plan.Document, stateValue plan.RunState, snapshot tools.CapabilityCatalogSnapshot) {
	defer s.planWG.Done()
	defer cancel()
	defer func() {
		s.planMu.Lock()
		delete(s.planCancels, runID)
		s.planMu.Unlock()
	}()
	runner := plan.Runner{
		Dispatch: func(ctx context.Context, name string, args map[string]any, onData func(string)) (*mcp.CallToolResult, error) {
			return s.DispatchTool(ctx, name, args, onData)
		},
		Capabilities:    planCapabilitiesFromSnapshot(snapshot),
		CatalogRevision: snapshot.Revision,
		Sink: func(value plan.RunState) error {
			return s.state.UpdatePlan(runID, value.Status, marshalPlanState(value), value.Error)
		},
	}
	final, runErr := runner.Run(ctx, doc, stateValue)
	status := final.Status
	cancelled := false
	if rec, ok := s.tasks.Get(runID); ok && rec.Status == tasks.StatusCancelled {
		cancelled = true
		status = "cancelled"
		final.Error = "cancelled by request"
	}
	if runErr != nil && ctx.Err() != nil {
		status = "unknown"
		if cancelled {
			status = "cancelled"
		}
	}
	if status == "" {
		status = "failed"
	}
	_ = s.state.UpdatePlan(runID, status, marshalPlanState(final), final.Error)
	if cancelled {
		return
	}
	if runErr != nil {
		s.tasks.Fail(runID, runErr.Error())
		return
	}
	s.tasks.Complete(runID, tasks.ToolResult{StructuredContent: planRunStateResult(final)})
}

func (s *Server) handleGetHostPlanRun(args map[string]any) (*mcp.CallToolResult, error) {
	runID, _ := args["runId"].(string)
	runID = strings.TrimSpace(runID)
	if runID == "" || s.state == nil {
		return tools.ErrorResult(fmt.Errorf("runId and durable plan state are required")), nil
	}
	record, found, err := s.state.GetPlan(runID)
	if err != nil {
		return tools.ErrorResult(fmt.Errorf("get plan run: %w", err)), nil
	}
	if !found {
		return tools.ErrorResult(fmt.Errorf("host plan run not found: %s", runID)), nil
	}
	return s.planRunResult(record), nil
}

func (s *Server) handleGetCapabilityCatalog() (*mcp.CallToolResult, error) {
	return structuredResult(s.CatalogSnapshot(), ""), nil
}

func (s *Server) planRunResult(record state.PlanRecord) *mcp.CallToolResult {
	result := map[string]any{
		"runId":           record.RunID,
		"planId":          record.PlanID,
		"generation":      record.Generation,
		"idempotencyKey":  record.IdempotencyKey,
		"documentHash":    record.DocumentHash,
		"catalogRevision": record.CatalogRevision,
		"status":          record.Status,
		"createdAt":       record.CreatedAt,
		"updatedAt":       record.UpdatedAt,
	}
	var stateValue any
	if strings.TrimSpace(record.StateJSON) != "" && json.Unmarshal([]byte(record.StateJSON), &stateValue) == nil {
		result["state"] = stateValue
		if object, ok := stateValue.(map[string]any); ok {
			if nodes, exists := object["nodes"]; exists {
				result["nodes"] = nodes
			}
		}
	}
	if _, exists := result["nodes"]; !exists {
		result["nodes"] = map[string]any{}
	}
	if record.ErrorMessage != "" {
		result["error"] = record.ErrorMessage
	}
	return structuredResult(result, "")
}

func planRunStateResult(value plan.RunState) map[string]any {
	return map[string]any{
		"runId":      value.RunID,
		"planId":     value.PlanID,
		"generation": value.Generation,
		"status":     value.Status,
		"nodes":      value.Nodes,
		"outputs":    redactTaskValue(value.Outputs),
		"error":      value.Error,
	}
}

func marshalPlanState(value plan.RunState) string {
	encoded, err := json.Marshal(planRunStateResult(value))
	if err != nil {
		return `{"status":"unknown","error":"failed to encode plan state"}`
	}
	return string(encoded)
}

func redactPlanDocument(encoded []byte) string {
	var value any
	if json.Unmarshal(encoded, &value) != nil {
		return `{"redacted":true}`
	}
	redacted, err := json.Marshal(redactTaskValue(value))
	if err != nil {
		return `{"redacted":true}`
	}
	return string(redacted)
}

// cancelHostPlan handles both the durable plan record and the live task. The
// boolean reports whether the operation ID was a plan run.
func (s *Server) cancelHostPlan(runID string) (*mcp.CallToolResult, bool) {
	if s.state == nil {
		return nil, false
	}
	record, found, err := s.state.GetPlan(runID)
	if err != nil || !found {
		return nil, false
	}
	if record.Status == "completed" || record.Status == "failed" || record.Status == "cancelled" {
		return tools.ErrorResult(fmt.Errorf("host plan run cannot be cancelled in status %q", record.Status)), true
	}
	s.planMu.Lock()
	if cancel := s.planCancels[runID]; cancel != nil {
		cancel()
	}
	s.planMu.Unlock()
	if rec, ok := s.tasks.Get(runID); ok && rec.Status == tasks.StatusWorking {
		s.tasks.Cancel(runID)
	}
	_ = s.state.UpdatePlan(runID, "cancelled", record.StateJSON, "cancelled by request")
	updated, _, getErr := s.state.GetPlan(runID)
	if getErr == nil {
		record = updated
	}
	return s.planRunResult(record), true
}
