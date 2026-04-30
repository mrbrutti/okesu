package normalize

import "testing"

func TestNormalizeHash(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"ABC123", "abc123"},
		{"AbCdEf01234567890123456789012345678901234567890123456789012345AB", "abcdef01234567890123456789012345678901234567890123456789012345ab"},
		{"  abc ", "abc"},
	}
	for _, c := range cases {
		if got := NormalizeHash(c.in); got != c.want {
			t.Errorf("NormalizeHash(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestNormalizeIPv4(t *testing.T) {
	cases := []struct {
		in, want string
		ok       bool
	}{
		{"1.2.3.4", "1.2.3.4", true},
		{"01.02.03.04", "1.2.3.4", true},
		{"  10.0.0.1  ", "10.0.0.1", true},
		{"not-an-ip", "", false},
	}
	for _, c := range cases {
		got, ok := NormalizeIPv4(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("NormalizeIPv4(%q) = (%q, %v), want (%q, %v)", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestNormalizeDomain(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"Example.COM", "example.com"},
		{"münchen.de", "xn--mnchen-3ya.de"},
		{"  TRAILING.dot.  ", "trailing.dot"},
	}
	for _, c := range cases {
		got, _ := NormalizeDomain(c.in)
		if got != c.want {
			t.Errorf("NormalizeDomain(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestRefang(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"example[.]com", "example.com"},
		{"hxxps://evil.example[.]com/path", "https://evil.example.com/path"},
		{"hxxp://example[.]com", "http://example.com"},
		{"plain.example.com", "plain.example.com"},
		{"1[.]2[.]3[.]4", "1.2.3.4"},
	}
	for _, c := range cases {
		if got := Refang(c.in); got != c.want {
			t.Errorf("Refang(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestNormalizeIPv6(t *testing.T) {
	cases := []struct {
		in, want string
		ok       bool
	}{
		{"2001:DB8::1", "2001:db8::1", true},
		{"::ffff:1.2.3.4", "", false}, // IPv4-mapped — reject
		{"not-v6", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		got, ok := NormalizeIPv6(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("NormalizeIPv6(%q) = (%q, %v), want (%q, %v)", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestNormalizeURL(t *testing.T) {
	cases := []struct {
		in, want string
		ok       bool
	}{
		{"https://Example.COM/Path", "https://example.com/Path", true},
		{"HTTP://Example.com", "http://example.com", true},
		{"", "", false},
		{"example.com", "", false},        // no scheme — reject
		{"https://", "", false},           // no host — reject
		{"  ", "", false},                 // whitespace-only — reject
	}
	for _, c := range cases {
		got, ok := NormalizeURL(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("NormalizeURL(%q) = (%q, %v), want (%q, %v)", c.in, got, ok, c.want, c.ok)
		}
	}
}
