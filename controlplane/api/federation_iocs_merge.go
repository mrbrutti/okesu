package api

import (
	"sort"

	"github.com/section9labs/okesu/controlplane/db"
)

// FederatedIOCRecord is the merged wire shape: every IOCRecord field
// plus a CPSources slice listing every CP whose DB has this row.
// Embedded IOCRecord fields marshal at the top level (Go default
// PascalCase casing); cp_sources is snake_case to match the existing
// federation conventions in findings.go.
type FederatedIOCRecord struct {
	*db.IOCRecord
	CPSources []*CPSourceRef `json:"cp_sources"`
}

// FederatedIOCObservation extends IOCObservation with origin-CP tagging.
// Each obs row comes from exactly one CP (no dedup across CPs — they're
// per-host events).
type FederatedIOCObservation struct {
	db.IOCObservation
	CPSource *CPSourceRef `json:"cp_source"`
}

// FederatedIOCRelationship is the deduped merged edge shape with
// kv-pair endpoints (per-CP int ids are not comparable across CPs).
type FederatedIOCRelationship struct {
	db.IOCRelationshipPaired
	CPSources []*CPSourceRef `json:"cp_sources"`
}

// contribIOCs / contribObs / contribRels are per-CP contribution
// bundles — tests pass these directly; runtime code builds them
// from the FanOut callback.
type contribIOCs struct {
	cp   *CPSourceRef
	rows []*db.IOCRecord
}

type contribObs struct {
	cp   *CPSourceRef
	rows []db.IOCObservation
}

type contribRels struct {
	cp   *CPSourceRef
	rows []db.IOCRelationshipPaired
}

// MergeIOCs deduplicates by (kind, normalized_value). For text fields,
// first non-empty wins iterating contributors in ascending CP-ID order
// with catalog-source rows ahead of observed-source. Sums
// observation_count, takes min(first_seen) and max(last_seen).
func MergeIOCs(contribs []contribIOCs) []FederatedIOCRecord {
	// Sort contributors so deterministic iteration: catalog source ahead
	// of observed within each CP, CP-ID ascending across the slice.
	type entry struct {
		cp     *CPSourceRef
		prio   int // 0 for catalog, 1 for observed
		record *db.IOCRecord
	}
	var flat []entry
	for _, c := range contribs {
		for _, r := range c.rows {
			prio := 1
			if r.Source == "catalog" {
				prio = 0
			}
			flat = append(flat, entry{cp: c.cp, prio: prio, record: r})
		}
	}
	sort.SliceStable(flat, func(i, j int) bool {
		if flat[i].prio != flat[j].prio {
			return flat[i].prio < flat[j].prio
		}
		return flat[i].cp.InstanceID < flat[j].cp.InstanceID
	})

	type key struct{ kind, val string }
	out := make(map[key]*FederatedIOCRecord)
	for _, e := range flat {
		k := key{kind: e.record.Kind, val: e.record.NormalizedValue}
		if existing, ok := out[k]; ok {
			mergeIOCFields(existing.IOCRecord, e.record)
			existing.CPSources = appendCPSourceUnique(existing.CPSources, e.cp)
			continue
		}
		// First-seen entry: use the record verbatim (it's already the
		// highest-priority contributor due to our sort).
		cpy := *e.record
		out[k] = &FederatedIOCRecord{IOCRecord: &cpy, CPSources: []*CPSourceRef{e.cp}}
	}

	var result []FederatedIOCRecord
	for _, m := range out {
		// Sort CPSources for stable output.
		sort.Slice(m.CPSources, func(i, j int) bool { return m.CPSources[i].InstanceID < m.CPSources[j].InstanceID })
		result = append(result, *m)
	}
	// Stable sort with secondary key on NormalizedValue so two rows with
	// identical LastSeen always emit in the same order across calls.
	sort.SliceStable(result, func(i, j int) bool {
		if !result[i].LastSeen.Equal(result[j].LastSeen) {
			return result[i].LastSeen.After(result[j].LastSeen)
		}
		return result[i].NormalizedValue < result[j].NormalizedValue
	})
	return result
}

