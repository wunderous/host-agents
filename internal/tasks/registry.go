package tasks

import (
	"encoding/json"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	DefaultTTL     = time.Hour
	PollIntervalMs = 3000
)

type Status string

const (
	StatusWorking       Status = "working"
	StatusCompleted     Status = "completed"
	StatusFailed        Status = "failed"
	StatusCancelled     Status = "cancelled"
	StatusInputRequired Status = "input_required"
)

type ToolResult struct {
	Content           []map[string]any `json:"content,omitempty"`
	StructuredContent any              `json:"structuredContent,omitempty"`
	IsError           bool             `json:"isError,omitempty"`
}

type Record struct {
	TaskID        string         `json:"taskId"`
	ToolName      string         `json:"toolName"`
	ToolArgs      map[string]any `json:"toolArgs"`
	Status        Status         `json:"status"`
	StatusMessage string         `json:"statusMessage,omitempty"`
	Description   string         `json:"description,omitempty"`
	Metadata      map[string]any `json:"metadata,omitempty"`
	CreatedAt     string         `json:"createdAt"`
	LastUpdatedAt string         `json:"lastUpdatedAt"`
	TTL           int64          `json:"ttl"`
	PollInterval  int            `json:"pollInterval"`
	Logs          []string       `json:"logs,omitempty"`
	InputRequests map[string]any `json:"inputRequests,omitempty"`
	InputRequest  map[string]any `json:"inputRequest,omitempty"`
	ToolResult    *ToolResult    `json:"-"`
	resultCh      chan ToolResult
	cancel        func()
	resume        func(map[string]any)
}

type Registry struct {
	mu    sync.RWMutex
	tasks map[string]*Record
}

func NewRegistry() *Registry {
	return &Registry{tasks: make(map[string]*Record)}
}

func (r *Registry) Create(toolName string, toolArgs map[string]any, ttl time.Duration, description string, metadata map[string]any) *Record {
	return r.create(toolName, toolArgs, ttl, description, metadata, nil)
}

func (r *Registry) CreateWithCancel(toolName string, toolArgs map[string]any, ttl time.Duration, description string, metadata map[string]any, cancel func()) *Record {
	return r.create(toolName, toolArgs, ttl, description, metadata, cancel)
}

// CreateWithInput creates a task that pauses before execution until the
// standard tasks/update method supplies every requested response. The resume
// callback runs after the registry returns to working state.
func (r *Registry) CreateWithInput(toolName string, toolArgs map[string]any, ttl time.Duration, description string, metadata map[string]any, cancel func(), inputRequests map[string]any, resume func(map[string]any)) *Record {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec := newRecord(uuid.NewString(), toolName, toolArgs, ttl, description, metadata, cancel)
	rec.Status = StatusInputRequired
	rec.StatusMessage = "The task requires input before it can continue."
	rec.InputRequests = cloneMap(inputRequests)
	rec.resume = resume
	r.tasks[rec.TaskID] = rec
	return cloneRecord(rec)
}

// CreateWithID restores a durable operation identity after a process restart.
// A working in-memory record is reused; a completed/failed/cancelled record is
// replaced only when the caller explicitly resumes it.
func (r *Registry) CreateWithID(taskID, toolName string, toolArgs map[string]any, ttl time.Duration, description string, metadata map[string]any, cancel func()) *Record {
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.tasks[taskID]; ok && existing.Status == StatusWorking {
		return cloneRecord(existing)
	}
	rec := newRecord(taskID, toolName, toolArgs, ttl, description, metadata, cancel)
	r.tasks[taskID] = rec
	return cloneRecord(rec)
}

