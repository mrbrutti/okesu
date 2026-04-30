package api

import (
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/section9labs/okesu/controlplane/db"
)

func mergeTs(s string) time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return t
}

func TestMergeIOCs_DedupsAndAggregates(t *testing.T) {
	cpA := &CPSourceRef{InstanceID: "cp-a", DisplayName: "A"}
	cpB := &CPSourceRef{InstanceID: "cp-b", DisplayName: "B"}
	rowsA := []*db.IOCRecord{
		{ID: 1, Kind: "sha256", NormalizedValue: "abc", Source: "catalog", Name: "from-a", Tags: "ransomware",
			ObservationCount: 3, FirstSeen: mergeTs("2026-04-01T00:00:00Z"), LastSeen: mergeTs("2026-04-15T00:00:00Z")},
	}
	rowsB := []*db.IOCRecord{
		{ID: 9, Kind: "sha256", NormalizedValue: "abc", Source: "observed", Name: "", Tags: "emotet",
			ObservationCount: 5, FirstSeen: mergeTs("2026-04-10T00:00:00Z"), LastSeen: mergeTs("2026-04-20T00:00:00Z")},
	}
	merged := MergeIOCs([]contribIOCs{{cp: cpA, rows: rowsA}, {cp: cpB, rows: rowsB}})
	if len(merged) != 1 {
		t.Fatalf("expected 1 merged row; got %d", len(merged))
	}
	m := merged[0]
	if m.Source != "catalog" {
		t.Errorf("Source = %q, want catalog (catalog wins over observed)", m.Source)
	}
	if m.Name != "from-a" {
		t.Errorf("Name = %q, want from-a (catalog row's non-empty wins)", m.Name)
	}
	if m.Tags != "ransomware" {
		t.Errorf("Tags = %q, want ransomware (catalog row's non-empty wins)", m.Tags)
	}
	if m.ObservationCount != 8 {
		t.Errorf("ObservationCount = %d, want 8 (3+5)", m.ObservationCount)
	}
	if !m.FirstSeen.Equal(mergeTs("2026-04-01T00:00:00Z")) {
		t.Errorf("FirstSeen = %v, want 2026-04-01 (min)", m.FirstSeen)
	}
	if !m.LastSeen.Equal(mergeTs("2026-04-20T00:00:00Z")) {
		t.Errorf("LastSeen = %v, want 2026-04-20 (max)", m.LastSeen)
	}
	if len(m.CPSources) != 2 || m.CPSources[0].InstanceID != "cp-a" || m.CPSources[1].InstanceID != "cp-b" {
		t.Errorf("CPSources = %+v, want [cp-a cp-b] sorted", m.CPSources)
	}
}

func TestMergeIOCs_PreservesNonOverlapping(t *testing.T) {
	cpA := &CPSourceRef{InstanceID: "cp-a"}
	cpB := &CPSourceRef{InstanceID: "cp-b"}
	rowsA := []*db.IOCRecord{{Kind: "sha256", NormalizedValue: "abc", Source: "catalog"}}
	rowsB := []*db.IOCRecord{{Kind: "ipv4", NormalizedValue: "1.2.3.4", Source: "observed"}}
	merged := MergeIOCs([]contribIOCs{{cp: cpA, rows: rowsA}, {cp: cpB, rows: rowsB}})
	if len(merged) != 2 {
		t.Fatalf("expected 2 rows; got %d", len(merged))
	}
}

