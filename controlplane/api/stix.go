package api

import (
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/section9labs/okesu/controlplane/db"
)

// STIX2ExportHandler emits the IOC table + ioc_relationships as a
// minimal STIX 2.1 bundle. v1 supports the subset operators need to
// pipe into MISP / OpenCTI: indicator SDOs and relationship SROs.
//
// Query params:
//
//	?since=<RFC3339>   // limit to IOCs last_seen since this timestamp
//	?kind=<kind>       // limit to a single IOC kind
func STIX2ExportHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		filter := db.IOCListFilter{Limit: 1000}
		if k := r.URL.Query().Get("kind"); k != "" {
			filter.Kind = k
		}
		since := r.URL.Query().Get("since")
		var sinceTime time.Time
		if since != "" {
			if t, err := time.Parse(time.RFC3339, since); err == nil {
				sinceTime = t
			}
		}

		iocs, err := store.ListIOCs(filter)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if !sinceTime.IsZero() {
			filtered := iocs[:0]
			for _, ioc := range iocs {
				if !ioc.LastSeen.Before(sinceTime) {
					filtered = append(filtered, ioc)
				}
			}
			iocs = filtered
		}

		objects := make([]map[string]any, 0, len(iocs)*2)
		iocSTIXIDs := make(map[int64]string, len(iocs))
		for _, ioc := range iocs {
			id := stixID("indicator", ioc.Kind+":"+ioc.NormalizedValue)
			iocSTIXIDs[ioc.ID] = id
			objects = append(objects, map[string]any{
				"type":         "indicator",
				"spec_version": "2.1",
				"id":           id,
				"created":      formatSTIXTime(ioc.FirstSeen),
				"modified":     formatSTIXTime(ioc.LastSeen),
				"pattern":      stixPatternFor(ioc.Kind, ioc.NormalizedValue),
				"pattern_type": "stix",
				"valid_from":   formatSTIXTime(ioc.FirstSeen),
				"name":         ioc.Kind + " " + ioc.NormalizedValue,
				"labels":       stixLabels(ioc),
			})
		}
		// Add relationships referenced by exported IOCs. ListIOCRelationships
		// returns both directions; dedupe by relationship ID so a single
		// edge isn't emitted twice when both endpoints are exported.
		seenRel := make(map[int64]bool)
		for _, ioc := range iocs {
			rels, err := store.ListIOCRelationships(ioc.ID)
			if err != nil {
				continue
			}
			for _, rel := range rels {
				if seenRel[rel.ID] {
					continue
				}
				subjID, ok1 := iocSTIXIDs[rel.SubjectID]
				objID, ok2 := iocSTIXIDs[rel.ObjectID]
				if !ok1 || !ok2 {
					continue
				}
				seenRel[rel.ID] = true
				objects = append(objects, map[string]any{
					"type":              "relationship",
					"spec_version":      "2.1",
					"id":                stixID("relationship", strconv.FormatInt(rel.ID, 10)),
					"created":           time.Now().UTC().Format(time.RFC3339),
					"modified":          time.Now().UTC().Format(time.RFC3339),
					"relationship_type": rel.Predicate,
					"source_ref":        subjID,
					"target_ref":        objID,
				})
			}
		}

		bundle := map[string]any{
			"type":         "bundle",
			"id":           stixID("bundle", strconv.FormatInt(time.Now().UnixNano(), 10)),
			"spec_version": "2.1",
			"objects":      objects,
		}
		w.Header().Set("Content-Type", "application/stix+json;version=2.1")
		_ = json.NewEncoder(w).Encode(bundle)
	}
}

// stixID returns the STIX 2.1 deterministic id form
// "<type>--<sha1-of-key>" formatted as a UUID-shaped slug. Production
// would use UUIDv5 with the STIX namespace; v1 uses a SHA-1 prefix
// since exporters care about uniqueness, not the namespace UUID.
func stixID(stixType, key string) string {
	h := sha1.Sum([]byte(stixType + ":" + key))
	return fmt.Sprintf("%s--%x-%x-%x-%x-%x", stixType,
		h[0:4], h[4:6], h[6:8], h[8:10], h[10:16])
}

// stixPatternFor builds the STIX-2.1 pattern string for an IOC.
func stixPatternFor(kind, value string) string {
	switch kind {
	case "sha256":
		return fmt.Sprintf("[file:hashes.'SHA-256' = '%s']", value)
	case "sha1":
		return fmt.Sprintf("[file:hashes.'SHA-1' = '%s']", value)
	case "md5":
		return fmt.Sprintf("[file:hashes.'MD5' = '%s']", value)
	case "ipv4":
		return fmt.Sprintf("[ipv4-addr:value = '%s']", value)
	case "ipv6":
		return fmt.Sprintf("[ipv6-addr:value = '%s']", value)
	case "domain":
		return fmt.Sprintf("[domain-name:value = '%s']", value)
	case "url":
		return fmt.Sprintf("[url:value = '%s']", value)
	case "cve":
		return fmt.Sprintf("[vulnerability:name = '%s']", value)
	}
	return fmt.Sprintf("[x-okesu-ioc:kind = '%s' AND x-okesu-ioc:value = '%s']", kind, value)
}

// stixLabels maps Okesu's IOC metadata to the STIX 2.1 indicator-type-ov
// vocabulary so STIX-aware tools can categorize the export.
func stixLabels(ioc *db.IOCRecord) []string {
	if ioc.Classification != "" {
		return []string{ioc.Classification}
	}
	return []string{"unknown"}
}

// formatSTIXTime emits RFC3339 with millisecond precision (STIX
// canonical form). Zero times become a sentinel that's still parseable.
func formatSTIXTime(t time.Time) string {
	if t.IsZero() {
		return time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	}
	return t.UTC().Format("2006-01-02T15:04:05.000Z")
}
