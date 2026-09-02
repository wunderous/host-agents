package plan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type Runner struct {
	Dispatch        Dispatcher
	Capabilities    map[string]Capability
	CatalogRevision string
	HostAgentID     string
	Sink            EventSink
}

var (
	ErrWaitReached    = errors.New("plan reached a durable wait")
	ErrWaitExpired    = errors.New("plan wait expired")
	ErrResumeConflict = errors.New("plan wait resume conflict")
)

func (r Runner) Run(ctx context.Context, doc Document, state RunState) (RunState, error) {
	if err := Validate(doc, r.Capabilities, r.CatalogRevision); err != nil {
		return state, err
	}
	if state.Status == RunStatusWaiting && state.Wait != nil && state.Wait.Status == RunStatusWaiting {
		return state, nil
	}
	levels, err := TopologicalLevels(doc)
	if err != nil {
		return state, err
	}
	if state.Nodes == nil {
		state.Nodes = make(map[string]NodeRunState, len(doc.Nodes))
	}
	if state.Outputs == nil {
		state.Outputs = make(map[string]any, len(doc.Nodes))
	}
	if state.Context == nil {
		state.Context = make(map[string]ContextEntry)
	}
	state.PlanID = doc.PlanID
	state.Generation = doc.Generation
	state.Status = "running"
	if err := r.emit(state); err != nil {
		return state, err
	}

	maxPasses := doc.Converge.MaxPasses
	if maxPasses <= 0 {
		maxPasses = doc.Defaults.MaxPasses
	}
	if maxPasses <= 0 {
		maxPasses = 1
	}
	for pass := 1; pass <= maxPasses; pass++ {
		for _, level := range levels {
			if err := r.runLevel(ctx, doc, level, &state); err != nil {
				if errors.Is(err, ErrWaitReached) {
					return state, nil
				}
				return r.abortRun(ctx, doc, &state, err)
			}
		}
		converged, err := r.checkConverged(ctx, doc, levels, &state, pass < maxPasses)
		if err != nil {
			return r.abortRun(ctx, doc, &state, err)
		}
		if converged {
			state.Status = "completed"
			state.Error = ""
			if err := r.emit(state); err != nil {
				return state, err
			}
			return state, nil
		}
	}

	message := fmt.Sprintf("plan did not converge after %d passes", maxPasses)
	if doc.Converge.AbortOnExhaustion {
		return r.abortRun(ctx, doc, &state, errors.New(message))
	}
	state.Status = "completed"
	state.Error = message
	if err := r.emit(state); err != nil {
		return state, err
	}
	return state, nil
}

