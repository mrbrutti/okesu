package agent

import "context"

// APIUnavailablePolicy is the hook invoked when an AI provider API is unreachable
// or returns a server-side error during a daemon tick.
//
// Implementations can buffer prompts, switch to a backup provider, alert operators,
// or trigger escalation logic. The Control Plane integration layer will implement
// this interface; the default is NoopAPIPolicy.
//
// OnAPIUnavailable is called after EventAPIUnavailable has already been emitted to
// the active sink. The call is synchronous within execTick — keep implementations
// fast or start a goroutine internally if heavier work is needed.
//
// Contract for Control Plane implementors:
//   - provider is "claude" or "codex"
//   - statusCode is the HTTP status returned by the provider (0 = network-level failure)
//   - err is the original unwrapped error from the provider SDK
//   - 429 = rate-limited; consider exponential back-off before the next tick
//   - 5xx = provider outage; consider alerting and/or switching to a backup provider
//   - 0   = network unreachable; check connectivity before retrying
type APIUnavailablePolicy interface {
	// OnAPIUnavailable is called once per failed tick, after EventAPIUnavailable is emitted.
	OnAPIUnavailable(ctx context.Context, provider, model string, statusCode int, err error)
}

// NoopAPIPolicy satisfies APIUnavailablePolicy with no action.
// This is the default when DaemonConfig.APIPolicy is nil.
type NoopAPIPolicy struct{}

func (NoopAPIPolicy) OnAPIUnavailable(_ context.Context, _, _ string, _ int, _ error) {}
