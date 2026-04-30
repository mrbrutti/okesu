package orchestrator

import (
	"strings"
	"testing"
)

func TestEvaluateEnrichmentFilter_EmptyFiresOnAny(t *testing.T) {
	// Unlike findings (where empty filter means "don't fire"),
	// enrichment writes are rare so empty means "fire on any
	// enrichment". This test locks that contract.
	got, err := EvaluateEnrichmentFilter("", EnrichmentPayload{
		IOCID: 1, IOCKind: "sha256", Adapter: "vt", Verdict: "clean",
	})
	if err != nil {
		t.Fatalf("filter: %v", err)
	}
	if !got {
		t.Fatalf("empty filter: got false, want true")
	}
}

func TestEvaluateEnrichmentFilter_VerdictMatch(t *testing.T) {
	cases := []struct {
		filter string
		p      EnrichmentPayload
		want   bool
	}{
		{
			filter: "enrichment.verdict == 'malicious'",
			p:      EnrichmentPayload{Verdict: "malicious"},
			want:   true,
		},
		{
			filter: "enrichment.verdict == 'malicious'",
			p:      EnrichmentPayload{Verdict: "clean"},
			want:   false,
		},
		{
			filter: "enrichment.adapter == 'virustotal' && enrichment.score > 80",
			p:      EnrichmentPayload{Adapter: "virustotal", Score: 90},
			want:   true,
		},
		{
			filter: "enrichment.adapter == 'virustotal' && enrichment.score > 80",
			p:      EnrichmentPayload{Adapter: "virustotal", Score: 50},
			want:   false,
		},
		{
			filter: "enrichment.kind == 'sha256' && enrichment.verdict in ['malicious','suspicious']",
			p:      EnrichmentPayload{IOCKind: "sha256", Verdict: "suspicious"},
			want:   true,
		},
	}
	for _, c := range cases {
		got, err := EvaluateEnrichmentFilter(c.filter, c.p)
		if err != nil {
			t.Errorf("filter %q: %v", c.filter, err)
			continue
		}
		if got != c.want {
			t.Errorf("filter %q on %+v: got %v, want %v", c.filter, c.p, got, c.want)
		}
	}
}

func TestEnrichmentTriggerPayload_Shape(t *testing.T) {
	got := EnrichmentTriggerPayload(EnrichmentPayload{
		IOCID:           42,
		IOCKind:         "sha256",
		NormalizedValue: "abc123",
		Adapter:         "virustotal",
		Verdict:         "malicious",
		Score:           95,
	})
	for _, expected := range []string{
		`"kind":"ioc_enriched"`,
		`"ioc_id":42`,
		`"ioc_kind":"sha256"`,
		`"normalized_value":"abc123"`,
		`"adapter":"virustotal"`,
		`"verdict":"malicious"`,
		`"score":95`,
	} {
		if !strings.Contains(got, expected) {
			t.Errorf("payload missing %s: %s", expected, got)
		}
	}
}

// TestSpec_AllowsIOCEnrichedTrigger checks that the spec validator
// accepts `on: ioc_enriched` (with or without a filter).
func TestSpec_AllowsIOCEnrichedTrigger(t *testing.T) {
	src := `---
version: 1
name: test-enriched-trigger
description: A test orchestration
trigger:
  on: ioc_enriched
  filter: "enrichment.verdict == 'malicious'"
steps:
  - id: noop
    agent: investigator
    prompt: "noop"
---
`
	if _, err := Parse(src); err != nil {
		t.Fatalf("parse: %v", err)
	}

	// And without filter — should also pass.
	src2 := `---
version: 1
name: test-enriched-trigger-noflt
description: A test orchestration
trigger:
  on: ioc_enriched
steps:
  - id: noop
    agent: investigator
    prompt: "noop"
---
`
	if _, err := Parse(src2); err != nil {
		t.Fatalf("parse without filter: %v", err)
	}
}