// Resume consumes the currently persisted wait exactly once. The durable
// state transition is emitted before Run is called again, so a process crash
// cannot dispatch a dependent action without first recording the consumed
// barrier and its context projection.
func (r Runner) Resume(ctx context.Context, doc Document, state RunState, request ResumeRequest) (RunState, error) {
	if err := Validate(doc, r.Capabilities, r.CatalogRevision); err != nil {
		return state, err
	}
	if err := ValidateResume(doc, state, request); err != nil {
		if errors.Is(err, ErrWaitExpired) {
			state.Status = RunStatusExpired
			if state.Wait != nil {
				state.Wait.Status = RunStatusExpired
			}
			if node, ok := state.Nodes[request.WaitNodeID]; ok {
				node.Status = StatusExpired
				node.Error = err.Error()
				node.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
				state.Nodes[request.WaitNodeID] = node
			}
			state.Error = err.Error()
			_ = r.emit(state)
		}
		return state, err
	}

	node, ok := nodeByID(doc, state.Wait.NodeID)
	if !ok || node.Wait == nil {
		return state, fmt.Errorf("%w: wait node %q is not declared", ErrResumeConflict, state.Wait.NodeID)
	}
	if state.Context == nil {
		state.Context = make(map[string]ContextEntry)
	}
	for _, delta := range node.Wait.ContextDelta {
		value, err := interpolateValue(delta.Value, EvalContext{
			Variables:  doc.Variables,
			NodeOutput: state.Outputs,
			Input:      request.Input,
			Context:    contextValues(state.Context),
		})
		if err != nil {
			return state, fmt.Errorf("context delta %q: %w", delta.Name, err)
		}
		if err := ValidateJSON(delta.Schema, value); err != nil {
			return state, fmt.Errorf("context delta %q: %w", delta.Name, err)
		}
		entry := ContextEntry{
			Name:           delta.Name,
			Value:          value,
			Schema:         delta.Schema,
			SchemaRevision: node.Wait.SchemaRevision,
			ProducerNode:   node.ID,
			Source:         request.Source,
			Secret:         delta.Secret,
			RecordedAt:     time.Now().UTC().Format(time.RFC3339Nano),
		}
		state.Context[delta.Name] = entry
		state.ContextHistory = append(state.ContextHistory, entry)
	}
	state.Nodes[node.ID] = NodeRunState{
		ID:          node.ID,
		Status:      StatusSatisfied,
		CompletedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	state.Wait = nil
	state.Status = "running"
	state.Error = ""
	if err := r.emit(state); err != nil {
		return state, err
	}
	return r.Run(ctx, doc, state)
}

// ValidateResume is the pure fence check used by both the runner and the MCP
// task adapter before accepting an input response.
func ValidateResume(doc Document, state RunState, request ResumeRequest) error {
	if state.Status != RunStatusWaiting || state.Wait == nil || state.Wait.Status != RunStatusWaiting {
		return fmt.Errorf("%w: run is not waiting", ErrResumeConflict)
	}
	wait := state.Wait
	if request.WaitNodeID != wait.NodeID || request.WaitRevision != wait.WaitRevision || request.SchemaRevision != wait.SchemaRevision {
		return fmt.Errorf("%w: wait revision, node, or schema does not match", ErrResumeConflict)
	}
	if !jsonEqual(request.Correlation, wait.Correlation) {
		return fmt.Errorf("%w: wait correlation does not match", ErrResumeConflict)
	}
	if strings.TrimSpace(request.Source) == "" {
		return fmt.Errorf("%w: resume source is required", ErrResumeConflict)
	}
	if request.Source != "operator" && request.Source != "authenticated-event" {
		return fmt.Errorf("%w: unsupported resume source %q", ErrResumeConflict, request.Source)
	}
	node, ok := nodeByID(doc, wait.NodeID)
	if !ok || node.Wait == nil {
		return fmt.Errorf("%w: wait node %q is not declared", ErrResumeConflict, wait.NodeID)
	}
	if wait.Trigger.Kind == "operator" && request.Source != "operator" {
		return fmt.Errorf("%w: this wait accepts operator input only", ErrResumeConflict)
	}
	for _, delta := range node.Wait.ContextDelta {
		if delta.Provenance == "operator" && request.Source != "operator" {
			return fmt.Errorf("%w: context %q accepts operator input only", ErrResumeConflict, delta.Name)
		}
		if delta.Provenance == "authenticated-event" && request.Source != "authenticated-event" {
			return fmt.Errorf("%w: context %q requires an authenticated event", ErrResumeConflict, delta.Name)
		}
	}
	if wait.ExpiresAt != "" {
		expiresAt, err := time.Parse(time.RFC3339Nano, wait.ExpiresAt)
		if err != nil || !time.Now().UTC().Before(expiresAt) {
			return ErrWaitExpired
		}
	}
	if err := ValidateJSON(wait.InputSchema, request.Input); err != nil {
		return fmt.Errorf("%w: input: %v", ErrResumeConflict, err)
	}
	return nil
}

func (r Runner) abortRun(ctx context.Context, doc Document, state *RunState, runErr error) (RunState, error) {
	state.Status = "failed"
	if ctx.Err() != nil {
		state.Status = "unknown"
	}
	state.Error = runErr.Error()
	_ = r.emit(*state)
	_ = r.compensateApplied(doc, state)
	return *state, runErr
}

// checkConverged performs a post-pass readiness sweep when another convergence
// pass is available. A failed assertion marks the node pending, leaving the
// next ordinary topological pass responsible for corrective action.
func (r Runner) checkConverged(ctx context.Context, doc Document, levels [][]Node, state *RunState, recheck bool) (bool, error) {
	if recheck {
		for _, level := range levels {
			for _, node := range level {
				current := state.Nodes[node.ID]
				if current.Status != StatusApplied && current.Status != StatusSatisfied && current.Status != StatusSkipped {
					return false, nil
				}
				if node.Validate == nil || current.Status == StatusSkipped {
					continue
				}
				result, failure, err := r.validateNodeReady(ctx, doc, node, nil, state.Outputs, state.Context)
				if err != nil {
					return false, err
				}
				if failure != nil {
					state.Nodes[node.ID] = NodeRunState{ID: node.ID, Status: StatusPending, Error: failure.Message}
					delete(state.Outputs, node.ID)
					_ = r.emit(*state)
					return false, nil
				}
				current.Status = StatusSatisfied
				current.Output = result
				current.Observed = result
				state.Nodes[node.ID] = current
				state.Outputs[node.ID] = result
			}
		}
	}
	for _, node := range doc.Nodes {
		status := state.Nodes[node.ID].Status
		if status != StatusApplied && status != StatusSatisfied && status != StatusSkipped {
			return false, nil
		}
	}
	return true, nil
}

func (r Runner) runLevel(ctx context.Context, doc Document, level []Node, state *RunState) error {
	for _, node := range level {
		if node.Wait != nil {
			// A wait is a graph barrier. Do not race siblings in the same level
			// against the durable pause or leave them partially dispatched.
			for _, serialNode := range level {
				if err := r.runNodeWithPreconditions(ctx, doc, serialNode, state); err != nil {
					return err
				}
			}
			return nil
		}
	}
	maxConcurrency := doc.Converge.MaxConcurrency
	if maxConcurrency <= 0 {
		maxConcurrency = len(level)
	}
	if maxConcurrency <= 1 || len(level) <= 1 {
		for _, node := range level {
			if err := r.runNodeWithPreconditions(ctx, doc, node, state); err != nil {
				if node.ContinueOnFailure {
					continue
				}
				return err
			}
		}
		return nil
	}

	type levelResult struct {
		index int
		node  Node
		state RunState
		err   error
	}
	levelCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan levelResult, len(level))
	semaphore := make(chan struct{}, maxConcurrency)
	for index, node := range level {
		index, node := index, node
		go func() {
			semaphore <- struct{}{}
			defer func() { <-semaphore }()
			local := cloneRunState(*state)
			localRunner := r
			localRunner.Sink = nil
			err := localRunner.runNodeWithPreconditions(levelCtx, doc, node, &local)
			if err != nil && !node.ContinueOnFailure {
				cancel()
			}
			results <- levelResult{index: index, node: node, state: local, err: err}
		}()
	}
	ordered := make([]levelResult, 0, len(level))
	for range level {
		ordered = append(ordered, <-results)
	}
	// Preserve the declaration/topological order for durable event output and
	// deterministic error selection even though dispatches ran concurrently.
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].index < ordered[j].index })
	for _, result := range ordered {
		state.Nodes[result.node.ID] = result.state.Nodes[result.node.ID]
		if output, ok := result.state.Outputs[result.node.ID]; ok {
			state.Outputs[result.node.ID] = output
		}
		if err := r.emit(*state); err != nil {
			return err
		}
	}
	for _, result := range ordered {
		if result.err != nil && !result.node.ContinueOnFailure {
			return result.err
		}
	}
	return nil
}

