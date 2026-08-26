package hostmcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	providercontract "github.com/wunderous/host-agents/contracts/provider"
	"github.com/wunderous/host-agents/internal/cordis"
	"github.com/wunderous/host-agents/internal/plan"
	"github.com/wunderous/host-agents/internal/recipe"
	"github.com/wunderous/host-agents/internal/state"
	"github.com/wunderous/host-agents/internal/tools"
	"github.com/wunderous/host-agents/internal/version"
)

func recipeSourceRequest(args map[string]any, requireHash bool) recipe.SourceRequest {
	return recipe.SourceRequest{
		Source:        recipeStringField(args, "source"),
		Revision:      recipeStringField(args, "revision"),
		SHA256:        recipeStringField(args, "sha256"),
		RequireSHA256: requireHash,
	}
}

func recipeStringField(args map[string]any, key string) string {
	value, _ := args[key].(string)
	return strings.TrimSpace(value)
}

func recipeBoolField(args map[string]any, key string) bool {
	value, _ := args[key].(bool)
	return value
}

func recipeInputValues(args map[string]any) (map[string]any, error) {
	if raw, ok := args["inputs"]; ok && raw != nil {
		values, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("inputs must be an object")
		}
		return values, nil
	}
	return map[string]any{}, nil
}

func (s *Server) loadRuntimeRecipe(args map[string]any, requireHash bool) (recipe.Loaded, tools.CapabilityCatalogSnapshot, error) {
	loaded, err := recipe.Load(recipeSourceRequest(args, requireHash))
	if err != nil {
		return recipe.Loaded{}, tools.CapabilityCatalogSnapshot{}, err
	}
	inputs, err := recipeInputValues(args)
	if err != nil {
		return recipe.Loaded{}, tools.CapabilityCatalogSnapshot{}, err
	}
	if err := loaded.ResolveInputs(inputs); err != nil {
		return recipe.Loaded{}, tools.CapabilityCatalogSnapshot{}, err
	}
	if err := recipe.ValidateHostAgentVersion(loaded.Document.Compatibility.MinHostAgentVersion, version.Version); err != nil {
		return recipe.Loaded{}, tools.CapabilityCatalogSnapshot{}, err
	}
	snapshot := s.CatalogSnapshot()
	if err := loaded.Validate(planCapabilitiesFromSnapshot(snapshot), snapshot.Revision); err != nil {
		return recipe.Loaded{}, tools.CapabilityCatalogSnapshot{}, err
	}
	return loaded, snapshot, nil
}

func runtimeRecipeMetadata(loaded recipe.Loaded, activate bool) map[string]any {
	metadata := map[string]any{
		"contractVersion": recipe.ContractVersion,
		"recipeId":        loaded.Document.RecipeID,
		"recipeVersion":   loaded.Document.RecipeVersion,
		"runtime":         loaded.Document.Runtime,
		"runtimeId":       loaded.Document.Runtime.ID,
		"source":          loaded.Source,
		"inputs":          loaded.RedactedInputs(),
		"secretInputs":    loaded.SecretInputNames(),
		"recipeHash":      loaded.Source.RecipeHash,
		"recipeDocument":  loaded.Document,
		"outputMapping":   loaded.Document.OutputMapping,
		"expandedPlan":    loaded.ExpandedPlan,
		"activate":        activate,
	}
	if loaded.Document.Activation != nil {
		activation := loaded.Document.Activation
		bindings := make(map[string]any, len(activation.InputBindings))
		inputNames := make(map[string]any, len(activation.InputBindings))
		for binding, inputName := range activation.InputBindings {
			bindings[binding] = loaded.Inputs[inputName]
			inputNames[binding] = inputName
		}
		metadata["activation"] = map[string]any{
			"capability":      activation.Capability,
			"servingContract": activation.ServingContract,
			"inputBindings":   bindings,
		}
		metadata["activationInputNames"] = inputNames
	}
	return metadata
}

func (s *Server) handleValidateRuntimeRecipe(args map[string]any) (*mcp.CallToolResult, error) {
	loaded, snapshot, err := s.loadRuntimeRecipe(args, false)
	if err != nil {
		return tools.ErrorResult(err), nil
	}
	planResult, err := s.validateHostPlanWithSnapshot(loaded.ExpandedPlan, snapshot)
	if err != nil {
		return tools.ErrorResult(err), nil
	}
	result := map[string]any{
		"valid":           true,
		"contractVersion": recipe.ContractVersion,
		"recipeId":        loaded.Document.RecipeID,
		"recipeVersion":   loaded.Document.RecipeVersion,
		"runtime":         loaded.Document.Runtime,
		"source":          loaded.Source,
		"inputs":          loaded.RedactedInputs(),
		"recipeHash":      loaded.Source.RecipeHash,
		"rawSha256":       loaded.Source.RawSHA256,
		"plan":            planResult,
	}
	return structuredResult(result, "runtime recipe is valid"), nil
}

