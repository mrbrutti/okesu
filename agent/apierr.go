package agent

import (
	"errors"
	"net/url"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	openai "github.com/openai/openai-go/v3"
)

// classifyRunError inspects an error returned from RunClaude or RunOpenAI and
// reports whether it represents an AI provider failure rather than a local
// logic or configuration error.
//
// Returns:
//   - isAPIError=true, statusCode>0  → HTTP-level provider error (4xx/5xx)
//   - isAPIError=true, statusCode=0  → network-level failure (DNS, refused, TLS, timeout)
//   - isAPIError=false               → local error (bad config, template render, etc.)
func classifyRunError(err error) (isAPIError bool, statusCode int) {
	if err == nil {
		return false, 0
	}

	// Anthropic SDK API error.
	var ae *anthropic.Error
	if errors.As(err, &ae) {
		return true, ae.StatusCode
	}

	// OpenAI / Codex SDK API error.
	var oe *openai.Error
	if errors.As(err, &oe) {
		return true, oe.StatusCode
	}

	// Network-level failure (DNS, connection refused, TLS handshake, timeout).
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return true, 0
	}

	return false, 0
}
