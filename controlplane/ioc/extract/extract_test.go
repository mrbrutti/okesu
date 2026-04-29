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
