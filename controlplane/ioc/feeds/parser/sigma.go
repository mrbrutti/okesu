package parser

import (
	"bytes"
	"strings"

	"github.com/section9labs/okesu/controlplane/ioc/catalog"
	"github.com/section9labs/okesu/controlplane/ioc/normalize"
)

// ParseSigmaBundle splits a multi-doc Sigma YAML stream (`---`-separated)
// into one CatalogEntry per doc and uses catalog.ParseSigmaHeader to
// extract title/tags/level from each doc body. Empty / whitespace-only
// docs are skipped silently. A single-doc input (no `---`) produces one
// entry.
func ParseSigmaBundle(body []byte) ([]catalog.CatalogEntry, error) {
	docs := splitYAMLDocs(body)
	var out []catalog.CatalogEntry
	for _, doc := range docs {
		trimmed := strings.TrimSpace(string(doc))
		if trimmed == "" {
			continue
		}
		name, tags, severity := catalog.ParseSigmaHeader(trimmed)
		norm, ok := normalize.NormalizeForKind("sigma_rule", trimmed)
		if !ok {
			continue
		}
		out = append(out, catalog.CatalogEntry{
			Kind:            "sigma_rule",
			Value:           trimmed,
			NormalizedValue: norm,
			Name:            name,
			Tags:            strings.Join(tags, ","),
			SeverityFloor:   severity,
		})
	}
	return out, nil
}

// splitYAMLDocs splits on lines that, after trimming, are exactly "---" —
// the canonical multi-doc separator. Preserves doc bodies; never splits
// on document content that happens to contain "---" inside a value.
func splitYAMLDocs(body []byte) [][]byte {
	lines := bytes.Split(body, []byte("\n"))
	var docs [][]byte
	var cur bytes.Buffer
	flush := func() {
		if cur.Len() > 0 {
			b := make([]byte, cur.Len())
			copy(b, cur.Bytes())
			docs = append(docs, b)
			cur.Reset()
		}
	}
	for _, ln := range lines {
		if bytes.Equal(bytes.TrimSpace(ln), []byte("---")) {
			flush()
			continue
		}
		cur.Write(ln)
		cur.WriteByte('\n')
	}
	flush()
	return docs
}
