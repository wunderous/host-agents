package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wunderous/host-agents/internal/plan"
	"github.com/wunderous/host-agents/internal/session"
)

type Config struct {
	Endpoint    string
	Token       string
	Transport   mcp.Transport
	PlanDir     string
	In          io.Reader
	Out         io.Writer
	Err         io.Writer
	AutoApprove bool
	NoPrompt    bool
}

type App struct {
	config        Config
	client        *Client
	catalog       *Catalog
	entities      *EntityIndex
	assistant     *Assistant
	platform      *PlatformAssistant
	assistantOn   bool
	sessionID     string
	sequence      int
	history       []session.ToolCallHistory
	trace         []session.Event
	platformTrace []PlatformTraceEvent
	input         lineReader
	pollStates    map[string]string
}

func Run(ctx context.Context, config Config) error {
	if useBubbleTea(config) {
		return runBubbleTea(ctx, config)
	}
	app, err := New(ctx, config)
	if err != nil {
		return err
	}
	defer app.Close()
	return app.Loop(ctx)
}

func New(ctx context.Context, config Config) (*App, error) {
	config = normalizeConfig(config)
	input, err := newLineReader(config.In, config.Out, config.NoPrompt)
	if err != nil {
		return nil, err
	}
	return newWithInput(ctx, config, input)
}

func NewInteractive(ctx context.Context, config Config) (*App, *interactiveIO, error) {
	config = normalizeConfig(config)
	input := newInteractiveIO(ctx)
	config.Out = input
	app, err := newWithInput(ctx, config, input)
	if err != nil {
		_ = input.Close()
		return nil, nil, err
	}
	return app, input, nil
}

func normalizeConfig(config Config) Config {
	if config.In == nil {
		config.In = os.Stdin
	}
	if config.Out == nil {
		config.Out = os.Stdout
	}
	if config.Err == nil {
		config.Err = os.Stderr
	}
	if config.PlanDir == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			base = os.TempDir()
		}
		config.PlanDir = filepath.Join(base, "opute-host-agent", "plans")
	}
	return config
}

func newWithInput(ctx context.Context, config Config, input lineReader) (*App, error) {
	var client *Client
	var err error
	if config.Transport != nil {
		client, err = ConnectWithTransport(ctx, config.Transport)
	} else {
		client, err = Connect(ctx, config.Endpoint, config.Token)
	}
	if err != nil {
		return nil, err
	}
	app := &App{config: config, client: client, entities: NewEntityIndex(nil), assistant: AssistantFromEnvironment(), platform: PlatformAssistantFromEnvironment(), sessionID: uuid.NewString(), input: input, pollStates: make(map[string]string)}
	if err := app.refresh(ctx); err != nil {
		_ = input.Close()
		_ = client.Close()
		return nil, err
	}
	if err := client.OpenSession(ctx, app.sessionID, app.catalog.Snapshot.Revision); err != nil {
		_ = input.Close()
		_ = client.Close()
		return nil, err
	}
	return app, nil
}

func (a *App) Close() error {
	if a == nil {
		return nil
	}
	var inputErr error
	if a.input != nil {
		inputErr = a.input.Close()
	}
	clientErr := a.client.Close()
	if inputErr != nil {
		return inputErr
	}
	return clientErr
}

