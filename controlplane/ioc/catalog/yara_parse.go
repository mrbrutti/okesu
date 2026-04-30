package catalog

import (
	"regexp"
	"strings"
)

// ParseYARAHeader pulls metadata out of a YARA rule body without
// requiring a full YARA grammar. Operators ship rule bodies verbatim
// in the catalog YAML; this lets us populate `name`, `tags`, and
// `severity_floor` automatically so the YAML wrapper stays minimal.
//
// Recognized:
//   - The `rule <name>` declaration line, optionally followed by a
//     `: <tag1> <tag2>` tag list.
//   - A `meta:` block with a `severity = "<value>"` line. Severity is
//     uppercased on return to match the rest of the codebase's
//     SEVERITY_FLOOR convention.
//
// On garbage input every return value is empty — callers should treat
// "no metadata extracted" as "use the YAML wrapper's explicit values
// (or none)".
//
// Intentionally NOT supported: nested rules, rule includes, complex
// meta types (booleans/integers). The catalog format is for curated
// rules where the author can be expected to use a clean header; if a
// rule needs richer metadata, the operator can override via the YAML
// wrapper's explicit `name`/`tags`/`severity_floor` fields.
func ParseYARAHeader(body string) (name string, tags []string, severity string) {
	if m := yaraRuleHeaderRE.FindStringSubmatch(body); m != nil {
		name = m[1]
		if m[2] != "" {
			for _, t := range strings.Fields(m[2]) {
				tags = append(tags, t)
			}
		}
	}
	if m := yaraSeverityRE.FindStringSubmatch(body); m != nil {
		severity = strings.ToUpper(strings.TrimSpace(m[1]))
	}
	return name, tags, severity
}

// `rule <name>` declaration. Tag list (`: t1 t2`) is optional; we
// capture everything up to the opening brace and split on whitespace.
var yaraRuleHeaderRE = regexp.MustCompile(`(?m)^\s*rule\s+([A-Za-z_][A-Za-z0-9_]*)\s*(?::\s*([^{]*?))?\s*\{`)

// `severity = "<value>"` inside a meta block. Quotes are required —
// YARA's grammar makes meta string values quoted, so an unquoted value
// is malformed and we skip it.
var yaraSeverityRE = regexp.MustCompile(`(?m)severity\s*=\s*"([^"]*)"`)
