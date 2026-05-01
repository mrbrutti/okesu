package api

import (
	"net/http"
	"strings"

	"github.com/section9labs/okesu/controlplane/db"
)

// SigmaRulesYmlHandler returns every sigma_rule IOC's body concatenated
// into one multi-doc YAML stream the log-sigma-hunter daimon can hand
// to ripgrep / a sigma backend / etc:
//
//	curl /api/catalog/sigma-rules.yml > /tmp/rules.yml
//
// Optional query param: ?tag=<value> filters to rules whose
// comma-separated `tags` column contains that value (exact match on
// one of the comma-split tokens). The IOC catalog table doesn't have
// a tag-index — for v1 the filter is in-Go, mirroring
// YARARulesYarHandler. With <5k rules this is fine; revisit if the
// catalog grows past that.
//
// Output format: documents separated by `\n---\n`, each preceded by
// three comment headers (# catalog name:, # tags:, # severity_floor:)
// so a human reading the bundle can trace each rule back to its
// catalog entry. The headers are YAML comments so a sigma parser
// reading the same stream ignores them.
//
// Viewer-readable.
func SigmaRulesYmlHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		filter := db.IOCListFilter{Kind: "sigma_rule", Limit: 5000}
		rules, err := store.ListIOCs(filter)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		want := strings.TrimSpace(r.URL.Query().Get("tag"))
		w.Header().Set("Content-Type", "application/yaml")
		first := true
		for _, ioc := range rules {
			if want != "" && !hasTag(ioc.Tags, want) {
				continue
			}
			if !first {
				w.Write([]byte("\n---\n\n"))
			}
			first = false
			w.Write([]byte("# catalog name: " + ioc.Name + "\n"))
			w.Write([]byte("# tags: " + ioc.Tags + "\n"))
			w.Write([]byte("# severity_floor: " + ioc.SeverityFloor + "\n"))
			w.Write([]byte(ioc.Value))
			w.Write([]byte("\n"))
		}
	}
}