func (r Runner) runNodeWithPreconditions(ctx context.Context, doc Document, node Node, state *RunState) error {
	if err := ctx.Err(); err != nil {
		state.Nodes[node.ID] = NodeRunState{ID: node.ID, Status: StatusUnknown, Error: err.Error()}
		return err
	}
	if blockedByDependency(node, state.Nodes) {
		state.Nodes[node.ID] = NodeRunState{ID: node.ID, Status: StatusSkipped, Error: "dependency did not complete"}
		if !node.ContinueOnFailure {
			return fmt.Errorf("node %s dependency failed", node.ID)
		}
		return nil
	}
	if failure, ok := EvaluateAssertions(map[string]any{"vars": doc.Variables, "nodes": state.Outputs, "context": contextValues(state.Context)}, node.When); !ok && len(node.When) > 0 {
		state.Nodes[node.ID] = NodeRunState{ID: node.ID, Status: StatusSkipped, Error: failure.Message}
		return nil
	}
	// A dependency applied in this reconciliation pass invalidates this
	// node's prior readiness observation. Carry that fact into runNode so a
	// downstream action cannot be skipped merely because it still looks ready
	// against the state that existed before the dependency changed.
	forceAction := dependencyApplied(state.Nodes, node)
	if previous := state.Nodes[node.ID]; (previous.Status == StatusApplied || previous.Status == StatusSatisfied) && !forceAction {
		if node.Validate != nil {
			result, failure, validateErr := r.validateNodeReady(ctx, doc, node, nil, state.Outputs, state.Context)
			if validateErr != nil {
				return validateErr
			}
			if failure == nil {
				previous.Status = StatusSatisfied
				previous.Output = result
				previous.Observed = result
				state.Nodes[node.ID] = previous
				state.Outputs[node.ID] = result
				return nil
			}
			state.Nodes[node.ID] = NodeRunState{ID: node.ID, Status: StatusPending}
			delete(state.Outputs, node.ID)
		} else {
			previous.Status = StatusSatisfied
			state.Nodes[node.ID] = previous
			return nil
		}
	}
	return r.runNode(ctx, doc, node, state, forceAction)
}

func cloneRunState(value RunState) RunState {
	clone := value
	clone.Nodes = make(map[string]NodeRunState, len(value.Nodes))
	for key, node := range value.Nodes {
		clone.Nodes[key] = node
	}
	clone.Outputs = make(map[string]any, len(value.Outputs))
	for key, output := range value.Outputs {
		clone.Outputs[key] = output
	}
	clone.Context = make(map[string]ContextEntry, len(value.Context))
	for key, entry := range value.Context {
		clone.Context[key] = entry
	}
	clone.ContextHistory = append([]ContextEntry(nil), value.ContextHistory...)
	if value.Wait != nil {
		wait := *value.Wait
		wait.Correlation = cloneMap(value.Wait.Correlation)
		wait.InputSchema = cloneMap(value.Wait.InputSchema)
		clone.Wait = &wait
	}
	return clone
}

