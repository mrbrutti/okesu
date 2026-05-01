// Package parser turns raw bytes from a feed fetch into
// []catalog.CatalogEntry rows ready to upsert into the iocs table.
//
// One file per parser; each parser is a pure function from raw bytes
// to entries. The Worker (controlplane/ioc/feeds/worker.go) selects
// which parser to call based on the FeedConfig.Parser enum.
package parser

import (
	"regexp"
	"strings"

	"github.com/section9labs/okesu/controlplane/ioc/catalog"
	"github.com/section9labs/okesu/controlplane/ioc/normalize"
)

// yaraRuleHeader matches "rule Name { ... }" or "rule Name : tag1 tag2 { ... }"
// at the top level of a .yar file. We split on these.
var yaraRuleHeader = regexp.MustCompile(`(?m)^[\t ]*rule[\t ]+([A-Za-z_][A-Za-z0-9_]*)\b`)

// ParseYARABundle splits a multi-rule .yar document into one
// CatalogEntry per rule and uses catalog.ParseYARAHeader to extract
// name/tags/severity from the rule body. Lines outside any rule
// (top-level comments, includes, imports) are dropped silently —
// rules they decorate carry the metadata via their own header.
func ParseYARABundle(body []byte) ([]catalog.CatalogEntry, error) {
	src := string(body)
	idx := yaraRuleHeader.FindAllStringIndex(src, -1)
	if len(idx) == 0 {
		return nil, nil
	}
	var out []catalog.CatalogEntry
	for i, m := range idx {
		start := m[0]
		end := len(src)
		if i+1 < len(idx) {
			end = idx[i+1][0]
		}
		ruleBody := strings.TrimSpace(src[start:end])
		name, tags, severity := catalog.ParseYARAHeader(ruleBody)
		norm, ok := normalize.NormalizeForKind("yara_rule", ruleBody)
		if !ok {
			continue
		}
		out = append(out, catalog.CatalogEntry{
			Kind:            "yara_rule",
			Value:           ruleBody,
			NormalizedValue: norm,
			Name:            name,
			Tags:            strings.Join(tags, ","),
			SeverityFloor:   severity,
		})
	}
	return out, nil
}