// mergeIOCFields folds non-empty fields from src into existing (dst
// keeps any value already set; src fills in blanks). Numeric and time
// fields aggregate.
func mergeIOCFields(dst, src *db.IOCRecord) {
	if dst.Source != "catalog" && src.Source == "catalog" {
		dst.Source = "catalog"
	}
	if dst.DefinitionPath == "" {
		dst.DefinitionPath = src.DefinitionPath
	}
	if dst.Confidence == "" {
		dst.Confidence = src.Confidence
	}
	if dst.Attribution == "" {
		dst.Attribution = src.Attribution
	}
	if dst.SeverityFloor == "" {
		dst.SeverityFloor = src.SeverityFloor
	}
	if dst.Classification == "" {
		dst.Classification = src.Classification
	}
	if dst.Notes == "" {
		dst.Notes = src.Notes
	}
	if dst.Name == "" {
		dst.Name = src.Name
	}
	if dst.Tags == "" {
		dst.Tags = src.Tags
	}
	dst.ObservationCount += src.ObservationCount
	if src.FirstSeen.Before(dst.FirstSeen) || dst.FirstSeen.IsZero() {
		dst.FirstSeen = src.FirstSeen
	}
	if src.LastSeen.After(dst.LastSeen) {
		dst.LastSeen = src.LastSeen
	}
}

// appendCPSourceUnique adds cp to s only if no entry has the same
// InstanceID. Avoids cps being listed twice when the same CP appears
// in multiple contribution batches (shouldn't happen, but defensive).
func appendCPSourceUnique(s []*CPSourceRef, cp *CPSourceRef) []*CPSourceRef {
	for _, x := range s {
		if x.InstanceID == cp.InstanceID {
			return s
		}
	}
	return append(s, cp)
}

// MergeIOCObservations unions observation rows across CPs, tags each
// with its origin CPSourceRef, sorts ObservedAt descending.
func MergeIOCObservations(contribs []contribObs) []FederatedIOCObservation {
	var out []FederatedIOCObservation
	for _, c := range contribs {
		for _, o := range c.rows {
			out = append(out, FederatedIOCObservation{IOCObservation: o, CPSource: c.cp})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ObservedAt.After(out[j].ObservedAt) })
	return out
}

// MergeIOCRelationships dedups by tuple (subject_kv, predicate, object_kv).
// First non-empty wins for Source/Confidence iterating in ascending
// CP-ID order. CPSources lists every CP that recorded the edge.
func MergeIOCRelationships(contribs []contribRels) []FederatedIOCRelationship {
	// Don't mutate the caller's slice — copy then sort.
	sorted := make([]contribRels, len(contribs))
	copy(sorted, contribs)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].cp.InstanceID < sorted[j].cp.InstanceID })

	type key struct {
		sk, sv, p, ok, ov string
	}
	out := make(map[key]*FederatedIOCRelationship)
	for _, c := range sorted {
		for _, r := range c.rows {
			k := key{sk: r.SubjectKind, sv: r.SubjectValue, p: r.Predicate, ok: r.ObjectKind, ov: r.ObjectValue}
			if existing, ok := out[k]; ok {
				if existing.Source == "" {
					existing.Source = r.Source
				}
				if existing.Confidence == "" {
					existing.Confidence = r.Confidence
				}
				existing.CPSources = appendCPSourceUnique(existing.CPSources, c.cp)
				continue
			}
			cpy := r
			out[k] = &FederatedIOCRelationship{IOCRelationshipPaired: cpy, CPSources: []*CPSourceRef{c.cp}}
		}
	}
	var result []FederatedIOCRelationship
	for _, m := range out {
		sort.Slice(m.CPSources, func(i, j int) bool { return m.CPSources[i].InstanceID < m.CPSources[j].InstanceID })
		result = append(result, *m)
	}
	// Stable wire-shape: sort by tuple so map-iteration randomness
	// doesn't leak through to the API consumer.
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].SubjectKind != result[j].SubjectKind {
			return result[i].SubjectKind < result[j].SubjectKind
		}
		if result[i].SubjectValue != result[j].SubjectValue {
			return result[i].SubjectValue < result[j].SubjectValue
		}
		if result[i].Predicate != result[j].Predicate {
			return result[i].Predicate < result[j].Predicate
		}
		if result[i].ObjectKind != result[j].ObjectKind {
			return result[i].ObjectKind < result[j].ObjectKind
		}
		return result[i].ObjectValue < result[j].ObjectValue
	})
	return result
}
