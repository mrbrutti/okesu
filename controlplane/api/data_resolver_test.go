package api

import (
	"context"
	"testing"

	"github.com/section9labs/okesu/controlplane/db"
)

// TestDataQuery_IOCsLookup_HitAndMiss exercises the iocs.lookup data
// query end-to-end through the resolver registry: a seeded catalog IOC
// must round-trip with its curated metadata, an unknown value must
// return valid:false (not an error — the hunt orchestration's `when:`
// branch reads valid as a bool).
func TestDataQuery_IOCsLookup_HitAndMiss(t *testing.T) {
	store := newTestStore(t)
	resolver := NewDataResolver(store)

	// Seed a catalog IOC. UpsertIOC takes the *normalized* value as the
	// dedup key — this matches what the catalog loader passes after
	// normalize.NormalizeForKind. The query handler must do the same
	// normalization on the *raw* lookup value or we'll silently miss.
	const (
		raw  = "DeadBeefDeadBeefDeadBeefDeadBeefDeadBeefDeadBeefDeadBeefDeadBeef"
		norm = "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
	)
	if _, _, err := store.UpsertIOC(&db.IOCUpsert{
		Kind:            "sha256",
		Value:           raw,
		NormalizedValue: norm,
		Source:          "catalog",
		SeverityFloor:   "HIGH",
		Attribution:     "test-actor",
		Classification:  "malware",
	}); err != nil {
		t.Fatalf("seed IOC: %v", err)
	}

	// Hit: pass the raw mixed-case form; the handler must normalize.
	res, err := resolver.Resolve(context.Background(), "iocs.lookup", map[string]any{
		"kind":  "sha256",
		"value": raw,
	})
	if err != nil {
		t.Fatalf("hit: resolve: %v", err)
	}
	got, ok := res.(map[string]any)
	if !ok {
		t.Fatalf("hit: result type = %T, want map[string]any", res)
	}
	if got["valid"] != true {
		t.Errorf("hit: valid = %v, want true", got["valid"])
	}
	if got["normalized_value"] != norm {
		t.Errorf("hit: normalized_value = %q, want %q", got["normalized_value"], norm)
	}
	if got["severity_floor"] != "HIGH" {
		t.Errorf("hit: severity_floor = %q, want HIGH", got["severity_floor"])
	}
	if got["attribution"] != "test-actor" {
		t.Errorf("hit: attribution = %q, want test-actor", got["attribution"])
	}
	if got["classification"] != "malware" {
		t.Errorf("hit: classification = %q, want malware", got["classification"])
	}
	if got["source"] != "catalog" {
		t.Errorf("hit: source = %q, want catalog", got["source"])
	}

	// Miss: a sha256 not in the catalog must return valid:false with
	// no error so the orchestration's `when:` branch sees a bool.
	missed := "0000000000000000000000000000000000000000000000000000000000000000"
	res, err = resolver.Resolve(context.Background(), "iocs.lookup", map[string]any{
		"kind":  "sha256",
		"value": missed,
	})
	if err != nil {
		t.Fatalf("miss: resolve returned error (want nil): %v", err)
	}
	got, ok = res.(map[string]any)
	if !ok {
		t.Fatalf("miss: result type = %T, want map[string]any", res)
	}
	if got["valid"] != false {
		t.Errorf("miss: valid = %v, want false", got["valid"])
	}
	if got["normalized_value"] != missed {
		t.Errorf("miss: normalized_value = %q, want %q", got["normalized_value"], missed)
	}
	if _, present := got["severity_floor"]; present {
		t.Errorf("miss: severity_floor unexpectedly set: %v", got["severity_floor"])
	}

	// Required-param validation: missing kind or value must error.
	if _, err := resolver.Resolve(context.Background(), "iocs.lookup", map[string]any{
		"kind": "sha256",
	}); err == nil {
		t.Errorf("missing value: want error, got nil")
	}
	if _, err := resolver.Resolve(context.Background(), "iocs.lookup", map[string]any{
		"value": raw,
	}); err == nil {
		t.Errorf("missing kind: want error, got nil")
	}
}
