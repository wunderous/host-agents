package plan

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func testCapability(name, effect string, required ...string) Capability {
	properties := map[string]any{}
	for _, property := range required {
		properties[property] = map[string]any{"type": "string"}
	}
	schema := map[string]any{"type": "object", "properties": properties}
	if len(required) > 0 {
		schema["required"] = required
	}
	return Capability{Name: name, InputSchema: schema, Effect: effect}
}

func callResult(value any) *mcp.CallToolResult {
	return &mcp.CallToolResult{StructuredContent: value}
}

func baseDocument(nodes ...Node) Document {
	return Document{
		ContractVersion: ContractVersion,
		PlanID:          "test-plan",
		Generation:      1,
		IdempotencyKey:  "test-run",
		Nodes:           nodes,
	}
}

func TestRunnerValidateFirstMakesSecondRunSatisfyWithoutMutation(t *testing.T) {
	var ready atomic.Bool
	var mutations atomic.Int32
	var checks atomic.Int32
	caps := map[string]Capability{
		"ensure": testCapability("ensure", "mutation"),
		"check":  testCapability("check", "read"),
	}
	doc := baseDocument(Node{
		ID:     "service",
		Action: &Action{Tool: "ensure"},
		Validate: &Validation{
			Tool:   "check",
			Assert: []Assertion{{Path: "/ready", Op: "eq", Value: true}},
		},
	})
	dispatch := func(_ context.Context, name string, _ map[string]any, _ func(string)) (*mcp.CallToolResult, error) {
		switch name {
		case "check":
			checks.Add(1)
			return callResult(map[string]any{"ready": ready.Load()}), nil
		case "ensure":
			mutations.Add(1)
			ready.Store(true)
			return callResult(map[string]any{"applied": true}), nil
		default:
			return nil, errors.New("unexpected tool")
		}
	}
	runner := Runner{Dispatch: dispatch, Capabilities: caps}
	state, err := runner.Run(context.Background(), doc, RunState{RunID: "run-1"})
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	if mutations.Load() != 1 {
		t.Fatalf("mutation calls after first run = %d, want 1", mutations.Load())
	}
	state, err = runner.Run(context.Background(), doc, state)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if mutations.Load() != 1 {
		t.Fatalf("mutation calls after second run = %d, want 1", mutations.Load())
	}
	if state.Nodes["service"].Status != StatusSatisfied {
		t.Fatalf("second run status = %q, want satisfied", state.Nodes["service"].Status)
	}
	if checks.Load() < 2 {
		t.Fatalf("readiness checks = %d, want at least 2", checks.Load())
	}
}

func TestRunnerRecordsFailedNodeAfterReadinessExhaustion(t *testing.T) {
	caps := map[string]Capability{
		"apply": testCapability("apply", "mutation"),
		"check": testCapability("check", "read"),
	}
	doc := baseDocument(Node{
		ID:     "service",
		Action: &Action{Tool: "apply"},
		Validate: &Validation{
			Tool:   "check",
			Assert: []Assertion{{Path: "/ready", Op: "eq", Value: true}},
		},
	})
	dispatch := func(_ context.Context, name string, _ map[string]any, _ func(string)) (*mcp.CallToolResult, error) {
		switch name {
		case "check":
			return callResult(map[string]any{"ready": false}), nil
		case "apply":
			return callResult(map[string]any{"applied": true}), nil
		default:
			return nil, errors.New("unexpected tool")
		}
	}

	state, err := (Runner{Dispatch: dispatch, Capabilities: caps}).Run(context.Background(), doc, RunState{RunID: "readiness-failure"})
	if err == nil {
		t.Fatal("readiness failure unexpectedly succeeded")
	}
	node := state.Nodes["service"]
	if node.Status != StatusFailed {
		t.Fatalf("node status = %q, want failed", node.Status)
	}
	if node.Error == "" || node.CompletedAt == "" {
		t.Fatalf("failed node lacks durable error/timestamp: %#v", node)
	}
}