func (r Runner) runNode(ctx context.Context, doc Document, node Node, state *RunState, forceAction ...bool) error {
	nodeCtx := ctx
	cancel := func() {}
	timeoutMs := node.TimeoutMs
	if timeoutMs <= 0 {
		timeoutMs = doc.Defaults.TimeoutMs
	}
	if timeoutMs > 0 {
		nodeCtx, cancel = context.WithTimeout(ctx, time.Duration(timeoutMs)*time.Millisecond)
	}
	defer cancel()
	started := time.Now().UTC().Format(time.RFC3339Nano)
	nodeState := NodeRunState{ID: node.ID, Status: StatusPending, StartedAt: started}
	state.Nodes[node.ID] = nodeState
	if err := r.emit(*state); err != nil {
		return err
	}
	if err := r.validateTarget(doc, node, state); err != nil {
		state.Nodes[node.ID] = failedNode(nodeState, err)
		_ = r.emit(*state)
		return err
	}
	if node.Wait != nil {
		waitRevision := 1
		if state.Wait != nil && state.Wait.NodeID == node.ID {
			waitRevision = state.Wait.WaitRevision + 1
		}
		expiresAt := node.Wait.ExpiresAt
		if expiresAt == "" && node.Wait.ExpiresInMs > 0 {
			expiresAt = time.Now().UTC().Add(time.Duration(node.Wait.ExpiresInMs) * time.Millisecond).Format(time.RFC3339Nano)
		}
		correlation, err := interpolateValue(node.Wait.Correlation, r.evalContext(doc, state, nil))
		if err != nil {
			state.Nodes[node.ID] = failedNode(nodeState, err)
			_ = r.emit(*state)
			return err
		}
		correlationMap, ok := correlation.(map[string]any)
		if correlation == nil {
			correlationMap = map[string]any{}
			ok = true
		}
		if !ok {
			err := fmt.Errorf("wait correlation must be an object")
			state.Nodes[node.ID] = failedNode(nodeState, err)
			_ = r.emit(*state)
			return err
		}
		state.Wait = &WaitState{
			NodeID:         node.ID,
			WaitID:         fmt.Sprintf("%s:%s:%d", state.RunID, node.ID, waitRevision),
			WaitRevision:   waitRevision,
			SchemaRevision: node.Wait.SchemaRevision,
			Trigger:        node.Wait.Trigger,
			Correlation:    correlationMap,
			InputSchema:    node.Wait.InputSchema,
			ExpiresAt:      expiresAt,
			Status:         RunStatusWaiting,
		}
		nodeState.Status = StatusWaiting
		state.Nodes[node.ID] = nodeState
		state.Status = RunStatusWaiting
		state.Error = ""
		if err := r.emit(*state); err != nil {
			return err
		}
		return ErrWaitReached
	}

	if node.ForEach != nil {
		err := r.runForEach(nodeCtx, doc, node, state, len(forceAction) > 0 && forceAction[0])
		if err != nil {
			current := state.Nodes[node.ID]
			current.ID = node.ID
			current.Error = err.Error()
			if nodeCtx.Err() != nil {
				current.Status = StatusUnknown
			} else {
				current.Status = StatusFailed
				current.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
			}
			state.Nodes[node.ID] = current
			_ = r.emit(*state)
		}
		return err
	}
	if node.Validate != nil && (len(forceAction) == 0 || !forceAction[0]) {
		// A failed preflight must yield to the node action immediately. Polling
		// here would wait for a resource that this node is responsible for
		// creating. A validation-only node has no action and may use bounded
		// polling to observe external readiness.
		result, failure, err := r.validateNode(nodeCtx, doc, node, nil, state.Outputs, state.Context)
		if node.Action == nil {
			result, failure, err = r.validateNodeReady(nodeCtx, doc, node, nil, state.Outputs, state.Context)
		}
		if err != nil {
			state.Nodes[node.ID] = failedNode(nodeState, err)
			_ = r.emit(*state)
			return err
		}
		if failure == nil {
			nodeState.Status = StatusSatisfied
			nodeState.Output = result
			nodeState.Observed = result
			nodeState.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
			state.Nodes[node.ID] = nodeState
			state.Outputs[node.ID] = result
			return r.emit(*state)
		}
	}
	if node.Action == nil {
		err := fmt.Errorf("node %q has no action after validation failed", node.ID)
		state.Nodes[node.ID] = failedNode(nodeState, err)
		_ = r.emit(*state)
		return err
	}
	attempts := node.Retry.MaxAttempts
	if attempts <= 0 {
		attempts = doc.Defaults.Retry.MaxAttempts
	}
	if attempts <= 0 {
		attempts = 1
	}
	for attempt := 1; attempt <= attempts; attempt++ {
		nodeState.Attempts = attempt
		state.Nodes[node.ID] = nodeState
		if err := r.emit(*state); err != nil {
			return err
		}
		args, err := InterpolateArgs(node.Action.Args, r.evalContext(doc, state, nil))
		if err != nil {
			return err
		}
		if err := r.validateArgs(node.Action.Tool, args); err != nil {
			return err
		}
		result, dispatchErr := r.dispatch(nodeCtx, node.Action.Tool, args)
		if dispatchErr != nil {
			if nodeCtx.Err() != nil {
				nodeState.Status = StatusUnknown
				nodeState.Error = nodeCtx.Err().Error()
				state.Nodes[node.ID] = nodeState
				_ = r.emit(*state)
				return dispatchErr
			}
			nodeState.Error = dispatchErr.Error()
			if attempt < attempts {
				if err := sleepBackoff(nodeCtx, node.Retry, doc.Defaults.Retry, attempt); err != nil {
					if nodeCtx.Err() != nil {
						nodeState.Status = StatusUnknown
						nodeState.Error = nodeCtx.Err().Error()
						state.Nodes[node.ID] = nodeState
						_ = r.emit(*state)
					}
					return err
				}
				continue
			}
			state.Nodes[node.ID] = failedNode(nodeState, dispatchErr)
			_ = r.emit(*state)
			return dispatchErr
		}
		nodeState.Output = structuredContent(result)
		if err := r.validateOutput(node.Action.Tool, nodeState.Output); err != nil {
			nodeState.Status = StatusFailed
			nodeState.Error = err.Error()
			state.Nodes[node.ID] = nodeState
			_ = r.emit(*state)
			return err
		}
		state.Outputs[node.ID] = nodeState.Output
		if node.Validate == nil {
			nodeState.Status = StatusApplied
			nodeState.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
			state.Nodes[node.ID] = nodeState
			return r.emit(*state)
		}
		validationOutput, failure, validateErr := r.validateNodeReady(nodeCtx, doc, node, nil, state.Outputs, state.Context)
		if validateErr == nil && failure == nil {
			nodeState.Observed = validationOutput
			nodeState.Status = StatusApplied
			nodeState.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
			state.Nodes[node.ID] = nodeState
			return r.emit(*state)
		}
		if node.Recover != nil {
			recoveryAttempts := node.Recover.MaxAttempts
			if recoveryAttempts <= 0 {
				recoveryAttempts = 1
			}
			for recoveryAttempt := 1; recoveryAttempt <= recoveryAttempts; recoveryAttempt++ {
				recoverArgs, interpolationErr := InterpolateArgs(node.Recover.Args, r.evalContext(doc, state, nil))
				if interpolationErr != nil {
					validateErr = interpolationErr
					break
				}
				if schemaErr := r.validateArgs(node.Recover.Tool, recoverArgs); schemaErr != nil {
					return schemaErr
				}
				if _, recoveryErr := r.dispatch(nodeCtx, node.Recover.Tool, recoverArgs); recoveryErr != nil {
					validateErr = recoveryErr
					if nodeCtx.Err() != nil {
						nodeState.Status = StatusUnknown
						nodeState.Error = nodeCtx.Err().Error()
						state.Nodes[node.ID] = nodeState
						_ = r.emit(*state)
						return recoveryErr
					}
					if recoveryAttempt < recoveryAttempts {
						if err := sleepBackoff(nodeCtx, Retry{}, doc.Defaults.Retry, recoveryAttempt); err != nil {
							return err
						}
					}
					continue
				}
				recoveryOutput, recoveryFailure, recoveryValidationErr := r.validateNodeReady(nodeCtx, doc, node, nil, state.Outputs, state.Context)
				if recoveryValidationErr == nil && recoveryFailure == nil {
					nodeState.Observed = recoveryOutput
					nodeState.Status = StatusApplied
					nodeState.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
					state.Nodes[node.ID] = nodeState
					return r.emit(*state)
				}
				if recoveryValidationErr != nil {
					validateErr = recoveryValidationErr
				} else if recoveryFailure != nil {
					failure = recoveryFailure
				}
			}
		}
		if validateErr != nil {
			nodeState.Error = validateErr.Error()
		}
		if failure != nil {
			nodeState.Observed = failure.Observed
			nodeState.Expected = failure.Expected
			nodeState.Error = failure.Message
		}
		if attempt < attempts {
			if err := sleepBackoff(nodeCtx, node.Retry, doc.Defaults.Retry, attempt); err != nil {
				if nodeCtx.Err() != nil {
					nodeState.Status = StatusUnknown
					nodeState.Error = nodeCtx.Err().Error()
					state.Nodes[node.ID] = nodeState
					_ = r.emit(*state)
				}
				return err
			}
			continue
		}
	}
	err := fmt.Errorf("node %q did not reach readiness", node.ID)
	state.Nodes[node.ID] = failedNode(nodeState, err)
	_ = r.emit(*state)
	return err
}