func (a *App) Loop(ctx context.Context) error {
	a.println("opute-host-agent connected; deterministic mode is ready (type /help for commands)")
	for {
		line, err := a.input.ReadLine("opute-host-agent> ", a.completions)
		if err == io.EOF {
			return nil
		}
		if err == ErrInputInterrupted {
			a.println("")
			continue
		}
		if err != nil {
			return err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if line == "/exit" || line == "/quit" || line == "exit" || line == "quit" {
			a.println("bye")
			return nil
		}
		if err := a.handle(ctx, line); err != nil {
			a.println("error: " + err.Error())
		}
	}
}

func (a *App) readLine(prompt string) (string, error) {
	if a == nil || a.input == nil {
		return "", io.EOF
	}
	return a.input.ReadLine(prompt, a.completions)
}

func (a *App) completions(line string) []string {
	if a == nil {
		return nil
	}
	return CompleteWithEntities(line, a.catalog, a.entities)
}

func (a *App) handle(ctx context.Context, line string) error {
	if !strings.HasPrefix(line, "/") && !strings.HasPrefix(line, "setup ") && !strings.HasPrefix(line, "setup\t") {
		command, err := ParseCommand(line)
		if err != nil {
			return err
		}
		if _, ok := a.catalog.Get(command.Name); ok {
			return a.callCapability(ctx, command.Name, command.Arguments, true)
		}
		if !a.assistantOn {
			return fmt.Errorf("deterministic mode accepts a capability command; use /assistant on for structured natural-language proposals")
		}
		return a.handleAssistant(ctx, line)
	}
	command, err := ParseCommand(line)
	if err != nil {
		return err
	}
	if command.Name == "setup" {
		return a.handleSetup(ctx, command)
	}
	switch command.Name {
	case "help":
		a.printHelp()
		return nil
	case "tools":
		for _, name := range a.catalog.Names("") {
			a.println(name)
		}
		return nil
	case "describe":
		name := command.Subcommand()
		if name == "" {
			return fmt.Errorf("usage: /describe <capability>")
		}
		return a.printJSON(a.catalog.byName[name])
	case "context":
		return a.printJSON(map[string]any{"sessionId": a.sessionID, "catalogRevision": a.catalog.Snapshot.Revision, "historyEntries": len(a.history), "assistant": a.assistantOn})
	case "trace":
		return a.printJSON(map[string]any{"host": a.trace, "platform": a.platformTrace})
	case "refresh":
		return a.refresh(ctx)
	case "assistant":
		return a.toggleAssistant(command.Subcommand())
	case "model":
		return a.modelCommand(ctx, command.Subcommand())
	default:
		return a.callCapability(ctx, command.Name, command.Arguments, true)
	}
}

func (a *App) handleSetup(ctx context.Context, command Command) error {
	subcommand := command.Subcommand()
	if len(command.Position) < 2 {
		return fmt.Errorf("usage: setup %s <file-or-run-id>", subcommand)
	}
	argument := command.Position[1]
	switch subcommand {
	case "graph":
		doc, err := a.readPlan(argument)
		if err != nil {
			return err
		}
		if err := plan.Validate(doc, a.planCapabilities(), a.catalog.Snapshot.Revision); err != nil {
			return err
		}
		levels, err := plan.TopologicalLevels(doc)
		if err != nil {
			return err
		}
		for index, level := range levels {
			ids := make([]string, 0, len(level))
			for _, node := range level {
				ids = append(ids, node.ID)
			}
			a.println(fmt.Sprintf("level %d: %s", index+1, strings.Join(ids, ", ")))
		}
		return nil
	case "validate":
		data, err := os.ReadFile(argument)
		if err != nil {
			return err
		}
		return a.callCapability(ctx, "validate_host_plan", map[string]any{"plan": string(data)}, false)
	case "apply":
		data, err := os.ReadFile(argument)
		if err != nil {
			return err
		}
		result, err := a.callCapabilityResult(ctx, "run_host_plan", map[string]any{"plan": string(data)}, true)
		if err != nil {
			return err
		}
		if runID, ok := resultString(result, "runId"); ok {
			if err := a.savePlan(runID, data); err != nil {
				a.println("warning: could not save plan for resume: " + err.Error())
			}
			final, err := a.client.WaitPlanRun(ctx, runID, PollOptions{OnUpdate: a.renderPollSnapshot})
			if err != nil {
				return err
			}
			_, err = a.printCallResult(final)
			return err
		}
		return nil
	case "status":
		return a.callCapability(ctx, "get_host_plan_run", map[string]any{"runId": argument}, false)
	case "resume":
		data, err := a.loadPlan(argument)
		if err != nil {
			return err
		}
		result, err := a.callCapabilityResult(ctx, "run_host_plan", map[string]any{"plan": string(data), "resume": true}, true)
		if err != nil {
			return err
		}
		if runID, ok := resultString(result, "runId"); ok {
			final, err := a.client.WaitPlanRun(ctx, runID, PollOptions{OnUpdate: a.renderPollSnapshot})
			if err != nil {
				return err
			}
			_, err = a.printCallResult(final)
			return err
		}
		return nil
	case "cancel":
		return a.callCapability(ctx, "cancel_operation", map[string]any{"operationId": argument}, true)
	default:
		return fmt.Errorf("unknown setup command %q", subcommand)
	}
}

func (a *App) callCapability(ctx context.Context, name string, args map[string]any, approval bool) error {
	_, err := a.callCapabilityResult(ctx, name, args, approval)
	return err
}

func (a *App) callCapabilityResult(ctx context.Context, name string, args map[string]any, approval bool) (map[string]any, error) {
	descriptor, ok := a.catalog.Get(name)
	if !ok {
		return nil, fmt.Errorf("unknown capability %q; use /refresh", name)
	}
	resolved, refs, err := a.resolveEntityArguments(args)
	if err != nil {
		return nil, err
	}
	if err := a.catalog.ValidateCall(name, resolved); err != nil {
		return nil, fmt.Errorf("%s arguments: %w", name, err)
	}
	if approval && descriptor.RequiresApproval {
		a.println(fmt.Sprintf("approval required: %s effect=%s capability=%s args=%s", descriptor.Title, descriptor.Effect, name, compactJSON(resolved)))
		if !a.config.AutoApprove {
			answer, err := a.readLine("approve [y/N]> ")
			if err != nil {
				return nil, fmt.Errorf("approval input ended: %w", err)
			}
			if !strings.EqualFold(strings.TrimSpace(answer), "y") && !strings.EqualFold(strings.TrimSpace(answer), "yes") {
				a.emit("approval.required", map[string]any{"capability": name, "approved": false})
				return nil, fmt.Errorf("approval refused")
			}
		}
	}
	a.emit("tool.call", map[string]any{"capability": name, "arguments": redactArguments(resolved), "references": refs})
	result, err := a.client.Call(ctx, name, resolved)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, fmt.Errorf("capability returned no result")
	}
	if result.IsError {
		a.recordCapabilityResult(name, resolved, result)
		return nil, fmt.Errorf("%s", resultText(result))
	}
	if operationID, ok := resultIdentifier(result, "taskId", "operationId"); ok {
		final, err := a.client.WaitOperation(ctx, operationID, PollOptions{OnUpdate: a.renderPollSnapshot})
		if err != nil {
			a.emit("error", map[string]any{"capability": name, "operationId": operationID, "message": err.Error()})
			return nil, err
		}
		result = final
	}
	a.recordCapabilityResult(name, resolved, result)
	return a.printCallResult(result)
}

