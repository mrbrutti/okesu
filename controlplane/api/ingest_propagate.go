package api

import (
	"strings"
	"time"

	"github.com/section9labs/okesu/controlplane/db"
	"github.com/section9labs/okesu/controlplane/ioc/extract"
	"github.com/section9labs/okesu/controlplane/ioc/normalize"
)

// PropagationResult is what propagateFromIOCs returns. The caller
// applies these to the FindingInsert before InsertFinding.
type PropagationResult struct {
	// Severity is the operator-visible final severity. Equal to the
	// agent's reported severity if no catalog IOC matched, or raised
	// to the highest matched severity_floor.
	Severity string

	// ClusterID, if non-empty, is the existing cluster the finding
	// joins. If empty, the caller should mint a new cluster_id from
	// the finding's own id post-insert.
	ClusterID string

	IOCConfidence     string
	IOCAttribution    string
	IOCClassification string

	// IOCIDs is the set of (kind, normalized_value) IOC table ids the
	// caller should use when calling RecordIOCObservation post-insert.
	// Stored here so the caller doesn't repeat the LookupIOC walk.
	IOCIDs []int64
}

// clusterWindowFor returns the dedup window for a given IOC kind. Per
// the spec: sha256 = 24h, ipv4/domain = 1h, others = 15m.
func clusterWindowFor(kind string) time.Duration {
	switch kind {
	case "sha256", "sha1", "md5":
		return 24 * time.Hour
	case "ipv4", "ipv6", "domain":
		return time.Hour
	default:
		return 15 * time.Minute
	}
}

// propagateFromIOCs upserts each extracted hit's IOC, then computes
// the propagation values to stamp on the FindingInsert. baseSeverity
// is the agent's reported severity ("MEDIUM" by default in ingest.go).
func propagateFromIOCs(store *db.Store, hits []extract.Hit, baseSeverity string) (PropagationResult, error) {
	out := PropagationResult{Severity: strings.ToUpper(baseSeverity)}
	if len(hits) == 0 {
		return out, nil
	}

	for _, h := range hits {
		// Re-use the catalog's normalizer so we don't drift between
		// the extractor's spelling and the catalog's spelling.
		norm := h.NormalizedValue
		if norm == "" {
			norm, _ = normalize.NormalizeForKind(h.Kind, h.Value)
		}
		if norm == "" {
			continue
		}
		id, _, err := store.UpsertIOC(&db.IOCUpsert{
			Kind:            h.Kind,
			Value:           h.Value,
			NormalizedValue: norm,
			Source:          "observed",
		})
		if err != nil {
			continue // upsert failure → skip this hit, don't fail ingest
		}
		out.IOCIDs = append(out.IOCIDs, id)
	}

	if len(out.IOCIDs) == 0 {
		return out, nil
	}

	// Severity floor + classification + attribution + confidence,
	// computed from catalog-source matches only.
	meta, err := store.PropagatedMetadataForIOCs(out.IOCIDs)
	if err == nil {
		if rank(meta.SeverityFloor) > rank(out.Severity) {
			out.Severity = meta.SeverityFloor
		}
		out.IOCConfidence = meta.Confidence
		out.IOCAttribution = meta.Attribution
		out.IOCClassification = meta.Classification
	}

	// Cluster: pick the smallest window across the hits' kinds and use
	// THAT window for the lookup. Most aggressive kind wins so e.g. an
	// ipv4 (1h) clusters even when a sha256 (24h) is also present.
	smallest := 24 * time.Hour
	for _, h := range hits {
		if w := clusterWindowFor(h.Kind); w < smallest {
			smallest = w
		}
	}
	if cid, err := store.FindClusterIDForIOCs(out.IOCIDs, smallest); err == nil && cid != "" {
		out.ClusterID = cid
	}

	return out, nil
}

func rank(s string) int {
	switch strings.ToUpper(s) {
	case "CRITICAL":
		return 5
	case "HIGH":
		return 4
	case "MEDIUM":
		return 3
	case "LOW":
		return 2
	case "INFO":
		return 1
	default:
		return 0
	}
}
