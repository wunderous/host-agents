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
	return s.handleRunHostPlanWithMetadata(args, nil, "run_host_plan", "Executing host plan...")
}

func (s *Server) handleRunHostPlanWithMetadata(args map[string]any, recipeMetadata map[string]any, taskName, taskDescription string) (*mcp.CallToolResult, error) {
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
	if recipeMetadata != nil {
		redactedPlan = redactPlanDocumentForRecipe(encoded, recipeMetadata)
	}
	resume, _ := args["resume"].(bool)

	record, found, err := s.state.FindPlan(doc.PlanID, doc.Generation, doc.IdempotencyKey)
	if err != nil {
		return tools.ErrorResult(fmt.Errorf("find plan run: %w", err)), nil
	}
	if found {
		if record.DocumentHash != hash {
			return tools.ErrorResult(fmt.Errorf("idempotency key already belongs to a different plan document: %s", record.RunID)), nil
		}
		if recipeMetadata != nil {
			if storedHash := persistedRecipeHash(record.RecipeJSON); storedHash != "" && storedHash != recipeHash(recipeMetadata) {
				return tools.ErrorResult(fmt.Errorf("idempotency key already belongs to a different runtime recipe: %s", record.RunID)), nil
			}
		}
		if record.Status == "working" || record.Status == "running" {
			return s.planRunResult(record), nil
		}
		if !resume {
			if recipeMetadata != nil && recipeBoolField(recipeMetadata, "activate") && record.Status == "completed" {
				record, err = s.ensureRecipeActivation(record, recipeMetadata)
				if err != nil {
					return tools.ErrorResult(err), nil
				}
				if err := s.activateCompletedProviderCandidate(recipeMetadata); err != nil {
					return tools.ErrorResult(err), nil
				}
			}
			return s.planRunResult(record), nil
		}
		if record.CatalogRevision != "" && record.CatalogRevision != snapshot.Revision {
			if err := s.state.UpdatePlanCatalogRevision(record.RunID, snapshot.Revision); err != nil {
				return tools.ErrorResult(fmt.Errorf("update resumed plan catalog revision: %w", err)), nil
			}
			record.CatalogRevision = snapshot.Revision
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
			RecipeJSON:      redactedMetadata(recipeMetadata),
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
				if !resume && recipeMetadata != nil && recipeBoolField(recipeMetadata, "activate") && record.Status == "completed" {
					record, err = s.ensureRecipeActivation(record, recipeMetadata)
					if err != nil {
						return tools.ErrorResult(err), nil
					}
					if err := s.activateCompletedProviderCandidate(recipeMetadata); err != nil {
						return tools.ErrorResult(err), nil
					}
				}
				return s.planRunResult(record), nil
			}
			if record.CatalogRevision != "" && record.CatalogRevision != snapshot.Revision {
				if err := s.state.UpdatePlanCatalogRevision(record.RunID, snapshot.Revision); err != nil {
					return tools.ErrorResult(fmt.Errorf("update resumed plan catalog revision: %w", err)), nil
				}
				record.CatalogRevision = snapshot.Revision
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
	taskArgs := map[string]any{"plan": redactTaskValue(args["plan"]), "resume": resume}
	if recipeMetadata != nil {
		taskArgs["recipe"] = redactTaskValue(recipeMetadata)
	}
	rec := s.tasks.CreateWithID(record.RunID, taskName, taskArgs, time.Hour, taskDescription, map[string]any{
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
	go s.executeHostPlan(taskCtx, cancel, rec.TaskID, doc, stateValue, snapshot, recipeMetadata)
	return s.planRunResult(record), nil
}

func initialPlanState(runID string, doc plan.Document) plan.RunState {
	nodes := make(map[string]plan.NodeRunState, len(doc.Nodes))
	for _, node := range doc.Nodes {
		nodes[node.ID] = plan.NodeRunState{ID: node.ID, Status: plan.StatusPending}
	}
	return plan.RunState{RunID: runID, PlanID: doc.PlanID, Generation: doc.Generation, Status: "pending", Nodes: nodes, Outputs: map[string]any{}}
}

func (s *Server) executeHostPlan(ctx context.Context, cancel context.CancelFunc, runID string, doc plan.Document, stateValue plan.RunState, snapshot tools.CapabilityCatalogSnapshot, recipeMetadata map[string]any) {
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
	if recipeMetadata != nil {
		applyRecipeOutputMapping(&final, recipeMetadata)
	}
	if runErr == nil && status == "completed" && recipeBoolField(recipeMetadata, "providerTeardown") {
		if err := s.completeProviderTeardown(recipeMetadata); err != nil {
			status = "failed"
			final.Status = status
			final.Error = err.Error()
			runErr = err
		}
	}
	var activeRuntime state.ActiveRuntimeRecord
	if runErr == nil && status == "completed" && recipeMetadata != nil && recipeBoolField(recipeMetadata, "activate") {
		var observation map[string]any
		var activationErr error
		activeRuntime, observation, activationErr = s.validateRecipeActivation(ctx, runID, recipeMetadata)
		if observation != nil {
			if final.Outputs == nil {
				final.Outputs = map[string]any{}
			}
			final.Outputs["activation"] = observation
		}
		if activationErr != nil {
			status = "failed"
			final.Status = status
			final.Error = activationErr.Error()
			runErr = activationErr
		}
		if activationErr == nil {
			if err := s.activateProviderGeneration(recipeMetadata); err != nil {
				status = "failed"
				final.Status = status
				final.Error = err.Error()
				runErr = err
			}
		}
	}
	if recipeMetadata != nil {
		generationID := recipeStringField(recipeMetadata, "providerGenerationId")
		if generationID != "" && !recipeBoolField(recipeMetadata, "providerTeardown") {
			if runErr != nil || status != "completed" {
				s.cleanupProviderCandidate(generationID, recipeStringField(recipeMetadata, "providerId"))
			} else {
				s.completeProviderCandidate(generationID)
			}
		}
	}
	if runErr == nil && status == "completed" && activeRuntime.Capability != "" {
		if err := s.state.CompletePlanWithActiveCapability(runID, marshalPlanState(final), activeRuntime); err != nil {
			status = "failed"
			final.Status = status
			final.Error = fmt.Sprintf("commit active runtime: %v", err)
			_ = s.state.UpdatePlan(runID, status, marshalPlanState(final), final.Error)
			runErr = err
		}
	} else {
		_ = s.state.UpdatePlan(runID, status, marshalPlanState(final), final.Error)
	}
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
	if record.RecipeJSON != "" {
		var recipeMetadata any
		if json.Unmarshal([]byte(record.RecipeJSON), &recipeMetadata) == nil {
			result["recipe"] = recipeMetadata
			if metadata, ok := recipeMetadata.(map[string]any); ok && s.state != nil {
				if activation, ok := metadata["activation"].(map[string]any); ok {
					if capability, ok := activation["capability"].(string); ok && strings.TrimSpace(capability) != "" {
						if active, found, err := s.state.GetActiveRuntime(capability); err == nil && found && active.RunID == record.RunID {
							activeResult := map[string]any{
								"capability":      active.Capability,
								"servingContract": active.ServingContract,
								"runtime":         active.Runtime,
								"recipeId":        active.RecipeID,
								"recipeVersion":   active.RecipeVersion,
								"recipeHash":      active.RecipeHash,
								"runId":           active.RunID,
								"activatedAt":     active.ActivatedAt,
							}
							var bindings any
							if json.Unmarshal([]byte(active.InputBindingsJSON), &bindings) == nil {
								activeResult["inputBindings"] = bindings
							}
							var observation any
							if json.Unmarshal([]byte(active.ObservationJSON), &observation) == nil {
								activeResult["observation"] = observation
							}
							if recipeStringField(metadata, "recipeKind") == "tunnel" {
								result["activeCapability"] = activeResult
							} else {
								result["activeRuntime"] = activeResult
							}
						}
					}
				}
			}
		}
	}
	return structuredResult(result, "")
}

func redactedMetadata(value map[string]any) string {
	if value == nil {
		return ""
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return `{"redacted":true}`
	}
	var generic any
	if err := json.Unmarshal(encoded, &generic); err != nil {
		return `{"redacted":true}`
	}
	redactRecipeMetadataSecrets(generic)
	redacted, err := json.Marshal(redactTaskValue(generic))
	if err != nil {
		return `{"redacted":true}`
	}
	return string(redacted)
}

func applyRecipeOutputMapping(stateValue *plan.RunState, metadata map[string]any) {
	mapping, ok := metadata["outputMapping"].(map[string]string)
	if !ok || len(mapping) == 0 {
		if generic, ok := metadata["outputMapping"].(map[string]any); ok {
			mapping = make(map[string]string, len(generic))
			for key, value := range generic {
				if path, ok := value.(string); ok {
					mapping[key] = path
				}
			}
		}
	}
	if len(mapping) == 0 {
		return
	}
	encoded, err := json.Marshal(stateValue)
	if err != nil {
		return
	}
	var document any
	if json.Unmarshal(encoded, &document) != nil {
		return
	}
	observation := make(map[string]any, len(mapping))
	for target, path := range mapping {
		if value, ok := recipeJSONPath(document, path); ok {
			observation[target] = value
		}
	}
	if len(observation) > 0 {
		if stateValue.Outputs == nil {
			stateValue.Outputs = map[string]any{}
		}
		stateValue.Outputs["recipeObservation"] = observation
	}
}

func recipeJSONPath(document any, path string) (any, bool) {
	current := document
	for _, part := range strings.Split(strings.Trim(path, "."), ".") {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = object[part]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

func recipeHash(metadata map[string]any) string {
	value, _ := metadata["recipeHash"].(string)
	return strings.TrimSpace(value)
}

func persistedRecipeHash(encoded string) string {
	if strings.TrimSpace(encoded) == "" {
		return ""
	}
	var metadata map[string]any
	if json.Unmarshal([]byte(encoded), &metadata) != nil {
		return ""
	}
	return recipeHash(metadata)
}

func redactRecipeMetadataSecrets(value any) {
	metadata, ok := value.(map[string]any)
	if !ok {
		return
	}
	secretNames, _ := metadata["secretInputs"].([]any)
	expandedPlan, _ := metadata["expandedPlan"].(map[string]any)
	variables, _ := expandedPlan["variables"].(map[string]any)
	inputs, _ := variables["inputs"].(map[string]any)
	for _, rawName := range secretNames {
		name, ok := rawName.(string)
		if ok && inputs != nil {
			inputs[name] = "[redacted]"
		}
	}
}

func redactPlanDocumentForRecipe(encoded []byte, metadata map[string]any) string {
	var document map[string]any
	if json.Unmarshal(encoded, &document) != nil {
		return `{"redacted":true}`
	}
	variables, _ := document["variables"].(map[string]any)
	inputs, _ := variables["inputs"].(map[string]any)
	secretNames, _ := metadata["secretInputs"].([]string)
	if len(secretNames) == 0 {
		if generic, ok := metadata["secretInputs"].([]any); ok {
			for _, value := range generic {
				if name, ok := value.(string); ok {
					secretNames = append(secretNames, name)
				}
			}
		}
	}
	for _, name := range secretNames {
		if inputs != nil {
			inputs[name] = "[redacted]"
		}
	}
	redacted, err := json.Marshal(redactTaskValue(document))
	if err != nil {
		return `{"redacted":true}`
	}
	return string(redacted)
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