func TestRunnerRecordsFailedValidationOnlyNode(t *testing.T) {
	caps := map[string]Capability{"check": testCapability("check", "read")}
	doc := baseDocument(Node{
		ID: "check",
		Validate: &Validation{
			Tool:   "check",
			Assert: []Assertion{{Path: "/ready", Op: "eq", Value: true}},
		},
	})
	dispatch := func(_ context.Context, _ string, _ map[string]any, _ func(string)) (*mcp.CallToolResult, error) {
		return callResult(map[string]any{"ready": false}), nil
	}

	state, err := (Runner{Dispatch: dispatch, Capabilities: caps}).Run(context.Background(), doc, RunState{RunID: "validation-only-failure"})
	if err == nil {
		t.Fatal("validation-only failure unexpectedly succeeded")
	}
	node := state.Nodes["check"]
	if node.Status != StatusFailed {
		t.Fatalf("node status = %q, want failed", node.Status)
	}
	if node.Error == "" || node.CompletedAt == "" {
		t.Fatalf("failed validation-only node lacks durable error/timestamp: %#v", node)
	}
}

func TestRunnerParallelIndependentNodes(t *testing.T) {
	var active atomic.Int32
	var maxActive atomic.Int32
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	dispatch := func(_ context.Context, name string, _ map[string]any, _ func(string)) (*mcp.CallToolResult, error) {
		if name != "probe" {
			return nil, errors.New("unexpected tool")
		}
		current := active.Add(1)
		for {
			previous := maxActive.Load()
			if current <= previous || maxActive.CompareAndSwap(previous, current) {
				break
			}
		}
		started <- struct{}{}
		<-release
		active.Add(-1)
		return callResult(map[string]any{"ok": true}), nil
	}
	caps := map[string]Capability{"probe": testCapability("probe", "read")}
	doc := baseDocument(
		Node{ID: "a", Action: &Action{Tool: "probe"}},
		Node{ID: "b", Action: &Action{Tool: "probe"}},
	)
	doc.Converge.MaxConcurrency = 2
	finished := make(chan struct{})
	var result RunState
	var runErr error
	go func() {
		result, runErr = (Runner{Dispatch: dispatch, Capabilities: caps}).Run(context.Background(), doc, RunState{RunID: "parallel"})
		close(finished)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first parallel node did not start")
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		close(release)
		<-finished
		t.Fatal("second independent node did not start concurrently")
	}
	close(release)
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("parallel run did not finish")
	}
	if runErr != nil || result.Status != "completed" {
		t.Fatalf("parallel run = status %q err %v", result.Status, runErr)
	}
	if maxActive.Load() != 2 {
		t.Fatalf("maximum active dispatches = %d, want 2", maxActive.Load())
	}
}

func TestRunnerCompensatesAppliedNodesInReverseOrder(t *testing.T) {
	var mu sync.Mutex
	var calls []string
	dispatch := func(_ context.Context, name string, _ map[string]any, _ func(string)) (*mcp.CallToolResult, error) {
		mu.Lock()
		calls = append(calls, name)
		mu.Unlock()
		if name == "fail" {
			return nil, errors.New("boom")
		}
		return callResult(map[string]any{"ok": true}), nil
	}
	caps := map[string]Capability{
		"apply": testCapability("apply", "read"),
		"fail":  testCapability("fail", "read"),
		"undo":  testCapability("undo", "read"),
	}
	doc := baseDocument(
		Node{ID: "a", Action: &Action{Tool: "apply"}, Compensate: &Action{Tool: "undo"}},
		Node{ID: "b", DependsOn: []string{"a"}, Action: &Action{Tool: "apply"}, Compensate: &Action{Tool: "undo"}},
		Node{ID: "c", DependsOn: []string{"b"}, Action: &Action{Tool: "fail"}},
	)
	_, err := (Runner{Dispatch: dispatch, Capabilities: caps}).Run(context.Background(), doc, RunState{RunID: "compensate"})
	if err == nil {
		t.Fatal("run unexpectedly succeeded")
	}
	mu.Lock()
	got := append([]string(nil), calls...)
	mu.Unlock()
	want := []string{"apply", "apply", "fail", "undo", "undo"}
	if len(got) != len(want) {
		t.Fatalf("calls = %#v, want %#v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("calls = %#v, want %#v", got, want)
		}
	}
}