// RestoreSnapshot rehydrates a task handle from durable state. A waiting task
// remains input_required so its owning adapter can attach a fresh continuation
// after restart; work that was executing is made failed because its
// in-memory continuation is gone.
func (r *Registry) RestoreSnapshot(snapshot map[string]any) (*Record, bool) {
	taskID, _ := snapshot["taskId"].(string)
	toolName, _ := snapshot["toolName"].(string)
	if taskID == "" || toolName == "" {
		return nil, false
	}
	toolArgs, _ := snapshot["toolArgs"].(map[string]any)
	description, _ := snapshot["description"].(string)
	metadata, _ := snapshot["metadata"].(map[string]any)
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.tasks[taskID]; ok && existing.Status == StatusWorking {
		return cloneRecord(existing), true
	}
	rec := newRecord(taskID, toolName, toolArgs, durationFromMilliseconds(snapshot["ttlMs"]), description, metadata, nil)
	if createdAt, ok := snapshot["createdAt"].(string); ok && createdAt != "" {
		rec.CreatedAt = createdAt
	}
	if updatedAt, ok := snapshot["lastUpdatedAt"].(string); ok && updatedAt != "" {
		rec.LastUpdatedAt = updatedAt
	}
	if ttl, ok := snapshot["ttlMs"].(float64); ok {
		rec.TTL = int64(ttl)
	}
	if poll, ok := snapshot["pollIntervalMs"].(float64); ok {
		rec.PollInterval = int(poll)
	}
	status, _ := snapshot["status"].(string)
	rec.StatusMessage, _ = snapshot["statusMessage"].(string)
	switch Status(status) {
	case StatusCompleted:
		rec.Status = StatusCompleted
		if result, ok := snapshot["result"]; ok {
			encoded, err := json.Marshal(result)
			if err == nil {
				var toolResult ToolResult
				if json.Unmarshal(encoded, &toolResult) == nil {
					rec.ToolResult = &toolResult
				}
			}
		}
	case StatusCancelled:
		rec.Status = StatusCancelled
	case StatusFailed:
		rec.Status = StatusFailed
	case StatusInputRequired:
		rec.Status = StatusInputRequired
		if inputRequests, ok := snapshot["inputRequests"].(map[string]any); ok {
			rec.InputRequests = cloneMap(inputRequests)
		}
		if inputRequest, ok := snapshot["inputRequest"].(map[string]any); ok {
			rec.InputRequest = cloneMap(inputRequest)
		}
	default:
		rec.Status = StatusFailed
		rec.StatusMessage = "The Host Agent restarted before the task completed."
	}
	r.tasks[taskID] = rec
	return cloneRecord(rec), true
}

// RequireInput durably changes a working task into an input barrier and
// installs the in-process continuation that will be invoked after the
// corresponding tasks/update response passes the owning contract's checks.
func (r *Registry) RequireInput(taskID string, inputRequests map[string]any, resume func(map[string]any)) (*Record, bool) {
	return r.RequireInputWithMetadata(taskID, inputRequests, nil, resume)
}

// RequireInputWithMetadata projects the typed wait envelope alongside the
// field schema. The metadata is descriptive only; the durable plan record
// remains the authority used to validate and resume the wait.
func (r *Registry) RequireInputWithMetadata(taskID string, inputRequests, inputRequest map[string]any, resume func(map[string]any)) (*Record, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.tasks[taskID]
	if !ok || (rec.Status != StatusWorking && rec.Status != StatusInputRequired) {
		return cloneRecord(rec), ok
	}
	rec.Status = StatusInputRequired
	rec.StatusMessage = "The task requires input before it can continue."
	rec.InputRequests = cloneMap(inputRequests)
	rec.InputRequest = cloneMap(inputRequest)
	rec.resume = resume
	rec.LastUpdatedAt = time.Now().UTC().Format(time.RFC3339)
	return cloneRecord(rec), true
}

// SetResume attaches the continuation for a restored input_required task.
func (r *Registry) SetResume(taskID string, resume func(map[string]any)) (*Record, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.tasks[taskID]
	if !ok || rec.Status != StatusInputRequired {
		return cloneRecord(rec), ok
	}
	rec.resume = resume
	return cloneRecord(rec), true
}

func durationFromMilliseconds(value any) time.Duration {
	if number, ok := value.(float64); ok && number > 0 {
		return time.Duration(number) * time.Millisecond
	}
	return DefaultTTL
}

