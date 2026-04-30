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