func TestMergeIOCs_DeterministicAcrossOrderings(t *testing.T) {
	// Two CPs each have the same row but with different metadata; output
	// should be identical regardless of which contribution arrives first
	// (the ascending-CP-ID iteration must drive determinism, not arrival
	// order).
	cpA := &CPSourceRef{InstanceID: "cp-a"}
	cpB := &CPSourceRef{InstanceID: "cp-b"}
	rowsA := []*db.IOCRecord{{Kind: "sha256", NormalizedValue: "abc", Source: "catalog", Attribution: "from-a"}}
	rowsB := []*db.IOCRecord{{Kind: "sha256", NormalizedValue: "abc", Source: "catalog", Attribution: "from-b"}}

	m1 := MergeIOCs([]contribIOCs{{cp: cpA, rows: rowsA}, {cp: cpB, rows: rowsB}})
	m2 := MergeIOCs([]contribIOCs{{cp: cpB, rows: rowsB}, {cp: cpA, rows: rowsA}})
	if m1[0].Attribution != "from-a" {
		t.Errorf("m1 Attribution = %q, want from-a (cp-a sorts first)", m1[0].Attribution)
	}
	if m2[0].Attribution != "from-a" {
		t.Errorf("m2 Attribution = %q, want from-a (deterministic regardless of arrival order)", m2[0].Attribution)
	}
}

func TestMergeIOCObservations_TagsOriginAndSorts(t *testing.T) {
	cpA := &CPSourceRef{InstanceID: "cp-a"}
	cpB := &CPSourceRef{InstanceID: "cp-b"}
	obsA := []db.IOCObservation{{IOCID: 1, Host: "host-a", ObservedAt: mergeTs("2026-04-01T00:00:00Z")}}
	obsB := []db.IOCObservation{{IOCID: 9, Host: "host-b", ObservedAt: mergeTs("2026-04-10T00:00:00Z")}}
	merged := MergeIOCObservations([]contribObs{{cp: cpA, rows: obsA}, {cp: cpB, rows: obsB}})
	if len(merged) != 2 {
		t.Fatalf("expected 2 observations; got %d", len(merged))
	}
	// Newest first
	if merged[0].Host != "host-b" || merged[0].CPSource.InstanceID != "cp-b" {
		t.Errorf("first row should be host-b on cp-b; got %+v", merged[0])
	}
	if merged[1].Host != "host-a" || merged[1].CPSource.InstanceID != "cp-a" {
		t.Errorf("second row should be host-a on cp-a; got %+v", merged[1])
	}
}

func TestMergeIOCObservations_NoDedup(t *testing.T) {
	// Same finding+host on two CPs should yield TWO observation rows
	// (per-CP events, not dedupable).
	cpA := &CPSourceRef{InstanceID: "cp-a"}
	cpB := &CPSourceRef{InstanceID: "cp-b"}
	obs := db.IOCObservation{IOCID: 1, Host: "host-x", FindingID: 42, ObservedAt: mergeTs("2026-04-01T00:00:00Z")}
	merged := MergeIOCObservations([]contribObs{
		{cp: cpA, rows: []db.IOCObservation{obs}},
		{cp: cpB, rows: []db.IOCObservation{obs}},
	})
	if len(merged) != 2 {
		t.Errorf("expected 2 observations (per-CP, no dedup); got %d", len(merged))
	}
}

func TestMergeIOCRelationships_DedupsByTuple(t *testing.T) {
	cpA := &CPSourceRef{InstanceID: "cp-a"}
	cpB := &CPSourceRef{InstanceID: "cp-b"}
	edge := db.IOCRelationshipPaired{SubjectKind: "domain", SubjectValue: "evil.com",
		Predicate: "resolves-to", ObjectKind: "ipv4", ObjectValue: "1.2.3.4",
		Source: "agent", Confidence: ""}
	edgeB := edge
	edgeB.Source = "" // empty on cp-b — cp-a's value should win
	edgeB.Confidence = "high"

	merged := MergeIOCRelationships([]contribRels{
		{cp: cpA, rows: []db.IOCRelationshipPaired{edge}},
		{cp: cpB, rows: []db.IOCRelationshipPaired{edgeB}},
	})
	if len(merged) != 1 {
		t.Fatalf("expected 1 deduped tuple; got %d", len(merged))
	}
	m := merged[0]
	if m.Source != "agent" {
		t.Errorf("Source = %q, want agent (first non-empty)", m.Source)
	}
	if m.Confidence != "high" {
		t.Errorf("Confidence = %q, want high (first non-empty)", m.Confidence)
	}
	cps := []string{m.CPSources[0].InstanceID, m.CPSources[1].InstanceID}
	sort.Strings(cps)
	if !reflect.DeepEqual(cps, []string{"cp-a", "cp-b"}) {
		t.Errorf("CPSources should be both CPs; got %+v", m.CPSources)
	}
}