func (r *Registry) create(toolName string, toolArgs map[string]any, ttl time.Duration, description string, metadata map[string]any, cancel func()) *Record {
	r.mu.Lock()
	defer r.mu.Unlock()
	id := uuid.NewString()
	rec := newRecord(id, toolName, toolArgs, ttl, description, metadata, cancel)
	r.tasks[id] = rec
	return cloneRecord(rec)
}

func newRecord(taskID, toolName string, toolArgs map[string]any, ttl time.Duration, description string, metadata map[string]any, cancel func()) *Record {
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	now := time.Now().UTC().Format(time.RFC3339)
	return &Record{
		TaskID:        taskID,
		ToolName:      toolName,
		ToolArgs:      cloneMap(toolArgs),
		Status:        StatusWorking,
		Description:   description,
		Metadata:      cloneMap(metadata),
		CreatedAt:     now,
		LastUpdatedAt: now,
		TTL:           int64(ttl / time.Millisecond),
		PollInterval:  PollIntervalMs,
		resultCh:      make(chan ToolResult, 1),
		cancel:        cancel,
	}
}

func (r *Registry) Get(taskID string) (*Record, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rec, ok := r.tasks[taskID]
	return cloneRecord(rec), ok
}

// IsWorking reports whether taskID currently owns active work without exposing
// the mutable task record to the caller.
func (r *Registry) IsWorking(taskID string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rec, ok := r.tasks[taskID]
	return ok && rec.Status == StatusWorking
}

func (r *Registry) List() []*Record {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Record, 0, len(r.tasks))
	for _, rec := range r.tasks {
		out = append(out, cloneRecord(rec))
	}
	return out
}

func (r *Registry) AppendLog(taskID, message string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.tasks[taskID]
	if !ok || rec.Status != StatusWorking {
		return
	}
	rec.Logs = append(rec.Logs, message)
	rec.LastUpdatedAt = time.Now().UTC().Format(time.RFC3339)
}

func (r *Registry) Complete(taskID string, result ToolResult) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.tasks[taskID]
	if !ok || rec.Status != StatusWorking {
		return
	}
	rec.Status = StatusCompleted
	rec.StatusMessage = "The operation completed successfully."
	rec.LastUpdatedAt = time.Now().UTC().Format(time.RFC3339)
	rec.ToolResult = cloneToolResult(&result)
	select {
	case rec.resultCh <- *cloneToolResult(&result):
	default:
	}
}

func (r *Registry) Fail(taskID string, message string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.tasks[taskID]
	if !ok || (rec.Status != StatusWorking && rec.Status != StatusInputRequired) {
		return
	}
	rec.Status = StatusFailed
	rec.StatusMessage = message
	rec.LastUpdatedAt = time.Now().UTC().Format(time.RFC3339)
	result := ToolResult{
		Content: []map[string]any{{"type": "text", "text": "Error: " + message}},
		IsError: true,
	}
	rec.ToolResult = &result
	select {
	case rec.resultCh <- result:
	default:
	}
}

func (r *Registry) Cancel(taskID string) (*Record, bool) {
	return r.CancelWithMessage(taskID, "The task was cancelled by request.")
}

// CancelWithMessage cancels working or input-required work and records the
// lifecycle reason for callers such as task-handle acknowledgement expiry.
func (r *Registry) CancelWithMessage(taskID, message string) (*Record, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.tasks[taskID]
	if !ok {
		return nil, false
	}
	if rec.Status != StatusWorking && rec.Status != StatusInputRequired {
		return nil, false
	}
	if rec.cancel != nil {
		rec.cancel()
	}
	rec.Status = StatusCancelled
	rec.StatusMessage = strings.TrimSpace(message)
	if rec.StatusMessage == "" {
		rec.StatusMessage = "The task was cancelled."
	}
	rec.LastUpdatedAt = time.Now().UTC().Format(time.RFC3339)
	result := ToolResult{
		Content: []map[string]any{{"type": "text", "text": "Error: " + rec.StatusMessage}},
		IsError: true,
	}
	rec.ToolResult = &result
	select {
	case rec.resultCh <- result:
	default:
	}
	return cloneRecord(rec), true
}

