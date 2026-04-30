package orchestrator

import "time"

// StepNodeProgressSink is the engine's hook for live per-host fan-out
// state. fanOut() calls OnDispatchStart immediately before each
// per-host Dispatch goroutine fires, and OnDispatchEnd as soon as the
// goroutine returns.
//
// The default sink is a no-op (used by tests + the engine before
// wiring). Production wires this to a db-backed implementation that
// inserts/updates rows in orchestration_step_node_dispatches.
//
// Errors returned by the sink are logged by fanOut() but do not
// abort the dispatch — telemetry must not gate execution.
type StepNodeProgressSink interface {
	OnDispatchStart(runID int64, stepID, host string, startedAt time.Time) error
	OnDispatchEnd(runID int64, stepID, host string,
		status, agentRunID string,
		findingsCount int,
		outputTail, errorStr string,
		endedAt time.Time) error
}

// noopProgressSink is the default. fanOut() uses this when the engine
// hasn't been wired with a real sink — e.g. unit tests that don't
// care about per-host telemetry.
type noopProgressSink struct{}

func (noopProgressSink) OnDispatchStart(int64, string, string, time.Time) error { return nil }
func (noopProgressSink) OnDispatchEnd(int64, string, string, string, string, int, string, string, time.Time) error {
	return nil
}
