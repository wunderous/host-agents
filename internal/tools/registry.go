package tools

import (
	"context"
	"fmt"
	"sort"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/wunderous/host-agents/internal/hostagent"
	"github.com/wunderous/host-agents/internal/resource"
)

// ToolHandler executes one host capability. Every dispatch entry is one of
// these, registered under exactly one name.
type ToolHandler func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error)

// Effect is how a capability changes the world. It is the value the capability
// descriptor publishes and the value host approval is gated on.
//
// Before W8 this lived in a tool-name-keyed table with a fallback chain ending
// in "read" for any name the table missed. That inference is the defect ADR
// 0009's Context paragraph names: a capability that nobody classified was
// published as read-only and executed without approval. It is now a required
// argument of register, so the compiler asks the question instead.
type Effect string

const (
	EffectRead        Effect = "read"
	EffectMutation    Effect = "mutation"
	EffectCredential  Effect = "credential_bearing"
	EffectDestructive Effect = "destructive"
)

// TaskMode is whether a capability outlives the request that started it. An
// aware capability is handed to the task/polling contract; an inline one is
// answered on the request.
type TaskMode string

const (
	TaskInline TaskMode = "inline"
	TaskAware  TaskMode = "aware"
)

// registration is one capability's behaviour: what it does to the host, how
// much of the host it consumes while doing it, whether it outlives its
// request, and the code that performs it. Its four parts used to be four
// tool-name-keyed tables in four packages, each of which could be forgotten
// independently (plan §9.3, W8).
type registration struct {
	Effect    Effect
	Admission resource.Class
	Task      TaskMode
	Handler   ToolHandler
}

// toolHandlers is the dispatch table. It replaces a 1,067-line switch whose
// only machine-readable form was its own source text (plan §2.4, W2).
var toolHandlers = map[string]registration{}

// register binds one handler to one tool name.
//
// The duplicate panic is the partition guarantee: once the eight domain
// packages each register their own names, a tool claimed by two domains fails
// at init rather than resolving to whichever file the linker saw last. That is
// what made partitioning the old internal/ops safe (W2, M3).
// effect, admission and task are separate positional parameters rather than
// fields of a struct literal because a struct literal has a zero value for
// every field it omits. Positionally, omitting one does not compile -- which is
// W8's exit criterion.
func register(name string, effect Effect, admission resource.Class, task TaskMode, handler ToolHandler) {
	if _, exists := toolHandlers[name]; exists {
		panic(fmt.Sprintf("dispatch: tool %q registered twice", name))
	}
	if handler == nil {
		panic(fmt.Sprintf("dispatch: tool %q registered with a nil handler", name))
	}
	switch effect {
	case EffectRead, EffectMutation, EffectCredential, EffectDestructive:
	default:
		panic(fmt.Sprintf("dispatch: tool %q registered with unknown effect %q", name, effect))
	}
	switch admission {
	case resource.ClassControl, resource.ClassNormal, resource.ClassHeavy:
	default:
		panic(fmt.Sprintf("dispatch: tool %q registered with unknown admission class %q", name, admission))
	}
	switch task {
	case TaskInline, TaskAware:
	default:
		panic(fmt.Sprintf("dispatch: tool %q registered with unknown task mode %q", name, task))
	}
	toolHandlers[name] = registration{Effect: effect, Admission: admission, Task: task, Handler: handler}
}

// LookupTool returns the handler registered for name, if any.
func LookupTool(name string) (ToolHandler, bool) {
	entry, ok := toolHandlers[name]
	if !ok {
		return nil, false
	}
	return entry.Handler, true
}

// RegisteredEffect returns the declared effect of a registered capability.
// Callers fall back to the residual tables only for names with no dispatch
// registration -- the transport-owned tools listed there.
func RegisteredEffect(name string) (string, bool) {
	entry, ok := toolHandlers[name]
	if !ok {
		return "", false
	}
	return string(entry.Effect), true
}

// RegisteredAdmissionClass returns the declared admission class of a
// registered capability.
func RegisteredAdmissionClass(name string) (resource.Class, bool) {
	entry, ok := toolHandlers[name]
	if !ok {
		return "", false
	}
	return entry.Admission, true
}

// IsTaskAware reports whether a registered capability outlives its request.
func IsTaskAware(name string) bool {
	entry, ok := toolHandlers[name]
	return ok && entry.Task == TaskAware
}

// RegisteredToolNames returns every registered dispatch name, sorted.
func RegisteredToolNames() []string {
	names := make([]string, 0, len(toolHandlers))
	for name := range toolHandlers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
