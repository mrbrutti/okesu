package agent

import (
	"regexp"
	"strings"
)

// RedactSecrets returns s with known secret-bearing substrings
// replaced by `[REDACTED:<kind>]` markers. Used as a defense-in-depth
// filter on every tool result before the daemon emits it as a JSONL
// event — protects against an LLM running `env` / `printenv` /
// `cat /etc/okesu/agents/*.env` and surfacing API keys / tokens /
// private keys in webhook events, finding evidence, and the Live
// Events feed on the Control Plane.
//
// Coverage (in order of application):
//
//  1. PEM-armored private keys (entire block, multi-line)
//  2. Anthropic, OpenAI, GitHub, GitLab, Slack, Stripe, AWS access-
//     key bare tokens (with their distinctive prefixes)
//  3. Bearer tokens in HTTP-style headers
//  4. JWT tokens (three base64url segments separated by `.`)
//  5. Env-style `KEY=value` assignments where KEY contains
//     `KEY|TOKEN|SECRET|PASSWORD|PASSWD|PWD|API_KEY|CREDENTIAL`
//  6. JSON/YAML `"key": "value"` and `key: value` where the key name
//     matches the same secret-name lexicon (case-insensitive)
//
// Intentionally over-redacts in ambiguous cases — false positives
// (a redacted log line) cost the operator readability; false
// negatives (a leaked key) cost the operator a credential. The
// per-pattern markers (`[REDACTED:anthropic-key]`,
// `[REDACTED:env-var]`, etc.) help operators recognize the kind
// without revealing the value.
func RedactSecrets(s string) string {
	if s == "" {
		return s
	}
	for _, p := range redactPatterns {
		s = p.re.ReplaceAllString(s, p.repl)
	}
	return s
}

type redactRule struct {
	re   *regexp.Regexp
	repl string
}

var redactPatterns = []redactRule{
	// 1. PEM private keys (the entire armored block).
	//    `(?s)` makes `.` match newlines — required for multi-line.
	{regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`),
		"[REDACTED:private-key]"},

	// 2. Bare branded tokens.
	//    Anthropic: sk-ant-... (≥20 chars after the prefix).
	{regexp.MustCompile(`sk-ant-[A-Za-z0-9_\-]{20,}`),
		"[REDACTED:anthropic-key]"},
	//    OpenAI: sk-... but NOT sk-ant-... (handled above). 20+ chars
	//    of base64-ish trailing material.
	{regexp.MustCompile(`\bsk-(?:proj-)?[A-Za-z0-9_\-]{32,}`),
		"[REDACTED:openai-key]"},
	//    GitHub: ghp_/gho_/ghu_/ghs_/ghr_ + 36 chars.
	{regexp.MustCompile(`\bgh[opusr]_[A-Za-z0-9]{36,}`),
		"[REDACTED:github-token]"},
	//    GitLab: glpat-... (20+).
	{regexp.MustCompile(`\bglpat-[A-Za-z0-9_\-]{20,}`),
		"[REDACTED:gitlab-token]"},
	//    Slack: xox[abprs]-... or xapp-...
	{regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9\-]{10,}`),
		"[REDACTED:slack-token]"},
	{regexp.MustCompile(`\bxapp-[A-Za-z0-9\-]{10,}`),
		"[REDACTED:slack-app-token]"},
	//    Stripe: sk_live_/sk_test_/rk_live_/rk_test_ + 24+ chars.
	{regexp.MustCompile(`\b(?:sk|rk)_(?:live|test)_[A-Za-z0-9]{24,}`),
		"[REDACTED:stripe-key]"},
	//    AWS access key id: AKIA + 16 caps. Secret key follows in 40-char base64.
	{regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`),
		"[REDACTED:aws-access-key-id]"},

	// 3. Bearer tokens in HTTP headers / curl logs.
	{regexp.MustCompile(`(?i)\b(authorization\s*:\s*bearer)\s+\S+`),
		"$1 [REDACTED:bearer]"},

	// 4. JWTs: three base64url segments. Match only "looks like JWT"
	//    (header starts with "eyJ" which is base64 of '{"').
	{regexp.MustCompile(`\beyJ[A-Za-z0-9_\-]{10,}\.eyJ[A-Za-z0-9_\-]{10,}\.[A-Za-z0-9_\-]{10,}`),
		"[REDACTED:jwt]"},

	// 5. Env-style assignments.
	//    `KEY=value` on a line, where KEY contains a sensitive lexeme.
	//    Anchor at start-of-line OR after whitespace so we don't
	//    butcher arbitrary `=` inside values.
	{regexp.MustCompile(`(?im)(^|[ \t])([A-Z][A-Z0-9_]*(?:KEY|TOKEN|SECRET|PASSWORD|PASSWD|PWD|CREDENTIAL|CREDENTIALS|AUTH)[A-Z0-9_]*)\s*=\s*\S+`),
		"$1$2=[REDACTED:env-var]"},

	// 6. JSON/YAML key:value with sensitive keys.
	//    "api_key": "value" or "password": "value"
	{regexp.MustCompile(`(?i)("(?:api[_\-]?key|access[_\-]?key|secret(?:[_\-]?key)?|password|passwd|token|auth(?:orization)?|credential[s]?)"\s*:\s*)"[^"]*"`),
		`$1"[REDACTED:json-secret]"`},
	//    YAML: `password: value` — value runs to end-of-line.
	{regexp.MustCompile(`(?im)^([ \t]*(?:api[_\-]?key|access[_\-]?key|secret(?:[_\-]?key)?|password|passwd|token|auth(?:orization)?|credential[s]?)\s*:\s*)\S+.*$`),
		"$1[REDACTED:yaml-secret]"},
}

// QuickCheckHasSecret returns true if RedactSecrets would change s.
// Useful for tests and metrics — skips the actual replacement work.
func QuickCheckHasSecret(s string) bool {
	for _, p := range redactPatterns {
		if p.re.MatchString(s) {
			return true
		}
	}
	return false
}

// Compile-time guard: use strings package implicitly via regexp.
var _ = strings.Builder{}