func (s *Server) handleRunRuntimeRecipe(args map[string]any) (*mcp.CallToolResult, error) {
	resumeRunID := recipeStringField(args, "runId")
	if recipeStringField(args, "source") == "" || (resumeRunID != "" && recipeBoolField(args, "resume")) {
		runID := resumeRunID
		if runID == "" || s.state == nil {
			return tools.ErrorResult(fmt.Errorf("source is required unless resuming with runId")), nil
		}
		record, found, err := s.state.GetPlan(runID)
		if err != nil {
			return tools.ErrorResult(fmt.Errorf("get runtime recipe run: %w", err)), nil
		}
		if !found || record.RecipeJSON == "" {
			return tools.ErrorResult(fmt.Errorf("runtime recipe run not found: %s", runID)), nil
		}
		var doc plan.Document
		if err := json.Unmarshal([]byte(record.PlanJSON), &doc); err != nil {
			return tools.ErrorResult(fmt.Errorf("decode persisted runtime recipe plan: %w", err)), nil
		}
		var metadata map[string]any
		if err := json.Unmarshal([]byte(record.RecipeJSON), &metadata); err != nil {
			return tools.ErrorResult(fmt.Errorf("decode persisted runtime recipe metadata: %w", err)), nil
		}
		if recipeBoolField(args, "activate") {
			metadata["activate"] = true
		}
		if persistedPlanHasRedactedSecret(doc, metadata) {
			return tools.ErrorResult(fmt.Errorf("runtime recipe resume requires secret inputs to be supplied through references; refusing to execute redacted values")), nil
		}
		return s.handleRunHostPlanWithMetadata(map[string]any{
			"plan":   doc,
			"resume": true,
		}, metadata, "run_runtime_recipe", "Executing runtime recipe...")
	}

	loaded, _, err := s.loadRuntimeRecipe(args, true)
	if err != nil {
		return tools.ErrorResult(err), nil
	}
	activate := recipeBoolField(args, "activate")
	if activate && loaded.Document.Activation == nil {
		return tools.ErrorResult(fmt.Errorf("recipe does not declare an activation contract; activation cannot be requested")), nil
	}
	metadata := runtimeRecipeMetadata(loaded, activate)
	if providerID := recipeStringField(args, "providerId"); providerID != "" {
		metadata["providerId"] = providerID
		metadata["providerVersion"] = recipeStringField(args, "providerVersion")
		metadata["providerGenerationId"] = recipeStringField(args, "providerGenerationId")
		metadata["providerManifest"] = redactTaskValue(args["providerManifest"])
	}
	return s.handleRunHostPlanWithMetadata(map[string]any{
		"plan":   loaded.ExpandedPlan,
		"resume": recipeBoolField(args, "resume"),
	}, metadata, "run_runtime_recipe", "Executing runtime recipe...")
}

func (s *Server) validateRuntimeActivation(ctx context.Context, runID string, metadata map[string]any) (state.ActiveRuntimeRecord, map[string]any, error) {
	return s.validateRecipeActivation(ctx, runID, metadata)
}

