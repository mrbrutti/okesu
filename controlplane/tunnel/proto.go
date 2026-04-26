// Package tunnel implements the Control Plane ↔ Node reverse mTLS tunnel.
//
// The Node dials the CP over WebSocket+mTLS and holds the connection open.
// The CP sends RunAgent requests over the tunnel; the Node spawns
// `okesu claude|codex|auto`, streams stdout JSONL lines back as LogLines,
// and emits an Exit message when the child terminates.
package tunnel

import "encoding/json"

// MsgType identifies the kind of message. We use a typed string so JSON
// marshalling stays readable on the wire and so a malformed message
// produces a useful error instead of a silent default.
type MsgType string

const (
	// CP → Node
	MsgRun    MsgType = "run"
	MsgCancel MsgType = "cancel"
	MsgPing   MsgType = "ping"

	// Node → CP
	MsgHello MsgType = "hello"
	MsgLine  MsgType = "line"
	MsgExit  MsgType = "exit"
	MsgPong  MsgType = "pong"
)

// Frame is the union envelope carried over the WebSocket. Exactly one of the
// payload fields is set per frame, picked by Type.
type Frame struct {
	Type   MsgType         `json:"type"`
	Hello  *HelloPayload   `json:"hello,omitempty"`
	Run    *RunPayload     `json:"run,omitempty"`
	Cancel *CancelPayload  `json:"cancel,omitempty"`
	Line   *LinePayload    `json:"line,omitempty"`
	Exit   *ExitPayload    `json:"exit,omitempty"`
}

// HelloPayload — first message from Node after connect. Identifies the node
// and reports its version.
type HelloPayload struct {
	Node    string `json:"node"`              // node name (CN of the cert)
	Version string `json:"version,omitempty"`
}

// RunPayload — CP asks the Node to spawn an agent run.
//
// The Node executes `okesu <Provider> [--agent <Agent>] [--model <Model>] [--effort <Effort>] [--max-turns <N>] <Prompt>`
// and streams JSONL output back as LogLines, then emits an Exit.
//
// Empty Provider defaults to "auto". API keys are NOT carried in this payload —
// the node uses its own environment (see `okesu node --env-file`).
type RunPayload struct {
	RunID    string `json:"run_id"`
	Provider string `json:"provider,omitempty"` // claude|codex|auto
	Model    string `json:"model,omitempty"`
	Effort   string `json:"effort,omitempty"`
	MaxTurns int    `json:"max_turns,omitempty"`
	Agent    string `json:"agent,omitempty"`    // optional --agent flag
	// AgentContent, when non-empty, carries the full *.md file body for
	// the named agent. Set by the CP when the operator picks an agent
	// from the Agent Library — the node writes the content to a temp
	// file and passes its path via --agent so the run uses the
	// CP-managed definition without requiring it to be pre-staged on
	// the node. Empty means "look up Agent by name from the node's
	// local search paths" (legacy behaviour).
	AgentContent string `json:"agent_content,omitempty"`
	Prompt       string `json:"prompt"`
}

// CancelPayload — CP asks the Node to terminate an in-flight run.
type CancelPayload struct {
	RunID string `json:"run_id"`
}

// LinePayload — Node forwards one stdout (or stderr-marked) line. The Data
// field is the raw line contents — typically a JSONL event.
type LinePayload struct {
	RunID  string `json:"run_id"`
	Stream string `json:"stream,omitempty"` // "stdout" (default) | "stderr"
	Data   string `json:"data"`
}

// ExitPayload — Node reports the child process exited.
type ExitPayload struct {
	RunID string `json:"run_id"`
	Code  int    `json:"code"`
	Error string `json:"error,omitempty"` // non-empty if Run() failed before exec
}

// Marshal serializes a frame to JSON (helper for clients).
func Marshal(f *Frame) ([]byte, error) { return json.Marshal(f) }

// Unmarshal parses a frame from JSON.
func Unmarshal(b []byte) (*Frame, error) {
	f := &Frame{}
	if err := json.Unmarshal(b, f); err != nil {
		return nil, err
	}
	return f, nil
}
