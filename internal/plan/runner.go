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
	Sink            EventSink
}

func (r Runner) Run(ctx context.Context, doc Document, state RunState) (RunState, error) {
	if err := Validate(doc, r.Capabilities, r.CatalogRevision); err != nil {
		return state, err
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
				result, failure, err := r.validateNodeReady(ctx, doc, node, nil, state.Outputs)
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
	if failure, ok := EvaluateAssertions(map[string]any{"vars": doc.Variables, "nodes": state.Outputs}, node.When); !ok && len(node.When) > 0 {
		state.Nodes[node.ID] = NodeRunState{ID: node.ID, Status: StatusSkipped, Error: failure.Message}
		return nil
	}
	if previous := state.Nodes[node.ID]; previous.Status == StatusApplied || previous.Status == StatusSatisfied {
		if node.Validate != nil {
			result, failure, validateErr := r.validateNodeReady(ctx, doc, node, nil, state.Outputs)
			if validateErr != nil {
				return validateErr
			}
			if failure == nil {
				previous.Status = StatusSatisfied
				previous.Output = result
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
	return r.runNode(ctx, doc, node, state)
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
	return clone
}

func (r Runner) runNode(ctx context.Context, doc Document, node Node, state *RunState) error {
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

	if node.ForEach != nil {
		err := r.runForEach(nodeCtx, doc, node, state)
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
	if node.Validate != nil {
		result, failure, err := r.validateNodeReady(nodeCtx, doc, node, nil, state.Outputs)
		if err != nil {
			state.Nodes[node.ID] = failedNode(nodeState, err)
			_ = r.emit(*state)
			return err
		}
		if failure == nil {
			nodeState.Status = StatusSatisfied
			nodeState.Output = result
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
		args, err := InterpolateArgs(node.Action.Args, EvalContext{Variables: doc.Variables, NodeOutput: state.Outputs})
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
		_, failure, validateErr := r.validateNodeReady(nodeCtx, doc, node, nil, state.Outputs)
		if validateErr == nil && failure == nil {
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
				recoverArgs, interpolationErr := InterpolateArgs(node.Recover.Args, EvalContext{Variables: doc.Variables, NodeOutput: state.Outputs})
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
				_, recoveryFailure, recoveryValidationErr := r.validateNodeReady(nodeCtx, doc, node, nil, state.Outputs)
				if recoveryValidationErr == nil && recoveryFailure == nil {
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

func (r Runner) runForEach(ctx context.Context, doc Document, node Node, state *RunState) error {
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
		itemOutput, err := r.runForEachItem(ctx, doc, node, itemMap, state.Outputs)
		if err != nil {
			return err
		}
		outputs = append(outputs, itemOutput)
	}
	state.Outputs[node.ID] = outputs
	state.Nodes[node.ID] = NodeRunState{ID: node.ID, Status: StatusApplied, Output: outputs, CompletedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	return r.emit(*state)
}

func (r Runner) runForEachItem(ctx context.Context, doc Document, node Node, item map[string]any, outputs map[string]any) (any, error) {
	attempts := attemptsFor(node, doc)
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if node.Validate != nil {
			validated, failure, err := r.validateNodeReady(ctx, doc, node, item, outputs)
			if err != nil {
				lastErr = err
			} else if failure == nil {
				return validated, nil
			} else {
				lastErr = fmt.Errorf("forEach node %q did not reach readiness: %s", node.ID, failure.Message)
			}
		}
		args, err := InterpolateArgs(node.Action.Args, EvalContext{Variables: doc.Variables, NodeOutput: outputs, Item: item})
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
			_, failure, validateErr := r.validateNodeReady(ctx, doc, node, item, outputs)
			if validateErr == nil && failure == nil {
				return itemOutput, nil
			}
			if validateErr != nil {
				lastErr = validateErr
			} else {
				lastErr = fmt.Errorf("forEach node %q did not reach readiness: %s", node.ID, failure.Message)
			}
			if node.Recover != nil {
				if recovered, recoveryErr := r.recoverForEachItem(ctx, doc, node, item, outputs); recoveryErr == nil && recovered {
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

func (r Runner) recoverForEachItem(ctx context.Context, doc Document, node Node, item map[string]any, outputs map[string]any) (bool, error) {
	recoveryAttempts := node.Recover.MaxAttempts
	if recoveryAttempts <= 0 {
		recoveryAttempts = 1
	}
	var lastErr error
	for attempt := 1; attempt <= recoveryAttempts; attempt++ {
		args, err := InterpolateArgs(node.Recover.Args, EvalContext{Variables: doc.Variables, NodeOutput: outputs, Item: item})
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
			_, failure, validationErr := r.validateNodeReady(ctx, doc, node, item, outputs)
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

func (r Runner) validateNode(ctx context.Context, doc Document, node Node, item map[string]any, outputs map[string]any) (any, *AssertionFailure, error) {
	if node.Validate == nil {
		return nil, nil, nil
	}
	args, err := InterpolateArgs(node.Validate.Args, EvalContext{Variables: doc.Variables, NodeOutput: outputs, Item: item})
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
func (r Runner) validateNodeReady(ctx context.Context, doc Document, node Node, item map[string]any, outputs map[string]any) (any, *AssertionFailure, error) {
	if node.Validate == nil {
		return nil, nil, nil
	}
	result, failure, err := r.validateNode(ctx, doc, node, item, outputs)
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
			result, failure, err = r.validateNode(validationCtx, doc, node, item, outputs)
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
	args, err := InterpolateArgs(node.Compensate.Args, EvalContext{Variables: doc.Variables, NodeOutput: state.Outputs})
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
	return r.Sink(state)
}

func blockedByDependency(node Node, states map[string]NodeRunState) bool {
	for _, dependency := range node.DependsOn {
		status := states[dependency].Status
		if status == StatusFailed || status == StatusUnknown || status == StatusSkipped || status == StatusCompensationFailed {
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
		value, err := resolveReference(strings.TrimSuffix(strings.TrimPrefix(source, "${"), "}"), EvalContext{Variables: doc.Variables, NodeOutput: state.Outputs})
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
