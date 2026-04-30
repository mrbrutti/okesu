package extract

import (
	"reflect"
	"sort"
	"testing"
)

func TestExtract_FindsExpectedKinds(t *testing.T) {
	in := `Saw hash 0000000000000000000000000000000000000000000000000000000000000000 talking
to 1.2.3.4 and example[.]com referencing CVE-2024-1234 and T1059.001.`
	got := Extract(in)
	wantKinds := []string{"cve", "domain", "ipv4", "mitre", "sha256"}
	sort.Strings(wantKinds)
	gotKinds := make([]string, 0, len(got))
	for _, h := range got {
		gotKinds = append(gotKinds, h.Kind)
	}
	sort.Strings(gotKinds)
	if !reflect.DeepEqual(gotKinds, wantKinds) {
		t.Errorf("kinds = %v, want %v", gotKinds, wantKinds)
	}
}

func TestExtract_NormalizesValues(t *testing.T) {
	got := Extract("seen at example[.]COM and 01.02.03.04")
	for _, h := range got {
		if h.Kind == "domain" && h.NormalizedValue != "example.com" {
			t.Errorf("domain not normalized: %+v", h)
		}
		if h.Kind == "ipv4" && h.NormalizedValue != "1.2.3.4" {
			t.Errorf("ipv4 not normalized: %+v", h)
		}
	}
}

func TestExtract_DedupesWithinInput(t *testing.T) {
	got := Extract("1.2.3.4 1.2.3.4 1.2.3.4")
	count := 0
	for _, h := range got {
		if h.Kind == "ipv4" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected one ipv4 hit; got %d", count)
	}
}

func TestExtract_URLTrimsTrailingPunctuation(t *testing.T) {
	cases := []struct {
		in, wantNorm string
	}{
		{"see https://example.com/foo, then click", "https://example.com/foo"},
		{"check [https://example.com/foo] also", "https://example.com/foo"},
		{"the link is https://example.com/foo.", "https://example.com/foo"},
	}
	for _, c := range cases {
		got := Extract(c.in)
		var found bool
		for _, h := range got {
			if h.Kind == "url" && h.NormalizedValue == c.wantNorm {
				found = true
			}
		}
		if !found {
			t.Errorf("Extract(%q) did not yield a URL hit normalized to %q; got hits=%+v", c.in, c.wantNorm, got)
		}
	}
}
