package catalog

import (
	"strings"

	"gopkg.in/yaml.v3"
)

// ParseSigmaHeader pulls operator-facing metadata out of a Sigma rule
// body. Sigma rules ARE YAML, so we parse them as such — but we only
// touch the keys we care about, ignoring everything else (detection,
// logsource, falsepositives, references, fields, etc.) so a malformed
// detection block doesn't break catalog ingest. The catalog stores
// the rule body verbatim in iocs.value; downstream consumers parse the
// detection block themselves if/when they grow Sigma-aware.
//
// Recognized:
//   - `title:` (becomes the IOC name)
//   - `tags:` list (becomes comma-joined ioc tags)
//   - `level:` (becomes severity_floor; uppercased to match the rest
//     of the codebase's SEVERITY_FLOOR convention).
//
// Returns empty strings/nil slice on parse failure — caller treats
// that as "use the YAML wrapper's explicit values".
func ParseSigmaHeader(body string) (title string, tags []string, level string) {
	var shape sigmaHeaderShape
	if err := yaml.Unmarshal([]byte(body), &shape); err != nil {
		return "", nil, ""
	}
	title = strings.TrimSpace(shape.Title)
	if len(shape.Tags) > 0 {
		tags = make([]string, 0, len(shape.Tags))
		for _, t := range shape.Tags {
			if t = strings.TrimSpace(t); t != "" {
				tags = append(tags, t)
			}
		}
	}
	level = strings.ToUpper(strings.TrimSpace(shape.Level))
	return title, tags, level
}

type sigmaHeaderShape struct {
	Title string   `yaml:"title"`
	Tags  []string `yaml:"tags"`
	Level string   `yaml:"level"`
}
