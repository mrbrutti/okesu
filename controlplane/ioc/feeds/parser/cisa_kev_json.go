package parser

import (
	"encoding/json"
	"strings"

	"github.com/section9labs/okesu/controlplane/ioc/catalog"
	"github.com/section9labs/okesu/controlplane/ioc/normalize"
)

type cisaKEVDoc struct {
	Vulnerabilities []cisaKEVVuln `json:"vulnerabilities"`
}

type cisaKEVVuln struct {
	CveID                      string `json:"cveID"`
	VendorProject              string `json:"vendorProject"`
	Product                    string `json:"product"`
	VulnerabilityName          string `json:"vulnerabilityName"`
	ShortDescription           string `json:"shortDescription"`
	KnownRansomwareCampaignUse string `json:"knownRansomwareCampaignUse"`
}

// ParseCISAKEVJSON reads CISA's Known Exploited Vulnerabilities catalog
// JSON and emits one cve-kind CatalogEntry per vulnerability. Every
// entry gets SeverityFloor=HIGH (KEV inclusion implies active
// exploitation) and Attribution=CISA. Tags carry vendor/product
// pivots, a ransomware boolean, and the cisa-kev source marker.
func ParseCISAKEVJSON(body []byte) ([]catalog.CatalogEntry, error) {
	var doc cisaKEVDoc
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, err
	}
	var out []catalog.CatalogEntry
	for _, v := range doc.Vulnerabilities {
		if v.CveID == "" {
			continue
		}
		norm, ok := normalize.NormalizeForKind("cve", v.CveID)
		if !ok {
			continue
		}
		var tags []string
		if v.VendorProject != "" {
			tags = append(tags, "vendor:"+v.VendorProject)
		}
		if v.Product != "" {
			tags = append(tags, "product:"+v.Product)
		}
		if strings.EqualFold(v.KnownRansomwareCampaignUse, "Known") {
			tags = append(tags, "ransomware")
		}
		tags = append(tags, "cisa-kev")
		out = append(out, catalog.CatalogEntry{
			Kind:            "cve",
			Value:           v.CveID,
			NormalizedValue: norm,
			Name:            v.VulnerabilityName,
			Notes:           v.ShortDescription,
			Tags:            strings.Join(tags, ","),
			SeverityFloor:   "HIGH",
			Attribution:     "CISA",
		})
	}
	return out, nil
}