// Update applies only responses keyed by currently outstanding input
// requests. Unknown or already-satisfied keys are ignored per MCP Tasks.
// When all requests are satisfied, the task resumes exactly once.
func (r *Registry) Update(taskID string, responses map[string]any) (*Record, bool) {
	r.mu.Lock()
	rec, ok := r.tasks[taskID]
	if !ok {
		r.mu.Unlock()
		return nil, false
	}
	if rec.Status != StatusInputRequired {
		r.mu.Unlock()
		return cloneRecord(rec), false
	}
	if rec.InputRequests == nil {
		rec.InputRequests = map[string]any{}
	}
	accepted := map[string]any{}
	for key, value := range responses {
		if _, outstanding := rec.InputRequests[key]; !outstanding {
			continue
		}
		accepted[key] = value
		delete(rec.InputRequests, key)
	}
	if len(rec.InputRequests) != 0 {
		rec.LastUpdatedAt = time.Now().UTC().Format(time.RFC3339)
		snapshot := cloneRecord(rec)
		r.mu.Unlock()
		return snapshot, true
	}
	rec.Status = StatusWorking
	rec.StatusMessage = "Input received; resuming the task."
	rec.InputRequest = nil
	rec.LastUpdatedAt = time.Now().UTC().Format(time.RFC3339)
	resume := rec.resume
	snapshot := cloneRecord(rec)
	r.mu.Unlock()
	if resume != nil {
		resume(accepted)
	}
	return snapshot, true
}

func (r *Registry) ToGetTaskResult(rec *Record) map[string]any {
	out := map[string]any{
		"resultType":     "complete",
		"taskId":         rec.TaskID,
		"status":         rec.Status,
		"createdAt":      rec.CreatedAt,
		"lastUpdatedAt":  rec.LastUpdatedAt,
		"ttlMs":          rec.TTL,
		"pollIntervalMs": rec.PollInterval,
	}
	if rec.StatusMessage != "" {
		out["statusMessage"] = rec.StatusMessage
	}
	if len(rec.InputRequests) > 0 {
		out["inputRequests"] = cloneMap(rec.InputRequests)
	}
	if rec.Status == StatusInputRequired && len(rec.InputRequest) > 0 {
		out["inputRequest"] = cloneMap(rec.InputRequest)
	}
	if rec.Status == StatusFailed {
		out["error"] = map[string]any{
			"code":    -32603,
			"message": rec.StatusMessage,
		}
	} else if rec.Status == StatusCompleted && rec.ToolResult != nil {
		out["result"] = map[string]any{
			"structuredContent": rec.ToolResult.StructuredContent,
			"content":           rec.ToolResult.Content,
			"isError":           rec.ToolResult.IsError,
		}
	}
	return out
}

// ToCreateTaskResult projects the normative 2026-07-28 flat task handle. The
// terminal result is deliberately not included: it is available only through
// tasks/get after the task reaches a terminal state.
func (r *Registry) ToCreateTaskResult(rec *Record) map[string]any {
	return map[string]any{
		"resultType":     "task",
		"taskId":         rec.TaskID,
		"status":         rec.Status,
		"createdAt":      rec.CreatedAt,
		"lastUpdatedAt":  rec.LastUpdatedAt,
		"ttlMs":          rec.TTL,
		"pollIntervalMs": rec.PollInterval,
	}
}

func cloneMap(value map[string]any) map[string]any {
	if value == nil {
		return nil
	}
	return cloneValue(value).(map[string]any)
}

// cloneRecord returns a stable snapshot for callers. Registry-owned records
// are mutated under Registry.mu; exposing those pointers would let an
// otherwise read-only caller race with completion, cancellation, or input
// updates after the registry lock is released.
func cloneRecord(rec *Record) *Record {
	if rec == nil {
		return nil
	}
	clone := *rec
	clone.ToolArgs = cloneMap(rec.ToolArgs)
	clone.Metadata = cloneMap(rec.Metadata)
	clone.Logs = append([]string(nil), rec.Logs...)
	clone.InputRequests = cloneMap(rec.InputRequests)
	clone.InputRequest = cloneMap(rec.InputRequest)
	clone.ToolResult = cloneToolResult(rec.ToolResult)
	return &clone
}