func (s *Server) validateRecipeActivation(ctx context.Context, runID string, metadata map[string]any) (state.ActiveRuntimeRecord, map[string]any, error) {
	if !recipeBoolField(metadata, "activate") {
		return state.ActiveRuntimeRecord{}, nil, nil
	}
	activation, ok := metadata["activation"].(map[string]any)
	if !ok {
		return state.ActiveRuntimeRecord{}, nil, fmt.Errorf("recipe activation metadata is incomplete")
	}
	capability := recipeStringField(activation, "capability")
	contract := recipeStringField(activation, "servingContract")
	bindings, ok := activation["inputBindings"].(map[string]any)
	if capability == "" || contract == "" || !ok {
		return state.ActiveRuntimeRecord{}, nil, fmt.Errorf("recipe activation requires capability, servingContract, and inputBindings")
	}
	flow, ok := s.activationValidationFlows()[contract]
	if !ok {
		return state.ActiveRuntimeRecord{}, nil, fmt.Errorf("no host-agent activation validation flow exists for serving contract %q", contract)
	}
	observation, err := flow(ctx, bindings, recipeStringField(metadata, "providerGenerationId"))
	if err != nil {
		return state.ActiveRuntimeRecord{}, nil, fmt.Errorf("activation validation failed: %w", err)
	}
	ready, readyOK := observation["ready"].(bool)
	if !readyOK || !ready {
		return state.ActiveRuntimeRecord{}, observation, fmt.Errorf("activation validation failed: capability is not ready for %s", contract)
	}
	if contract == "openai-chat.v1" {
		streamingReady, streamingOK := observation["streamingChatReady"].(bool)
		if !streamingOK || !streamingReady {
			return state.ActiveRuntimeRecord{}, observation, fmt.Errorf("activation validation failed: runtime is not ready for %s", contract)
		}
	}
	observationJSON, err := json.Marshal(observation)
	if err != nil {
		return state.ActiveRuntimeRecord{}, nil, fmt.Errorf("encode runtime activation observation: %w", err)
	}
	bindingsJSON, err := json.Marshal(redactedActivationBindings(metadata, bindings))
	if err != nil {
		return state.ActiveRuntimeRecord{}, nil, fmt.Errorf("encode runtime activation bindings: %w", err)
	}
	active := state.ActiveRuntimeRecord{
		Capability:        capability,
		ServingContract:   contract,
		Runtime:           firstNonEmpty(recipeStringField(metadata, "runtimeId"), recipeStringField(metadata, "providerId")),
		Provider:          recipeStringField(metadata, "providerId"),
		RecipeID:          recipeStringField(metadata, "recipeId"),
		RecipeVersion:     recipeStringField(metadata, "recipeVersion"),
		RecipeHash:        recipeStringField(metadata, "recipeHash"),
		RunID:             runID,
		InputBindingsJSON: string(bindingsJSON),
		ObservationJSON:   string(observationJSON),
	}
	return active, observation, nil
}

func (s *Server) activateProviderGeneration(metadata map[string]any) error {
	if !recipeBoolField(metadata, "activate") {
		return nil
	}
	generationID := recipeStringField(metadata, "providerGenerationId")
	if generationID == "" {
		return nil
	}
	s.providerMu.RLock()
	manifest, manifestOK := s.providerCandidateManifests[generationID]
	previousManifest, previousManifestOK := s.providerManifests[manifest.Provider.ID]
	s.providerMu.RUnlock()
	if err := s.providerLifecycle.MarkReady(generationID); err != nil {
		return fmt.Errorf("provider generation readiness: %w", err)
	}
	if ready, ok := s.providerLifecycle.Get(generationID); ok {
		if err := s.persistProviderGeneration(ready); err != nil {
			return fmt.Errorf("persist provider generation readiness: %w", err)
		}
	}
	s.emitProviderLifecycleEvent(context.Background(), ProviderEventReady, manifest.Provider.ID, generationID, "")
	// Admission is asked before Activate, so a denial stops the transition
	// rather than reporting on one that already happened.
	if err := s.admitProvider(context.Background(), manifest.Provider.ID, generationID); err != nil {
		_ = s.providerLifecycle.Fail(generationID, err.Error())
		_ = s.restoreProviderManifest(manifest.Provider.ID, previousManifest, previousManifestOK)
		return err
	}
	// Publish and validate the candidate's neutral capability surface before
	// moving the lifecycle manager to active. Dispatch remains fail-closed until
	// activation because the candidate is not in the active adapter map.
	if manifestOK {
		if err := s.registerProviderServicesForGeneration(manifest, generationID); err != nil {
			_ = s.providerLifecycle.Fail(generationID, err.Error())
			_ = s.restoreProviderManifest(manifest.Provider.ID, previousManifest, previousManifestOK)
			return fmt.Errorf("register provider services: %w", err)
		}
	}
	previous, activated, err := s.providerLifecycle.Activate(generationID)
	if err != nil {
		if manifestOK {
			_ = s.restoreProviderManifest(manifest.Provider.ID, previousManifest, previousManifestOK)
		}
		return fmt.Errorf("provider generation activation: %w", err)
	}
	if previous != nil {
		if err := s.persistProviderGeneration(*previous); err != nil {
			return s.rollbackProviderActivation(manifest, manifestOK, previousManifest, previousManifestOK, activated, previous, fmt.Errorf("persist draining provider generation: %w", err))
		}
	}
	if err := s.persistProviderGeneration(activated); err != nil {
		return s.rollbackProviderActivation(manifest, manifestOK, previousManifest, previousManifestOK, activated, previous, fmt.Errorf("persist active provider generation: %w", err))
	}
	s.providerMu.Lock()
	adapter := s.providerCandidates[generationID]
	s.providerMu.Unlock()
	if adapter != nil {
		if err := s.mountProviderGeneration(manifest, generationID, adapter); err != nil {
			return s.rollbackProviderActivation(manifest, manifestOK, previousManifest, previousManifestOK, activated, previous, fmt.Errorf("mount provider generation: %w", err))
		}
	}
	s.providerMu.Lock()
	if manifestOK {
		s.providerValidation[activated.Provider.ID] = manifest.Validation.Operation
		s.providerManifests[activated.Provider.ID] = manifest
	}
	s.providerMu.Unlock()
	s.completeProviderCandidate(generationID)
	// The predecessor is disposed last, once the replacement is mounted and its
	// durable rows are written. Overlapping the two is what makes the swap
	// reversible: every failure above this point rolls back by disposing the
	// replacement, with the predecessor never having been touched.
	if previous != nil {
		s.emitProviderLifecycleEvent(context.Background(), ProviderEventDraining, previous.Provider.ID, previous.ID, "superseded")
		if err := s.unmountProviderGeneration(previous.Provider.ID, previous.ID); err != nil {
			// The replacement is live and durable; failing the activation now
			// would be a lie. Report the leak on the stopped event instead.
			s.emitProviderLifecycleEvent(context.Background(), ProviderEventStopped, previous.Provider.ID, previous.ID, fmt.Sprintf("superseded; dispose failed: %v", err))
		} else {
			s.emitProviderLifecycleEvent(context.Background(), ProviderEventStopped, previous.Provider.ID, previous.ID, "superseded")
		}
	}
	// Emitted only once the mount and the durable rows are in place, so an
	// observer that reacts to activation always finds a usable generation.
	s.emitProviderLifecycleEvent(context.Background(), ProviderEventActivated, activated.Provider.ID, activated.ID, "")
	return nil
}

