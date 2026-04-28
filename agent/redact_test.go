package agent

import (
	"strings"
	"testing"
)

func TestRedactSecrets(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string // marker that should appear in output
		gone string // substring that should NOT appear in output
	}{
		{
			"anthropic key",
			"export ANTHROPIC_API_KEY=sk-ant-api03-abcDEF123456789012345678901234",
			"[REDACTED:env-var]",
			"sk-ant-api03",
		},
		{
			"openai key bare",
			`{"key": "sk-proj-aBcDeFgHiJkLmNoPqRsTuVwXyZ1234567890aBcD"}`,
			"[REDACTED",
			"aBcDeFgH",
		},
		{
			"github token bare (lowercase prefix)",
			"token=ghp_aBcDeFgHiJkLmNoPqRsTuVwXyZ1234567890ab found",
			"[REDACTED:github-token]",
			"ghp_aBcDeFgH",
		},
		{
			"github token in prose",
			"my token is ghp_aBcDeFgHiJkLmNoPqRsTuVwXyZ1234567890ab thanks",
			"[REDACTED:github-token]",
			"ghp_aBcDeFgH",
		},
		{
			"AWS access key",
			"AKIAIOSFODNN7EXAMPLE was here",
			"[REDACTED:aws-access-key-id]",
			"AKIAIOSFODNN7EXAMPLE",
		},
		{
			"bearer header",
			"Authorization: Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.signaturehere",
			"[REDACTED:",
			"eyJhbGciOi",
		},
		{
			"PEM private key",
			`-----BEGIN OPENSSH PRIVATE KEY-----
b3BlbnNzaC1rZXktdjEAAAAACmFlczI1Ni1jdHIAAAAGYmNyeXB0
-----END OPENSSH PRIVATE KEY-----`,
			"[REDACTED:private-key]",
			"openssh-key-v1",
		},
		{
			"env-var assignment with TOKEN",
			"OKESU_WEBHOOK_TOKEN=super-secret-value",
			"[REDACTED:env-var]",
			"super-secret-value",
		},
		{
			"env-var assignment with PASSWORD",
			"DB_PASSWORD=hunter2",
			"[REDACTED:env-var]",
			"hunter2",
		},
		{
			"json api_key",
			`{"api_key": "abc123def456ghi789"}`,
			"[REDACTED:json-secret]",
			"abc123def456",
		},
		{
			"yaml password",
			"password: hunter2",
			"[REDACTED:yaml-secret]",
			"hunter2",
		},
		{
			"benign output untouched",
			"agent edr started successfully on host node-debian",
			"", // no marker expected
			"",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := RedactSecrets(tc.in)
			if tc.want != "" && !strings.Contains(got, tc.want) {
				t.Errorf("expected marker %q in output, got %q", tc.want, got)
			}
			if tc.gone != "" && strings.Contains(got, tc.gone) {
				t.Errorf("secret value %q leaked into output: %q", tc.gone, got)
			}
		})
	}
}

func TestRedactSecrets_PreservesStructure(t *testing.T) {
	// A multi-line env-style block should keep its line breaks +
	// non-secret lines intact.
	in := `PATH=/usr/local/bin:/usr/bin:/bin
HOME=/home/okesu
ANTHROPIC_API_KEY=sk-ant-api03-aBcDeFgHiJkLmNoPqRsTuVwXyZ1234567890ab
USER=okesu`
	got := RedactSecrets(in)
	if !strings.Contains(got, "PATH=/usr/local/bin:/usr/bin:/bin") {
		t.Error("PATH line should be preserved")
	}
	if !strings.Contains(got, "USER=okesu") {
		t.Error("USER line should be preserved")
	}
	if strings.Contains(got, "sk-ant-api03") {
		t.Errorf("anthropic key leaked: %s", got)
	}
}