func (r Runner) runForEach(ctx context.Context, doc Document, node Node, state *RunState, forceAction bool) error {
	value, err := resolveSource(node.ForEach.Source, doc, state)
	if err != nil {
		return err
	}
	if node.ForEach.Path != "" {
		var exists bool
		value, exists, err = resolveJSONPointer(value, node.ForEach.Path)
		if err != nil || !exists {
			return fmt.Errorf("forEach source path %q was not found", node.ForEach.Path)
		}
	}
	items, ok := anySlice(value)
	if !ok || len(items) > maxFanOut {
		return fmt.Errorf("forEach source must be an array with at most %d items", maxFanOut)
	}
	outputs := make([]any, 0, len(items))
	for _, item := range items {
		itemMap, ok := item.(map[string]any)
		if !ok {
			itemMap = map[string]any{node.ForEach.As: item}
		} else if node.ForEach.As != "" {
			itemMap = cloneMap(itemMap)
			itemMap[node.ForEach.As] = item
		}
		if _, matches := EvaluateAssertions(item, node.ForEach.Filter); len(node.ForEach.Filter) > 0 && !matches {
			continue
		}
		if node.Action == nil {
			return fmt.Errorf("forEach node %q requires an action", node.ID)
		}
		itemOutput, err := r.runForEachItem(ctx, doc, node, itemMap, state.Outputs, state.Context, forceAction)
		if err != nil {
			return err
		}
		outputs = append(outputs, itemOutput)
	}
	state.Outputs[node.ID] = outputs
	state.Nodes[node.ID] = NodeRunState{ID: node.ID, Status: StatusApplied, Output: outputs, CompletedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	return r.emit(*state)
}

func (r Runner) runForEachItem(ctx context.Context, doc Document, node Node, item map[string]any, outputs map[string]any, contexts map[string]ContextEntry, forceAction bool) (any, error) {
	attempts := attemptsFor(node, doc)
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if node.Validate != nil && !forceAction {
			validated, failure, err := r.validateNodeReady(ctx, doc, node, item, outputs, contexts)
			if err != nil {
				lastErr = err
			} else if failure == nil {
				return validated, nil
			} else {
				lastErr = fmt.Errorf("forEach node %q did not reach readiness: %s", node.ID, failure.Message)
			}
		}
		args, err := InterpolateArgs(node.Action.Args, evaluationContext(doc, outputs, contexts, item))
		if err != nil {
			return nil, err
		}
		if err := r.validateArgs(node.Action.Tool, args); err != nil {
			return nil, err
		}
		result, dispatchErr := r.dispatch(ctx, node.Action.Tool, args)
		if dispatchErr != nil {
			lastErr = dispatchErr
		} else {
			itemOutput := structuredContent(result)
			if err := r.validateOutput(node.Action.Tool, itemOutput); err != nil {
				return nil, err
			}
			if node.Validate == nil {
				return itemOutput, nil
			}
			_, failure, validateErr := r.validateNodeReady(ctx, doc, node, item, outputs, contexts)
			if validateErr == nil && failure == nil {
				return itemOutput, nil
			}
			if validateErr != nil {
				lastErr = validateErr
			} else {
				lastErr = fmt.Errorf("forEach node %q did not reach readiness: %s", node.ID, failure.Message)
			}
			if node.Recover != nil {
				if recovered, recoveryErr := r.recoverForEachItem(ctx, doc, node, item, outputs, contexts); recoveryErr == nil && recovered {
					return itemOutput, nil
				} else if recoveryErr != nil {
					lastErr = recoveryErr
				}
			}
		}
		if attempt < attempts {
			if err := sleepBackoff(ctx, node.Retry, doc.Defaults.Retry, attempt); err != nil {
				return nil, err
			}
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("forEach node %q did not complete", node.ID)
	}
	return nil, lastErr
}

func (r Runner) recoverForEachItem(ctx context.Context, doc Document, node Node, item map[string]any, outputs map[string]any, contexts map[string]ContextEntry) (bool, error) {
	recoveryAttempts := node.Recover.MaxAttempts
	if recoveryAttempts <= 0 {
		recoveryAttempts = 1
	}
	var lastErr error
	for attempt := 1; attempt <= recoveryAttempts; attempt++ {
		args, err := InterpolateArgs(node.Recover.Args, evaluationContext(doc, outputs, contexts, item))
		if err != nil {
			return false, err
		}
		if err := r.validateArgs(node.Recover.Tool, args); err != nil {
			return false, err
		}
		result, err := r.dispatch(ctx, node.Recover.Tool, args)
		if err == nil {
			if outputErr := r.validateOutput(node.Recover.Tool, structuredContent(result)); outputErr != nil {
				return false, outputErr
			}
			_, failure, validationErr := r.validateNodeReady(ctx, doc, node, item, outputs, contexts)
			if validationErr == nil && failure == nil {
				return true, nil
			}
			if validationErr != nil {
				lastErr = validationErr
			} else {
				lastErr = fmt.Errorf("forEach node %q did not reach readiness: %s", node.ID, failure.Message)
			}
		} else {
			lastErr = err
		}
		if attempt < recoveryAttempts {
			if err := sleepBackoff(ctx, node.Retry, doc.Defaults.Retry, attempt); err != nil {
				return false, err
			}
		}
	}
	return false, lastErr
}

func (r Runner) validateNode(ctx context.Context, doc Document, node Node, item map[string]any, outputs map[string]any, contexts ...map[string]ContextEntry) (any, *AssertionFailure, error) {
	if node.Validate == nil {
		return nil, nil, nil
	}
	args, err := InterpolateArgs(node.Validate.Args, evaluationContext(doc, outputs, firstContext(contexts), item))
	if err != nil {
		return nil, nil, err
	}
	if err := r.validateArgs(node.Validate.Tool, args); err != nil {
		return nil, nil, err
	}
	result, err := r.dispatch(ctx, node.Validate.Tool, args)
	if err != nil {
		return nil, nil, err
	}
	content := structuredContent(result)
	if err := r.validateOutput(node.Validate.Tool, content); err != nil {
		return nil, nil, err
	}
	failure, ok := EvaluateAssertions(content, node.Validate.Assert)
	if !ok {
		return content, failure, nil
	}
	return content, nil, nil
}

// validateNodeReady performs one bounded readiness check, then polls only when
// the document explicitly supplies a validation timeout. A failed preflight
// with no timeout is intentionally returned immediately so a mutation can be
// applied; an action with a timeout gets honest, bounded readiness polling.
func (r Runner) validateNodeReady(ctx context.Context, doc Document, node Node, item map[string]any, outputs map[string]any, contexts ...map[string]ContextEntry) (any, *AssertionFailure, error) {
	if node.Validate == nil {
		return nil, nil, nil
	}
	result, failure, err := r.validateNode(ctx, doc, node, item, outputs, contexts...)
	if err != nil || failure == nil || node.Validate.TimeoutMs <= 0 {
		return result, failure, err
	}
	validationCtx, cancel := context.WithTimeout(ctx, time.Duration(node.Validate.TimeoutMs)*time.Millisecond)
	defer cancel()
	interval := node.Validate.PollIntervalMs
	if interval <= 0 {
		interval = 250
	}
	ticker := time.NewTicker(time.Duration(interval) * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-validationCtx.Done():
			return result, failure, nil
		case <-ticker.C:
			result, failure, err = r.validateNode(validationCtx, doc, node, item, outputs, contexts...)
			if err != nil || failure == nil {
				return result, failure, err
			}
		}
	}
}