func (a *App) printCallResult(result *mcp.CallToolResult) (map[string]any, error) {
	if result == nil {
		return nil, fmt.Errorf("capability returned no result")
	}
	if result.IsError {
		return nil, fmt.Errorf("%s", resultText(result))
	}
	value := map[string]any{}
	if result.StructuredContent != nil {
		encoded, _ := json.Marshal(result.StructuredContent)
		_ = json.Unmarshal(encoded, &value)
	}
	if len(value) == 0 && resultText(result) != "" {
		a.println(resultText(result))
	} else {
		_ = a.printJSON(value)
	}
	return value, nil
}

func (a *App) recordCapabilityResult(name string, arguments map[string]any, result *mcp.CallToolResult) {
	status := "success"
	if result == nil || result.IsError {
		status = "error"
	}
	var structured any
	if result != nil {
		structured = result.StructuredContent
	}
	a.emit("tool.result", map[string]any{"capability": name, "isError": status == "error", "text": resultText(result), "structured": redactAny(structured)})
	a.history = append(a.history, session.ToolCallHistory{CallID: uuid.NewString(), ToolName: name, TurnID: a.sessionID, Arguments: redactArguments(arguments), Status: status})
	if len(a.history) > session.MaxHistoryEntries {
		a.history = a.history[len(a.history)-session.MaxHistoryEntries:]
	}
}