func TestValidateRejectsUnknownNodeReferenceAndTypedInput(t *testing.T) {
	caps := map[string]Capability{"probe": testCapability("probe", "read", "name")}
	doc := baseDocument(Node{ID: "node", Action: &Action{Tool: "probe", Args: map[string]any{"name": "${nodes:missing}"}}})
	if err := Validate(doc, caps, ""); err == nil {
		t.Fatal("unknown node reference was accepted")
	}
	doc.Nodes[0].Action.Args = map[string]any{"name": 42}
	if err := Validate(doc, caps, ""); err == nil {
		t.Fatal("typed input mismatch was accepted")
	}
}

func TestValidateRejectsIncompatibleProducerOutputReference(t *testing.T) {
	producer := testCapability("producer", "read")
	producer.OutputSchema = map[string]any{
		"type":       "object",
		"properties": map[string]any{"name": map[string]any{"type": "string"}},
	}
	consumer := testCapability("consumer", "read", "count")
	consumer.InputSchema["properties"].(map[string]any)["count"] = map[string]any{"type": "integer"}
	doc := baseDocument(
		Node{ID: "producer", Action: &Action{Tool: "producer"}},
		Node{ID: "consumer", DependsOn: []string{"producer"}, Action: &Action{Tool: "consumer", Args: map[string]any{"count": "${nodes.producer.output.name}"}}},
	)
	if err := Validate(doc, map[string]Capability{"producer": producer, "consumer": consumer}, ""); err == nil {
		t.Fatal("incompatible producer/consumer reference was accepted")
	}
}

func TestRunnerValidationPollingIsBoundedAndCanObserveReadiness(t *testing.T) {
	var checks atomic.Int32
	caps := map[string]Capability{
		"check": testCapability("check", "read"),
	}
	doc := baseDocument(Node{ID: "ready", Validate: &Validation{
		Tool: "check", TimeoutMs: 100, PollIntervalMs: 1,
		Assert: []Assertion{{Path: "/ready", Op: "eq", Value: true}},
	}})
	dispatch := func(_ context.Context, _ string, _ map[string]any, _ func(string)) (*mcp.CallToolResult, error) {
		return callResult(map[string]any{"ready": checks.Add(1) > 1}), nil
	}
	state, err := (Runner{Dispatch: dispatch, Capabilities: caps}).Run(context.Background(), doc, RunState{RunID: "poll"})
	if err != nil || state.Nodes["ready"].Status != StatusSatisfied {
		t.Fatalf("bounded readiness run = %#v err=%v", state.Nodes["ready"], err)
	}
	if checks.Load() < 2 {
		t.Fatalf("readiness checks = %d, want at least 2", checks.Load())
	}
}

func TestRunnerConvergesAcrossPassesAfterReadinessRegression(t *testing.T) {
	var checks atomic.Int32
	var mutations atomic.Int32
	caps := map[string]Capability{
		"ensure": testCapability("ensure", "mutation"),
		"check":  testCapability("check", "read"),
	}
	caps["ensure"] = Capability{Name: "ensure", InputSchema: caps["ensure"].InputSchema, OutputSchema: map[string]any{"type": "object"}, Effect: "mutation", Idempotent: true}
	doc := baseDocument(Node{
		ID:     "service",
		Action: &Action{Tool: "ensure"},
		Validate: &Validation{
			Tool:   "check",
			Assert: []Assertion{{Path: "/ready", Op: "eq", Value: true}},
		},
	})
	doc.Converge.MaxPasses = 2
	dispatch := func(_ context.Context, name string, _ map[string]any, _ func(string)) (*mcp.CallToolResult, error) {
		switch name {
		case "ensure":
			mutations.Add(1)
			return callResult(map[string]any{"applied": true}), nil
		case "check":
			call := checks.Add(1)
			// Initial preflight fails, post-action validation passes, the
			// convergence sweep observes a regression, and the next pass
			// preflight observes readiness again.
			return callResult(map[string]any{"ready": call == 2 || call >= 4}), nil
		default:
			return nil, errors.New("unexpected tool")
		}
	}
	state, err := (Runner{Dispatch: dispatch, Capabilities: caps}).Run(context.Background(), doc, RunState{RunID: "converge"})
	if err != nil {
		t.Fatalf("convergence run: %v", err)
	}
	if state.Status != "completed" || state.Nodes["service"].Status != StatusSatisfied {
		t.Fatalf("convergence state = %#v", state)
	}
	if mutations.Load() != 1 {
		t.Fatalf("mutation calls = %d, want 1", mutations.Load())
	}
	if checks.Load() != 4 {
		t.Fatalf("readiness checks = %d, want 4", checks.Load())
	}
}