// rollbackProviderActivation undoes a failed activation. The predecessor is
// deliberately still mounted at this point — it is disposed only after the
// replacement has fully succeeded — so rolling back is disposing what was just
// mounted, not reconstructing what was torn down. There is no restore path to
// get wrong.
func (s *Server) rollbackProviderActivation(candidate providercontract.InstallManifest, candidateOK bool, previousManifest providercontract.InstallManifest, previousManifestOK bool, activated cordis.ProviderGeneration, previous *cordis.ProviderGeneration, cause error) error {
	unmountErr := s.unmountProviderGeneration(activated.Provider.ID, activated.ID)
	previousID := ""
	if previous != nil {
		previousID = previous.ID
	}
	rollbackErr := s.providerLifecycle.RollbackActivation(activated.ID, previousID)
	if rollbackErr == nil && previous != nil {
		if restored, ok := s.providerLifecycle.Get(previous.ID); ok {
			if err := s.persistProviderGeneration(restored); err != nil {
				rollbackErr = fmt.Errorf("persist restored provider generation: %w", err)
			}
		}
	}
	manifestErr := error(nil)
	if candidateOK {
		manifestErr = s.restoreProviderManifest(candidate.Provider.ID, previousManifest, previousManifestOK)
	}
	if rollbackErr != nil || manifestErr != nil || unmountErr != nil {
		return fmt.Errorf("%w; rollback failed: lifecycle=%v catalog=%v unmount=%v", cause, rollbackErr, manifestErr, unmountErr)
	}
	return cause
}

func (s *Server) restoreProviderManifest(providerID string, manifest providercontract.InstallManifest, ok bool) error {
	if ok {
		generationID := func() string {
			if active, activeOK := s.providerLifecycle.Active(manifest.Provider.ID); activeOK {
				return active.ID
			}
			return ""
		}()
		if generationID == "" {
			return fmt.Errorf("provider %q has no active generation for catalog restore", providerID)
		}
		return s.registerProviderServicesForGeneration(manifest, generationID)
	}
	s.retireProviderCapabilities(providerID, "")
	return nil
}