func (a *App) renderPollSnapshot(snapshot PollSnapshot) {
	if a == nil {
		return
	}
	stateKey := snapshot.ID + "|" + snapshot.Status + "|" + compactJSON(snapshot.Raw)
	if a.pollStates[snapshot.ID] == stateKey {
		return
	}
	a.pollStates[snapshot.ID] = stateKey
	message := snapshot.Status
	if snapshot.Message != "" {
		message += ": " + snapshot.Message
	}
	a.println(fmt.Sprintf("operation %s: %s", snapshot.ID, message))
	if nodes, ok := snapshot.Raw["nodes"].(map[string]any); ok {
		ids := make([]string, 0, len(nodes))
		for id := range nodes {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			node, _ := nodes[id].(map[string]any)
			status := stringValue(node["status"])
			if status == "" {
				continue
			}
			nodeKey := snapshot.ID + "|node|" + id
			if a.pollStates[nodeKey] == status {
				continue
			}
			a.pollStates[nodeKey] = status
			a.println(fmt.Sprintf("  node %s: %s", id, status))
			kind := "setup.node.started"
			switch status {
			case "applied", "satisfied", "skipped", "compensated":
				kind = "setup.node.completed"
			case "failed", "unknown", "compensation_failed":
				kind = "setup.node.failed"
			}
			a.emit(kind, map[string]any{"runId": snapshot.ID, "nodeId": id, "status": status, "message": stringValue(node["error"])})
		}
		return
	}
	a.emit("tool.result", map[string]any{"operationId": snapshot.ID, "status": snapshot.Status, "message": snapshot.Message})
}

func (a *App) refresh(ctx context.Context) error {
	if a.catalog == nil {
		snapshot, err := a.client.CapabilityCatalog(ctx)
		if err != nil {
			return err
		}
		a.catalog = NewCatalog(snapshot)
	} else if err := a.catalog.Refresh(ctx, a.client); err != nil {
		return err
	}
	if result, err := a.client.Call(ctx, "list_vms", map[string]any{"fast": true}); err == nil && result != nil && !result.IsError {
		a.ingestVMs(result.StructuredContent)
	}
	a.println("catalog revision: " + a.catalog.Snapshot.Revision)
	return nil
}

func (a *App) ingestVMs(value any) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return
	}
	var envelope struct {
		VMs []struct {
			Name       string `json:"name"`
			ProviderID string `json:"providerId"`
			Status     string `json:"status"`
		} `json:"vms"`
	}
	if json.Unmarshal(encoded, &envelope) != nil {
		return
	}
	index := NewEntityIndex(nil)
	for _, vm := range envelope.VMs {
		if strings.TrimSpace(vm.Name) == "" {
			continue
		}
		_ = index.Add(Entity{Kind: "vm", CanonicalField: "name", CanonicalValue: vm.Name, DisplayName: vm.Name, Provider: vm.ProviderID, Source: "host-agent:list_vms", CatalogRevision: a.catalog.Snapshot.Revision})
	}
	a.entities = index
}

func (a *App) resolveEntityArguments(args map[string]any) (map[string]any, []session.EntityReference, error) {
	refs := make([]session.EntityReference, 0)
	var resolve func(any) (any, error)
	resolve = func(value any) (any, error) {
		switch typed := value.(type) {
		case string:
			if !strings.HasPrefix(typed, "@") {
				return value, nil
			}
			ref, matches, err := a.entities.Resolve(typed, -1)
			if err != nil {
				if len(matches) == 0 || a.config.NoPrompt {
					if len(matches) > 0 {
						a.println(fmt.Sprintf("entity selection required for %s: %s", typed, entityMatchSummary(matches)))
					}
					return nil, err
				}
				ref, err = a.selectEntity(typed, matches)
				if err != nil {
					return nil, err
				}
			}
			refs = append(refs, ref)
			return ref.CanonicalValue, nil
		case map[string]any:
			out := make(map[string]any, len(typed))
			for key, child := range typed {
				resolved, err := resolve(child)
				if err != nil {
					return nil, err
				}
				out[key] = resolved
			}
			return out, nil
		case []any:
			out := make([]any, len(typed))
			for index, child := range typed {
				resolved, err := resolve(child)
				if err != nil {
					return nil, err
				}
				out[index] = resolved
			}
			return out, nil
		default:
			return value, nil
		}
	}
	resolved, err := resolve(args)
	if err != nil {
		return nil, nil, err
	}
	return resolved.(map[string]any), refs, nil
}

