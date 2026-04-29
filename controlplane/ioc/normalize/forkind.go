package normalize

// NormalizeForKind returns the canonical form of value for the given IOC
// kind, plus a bool indicating whether the value was successfully
// normalized by a kind-specific normalizer.
//
// For typed kinds (ipv4, ipv6, domain, url, sha256, sha1, md5), ok=false
// signals the value did not match the kind's expected shape. Callers
// curating data (e.g., the catalog YAML loader) should treat this as
// an authoring error. Callers extracting from free-form text (e.g., the
// finding-ingest extractor) can ignore ok and use the trimmed lowercase
// fallback that's still returned.
//
// For untyped kinds (cve, mitre, yara_rule, sigma_rule, anything else),
// ok=true and the value is normalized via lowercase+trim — there is no
// purpose-built canonicalizer for them, but the form is still
// deterministic enough to dedup on.
//
// The function is the single source of truth for "given a kind, what
// does the canonical bytes-on-disk form look like?" Catalog writers and
// finding-ingest extractors must converge on the same answer or lookups
// silently miss.
func NormalizeForKind(kind, value string) (string, bool) {
	value = Refang(value)
	switch kind {
	case "sha256":
		v := NormalizeHash(value)
		return v, isHexLen(v, 64)
	case "sha1":
		v := NormalizeHash(value)
		return v, isHexLen(v, 40)
	case "md5":
		v := NormalizeHash(value)
		return v, isHexLen(v, 32)
	case "ipv4":
		return NormalizeIPv4(value)
	case "ipv6":
		return NormalizeIPv6(value)
	case "domain":
		return NormalizeDomain(value)
	case "url":
		return NormalizeURL(value)
	}
	return NormalizeHash(value), true
}

func isHexLen(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(('0' <= c && c <= '9') || ('a' <= c && c <= 'f')) {
			return false
		}
	}
	return true
}