func (r Runner) dispatch(ctx context.Context, name string, args map[string]any) (*mcp.CallToolResult, error) {
	if r.Dispatch == nil {
		return nil, fmt.Errorf("plan dispatcher is required")
	}
	result, err := r.Dispatch(ctx, name, args, func(string) {})
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, fmt.Errorf("tool %s returned no result", name)
	}
	if result.IsError {
		return nil, fmt.Errorf("tool %s returned an error: %s", name, resultText(result))
	}
	return result, nil
}

func (r Runner) validateArgs(name string, args map[string]any) error {
	capability, ok := r.Capabilities[name]
	if !ok {
		return fmt.Errorf("unknown plan capability %q", name)
	}
	if err := ValidateJSON(capability.InputSchema, args); err != nil {
		return fmt.Errorf("capability %s arguments: %w", name, err)
	}
	return nil
}

func (r Runner) validateOutput(name string, value any) error {
	capability, ok := r.Capabilities[name]
	if !ok || len(capability.OutputSchema) == 0 {
		return nil
	}
	if err := ValidateJSON(capability.OutputSchema, value); err != nil {
		return fmt.Errorf("capability %s result: %w", name, err)
	}
	return nil
}

func (r Runner) compensate(node Node, doc Document, state *RunState) error {
	if node.Compensate == nil {
		return nil
	}
	args, err := InterpolateArgs(node.Compensate.Args, r.evalContext(doc, state, nil))
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := r.validateArgs(node.Compensate.Tool, args); err != nil {
		state.Nodes[node.ID] = NodeRunState{ID: node.ID, Status: StatusCompensationFailed, Error: err.Error()}
		_ = r.emit(*state)
		return err
	}
	if _, err := r.dispatch(ctx, node.Compensate.Tool, args); err != nil {
		current := state.Nodes[node.ID]
		current.ID = node.ID
		current.Status = StatusCompensationFailed
		current.Error = err.Error()
		state.Nodes[node.ID] = current
		_ = r.emit(*state)
		return err
	}
	current := state.Nodes[node.ID]
	current.ID = node.ID
	current.Status = StatusCompensated
	current.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
	state.Nodes[node.ID] = current
	return r.emit(*state)
}