func TestRunnerRecoveryRetriesAndRevalidates(t *testing.T) {
	var recoveries atomic.Int32
	ready := atomic.Bool{}
	caps := map[string]Capability{
		"apply":   testCapability("apply", "mutation"),
		"check":   testCapability("check", "read"),
		"recover": testCapability("recover", "mutation"),
	}
	caps["recover"] = Capability{Name: "recover", InputSchema: caps["recover"].InputSchema, Effect: "mutation", Idempotent: true}
	doc := baseDocument(Node{
		ID:     "service",
		Action: &Action{Tool: "apply"},
		Validate: &Validation{
			Tool:   "check",
			Assert: []Assertion{{Path: "/ready", Op: "eq", Value: true}},
		},
		Recover: &Recovery{Action: Action{Tool: "recover"}, MaxAttempts: 2},
	})
	dispatch := func(_ context.Context, name string, _ map[string]any, _ func(string)) (*mcp.CallToolResult, error) {
		switch name {
		case "apply":
			return callResult(map[string]any{"applied": true}), nil
		case "check":
			return callResult(map[string]any{"ready": ready.Load()}), nil
		case "recover":
			if recoveries.Add(1) == 2 {
				ready.Store(true)
			}
			return callResult(map[string]any{"recovered": true}), nil
		default:
			return nil, errors.New("unexpected tool")
		}
	}
	state, err := (Runner{Dispatch: dispatch, Capabilities: caps}).Run(context.Background(), doc, RunState{RunID: "recover"})
	if err != nil {
		t.Fatalf("recovery run: %v", err)
	}
	if state.Nodes["service"].Status != StatusApplied {
		t.Fatalf("recovery node status = %q, want applied", state.Nodes["service"].Status)
	}
	if recoveries.Load() != 2 {
		t.Fatalf("recovery calls = %d, want 2", recoveries.Load())
	}
}

func TestRunnerForEachUsesBoundedActionRetries(t *testing.T) {
	var calls atomic.Int32
	caps := map[string]Capability{
		"apply": testCapability("apply", "read", "name"),
	}
	doc := baseDocument(Node{
		ID:      "services",
		Retry:   Retry{MaxAttempts: 2},
		Action:  &Action{Tool: "apply", Args: map[string]any{"name": "${item.name}"}},
		ForEach: &ForEach{Source: "${vars.items}", As: "item"},
	})
	doc.Variables = map[string]any{"items": []any{map[string]any{"name": "one"}}}
	dispatch := func(_ context.Context, name string, args map[string]any, _ func(string)) (*mcp.CallToolResult, error) {
		if name != "apply" || args["name"] != "one" {
			return nil, errors.New("unexpected forEach action")
		}
		if calls.Add(1) == 1 {
			return nil, errors.New("transient action failure")
		}
		return callResult(map[string]any{"name": args["name"]}), nil
	}
	state, err := (Runner{Dispatch: dispatch, Capabilities: caps}).Run(context.Background(), doc, RunState{RunID: "foreach-retry"})
	if err != nil {
		t.Fatalf("forEach retry run: %v", err)
	}
	if calls.Load() != 2 || state.Nodes["services"].Status != StatusApplied {
		t.Fatalf("forEach retry state = %#v calls=%d", state.Nodes["services"], calls.Load())
	}
}

