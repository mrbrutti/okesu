package feeds

import (
	"testing"
)

func TestSeedRegistryDefaults_IdempotentAndDisabledUntilConsent(t *testing.T) {
	st := newTestStore(t)
	if err := SeedRegistryDefaults(st); err != nil {
		t.Fatalf("seed: %v", err)
	}

	all, err := st.ListFeedConfigs()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) == 0 {
		t.Fatal("expected at least one default-installed feed")
	}
	for _, fc := range all {
		if fc.Enabled {
			t.Fatalf("expected %s disabled until consent", fc.Slug)
		}
		if !fc.InstalledFromRegistry {
			t.Fatalf("expected %s flagged from registry", fc.Slug)
		}
	}

	count1 := len(all)
	if err := SeedRegistryDefaults(st); err != nil {
		t.Fatal(err)
	}
	all2, _ := st.ListFeedConfigs()
	if len(all2) != count1 {
		t.Fatalf("idempotent: expected %d, got %d", count1, len(all2))
	}
}

func TestRegistry_AllEntriesHaveValidParserEnum(t *testing.T) {
	valid := map[string]bool{
		"yara": true, "sigma": true, "urlhaus_csv": true,
		"threatfox_csv": true, "cisa_kev_json": true,
	}
	validKind := map[string]bool{"single_file": true, "git": true}
	for _, def := range Registry {
		if !valid[def.Parser] {
			t.Errorf("registry %q: parser %q not in CHECK enum", def.Slug, def.Parser)
		}
		if !validKind[def.Kind] {
			t.Errorf("registry %q: kind %q not in CHECK enum", def.Slug, def.Kind)
		}
		if def.Slug == "" || def.Name == "" || def.URL == "" {
			t.Errorf("registry %q: missing required field (slug/name/url)", def.Slug)
		}
		if def.DefaultIntervalSeconds <= 0 {
			t.Errorf("registry %q: default interval must be > 0", def.Slug)
		}
	}
}
