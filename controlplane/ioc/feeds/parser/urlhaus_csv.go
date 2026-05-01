package parser

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"io"
	"strings"

	"github.com/section9labs/okesu/controlplane/ioc/catalog"
	"github.com/section9labs/okesu/controlplane/ioc/normalize"
)

// ParseURLhausCSV reads abuse.ch's URLhaus full CSV. Comment lines
// starting with '#' are skipped (the file ships with a comment header
// and footer).
//
// Schema: id, dateadded, url, url_status, last_online, threat, tags, urlhaus_link, reporter.
func ParseURLhausCSV(body []byte) ([]catalog.CatalogEntry, error) {
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
		if len(rec) < 7 {
			continue
		}
		urlVal := strings.TrimSpace(rec[2])
		threat := strings.TrimSpace(rec[5])
		tagsCol := strings.TrimSpace(rec[6])
		if urlVal == "" {
			continue
		}
		norm, ok := normalize.NormalizeForKind("url", urlVal)
		if !ok {
			continue
		}
		var tags []string
		if threat != "" {
			tags = append(tags, threat)
		}
		if tagsCol != "" {
			for _, t := range strings.Split(tagsCol, ",") {
				if t = strings.TrimSpace(t); t != "" {
					tags = append(tags, t)
				}
			}
		}
		out = append(out, catalog.CatalogEntry{
			Kind:            "url",
			Value:           urlVal,
			NormalizedValue: norm,
			Tags:            strings.Join(tags, ","),
			Attribution:     "abuse.ch URLhaus",
		})
	}
	return out, nil
}

// stripCSVComments removes lines that start with '#' (after leading
// whitespace). Reused by other CSV parsers (ThreatFox) in this package.
func stripCSVComments(body []byte) []byte {
	var buf bytes.Buffer
	sc := bufio.NewScanner(bytes.NewReader(body))
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		ln := sc.Text()
		if strings.HasPrefix(strings.TrimSpace(ln), "#") {
			continue
		}
		buf.WriteString(ln)
		buf.WriteByte('\n')
	}
	return buf.Bytes()
}