func (a *App) selectEntity(token string, matches []EntityMatch) (session.EntityReference, error) {
	if len(matches) == 0 {
		return session.EntityReference{}, fmt.Errorf("no authorized entity matches %q", token)
	}
	a.println(fmt.Sprintf("select an authorized entity for %s:", token))
	for index, match := range matches {
		a.println(fmt.Sprintf("  %d) %s", index+1, formatEntityMatch(match)))
	}
	for attempt := 0; attempt < 3; attempt++ {
		answer, err := a.readLine(fmt.Sprintf("select entity [1-%d] or q to cancel> ", len(matches)))
		if err != nil {
			return session.EntityReference{}, err
		}
		selected, err := parseSelection(answer, len(matches))
		if err == ErrInputInterrupted {
			return session.EntityReference{}, fmt.Errorf("entity selection cancelled")
		}
		if err != nil {
			a.println("error: " + err.Error())
			continue
		}
		ref, _, err := a.entities.Resolve(token, selected)
		if err == nil {
			ref.Selection = "explicit"
			return ref, nil
		}
		a.println("error: " + err.Error())
	}
	return session.EntityReference{}, fmt.Errorf("entity selection attempts exhausted for %q", token)
}

func formatEntityMatch(match EntityMatch) string {
	entity := match.Entity
	label := entity.Kind + ":" + entity.CanonicalValue
	if strings.TrimSpace(entity.DisplayName) != "" && entity.DisplayName != entity.CanonicalValue {
		label += " (" + entity.DisplayName + ")"
	}
	if match.Method != "" {
		label += " [" + match.Method + "]"
	}
	return label
}

func (a *App) toggleAssistant(value string) error {
	switch strings.ToLower(value) {
	case "on":
		if a.platform == nil && a.assistant == nil {
			a.assistantOn = false
			return fmt.Errorf("no local or Platform assistant provider configured; deterministic mode remains active")
		}
		a.assistantOn = true
		if a.platform != nil {
			a.println("connected assistant mode on; Platform owns intent, authorization, and model execution")
		} else {
			a.println("assistant mode on; proposals remain catalog- and approval-gated")
		}
	case "off":
		a.assistantOn = false
		a.println("assistant mode off; deterministic mode active")
	default:
		return fmt.Errorf("usage: /assistant on|off")
	}
	return nil
}

func (a *App) modelCommand(ctx context.Context, command string) error {
	switch command {
	case "setup":
		a.println("configure OPUTE_TUI_MODEL_URL for a provider that returns a typed command or host-plan.v1 proposal; no provider is required")
		return nil
	case "status":
		return a.callCapability(ctx, "check_local_llm_prerequisites", map[string]any{}, false)
	case "test":
		return a.callCapability(ctx, "probe_local_llm", map[string]any{}, false)
	default:
		return fmt.Errorf("usage: /model setup|status|test")
	}
}

