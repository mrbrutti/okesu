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
	EventActionDenied    EventType = "action_denied"    // RBAC blocked a tool call
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

	// Daemon mode fields
	Agent     string `json:"agent,omitempty"`     // agent name
	Host      string `json:"host,omitempty"`      // hostname running the daemon
	Tick      int64  `json:"tick,omitempty"`      // tick sequence number
	Result    string `json:"result,omitempty"`    // tick_done: completed|skipped|error
	Collector string `json:"collector,omitempty"` // collector_result: collector name
	Bytes     int    `json:"bytes,omitempty"`     // collector_result: output size
	Severity  string `json:"severity,omitempty"`  // finding: critical|high|medium|low|info
	Title     string `json:"title,omitempty"`     // finding: short title
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
