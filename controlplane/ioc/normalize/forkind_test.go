package normalize

import (
	"strings"
	"testing"
)

func TestNormalizeForKind(t *testing.T) {
	cases := []struct {
		name     string
		kind     string
		value    string
		want     string
		wantOK   bool
	}{
		{"sha256 valid", "sha256", "ABC0000000000000000000000000000000000000000000000000000000000000", "abc0000000000000000000000000000000000000000000000000000000000000", true},
		{"sha256 too short", "sha256", "abc", "abc", false},
		{"sha1 valid", "sha1", strRepeat("AbCdEf01", 5), strRepeat("abcdef01", 5), true},
		{"md5 valid", "md5", strRepeat("DeAdBeEf", 4), strRepeat("deadbeef", 4), true},
		{"ipv4 valid", "ipv4", "1.2.3.4", "1.2.3.4", true},
		{"ipv4 invalid", "ipv4", "not-an-ip", "", false},
		{"ipv6 valid", "ipv6", "2001:DB8::1", "2001:db8::1", true},
		{"domain valid", "domain", "Example.COM", "example.com", true},
		{"domain refanged", "domain", "example[.]com", "example.com", true},
		{"url valid", "url", "HTTPS://Example.COM/p", "https://example.com/p", true},
		{"url no scheme", "url", "example.com", "", false},
		{"cve untyped", "cve", "CVE-2024-1234", "cve-2024-1234", true},
		{"unknown kind", "spaceship", "VALUE", "value", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := NormalizeForKind(c.kind, c.value)
			if got != c.want || ok != c.wantOK {
				t.Errorf("NormalizeForKind(%q, %q) = (%q, %v); want (%q, %v)", c.kind, c.value, got, ok, c.want, c.wantOK)
			}
		})
	}
}

func strRepeat(s string, n int) string {
	var b strings.Builder
	b.Grow(len(s) * n)
	for range n {
		b.WriteString(s)
	}
	return b.String()
}
