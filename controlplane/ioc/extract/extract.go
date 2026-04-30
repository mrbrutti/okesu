// Package extract pulls IOCs out of free-form text. Best-effort: returns
// every match it finds; caller decides what to do with low-confidence
// hits. Idempotent (handles defanged input via normalize.Refang first).
package extract

import (
	"regexp"
	"strings"

	"github.com/section9labs/okesu/controlplane/ioc/normalize"
)

// Hit is one extracted IOC. Value is the raw match (post-refang);
// NormalizedValue is what to use as a dedup key.
type Hit struct {
	Kind            string
	Value           string
	NormalizedValue string
}

// Compile-once regexes. Boundaries err on the side of false-positives;
// we'd rather over-extract and let the IOC table dedup than miss things.
var (
	reSHA256 = regexp.MustCompile(`\b[a-fA-F0-9]{64}\b`)
	reSHA1   = regexp.MustCompile(`\b[a-fA-F0-9]{40}\b`)
	reMD5    = regexp.MustCompile(`\b[a-fA-F0-9]{32}\b`)
	reIPv4   = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`)
	reIPv6   = regexp.MustCompile(`\b[0-9a-fA-F:]{2,}:[0-9a-fA-F:]+\b`)
	reDomain = regexp.MustCompile(`\b(?:[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?\.)+[a-zA-Z]{2,}\b`)
	reURL    = regexp.MustCompile(`https?://[^\s)>"']+`)
	reCVE    = regexp.MustCompile(`\bCVE-\d{4}-\d{4,7}\b`)
	reMITRE  = regexp.MustCompile(`\bT\d{4}(?:\.\d{3})?\b`)
)

// Extract returns the unique set of IOCs found in the text. Order is
// stable: by kind, then first-seen position.
func Extract(text string) []Hit {
	text = normalize.Refang(text)

	var hits []Hit
	seen := make(map[string]bool) // kind|normalized

	add := func(kind, value, norm string) {
		if norm == "" {
			return
		}
		key := kind + "|" + norm
		if seen[key] {
			return
		}
		seen[key] = true
		hits = append(hits, Hit{Kind: kind, Value: value, NormalizedValue: norm})
	}

	// Hashes — sha256 first (most specific), then sha1/md5 with overlap
	// guard to avoid double-emitting an md5 prefix of a sha256 match.
	sha256Spans := reSHA256.FindAllStringIndex(text, -1)
	for _, span := range sha256Spans {
		v := text[span[0]:span[1]]
		add("sha256", v, normalize.NormalizeHash(v))
	}
	for _, span := range reSHA1.FindAllStringIndex(text, -1) {
		if overlapsAny(span, sha256Spans) {
			continue
		}
		v := text[span[0]:span[1]]
		add("sha1", v, normalize.NormalizeHash(v))
	}
	for _, span := range reMD5.FindAllStringIndex(text, -1) {
		if overlapsAny(span, sha256Spans) {
			continue
		}
		v := text[span[0]:span[1]]
		add("md5", v, normalize.NormalizeHash(v))
	}

	// IPs.
	for _, m := range reIPv4.FindAllString(text, -1) {
		if v, ok := normalize.NormalizeIPv4(m); ok {
			add("ipv4", m, v)
		}
	}
	for _, m := range reIPv6.FindAllString(text, -1) {
		if v, ok := normalize.NormalizeIPv6(m); ok {
			add("ipv6", m, v)
		}
	}

	// URLs (extract before domains so we capture the full URL).
	// trimTrailingPunct strips characters that are usually sentence
	// punctuation rather than part of the URL — `https://example.com/foo,`
	// in `"see https://example.com/foo, then click"` should yield the
	// URL without the trailing comma.
	urlSpans := reURL.FindAllStringIndex(text, -1)
	for i, span := range urlSpans {
		m := text[span[0]:span[1]]
		trimmed := strings.TrimRight(m, ".,;:!?]}>)")
		if trimmed != m {
			urlSpans[i] = []int{span[0], span[0] + len(trimmed)}
			m = trimmed
		}
		if v, ok := normalize.NormalizeURL(m); ok {
			add("url", m, v)
		}
	}

	// Domains — skip those entirely inside a URL match to avoid duplication.
	for _, span := range reDomain.FindAllStringIndex(text, -1) {
		if overlapsAny(span, urlSpans) {
			continue
		}
		m := text[span[0]:span[1]]
		if v, ok := normalize.NormalizeDomain(m); ok {
			add("domain", m, v)
		}
	}

	for _, m := range reCVE.FindAllString(text, -1) {
		add("cve", m, strings.ToUpper(m))
	}
	for _, m := range reMITRE.FindAllString(text, -1) {
		add("mitre", m, strings.ToUpper(m))
	}

	return hits
}

func overlapsAny(span []int, others [][]int) bool {
	for _, o := range others {
		if span[0] < o[1] && o[0] < span[1] {
			return true
		}
	}
	return false
}
