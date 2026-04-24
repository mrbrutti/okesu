package agent

import (
	"fmt"
	"os"
)

// EventType identifies the kind of JSONL event emitted to stdout.
type EventType string

const (
	// Task mode events
	EventInit       EventType = "init"        // session started
	EventText       EventType = "text"        // text delta from model
	EventToolCall   EventType = "tool_call"   // model is calling a tool
	EventToolResult EventType = "tool_result" // tool execution result
	EventDone       EventType = "done"        // session complete
	EventError      EventType = "error"       // fatal error

	// Daemon mode events
	EventDaemonStart     EventType = "daemon_start"     // daemon process started
	EventDaemonStop      EventType = "daemon_stop"      // daemon shutting down cleanly
	EventTickStart       EventType = "tick_start"       // tick beginning
	EventTickDone        EventType = "tick_done"        // tick complete
	EventCollectorResult EventType = "collector_result" // pre-collector finished
	EventFinding         EventType = "finding"          // agent-reported security finding
	EventActionTaken     EventType = "action_taken"     // tool executed after RBAC allow
	EventActionDenied    EventType = "action_denied"    // RBAC blocked a tool call
	EventAPIUnavailable  EventType = "api_unavailable"  // AI API unreachable
	EventConfigReloaded  EventType = "config_reloaded"  // management plane pushed new config
)

// Event is the canonical JSONL line written to stdout.
// Consumers read line-by-line and parse each as JSON.
type Event struct {
	Type       EventType   `json:"type"`
	Provider   string      `json:"provider,omitempty"`
	Model      string      `json:"model,omitempty"`
	Text       string      `json:"text,omitempty"`
	ToolID     string      `json:"tool_id,omitempty"`
	ToolName   string      `json:"tool_name,omitempty"`
	Input      interface{} `json:"input,omitempty"`
	Output     string      `json:"output,omitempty"`
	Error      string      `json:"error,omitempty"`
	StopReason string      `json:"stop_reason,omitempty"`
	Usage      *Usage      `json:"usage,omitempty"`
	Turn       int         `json:"turn,omitempty"`
	Ts         int64       `json:"ts"`

	// Daemon lifecycle fields
	Agent      string `json:"agent,omitempty"`       // agent name
	Host       string `json:"host,omitempty"`        // hostname running the daemon
	Tick       int64  `json:"tick,omitempty"`        // tick sequence number
	Result     string `json:"result,omitempty"`      // tick_done: completed|skipped|error
	Duration   string `json:"duration,omitempty"`    // tick_done: wall time e.g. "1.4s"
	Findings   int    `json:"findings,omitempty"`    // tick_done: count of findings emitted
	ActionsTaken int  `json:"actions_taken,omitempty"` // tick_done: count of allowed tool calls

	// Collector fields
	Collector string `json:"collector,omitempty"` // collector_result: collector name
	Bytes     int    `json:"bytes,omitempty"`     // collector_result: output size

	// Finding fields
	Severity  string `json:"severity,omitempty"`  // finding: critical|high|medium|low|info
	Title     string `json:"title,omitempty"`     // finding: short human-readable title
	Evidence  string `json:"evidence,omitempty"`  // finding: raw lines from telemetry
	Resource  string `json:"resource,omitempty"`  // finding: affected resource (pid:N, path:/...)
	DedupKey  string `json:"dedup_key,omitempty"` // finding: dedup cache key

	// RBAC / action fields
	Reason string `json:"reason,omitempty"` // action_denied: why the call was blocked
}

// Usage reports token consumption at session end.
type Usage struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
}

// Emit writes an event via the active global sink.
// Thread-safe: sink implementations are required to be concurrent-safe.
func Emit(e Event) {
	emitToSink(e)
}

// EmitError writes an error event and a human message to stderr.
func EmitError(err error) {
	Emit(Event{Type: EventError, Error: err.Error()})
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
}
