package feeds

import (
	"github.com/section9labs/okesu/controlplane/db"
)

// FeedDef is the compile-time definition of a well-known feed.
// Editing this list is a deliberate code change — same posture as
// catalog.validKinds in controlplane/ioc/catalog/catalog.go.
//
// URLs in this file are best-effort and may need updating as upstream
// projects rotate hosting. A CI smoke test that GETs each URL once is
// tracked in the IOC Feeds spec as a follow-up.
type FeedDef struct {
	Slug                   string
	Name                   string
	Kind                   string // "single_file" | "git"
	URL                    string
	Subpath                string // for git kinds; "" means repo root
	Parser                 string // closed enum: yara | sigma | urlhaus_csv | threatfox_csv | cisa_kev_json
	License                string
	Description            string
	DefaultIntervalSeconds int
	DefaultInstalled       bool // seeded on first boot when true
}

// Registry is the curated list operators see in "Browse well-known
// feeds." Three are DefaultInstalled (small + high-signal); the rest
// are one click away in the registry browser.
var Registry = []FeedDef{
	{
		Slug:                   "cisa-kev",
		Name:                   "CISA Known Exploited Vulnerabilities",
		Kind:                   "single_file",
		Parser:                 "cisa_kev_json",
		URL:                    "https://www.cisa.gov/sites/default/files/feeds/known_exploited_vulnerabilities.json",
		DefaultIntervalSeconds: 86400,
		DefaultInstalled:       true,
		License:                "U.S. Government Work — public domain",
		Description:            "Vulnerabilities CISA has confirmed are actively exploited in the wild.",
	},
	{
		Slug:                   "abusech-threatfox",
		Name:                   "abuse.ch ThreatFox",
		Kind:                   "single_file",
		Parser:                 "threatfox_csv",
		URL:                    "https://threatfox.abuse.ch/export/csv/full/",
		DefaultIntervalSeconds: 86400,
		DefaultInstalled:       true,
		License:                "CC0 1.0",
		Description:            "Hashes, IPs, and domains from current malware campaigns.",
	},
	{
		Slug:                   "yara-forge-core",
		Name:                   "YARA Forge — Core bundle",
		Kind:                   "single_file",
		Parser:                 "yara",
		URL:                    "https://github.com/YARAHQ/yara-forge/releases/latest/download/yara-forge-rules-core.zip",
		DefaultIntervalSeconds: 86400,
		DefaultInstalled:       true,
		License:                "mixed (per-rule headers)",
		Description:            "Aggregated, deduped, performance-tested superset of public YARA rules. (URL points at a release zip; a future archive-kind feed type will handle this — the v1 single_file fetch will not parse a zip correctly. Tracked as a backlog issue.)",
	},
	{
		Slug:                   "abusech-urlhaus",
		Name:                   "abuse.ch URLhaus",
		Kind:                   "single_file",
		Parser:                 "urlhaus_csv",
		URL:                    "https://urlhaus.abuse.ch/downloads/csv/",
		DefaultIntervalSeconds: 86400,
		DefaultInstalled:       false,
		License:                "CC0 1.0",
		Description:            "Active malware-distribution URLs.",
	},
	{
		Slug:                   "sigmahq-sigma",
		Name:                   "SigmaHQ — Sigma",
		Kind:                   "git",
		Parser:                 "sigma",
		URL:                    "https://github.com/SigmaHQ/sigma.git",
		Subpath:                "rules/",
		DefaultIntervalSeconds: 604800, // weekly
		DefaultInstalled:       false,
		License:                "DRL 1.1",
		Description:            "Canonical Sigma rule repository. Large; weekly default refresh.",
	},
	{
		Slug:                   "neo23x0-signature-base",
		Name:                   "Neo23x0/signature-base",
		Kind:                   "git",
		Parser:                 "yara",
		URL:                    "https://github.com/Neo23x0/signature-base.git",
		Subpath:                "yara/",
		DefaultIntervalSeconds: 604800,
		DefaultInstalled:       false,
		License:                "Detection Rule License (DRL) 1.1",
		Description:            "Florian Roth's curated YARA collection.",
	},
	{
		Slug:                   "elastic-protections-artifacts",
		Name:                   "Elastic Protections Artifacts",
		Kind:                   "git",
		Parser:                 "yara",
		URL:                    "https://github.com/elastic/protections-artifacts.git",
		Subpath:                "yara/rules/",
		DefaultIntervalSeconds: 604800,
		DefaultInstalled:       false,
		License:                "Elastic License 2.0",
		Description:            "Production YARA rules from Elastic Security.",
	},
}

// SeedRegistryDefaults inserts every Registry entry with
// DefaultInstalled = true if the slug is not already present in the
// ioc_feeds table. Inserts go in with enabled = false; the consent
// banner flow flips them on later. Safe to call on every CP boot.
func SeedRegistryDefaults(store *db.Store) error {
	for _, def := range Registry {
		if !def.DefaultInstalled {
			continue
		}
		if existing, err := store.GetFeedConfigBySlug(def.Slug); err == nil && existing != nil {
			continue
		}
		_, err := store.InsertFeedConfig(&db.FeedConfigInsert{
			Slug:                   def.Slug,
			Name:                   def.Name,
			Kind:                   def.Kind,
			URL:                    def.URL,
			Subpath:                def.Subpath,
			Parser:                 def.Parser,
			RefreshIntervalSeconds: def.DefaultIntervalSeconds,
			Enabled:                false,
			InstalledFromRegistry:  true,
		})
		if err != nil {
			return err
		}
	}
	return nil
}
