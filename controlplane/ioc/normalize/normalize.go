// Package normalize provides pure-function normalizers for IOC values.
// All normalizers are deterministic and idempotent: f(f(x)) == f(x).
package normalize

import (
	"net"
	"net/url"
	"strings"

	"golang.org/x/net/idna"
)

// NormalizeHash lowercases and trims a hash value. Caller is expected to
// have already validated length/charset for the kind (sha256/sha1/md5).
func NormalizeHash(in string) string {
	return strings.ToLower(strings.TrimSpace(in))
}

// NormalizeIPv4 returns the canonical dotted-quad form (no leading zeros,
// no surrounding whitespace). Returns ok=false if the input does not parse
// as an IPv4 address. Tolerates leading zeros in octets (e.g. "01.02.03.04"),
// which Go 1.17+ net.ParseIP rejects by default.
func NormalizeIPv4(in string) (string, bool) {
	in = strings.TrimSpace(in)
	// Strip leading zeros from each octet so net.ParseIP accepts forms like
	// "01.02.03.04". We only do this if the input has exactly 4 dot-separated
	// numeric parts; anything else falls through to ParseIP unchanged.
	if parts := strings.Split(in, "."); len(parts) == 4 {
		stripped := make([]string, 4)
		allNumeric := true
		for i, p := range parts {
			if p == "" {
				allNumeric = false
				break
			}
			allDigits := true
			for _, r := range p {
				if r < '0' || r > '9' {
					allDigits = false
					break
				}
			}
			if !allDigits {
				allNumeric = false
				break
			}
			t := strings.TrimLeft(p, "0")
			if t == "" {
				t = "0"
			}
			stripped[i] = t
		}
		if allNumeric {
			in = strings.Join(stripped, ".")
		}
	}
	ip := net.ParseIP(in)
	if ip == nil {
		return "", false
	}
	v4 := ip.To4()
	if v4 == nil {
		return "", false
	}
	return v4.String(), true
}

// NormalizeIPv6 returns the canonical RFC 5952 form. Returns ok=false if
// the input does not parse as an IPv6 address.
func NormalizeIPv6(in string) (string, bool) {
	in = strings.TrimSpace(in)
	ip := net.ParseIP(in)
	if ip == nil || ip.To4() != nil {
		return "", false
	}
	return ip.String(), true
}

// NormalizeDomain lowercases the input, strips trailing dots, and applies
// Punycode (IDNA) to non-ASCII labels. Returns ok=false on encoding error.
func NormalizeDomain(in string) (string, bool) {
	in = strings.TrimSpace(in)
	in = strings.TrimSuffix(in, ".")
	in = strings.ToLower(in)
	if in == "" {
		return "", false
	}
	out, err := idna.Lookup.ToASCII(in)
	if err != nil {
		return "", false
	}
	return out, true
}

// NormalizeURL returns a string with scheme + host normalized; path is
// preserved as-is. Returns ok=false if url.Parse fails or the input has
// no scheme/host (url.Parse accepts bare paths like "example.com" as
// valid; we reject those because we expect callers to canonicalize
// fully-qualified URLs only).
func NormalizeURL(in string) (string, bool) {
	in = strings.TrimSpace(in)
	if in == "" {
		return "", false
	}
	u, err := url.Parse(in)
	if err != nil {
		return "", false
	}
	if u.Scheme == "" || u.Host == "" {
		return "", false
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	return u.String(), true
}

// Refang reverses common defanging conventions used by analysts to share
// IOCs without making them clickable. Idempotent on already-fanged input.
//
//	example[.]com   -> example.com
//	hxxps://x       -> https://x
//	hxxp://x        -> http://x
//	1[.]2[.]3[.]4   -> 1.2.3.4
func Refang(in string) string {
	out := strings.ReplaceAll(in, "[.]", ".")
	out = strings.ReplaceAll(out, "[:]", ":")
	out = strings.ReplaceAll(out, "(.)", ".")
	out = strings.ReplaceAll(out, "hxxps://", "https://")
	out = strings.ReplaceAll(out, "hxxp://", "http://")
	return out
}
