package catalog

import (
	"reflect"
	"testing"
)

func TestParseSigmaHeader_Basic(t *testing.T) {
	body := `title: Suspicious PowerShell Base64 Encoded Command
id: 12345678-1234-1234-1234-123456789012
status: stable
description: Detects suspicious PowerShell with -EncodedCommand
author: operator@example.com
date: 2026/04/29
tags:
    - attack.execution
    - attack.t1059.001
level: high
logsource:
    product: windows
    category: process_creation
detection:
    selection:
        Image|endswith: '\powershell.exe'
    condition: selection
`
	title, tags, level := ParseSigmaHeader(body)
	if title != "Suspicious PowerShell Base64 Encoded Command" {
		t.Errorf("title = %q", title)
	}
	wantTags := []string{"attack.execution", "attack.t1059.001"}
	if !reflect.DeepEqual(tags, wantTags) {
		t.Errorf("tags = %v, want %v", tags, wantTags)
	}
	if level != "HIGH" {
		t.Errorf("level = %q, want HIGH (parser must uppercase)", level)
	}
}

func TestParseSigmaHeader_MinimalNoTags(t *testing.T) {
	body := `title: Minimal Rule
detection:
    selection:
        EventID: 4625
    condition: selection
`
	title, tags, level := ParseSigmaHeader(body)
	if title != "Minimal Rule" {
		t.Errorf("title = %q", title)
	}
	if len(tags) != 0 {
		t.Errorf("tags should be empty; got %v", tags)
	}
	if level != "" {
		t.Errorf("level should be empty; got %q", level)
	}
}

func TestParseSigmaHeader_Garbage(t *testing.T) {
	body := "not yaml at all: : : ::"
	// Not valid YAML — parser must not panic; returns empty.
	title, tags, level := ParseSigmaHeader(body)
	if title != "" || len(tags) != 0 || level != "" {
		t.Errorf("garbage input should yield empty values; got title=%q tags=%v level=%q",
			title, tags, level)
	}
}