func TestMergeIOCRelationships_DistinctTuples(t *testing.T) {
	// Different predicate on the same kv pair → two distinct edges.
	cpA := &CPSourceRef{InstanceID: "cp-a"}
	rels := []db.IOCRelationshipPaired{
		{SubjectKind: "domain", SubjectValue: "evil.com", Predicate: "resolves-to", ObjectKind: "ipv4", ObjectValue: "1.2.3.4"},
		{SubjectKind: "domain", SubjectValue: "evil.com", Predicate: "hosted-at", ObjectKind: "ipv4", ObjectValue: "1.2.3.4"},
	}
	merged := MergeIOCRelationships([]contribRels{{cp: cpA, rows: rels}})
	if len(merged) != 2 {
		t.Errorf("expected 2 distinct tuples (different predicates); got %d", len(merged))
	}
}

// Wire shape must be deterministic — Go map iteration is randomized,
// so without an explicit final sort the order would shift between calls.
// Lock in tuple-ascending order.
func TestMergeIOCRelationships_DeterministicOutputOrder(t *testing.T) {
	cpA := &CPSourceRef{InstanceID: "cp-a"}
	rels := []db.IOCRelationshipPaired{
		{SubjectKind: "domain", SubjectValue: "z.example", Predicate: "resolves-to", ObjectKind: "ipv4", ObjectValue: "9.9.9.9"},
		{SubjectKind: "domain", SubjectValue: "a.example", Predicate: "resolves-to", ObjectKind: "ipv4", ObjectValue: "1.1.1.1"},
		{SubjectKind: "domain", SubjectValue: "m.example", Predicate: "hosted-at", ObjectKind: "ipv4", ObjectValue: "5.5.5.5"},
	}
	merged := MergeIOCRelationships([]contribRels{{cp: cpA, rows: rels}})
	if len(merged) != 3 {
		t.Fatalf("expected 3 tuples; got %d", len(merged))
	}
	want := []string{"a.example", "m.example", "z.example"}
	got := []string{merged[0].SubjectValue, merged[1].SubjectValue, merged[2].SubjectValue}
	if !reflect.DeepEqual(want, got) {
		t.Errorf("expected SubjectValue ascending %v; got %v", want, got)
	}
}

// Mirrored by IOCs: equal LastSeen rows must emit in stable order.
func TestMergeIOCs_StableOrderOnEqualLastSeen(t *testing.T) {
	cpA := &CPSourceRef{InstanceID: "cp-a"}
	common := mergeTs("2026-04-01T00:00:00Z")
	rows := []*db.IOCRecord{
		{Kind: "sha256", NormalizedValue: "zzz", Source: "catalog", LastSeen: common},
		{Kind: "sha256", NormalizedValue: "aaa", Source: "catalog", LastSeen: common},
	}
	merged := MergeIOCs([]contribIOCs{{cp: cpA, rows: rows}})
	if len(merged) != 2 {
		t.Fatalf("expected 2 rows; got %d", len(merged))
	}
	// Ascending NormalizedValue tiebreaker.
	if merged[0].NormalizedValue != "aaa" || merged[1].NormalizedValue != "zzz" {
		t.Errorf("equal LastSeen tiebreak should be ascending NormalizedValue; got [%s, %s]",
			merged[0].NormalizedValue, merged[1].NormalizedValue)
	}
}