func cloneToolResult(result *ToolResult) *ToolResult {
	if result == nil {
		return nil
	}
	clone := *result
	if result.Content != nil {
		clone.Content = make([]map[string]any, len(result.Content))
		for i, item := range result.Content {
			clone.Content[i] = cloneMap(item)
		}
	}
	clone.StructuredContent = cloneValue(result.StructuredContent)
	return &clone
}

func cloneValue(value any) any {
	reflected := reflect.ValueOf(value)
	if !reflected.IsValid() {
		return nil
	}
	return cloneReflectValue(reflected, make(map[cloneVisit]reflect.Value)).Interface()
}

type cloneVisit struct {
	typ reflect.Type
	ptr uintptr
	len int
}

func cloneReflectValue(value reflect.Value, visited map[cloneVisit]reflect.Value) reflect.Value {
	switch value.Kind() {
	case reflect.Interface:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		clone := reflect.New(value.Type()).Elem()
		clone.Set(cloneReflectValue(value.Elem(), visited))
		return clone
	case reflect.Map:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		visit := cloneVisit{typ: value.Type(), ptr: value.Pointer()}
		if prior, ok := visited[visit]; ok {
			return prior
		}
		clone := reflect.MakeMapWithSize(value.Type(), value.Len())
		visited[visit] = clone
		iter := value.MapRange()
		for iter.Next() {
			key := cloneReflectValue(iter.Key(), visited)
			clone.SetMapIndex(key, cloneReflectValue(iter.Value(), visited))
		}
		return clone
	case reflect.Slice:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		visit := cloneVisit{typ: value.Type(), ptr: value.Pointer(), len: value.Len()}
		if prior, ok := visited[visit]; ok {
			return prior
		}
		clone := reflect.MakeSlice(value.Type(), value.Len(), value.Len())
		visited[visit] = clone
		for i := 0; i < value.Len(); i++ {
			clone.Index(i).Set(cloneReflectValue(value.Index(i), visited))
		}
		return clone
	case reflect.Array:
		clone := reflect.New(value.Type()).Elem()
		for i := 0; i < value.Len(); i++ {
			clone.Index(i).Set(cloneReflectValue(value.Index(i), visited))
		}
		return clone
	case reflect.Pointer:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		visit := cloneVisit{typ: value.Type(), ptr: value.Pointer()}
		if prior, ok := visited[visit]; ok {
			return prior
		}
		clone := reflect.New(value.Type().Elem())
		visited[visit] = clone
		clone.Elem().Set(cloneReflectValue(value.Elem(), visited))
		return clone
	case reflect.Struct:
		clone := reflect.New(value.Type()).Elem()
		clone.Set(value)
		for i := 0; i < value.NumField(); i++ {
			if clone.Field(i).CanSet() && value.Field(i).CanInterface() {
				clone.Field(i).Set(cloneReflectValue(value.Field(i), visited))
			}
		}
		return clone
	default:
		return value
	}
}

// TaskAwareTools is the residue of the pre-W8 table: names with no dispatch
// registration to declare a task mode at, because the transport or a provider
// handles them rather than a host capability. A registered capability declares
// tools.TaskAware at its registration site instead, and
// TestResidualTaskTableHoldsOnlyUnregisteredNames keeps the two from
// overlapping.
var TaskAwareTools = map[string]bool{
	"request_task_input":            true,
	"install_cloudflared_connector": true,
	"delete_cloudflared_connector":  true,
	"configure_network":             true,
	"remove_vm_network_device":      true,
	"install_host_agent":            true,
	"run_host_plan":                 true,
	"run_host_local_recipe":         true,
	"run_runtime_recipe":            true,
	"run_tunnel_recipe":             true,
	"opute.provider.install":        true,
	"opute.provider.reload":         true,
	"opute.provider.teardown":       true,
}
