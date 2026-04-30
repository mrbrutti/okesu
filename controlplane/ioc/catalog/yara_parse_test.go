package catalog

import (
	"reflect"
	"testing"
)

func TestParseYARAHeader_Basic(t *testing.T) {
	body := `rule SuspiciousPowerShell : powershell suspicious
{
    meta:
        description = "Detects suspicious PowerShell encoded commands"
        author = "operator@example.com"
        severity = "HIGH"
    strings:
        $a = "FromBase64String"
    condition:
        $a
}`
	name, tags, severity := ParseYARAHeader(body)
	if name != "SuspiciousPowerShell" {
		t.Errorf("name = %q, want SuspiciousPowerShell", name)
	}
	wantTags := []string{"powershell", "suspicious"}
	if !reflect.DeepEqual(tags, wantTags) {
		t.Errorf("tags = %v, want %v", tags, wantTags)
	}
	if severity != "HIGH" {
		t.Errorf("severity = %q, want HIGH", severity)
	}
}

func TestParseYARAHeader_NoTagsNoSeverity(t *testing.T) {
	body := `rule MinimalRule
{
    condition:
        true
}`
	name, tags, severity := ParseYARAHeader(body)
	if name != "MinimalRule" {
		t.Errorf("name = %q", name)
	}
	if len(tags) != 0 {
		t.Errorf("tags should be empty; got %v", tags)
	}
	if severity != "" {
		t.Errorf("severity = %q, want empty", severity)
	}
}

func TestParseYARAHeader_LowercaseSeverity(t *testing.T) {
	body := `rule R { meta: severity = "high" condition: true }`
	_, _, severity := ParseYARAHeader(body)
	// Parser uppercases since severity_floor is uppercase in the rest
	// of the codebase.
	if severity != "HIGH" {
		t.Errorf("severity = %q, want HIGH (parser must uppercase)", severity)
	}
}

func TestParseYARAHeader_Garbage(t *testing.T) {
	// Not a YARA rule — parser must not panic; returns empty.
	body := `not a rule, just some text`
	name, tags, severity := ParseYARAHeader(body)
	if name != "" || len(tags) != 0 || severity != "" {
		t.Errorf("garbage input should yield empty values; got name=%q tags=%v severity=%q",
			name, tags, severity)
	}
}

// `private rule X { ... }` and `global rule X { ... }` are valid YARA
// syntax. Third-party rule corpora (ESET, Mandiant, neo23x0/signature-base)
// ship a meaningful fraction of rules with those modifiers; they affect
// the YARA engine's matching semantics but not the rule's identity, so
// the parser must extract the same name/tags as the un-modified form.
func TestParseYARAHeader_PrivateModifier(t *testing.T) {
	body := `private rule InternalHelper : helper internal
{
    meta:
        severity = "LOW"
    condition:
        true
}`
	name, tags, severity := ParseYARAHeader(body)
	if name != "InternalHelper" {
		t.Errorf("name = %q, want InternalHelper", name)
	}
	if len(tags) != 2 || tags[0] != "helper" || tags[1] != "internal" {
		t.Errorf("tags = %v, want [helper internal]", tags)
	}
	if severity != "LOW" {
		t.Errorf("severity = %q, want LOW", severity)
	}
}

func TestParseYARAHeader_GlobalModifier(t *testing.T) {
	body := `global rule SystemwideMatch { condition: true }`
	name, _, _ := ParseYARAHeader(body)
	if name != "SystemwideMatch" {
		t.Errorf("name = %q, want SystemwideMatch", name)
	}
}

// Both modifiers can appear together (`global private` or `private global`).
// The parser handles arbitrary order/repetition.
func TestParseYARAHeader_PrivateAndGlobal(t *testing.T) {
	body := `private global rule HiddenSystemwide { condition: true }`
	name, _, _ := ParseYARAHeader(body)
	if name != "HiddenSystemwide" {
		t.Errorf("name = %q, want HiddenSystemwide", name)
	}
}