func TestRunnerForEachUsesBoundedRecoveryPerItem(t *testing.T) {
	var recoveries atomic.Int32
	var ready atomic.Bool
	caps := map[string]Capability{
		"apply":   testCapability("apply", "mutation", "name"),
		"check":   testCapability("check", "read", "name"),
		"recover": testCapability("recover", "mutation", "name"),
	}
	caps["apply"] = Capability{Name: "apply", InputSchema: caps["apply"].InputSchema, Effect: "mutation", Idempotent: true}
	caps["recover"] = Capability{Name: "recover", InputSchema: caps["recover"].InputSchema, Effect: "mutation", Idempotent: true}
	doc := baseDocument(Node{
		ID:     "services",
		Action: &Action{Tool: "apply", Args: map[string]any{"name": "${item.name}"}},
		Validate: &Validation{
			Tool: "check", Args: map[string]any{"name": "${item.name}"},
			Assert: []Assertion{{Path: "/ready", Op: "eq", Value: true}},
		},
		Recover: &Recovery{Action: Action{Tool: "recover", Args: map[string]any{"name": "${item.name}"}}, MaxAttempts: 2},
		ForEach: &ForEach{Source: "${vars.items}", As: "item"},
	})
	doc.Variables = map[string]any{"items": []any{map[string]any{"name": "one"}}}
	dispatch := func(_ context.Context, name string, args map[string]any, _ func(string)) (*mcp.CallToolResult, error) {
		if args["name"] != "one" {
			return nil, errors.New("unexpected forEach argument")
		}
		switch name {
		case "apply":
			return callResult(map[string]any{"applied": true}), nil
		case "check":
			return callResult(map[string]any{"ready": ready.Load()}), nil
		case "recover":
			if recoveries.Add(1) == 2 {
				ready.Store(true)
			}
			return callResult(map[string]any{"recovered": true}), nil
		default:
			return nil, errors.New("unexpected forEach tool")
		}
	}
	state, err := (Runner{Dispatch: dispatch, Capabilities: caps}).Run(context.Background(), doc, RunState{RunID: "foreach-recovery"})
	if err != nil {
		t.Fatalf("forEach recovery run: %v", err)
	}
	if recoveries.Load() != 2 || state.Nodes["services"].Status != StatusApplied {
		t.Fatalf("forEach recovery state = %#v recoveries=%d", state.Nodes["services"], recoveries.Load())
	}
}

func TestRunnerCancellationMarksUnknownAndResumeReconciles(t *testing.T) {
	started := make(chan struct{})
	dispatch := func(ctx context.Context, _ string, _ map[string]any, _ func(string)) (*mcp.CallToolResult, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	doc := baseDocument(Node{ID: "blocked", Action: &Action{Tool: "probe"}})
	caps := map[string]Capability{"probe": testCapability("probe", "read")}
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan RunState, 1)
	go func() {
		state, _ := (Runner{Dispatch: dispatch, Capabilities: caps}).Run(ctx, doc, RunState{RunID: "cancel"})
		finished <- state
	}()
	<-started
	cancel()
	unknown := <-finished
	if unknown.Status != "unknown" || unknown.Nodes["blocked"].Status != StatusUnknown {
		t.Fatalf("cancelled state = %#v", unknown)
	}

	resumed, err := (Runner{
		Dispatch: func(context.Context, string, map[string]any, func(string)) (*mcp.CallToolResult, error) {
			return callResult(map[string]any{"ok": true}), nil
		},
		Capabilities: caps,
	}).Run(context.Background(), doc, unknown)
	if err != nil || resumed.Status != "completed" || resumed.Nodes["blocked"].Status != StatusApplied {
		t.Fatalf("resumed state = %#v err=%v", resumed, err)
	}
}
