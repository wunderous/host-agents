package tasks

import (
	"encoding/json"
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
	return rec
}

// CreateWithID restores a durable operation identity after a process restart.
// A working in-memory record is reused; a completed/failed/cancelled record is
// replaced only when the caller explicitly resumes it.
func (r *Registry) CreateWithID(taskID, toolName string, toolArgs map[string]any, ttl time.Duration, description string, metadata map[string]any, cancel func()) *Record {
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.tasks[taskID]; ok && existing.Status == StatusWorking {
		return existing
	}
	rec := newRecord(taskID, toolName, toolArgs, ttl, description, metadata, cancel)
	r.tasks[taskID] = rec
	return rec
}

// RestoreSnapshot rehydrates a terminal task handle from durable state. Work
// that was still executing when the process stopped is made failed because
// the in-memory continuation is gone; the handle itself remains queryable.
func (r *Registry) RestoreSnapshot(snapshot map[string]any) (*Record, bool) {
	taskID, _ := snapshot["taskId"].(string)
	toolName, _ := snapshot["toolName"].(string)
	if taskID == "" || toolName == "" {
		return nil, false
	}
	toolArgs, _ := snapshot["toolArgs"].(map[string]any)
	description, _ := snapshot["description"].(string)
	metadata, _ := snapshot["metadata"].(map[string]any)
	rec := r.CreateWithID(taskID, toolName, toolArgs, durationFromMilliseconds(snapshot["ttlMs"]), description, metadata, nil)
	r.mu.Lock()
	defer r.mu.Unlock()
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
	default:
		rec.Status = StatusFailed
		rec.StatusMessage = "The Host Agent restarted before the task completed."
	}
	r.tasks[taskID] = rec
	return rec, true
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
	return rec
}

func newRecord(taskID, toolName string, toolArgs map[string]any, ttl time.Duration, description string, metadata map[string]any, cancel func()) *Record {
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	now := time.Now().UTC().Format(time.RFC3339)
	return &Record{
		TaskID:        taskID,
		ToolName:      toolName,
		ToolArgs:      toolArgs,
		Status:        StatusWorking,
		Description:   description,
		Metadata:      metadata,
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
	return rec, ok
}

func (r *Registry) List() []*Record {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Record, 0, len(r.tasks))
	for _, rec := range r.tasks {
		out = append(out, rec)
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
	rec.ToolResult = &result
	select {
	case rec.resultCh <- result:
	default:
	}
}

func (r *Registry) Fail(taskID string, message string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.tasks[taskID]
	if !ok || rec.Status != StatusWorking {
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
	rec.StatusMessage = "The task was cancelled by request."
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
	return rec, true
}

// Update applies only responses keyed by currently outstanding input
// requests. Unknown or already-satisfied keys are ignored per MCP Tasks.
// When all requests are satisfied, the task resumes exactly once.
func (r *Registry) Update(taskID string, responses map[string]any) (*Record, bool) {
	r.mu.Lock()
	rec, ok := r.tasks[taskID]
	if !ok || rec.Status != StatusInputRequired {
		r.mu.Unlock()
		return rec, ok
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
		r.mu.Unlock()
		return rec, true
	}
	rec.Status = StatusWorking
	rec.StatusMessage = "Input received; resuming the task."
	rec.LastUpdatedAt = time.Now().UTC().Format(time.RFC3339)
	resume := rec.resume
	r.mu.Unlock()
	if resume != nil {
		resume(accepted)
	}
	return rec, true
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
	out := make(map[string]any, len(value))
	for key, item := range value {
		out[key] = item
	}
	return out
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
	"run_runtime_recipe":            true,
	"run_tunnel_recipe":             true,
	"opute.provider.install":        true,
	"opute.provider.reload":         true,
	"opute.provider.teardown":       true,
}
