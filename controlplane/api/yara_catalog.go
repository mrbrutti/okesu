package api

import (
	"net/http"
	"strings"

	"github.com/section9labs/okesu/controlplane/db"
)

// YARARulesYarHandler returns every yara_rule IOC's body concatenated
// into a single .yar document the binary-analyzer agent can hand to
// the system `yara` CLI:
//
//	curl /api/catalog/yara-rules.yar > /tmp/rules.yar
//	yara -r /tmp/rules.yar /path/to/binary
//
// Optional query param: ?tag=<value> filters to rules whose
// comma-separated `tags` column contains that value (exact match on
// one of the comma-split tokens). The IOC catalog table doesn't have
// a tag-index — for v1 the filter is in-Go. With <1k rules this is
// fine; revisit if the catalog grows past that.
func YARARulesYarHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		filter := db.IOCListFilter{Kind: "yara_rule", Limit: 1000}
		rules, err := store.ListIOCs(filter)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		want := strings.TrimSpace(r.URL.Query().Get("tag"))
		w.Header().Set("Content-Type", "text/x-yara")
		first := true
		for _, ioc := range rules {
			if want != "" && !hasTag(ioc.Tags, want) {
				continue
			}
			if !first {
				w.Write([]byte("\n\n"))
			}
			first = false
			// Emit a comment header so a human reading the bundle can
			// trace each rule back to its catalog entry.
			w.Write([]byte("// catalog name: " + ioc.Name + "\n"))
			w.Write([]byte("// tags: " + ioc.Tags + "\n"))
			w.Write([]byte(ioc.Value))
		}
	}
}

// hasTag returns true if the comma-separated `tags` column contains
// `want` as one of its tokens (case-sensitive, whitespace-trimmed).
func hasTag(tags, want string) bool {
	for _, t := range strings.Split(tags, ",") {
		if strings.TrimSpace(t) == want {
			return true
		}
	}
	return false
}
