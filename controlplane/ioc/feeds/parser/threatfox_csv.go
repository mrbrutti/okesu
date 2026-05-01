package parser

import (
	"bytes"
	"encoding/csv"
	"io"
	"strings"

	"github.com/section9labs/okesu/controlplane/ioc/catalog"
	"github.com/section9labs/okesu/controlplane/ioc/normalize"
)

// ParseThreatFoxCSV reads abuse.ch's ThreatFox full CSV. Reuses the
// stripCSVComments helper from urlhaus_csv.go.
//
// Schema: first_seen_utc, ioc_id, ioc_value, ioc_type, threat_type,
// fk_malware, malware_alias, malware_printable, last_seen_utc,
// confidence_level, reference, tags, anonymous, reporter.
func ParseThreatFoxCSV(body []byte) ([]catalog.CatalogEntry, error) {
	stripped := stripCSVComments(body)
	r := csv.NewReader(bytes.NewReader(stripped))
	r.FieldsPerRecord = -1
	r.LazyQuotes = true

	var out []catalog.CatalogEntry
	for {
		rec, err := r.Read()
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}
		if len(rec) < 12 {
			continue
		}
		ev := strings.TrimSpace(rec[2])
		ioctype := strings.TrimSpace(rec[3])
		malware := strings.TrimSpace(rec[7])
		tagsCol := strings.TrimSpace(rec[11])
		kind, normVal := mapThreatFoxKind(ioctype, ev)
		if kind == "" {
			continue
		}
		norm, ok := normalize.NormalizeForKind(kind, normVal)
		if !ok {
			continue
		}
		var tags []string
		if malware != "" {
			tags = append(tags, "malware:"+malware)
		}
		if tagsCol != "" {
			for _, t := range strings.Split(tagsCol, ",") {
				if t = strings.TrimSpace(t); t != "" {
					tags = append(tags, t)
				}
			}
		}
		out = append(out, catalog.CatalogEntry{
			Kind:            kind,
			Value:           normVal,
			NormalizedValue: norm,
			Tags:            strings.Join(tags, ","),
			Attribution:     "abuse.ch ThreatFox",
		})
	}
	return out, nil
}

// mapThreatFoxKind translates ThreatFox's ioc_type to the catalog's
// kind enum and returns the bare value (port stripped for ip:port).
func mapThreatFoxKind(ioctype, value string) (kind, val string) {
	switch ioctype {
	case "sha256_hash":
		return "sha256", value
	case "sha1_hash":
		return "sha1", value
	case "md5_hash":
		return "md5", value
	case "domain":
		return "domain", value
	case "url":
		return "url", value
	case "ipv4":
		return "ipv4", value
	case "ipv6":
		return "ipv6", value
	case "ip:port":
		v := value
		if strings.HasPrefix(v, "[") {
			if i := strings.Index(v, "]"); i > 0 {
				return "ipv6", v[1:i]
			}
		}
		if i := strings.LastIndex(v, ":"); i > 0 {
			return "ipv4", v[:i]
		}
		return "ipv4", v
	}
	return "", ""
}