// activationValidationFlows is the host-agent-owned contract validator
// registry. Recipes select a serving contract; they never select an
// implementation-specific validator or executable. New generic capabilities
// add another contract flow without changing the recipe executor.
func (s *Server) activationValidationFlows() map[string]func(context.Context, map[string]any, string) (map[string]any, error) {
	return map[string]func(context.Context, map[string]any, string) (map[string]any, error){
		"kubernetes.v1": func(ctx context.Context, bindings map[string]any, generationID string) (map[string]any, error) {
			providerID := recipeStringField(bindings, "providerId")
			if providerID == "" {
				return nil, fmt.Errorf("kubernetes.v1 activation requires input binding providerId")
			}
			var session *cordis.GenerationSession
			var err error
			if generationID != "" {
				session, err = s.providerLifecycle.OpenSessionForGeneration(providerID, generationID)
			} else {
				session, err = s.providerLifecycle.OpenSession(providerID)
			}
			if err != nil {
				return nil, err
			}
			defer session.Close()
			s.providerMu.RLock()
			adapter := s.providerGenerationAdapter(providerID, session.GenerationID())
			if adapter == nil {
				// Activation validation runs *before* activateProviderGeneration
				// promotes the generation's adapter out of providerCandidates,
				// so the generation under validation is still a candidate here.
				// Reading only the active map made every kubernetes.v1
				// activation fail with "is not connected" while its provider MCP
				// was connected and serving — the capability could never be
				// installed through opute.provider.install.
				adapter = s.providerCandidates[session.GenerationID()]
			}
			s.providerMu.RUnlock()
			if adapter == nil {
				return nil, fmt.Errorf("Kubernetes provider %q is not connected", providerID)
			}
			result, err := adapter.CallSynchronousOnly(ctx, "opute.capability.kubernetes.validate", map[string]any{})
			if err != nil {
				return nil, err
			}
			if result == nil || result.IsError {
				return nil, fmt.Errorf("Kubernetes provider %q validation failed", providerID)
			}
			observation, ok := result.StructuredContent.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("Kubernetes provider %q returned invalid validation evidence", providerID)
			}
			ready, _ := observation["ready"].(bool)
			if !ready {
				return nil, fmt.Errorf("Kubernetes provider %q is not ready", providerID)
			}
			return observation, nil
		},
		"openai-chat.v1": func(ctx context.Context, bindings map[string]any, _ string) (map[string]any, error) {
			endpoint := recipeStringField(bindings, "endpoint")
			modelRef := recipeStringField(bindings, "modelRef")
			if endpoint == "" {
				return nil, fmt.Errorf("openai-chat.v1 activation requires input binding endpoint")
			}
			probe, err := s.DispatchTool(ctx, "probe_openai_compatible_server", map[string]any{
				"endpoint":    endpoint,
				"modelRef":    modelRef,
				"includeChat": true,
				"bearerToken": recipeStringField(bindings, "bearerToken"),
			}, nil)
			if err != nil {
				return nil, err
			}
			if probe == nil || probe.IsError {
				return nil, fmt.Errorf("runtime probe returned an error")
			}
			observation, ok := structuredObject(probe.StructuredContent)
			if !ok {
				return nil, fmt.Errorf("runtime probe returned no observation")
			}
			return observation, nil
		},
		"http-exposure.v1": func(ctx context.Context, bindings map[string]any, _ string) (map[string]any, error) {
			endpoints := []string{}
			if endpoint := recipeStringField(bindings, "endpoint"); endpoint != "" {
				endpoints = append(endpoints, endpoint)
			}
			if raw, ok := bindings["endpoints"].([]any); ok {
				for _, value := range raw {
					if endpoint, ok := value.(string); ok && strings.TrimSpace(endpoint) != "" {
						endpoints = append(endpoints, strings.TrimSpace(endpoint))
					}
				}
			}
			if len(endpoints) == 0 {
				return nil, fmt.Errorf("http-exposure.v1 activation requires endpoint or endpoints input binding")
			}
			checks := make([]any, 0, len(endpoints))
			for _, endpoint := range endpoints {
				probe, err := s.DispatchTool(ctx, "probe_http_endpoint", map[string]any{"endpoint": endpoint}, nil)
				if err != nil {
					return nil, err
				}
				if probe == nil || probe.IsError {
					return nil, fmt.Errorf("HTTP endpoint probe returned an error for %s", endpoint)
				}
				observation, ok := structuredObject(probe.StructuredContent)
				if !ok {
					return nil, fmt.Errorf("HTTP endpoint probe returned no observation for %s", endpoint)
				}
				checks = append(checks, observation)
				ready, _ := observation["ready"].(bool)
				if !ready {
					return map[string]any{"servingContract": "http-exposure.v1", "ready": false, "checks": checks}, nil
				}
			}
			return map[string]any{"servingContract": "http-exposure.v1", "ready": true, "checks": checks}, nil
		},
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func redactedActivationBindings(metadata map[string]any, bindings map[string]any) map[string]any {
	result := make(map[string]any, len(bindings))
	secretNames := map[string]bool{}
	if raw, ok := metadata["secretInputs"].([]string); ok {
		for _, name := range raw {
			secretNames[name] = true
		}
	}
	if raw, ok := metadata["secretInputs"].([]any); ok {
		for _, value := range raw {
			if name, ok := value.(string); ok {
				secretNames[name] = true
			}
		}
	}
	inputNames, _ := metadata["activationInputNames"].(map[string]any)
	for binding, value := range bindings {
		if secretNames[fmt.Sprint(inputNames[binding])] {
			result[binding] = "[redacted]"
		} else {
			result[binding] = value
		}
	}
	return result
}

func (s *Server) ensureRecipeActivation(record state.PlanRecord, metadata map[string]any) (state.PlanRecord, error) {
	if !recipeBoolField(metadata, "activate") || record.Status != "completed" {
		return record, nil
	}
	activation, ok := metadata["activation"].(map[string]any)
	if !ok {
		return state.PlanRecord{}, fmt.Errorf("runtime recipe activation metadata is incomplete")
	}
	capability := recipeStringField(activation, "capability")
	if active, found, err := s.state.GetActiveRuntime(capability); err != nil {
		return state.PlanRecord{}, err
	} else if found && active.RunID == record.RunID {
		return record, nil
	}
	var stateValue plan.RunState
	if err := json.Unmarshal([]byte(record.StateJSON), &stateValue); err != nil {
		return state.PlanRecord{}, fmt.Errorf("decode completed recipe state: %w", err)
	}
	active, observation, err := s.validateRuntimeActivation(context.Background(), record.RunID, metadata)
	if err != nil {
		return state.PlanRecord{}, err
	}
	if stateValue.Outputs == nil {
		stateValue.Outputs = map[string]any{}
	}
	stateValue.Outputs["activation"] = observation
	if err := s.state.CompletePlanWithActiveRuntime(record.RunID, s.marshalPlanState(stateValue, nil), active); err != nil {
		return state.PlanRecord{}, err
	}
	updated, found, err := s.state.GetPlan(record.RunID)
	if err != nil {
		return state.PlanRecord{}, err
	}
	if !found {
		return state.PlanRecord{}, fmt.Errorf("activated recipe run disappeared: %s", record.RunID)
	}
	return updated, nil
}

func (s *Server) ensureRuntimeRecipeActivation(record state.PlanRecord, metadata map[string]any) (state.PlanRecord, error) {
	return s.ensureRecipeActivation(record, metadata)
}

// activateCompletedProviderCandidate covers reconnects after a restart. The
// durable recipe run may already be completed, while the executable provider
// adapter and its candidate generation are process-local and must be rebound.
func (s *Server) activateCompletedProviderCandidate(metadata map[string]any) error {
	if recipeStringField(metadata, "providerGenerationId") == "" {
		return nil
	}
	if err := s.activateProviderGeneration(metadata); err != nil {
		return fmt.Errorf("provider generation activation after reconnect: %w", err)
	}
	s.completeProviderCandidate(recipeStringField(metadata, "providerGenerationId"))
	return nil
}

func structuredObject(value any) (map[string]any, bool) {
	if object, ok := value.(map[string]any); ok {
		return object, true
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, false
	}
	var object map[string]any
	if err := json.Unmarshal(encoded, &object); err != nil || object == nil {
		return nil, false
	}
	return object, true
}

func persistedPlanHasRedactedSecret(doc plan.Document, metadata map[string]any) bool {
	variables := doc.Variables
	inputs, _ := variables["inputs"].(map[string]any)
	secretNames, _ := metadata["secretInputs"].([]any)
	for _, rawName := range secretNames {
		name, ok := rawName.(string)
		if ok && inputs != nil && inputs[name] == "[redacted]" {
			return true
		}
	}
	return false
}

func (s *Server) handleGetRuntimeRecipeRun(args map[string]any) (*mcp.CallToolResult, error) {
	return s.handleGetHostPlanRun(args)
}
