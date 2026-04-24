package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// EventType identifies the kind of JSONL event emitted to stdout.
type EventType string

const (
	EventInit       EventType = "init"        // session started
	EventText       EventType = "text"        // text delta from model
	EventToolCall   EventType = "tool_call"   // model is calling a tool
	EventToolResult EventType = "tool_result" // tool execution result
	EventDone       EventType = "done"        // session complete
	EventError      EventType = "error"       // fatal error
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
}

// Usage reports token consumption at session end.
type Usage struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
}

// mu protects stdout writes — tool handlers run in parallel goroutines.
var mu sync.Mutex

// Emit writes an event as a single JSONL line to stdout.
func Emit(e Event) {
	e.Ts = time.Now().UnixMilli()
	b, err := json.Marshal(e)
	if err != nil {
		fmt.Fprintf(os.Stderr, "jsonl marshal error: %v\n", err)
		return
	}
	mu.Lock()
	fmt.Printf("%s\n", b)
	mu.Unlock()
}

// EmitError writes an error event to stdout and a human message to stderr.
func EmitError(err error) {
	Emit(Event{Type: EventError, Error: err.Error()})
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
}