func (a *App) handleAssistant(ctx context.Context, input string) error {
	if a.platform != nil {
		return a.handlePlatformAssistant(ctx, input)
	}
	request := session.Request{ContractVersions: []string{session.ContractVersion}, SessionID: a.sessionID, TurnID: uuid.NewString(), CatalogRevision: a.catalog.Snapshot.Revision, Input: input, ToolCallHistory: append([]session.ToolCallHistory(nil), a.history...)}
	if err := request.Validate(); err != nil {
		return err
	}
	proposal, err := a.assistant.Propose(ctx, input, request, a.catalog.Snapshot)
	if err != nil {
		a.assistantOn = false
		return fmt.Errorf("assistant unavailable; deterministic mode remains active: %w", err)
	}
	a.emit("command.proposed", proposal)
	if err := a.validateProposalReferences(proposal.References); err != nil {
		return err
	}
	planDescriptor, planCapabilityKnown := a.catalog.Get("run_host_plan")
	if proposal.Kind == "command" {
		descriptor, ok := a.catalog.Get(proposal.Capability)
		if !ok {
			return fmt.Errorf("proposal references an unknown capability %q", proposal.Capability)
		}
		if proposal.Effect != "" && proposal.Effect != descriptor.Effect {
			return fmt.Errorf("proposal effect %q does not match capability effect %q", proposal.Effect, descriptor.Effect)
		}
		if proposal.RequiresApproval != descriptor.RequiresApproval {
			return fmt.Errorf("proposal approval flag does not match capability policy")
		}
		arguments := proposal.Arguments
		if arguments == nil {
			arguments = map[string]any{}
		}
		_, err := a.callCapabilityResult(ctx, proposal.Capability, arguments, true)
		return err
	}
	if !planCapabilityKnown {
		return fmt.Errorf("proposal requires the run_host_plan capability, which is not in the current catalog")
	}
	if proposal.Effect != "" && proposal.Effect != planDescriptor.Effect {
		return fmt.Errorf("proposal effect %q does not match run_host_plan effect %q", proposal.Effect, planDescriptor.Effect)
	}
	if proposal.RequiresApproval != planDescriptor.RequiresApproval {
		return fmt.Errorf("proposal approval flag does not match run_host_plan policy")
	}
	encoded, err := json.Marshal(proposal.Plan)
	if err != nil {
		return err
	}
	doc, err := plan.Decode(encoded)
	if err != nil {
		return err
	}
	if err := plan.Validate(doc, a.planCapabilities(), a.catalog.Snapshot.Revision); err != nil {
		return err
	}
	return a.callCapability(ctx, "run_host_plan", map[string]any{"plan": proposal.Plan}, true)
}

func (a *App) handlePlatformAssistant(ctx context.Context, input string) error {
	if a.platform == nil {
		return fmt.Errorf("Platform assistant is not configured")
	}
	request := session.Request{
		ContractVersions: []string{session.ContractVersion},
		SessionID:        a.sessionID,
		TurnID:           uuid.NewString(),
		CatalogRevision:  a.catalog.Snapshot.Revision,
		Input:            input,
		ToolCallHistory:  append([]session.ToolCallHistory(nil), a.history...),
	}
	if err := request.Validate(); err != nil {
		return err
	}
	turn, err := a.platform.Send(ctx, input, request)
	if err != nil {
		return fmt.Errorf("Platform chat unavailable; deterministic mode remains active: %w", err)
	}
	for _, event := range turn.Trace {
		if redacted, ok := redactAny(event.Data).(map[string]any); ok {
			event.Data = redacted
		} else {
			event.Data = nil
		}
		a.platformTrace = append(a.platformTrace, event)
		if event.Label != "" {
			a.println(fmt.Sprintf("platform: %s", event.Label))
		}
	}
	if len(a.platformTrace) > session.MaxEvents {
		a.platformTrace = a.platformTrace[len(a.platformTrace)-session.MaxEvents:]
	}
	if len(turn.ToolHistory) > 0 {
		a.history = append(a.history, turn.ToolHistory...)
		if len(a.history) > session.MaxHistoryEntries {
			a.history = a.history[len(a.history)-session.MaxHistoryEntries:]
		}
	}
	if strings.TrimSpace(turn.Text) == "" {
		return fmt.Errorf("Platform chat returned no assistant text")
	}
	a.println(turn.Text)
	return nil
}

func (a *App) validateProposalReferences(references []session.EntityReference) error {
	for _, reference := range references {
		if reference.CatalogRevision != "" && reference.CatalogRevision != a.catalog.Snapshot.Revision {
			return fmt.Errorf("proposal entity reference %q is stale for catalog revision %q", reference.OriginalToken, a.catalog.Snapshot.Revision)
		}
		matches := a.entities.Search("@" + reference.Kind + ":" + reference.CanonicalValue)
		verified := false
		for _, match := range matches {
			if match.Method == "exact_canonical" && match.Entity.CanonicalField == reference.CanonicalField && match.Entity.CanonicalValue == reference.CanonicalValue {
				verified = true
				break
			}
		}
		if !verified {
			return fmt.Errorf("proposal entity reference %q has no current authorized observation", reference.CanonicalValue)
		}
	}
	return nil
}