func (r Runner) compensateApplied(doc Document, state *RunState) error {
	ordered, err := ReverseTopologicalNodes(doc)
	if err != nil {
		return err
	}
	var firstErr error
	for _, node := range ordered {
		current := state.Nodes[node.ID]
		if current.Status != StatusApplied || node.Compensate == nil {
			continue
		}
		if err := r.compensate(node, doc, state); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (r Runner) emit(state RunState) error {
	if r.Sink == nil {
		return nil
	}
	return r.Sink(redactedRunState(state))
}

func (r Runner) evalContext(doc Document, state *RunState, item map[string]any) EvalContext {
	return evaluationContext(doc, state.Outputs, state.Context, item)
}

func evaluationContext(doc Document, outputs map[string]any, contexts map[string]ContextEntry, item map[string]any) EvalContext {
	return EvalContext{
		Variables:  doc.Variables,
		NodeOutput: outputs,
		Item:       item,
		Context:    contextValues(contexts),
	}
}

func firstContext(contexts []map[string]ContextEntry) map[string]ContextEntry {
	if len(contexts) == 0 {
		return nil
	}
	return contexts[0]
}

func contextValues(entries map[string]ContextEntry) map[string]any {
	values := make(map[string]any, len(entries))
	for name, entry := range entries {
		values[name] = entry.Value
	}
	return values
}

func redactedRunState(state RunState) RunState {
	redacted := cloneRunState(state)
	for name, entry := range redacted.Context {
		if entry.Secret {
			entry.Value = nil
			redacted.Context[name] = entry
		}
	}
	for index, entry := range redacted.ContextHistory {
		if entry.Secret {
			entry.Value = nil
			redacted.ContextHistory[index] = entry
		}
	}
	return redacted
}

func (r Runner) validateTarget(doc Document, node Node, state *RunState) error {
	if node.Target == nil || strings.TrimSpace(r.HostAgentID) == "" {
		return nil
	}
	value, err := interpolateValue(node.Target.HostRef, r.evalContext(doc, state, nil))
	if err != nil {
		return fmt.Errorf("target hostRef: %w", err)
	}
	hostRef, ok := value.(string)
	if !ok || strings.TrimSpace(hostRef) == "" {
		return fmt.Errorf("target hostRef must resolve to a non-empty string")
	}
	if hostRef != r.HostAgentID {
		return fmt.Errorf("target hostRef %q does not match Host Agent %q", hostRef, r.HostAgentID)
	}
	return nil
}

func blockedByDependency(node Node, states map[string]NodeRunState) bool {
	for _, dependency := range node.DependsOn {
		status := states[dependency].Status
		if status == StatusFailed || status == StatusUnknown || status == StatusSkipped || status == StatusCompensationFailed || status == StatusWaiting || status == StatusExpired {
			return true
		}
	}
	return false
}

// dependencyApplied reports whether a dependency executed its action during
// the current reconciliation pass. StatusApplied is distinct from
// StatusSatisfied: the latter means the existing observation was enough,
// while the former means a mutation ran and downstream preflight observations
// are no longer reusable.
func dependencyApplied(states map[string]NodeRunState, node Node) bool {
	for _, dependency := range node.DependsOn {
		if states[dependency].Status == StatusApplied {
			return true
		}
	}
	return false
}

func failedNode(state NodeRunState, err error) NodeRunState {
	state.Status = StatusFailed
	state.Error = err.Error()
	state.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
	return state
}

func sleepBackoff(ctx context.Context, node Retry, defaults Retry, attempt int) error {
	backoff := node.BackoffMs
	if backoff <= 0 {
		backoff = defaults.BackoffMs
	}
	if backoff <= 0 {
		return nil
	}
	factor := node.BackoffFactor
	if factor <= 0 {
		factor = defaults.BackoffFactor
	}
	if factor <= 0 {
		factor = 2
	}
	delay := time.Duration(backoff) * time.Millisecond
	for index := 1; index < attempt; index++ {
		delay *= time.Duration(factor)
		if delay > 5*time.Minute {
			delay = 5 * time.Minute
			break
		}
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func structuredContent(result *mcp.CallToolResult) any {
	if result == nil || result.StructuredContent == nil {
		return map[string]any{}
	}
	// MCP implementations may return a typed Go struct even though the wire
	// contract is JSON. Normalize it once at the plan boundary so output-schema
	// validation and downstream interpolation observe the same JSON shape.
	encoded, err := json.Marshal(result.StructuredContent)
	if err != nil {
		return result.StructuredContent
	}
	var value any
	if err := json.Unmarshal(encoded, &value); err != nil {
		return result.StructuredContent
	}
	return value
}

func resultText(result *mcp.CallToolResult) string {
	if result == nil {
		return ""
	}
	for _, content := range result.Content {
		if text, ok := content.(*mcp.TextContent); ok && strings.TrimSpace(text.Text) != "" {
			return text.Text
		}
	}
	return "operation failed"
}

func resolveSource(source string, doc Document, state *RunState) (any, error) {
	if strings.HasPrefix(source, "${") && strings.HasSuffix(source, "}") {
		value, err := resolveReference(strings.TrimSuffix(strings.TrimPrefix(source, "${"), "}"), evaluationContext(doc, state.Outputs, state.Context, nil))
		if err != nil {
			return nil, err
		}
		return value, nil
	}
	return nil, fmt.Errorf("forEach source must be an interpolation reference")
}

func stateJSON(state RunState) string {
	encoded, _ := json.Marshal(state)
	return string(encoded)
}
