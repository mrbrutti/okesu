// Package catalog parses the YAML IOC catalog format used to ship
// curated indicators alongside the project (under catalog/iocs/*.yaml).
//
// File schema:
//
//	iocs:
//	  - kind: sha256          # required
//	    value: "abc123..."    # required
//	    confidence: high      # optional: low | medium | high
//	    attribution: apt-foo  # optional
//	    severity_floor: HIGH  # optional: INFO | LOW | MEDIUM | HIGH | CRITICAL
//	    classification: ...   # optional
//	    notes: ...            # optional
//
// LoadDir walks the directory and returns a flat slice of CatalogEntry
// records, each annotated with the source DefinitionPath. The CP boots
// with an empty catalog if the directory is missing — operators can drop
// files in over time.
//
// LoadAndUpsert calls LoadDir and pushes every entry through an IOCStore
// (the *db.Store in production, a stub in tests). Used at CP boot and on
// SIGHUP to (re)hydrate the iocs table from the on-disk catalog.
package catalog

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/section9labs/okesu/controlplane/db"
	"github.com/section9labs/okesu/controlplane/ioc/normalize"
)

// CatalogEntry is one indicator after normalization, ready to upsert.
type CatalogEntry struct {
	Kind            string
	Value           string
	NormalizedValue string
	Source          string // always "catalog"
	DefinitionPath  string // file the entry came from
	Confidence      string
	Attribution     string
	SeverityFloor   string
	Classification  string
	Notes           string
}

type fileShape struct {
	IOCs []entryShape `yaml:"iocs"`
}

type entryShape struct {
	Kind           string `yaml:"kind"`
	Value          string `yaml:"value"`
	Confidence     string `yaml:"confidence,omitempty"`
	Attribution    string `yaml:"attribution,omitempty"`
	SeverityFloor  string `yaml:"severity_floor,omitempty"`
	Classification string `yaml:"classification,omitempty"`
	Notes          string `yaml:"notes,omitempty"`
}

// validKinds is the closed set of IOC kinds the catalog recognizes.
// Adding a new kind requires a deliberate code change here AND in the
// normalizer dispatch in NormalizeForKind — the goal is to fail loud
// when a YAML file uses a typo or an unsupported indicator type.
var validKinds = map[string]bool{
	"sha256": true, "sha1": true, "md5": true,
	"ipv4": true, "ipv6": true, "domain": true, "url": true,
	"cve": true, "mitre": true,
	"yara_rule": true, "sigma_rule": true,
}

// LoadDir parses every *.yaml/*.yml file under dir and returns all
// entries flattened. A missing dir is not an error (returns empty) so
// the CP boots cleanly when an operator hasn't dropped any catalog
// files in yet.
func LoadDir(dir string) ([]CatalogEntry, error) {
	if dir == "" {
		return nil, nil
	}
	st, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("catalog: %q is not a directory", dir)
	}

	var out []CatalogEntry
	err = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		ext := filepath.Ext(path)
		if ext != ".yaml" && ext != ".yml" {
			return nil
		}
		entries, err := loadFile(path)
		if err != nil {
			return fmt.Errorf("catalog: %s: %w", path, err)
		}
		out = append(out, entries...)
		return nil
	})
	return out, err
}

func loadFile(path string) ([]CatalogEntry, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var shape fileShape
	if err := yaml.Unmarshal(body, &shape); err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	var out []CatalogEntry
	for i, e := range shape.IOCs {
		if !validKinds[e.Kind] {
			return nil, fmt.Errorf("entry %d: unknown kind %q", i, e.Kind)
		}
		if e.Value == "" {
			return nil, fmt.Errorf("entry %d: value is required", i)
		}
		out = append(out, CatalogEntry{
			Kind:            e.Kind,
			Value:           e.Value,
			NormalizedValue: NormalizeForKind(e.Kind, e.Value),
			Source:          "catalog",
			DefinitionPath:  path,
			Confidence:      e.Confidence,
			Attribution:     e.Attribution,
			SeverityFloor:   e.SeverityFloor,
			Classification:  e.Classification,
			Notes:           e.Notes,
		})
	}
	return out, nil
}

// NormalizeForKind dispatches to the right normalizer based on kind.
// Unknown kinds fall back to lowercase+trim. Exported so other packages
// (e.g., the api package's iocs.lookup data resolver in Group G) use
// the same canonical form as the catalog loader — divergent
// normalization between writer and reader silently breaks lookups.
func NormalizeForKind(kind, value string) string {
	value = normalize.Refang(value)
	switch kind {
	case "sha256", "sha1", "md5":
		return normalize.NormalizeHash(value)
	case "ipv4":
		if v, ok := normalize.NormalizeIPv4(value); ok {
			return v
		}
	case "ipv6":
		if v, ok := normalize.NormalizeIPv6(value); ok {
			return v
		}
	case "domain":
		if v, ok := normalize.NormalizeDomain(value); ok {
			return v
		}
	case "url":
		if v, ok := normalize.NormalizeURL(value); ok {
			return v
		}
	}
	// Default: lowercase+trim. Covers cve / mitre / yara_rule / sigma_rule
	// where there's no purpose-built canonicalizer, and is a safe
	// fallback when a typed normalizer rejected the input — the row
	// still goes in, just keyed off the trimmed form.
	return normalize.NormalizeHash(value)
}

// IOCStore is the subset of *db.Store this package needs. Defined here
// so tests can stub it without pulling in the full store. Keep this
// interface narrow — adding methods couples every test to extra
// machinery. Group G's iocs.lookup will define its own read interface.
type IOCStore interface {
	UpsertIOC(in *db.IOCUpsert) (id int64, created bool, err error)
}

// LoadAndUpsert walks the catalog dir and upserts every entry into the
// store. Returns the number of entries processed (NOT the number of
// new rows — db.UpsertIOC handles the create-vs-update split itself,
// and a "5 entries refreshed" log is enough for operator visibility).
func LoadAndUpsert(dir string, store IOCStore) (int, error) {
	entries, err := LoadDir(dir)
	if err != nil {
		return 0, err
	}
	for _, e := range entries {
		if _, _, err := store.UpsertIOC(&db.IOCUpsert{
			Kind:            e.Kind,
			Value:           e.Value,
			NormalizedValue: e.NormalizedValue,
			Source:          e.Source,
			DefinitionPath:  e.DefinitionPath,
			Confidence:      e.Confidence,
			Attribution:     e.Attribution,
			SeverityFloor:   e.SeverityFloor,
			Classification:  e.Classification,
			Notes:           e.Notes,
		}); err != nil {
			return 0, fmt.Errorf("upsert %s/%s: %w", e.Kind, e.NormalizedValue, err)
		}
	}
	return len(entries), nil
}