func (a *App) planCapabilities() map[string]plan.Capability {
	capabilities := make(map[string]plan.Capability, len(a.catalog.Snapshot.Tools))
	for _, descriptor := range a.catalog.Snapshot.Tools {
		capabilities[descriptor.Name] = plan.Capability{Name: descriptor.Name, InputSchema: descriptor.InputSchema, OutputSchema: descriptor.OutputSchema, Effect: descriptor.Effect, Idempotent: descriptor.Idempotent}
	}
	return capabilities
}

func (a *App) readPlan(path string) (plan.Document, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return plan.Document{}, err
	}
	return plan.Decode(data)
}

func (a *App) savePlan(runID string, data []byte) error {
	if err := os.MkdirAll(a.config.PlanDir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(a.config.PlanDir, safePlanID(runID)+".plan"), data, 0o600)
}

func (a *App) loadPlan(runID string) ([]byte, error) {
	return os.ReadFile(filepath.Join(a.config.PlanDir, safePlanID(runID)+".plan"))
}

func safePlanID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, value)
}

func (a *App) emit(kind string, payload any) {
	a.sequence++
	event := session.NewEvent(a.sessionID, uuid.NewString(), a.catalog.Snapshot.Revision, a.sequence, kind, redactAny(payload))
	if event.Validate() != nil {
		return
	}
	a.trace = append(a.trace, event)
	if len(a.trace) > session.MaxEvents {
		a.trace = a.trace[len(a.trace)-session.MaxEvents:]
	}
}

func (a *App) printHelp() {
	a.println("/help  /tools  /describe <capability>  /context  /trace  /refresh")
	a.println("/model setup|status|test  /assistant on|off  setup validate|graph|apply <file>")
	a.println("setup status|resume|cancel <run-id>  <capability> key=value ...  /exit")
}

func (a *App) printJSON(value any) error {
	encoded, err := json.MarshalIndent(redactAny(value), "", "  ")
	if err != nil {
		return err
	}
	if len(encoded) > 16*1024 {
		encoded = append(encoded[:16*1024], []byte("\n...[truncated]")...)
	}
	a.println(string(encoded))
	return nil
}

func (a *App) println(value string) {
	fmt.Fprintln(a.config.Out, value)
}

func compactJSON(value any) string {
	encoded, _ := json.Marshal(redactAny(value))
	return string(encoded)
}

func resultString(result map[string]any, key string) (string, bool) {
	value, ok := result[key].(string)
	return value, ok && strings.TrimSpace(value) != ""
}

func resultIdentifier(result *mcp.CallToolResult, keys ...string) (string, bool) {
	if result == nil || result.StructuredContent == nil {
		return "", false
	}
	encoded, err := json.Marshal(result.StructuredContent)
	if err != nil {
		return "", false
	}
	var value map[string]any
	if json.Unmarshal(encoded, &value) != nil {
		return "", false
	}
	for _, key := range keys {
		if identifier, ok := value[key].(string); ok && strings.TrimSpace(identifier) != "" {
			return identifier, true
		}
	}
	return "", false
}

func redactArguments(value map[string]any) map[string]any {
	redacted, _ := redactAny(value).(map[string]any)
	return redacted
}

func redactAny(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, child := range typed {
			lower := strings.ToLower(key)
			if strings.Contains(lower, "token") || strings.Contains(lower, "password") || strings.Contains(lower, "secret") || lower == "manifest" || lower == "sql" {
				out[key] = "[redacted]"
			} else {
				out[key] = redactAny(child)
			}
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for index, child := range typed {
			out[index] = redactAny(child)
		}
		return out
	default:
		return value
	}
}

func entityMatchSummary(matches []EntityMatch) string {
	values := make([]string, 0, len(matches))
	for _, match := range matches {
		values = append(values, match.Entity.Kind+":"+match.Entity.CanonicalValue)
	}
	sort.Strings(values)
	return strings.Join(values, ", ")
}
