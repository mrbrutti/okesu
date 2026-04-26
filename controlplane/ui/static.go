// Package ui serves the embedded React app.
//
// The web/dist directory is embedded into the binary via embed.FS. If web/dist
// is empty (e.g. when developing without running `npm run build`), the
// package serves a minimal placeholder page so the server still starts.
package ui

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:dist
var distFS embed.FS

// Handler returns an http.Handler that serves the embedded web/dist tree.
// Unknown paths fall back to /index.html so client-side routing works.
//
// We serve index.html manually rather than via http.FileServer because the
// stdlib FileServer redirects /index.html -> / which causes a redirect loop
// when combined with the SPA fallback.
func Handler() http.Handler {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		return placeholderHandler{}
	}
	if !hasIndex(sub) {
		return placeholderHandler{}
	}
	indexHTML, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		return placeholderHandler{}
	}
	files := http.FileServer(http.FS(sub))

	serveIndex := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(indexHTML)
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" || r.URL.Path == "/index.html" {
			serveIndex(w, r)
			return
		}
		// Try the requested file; if missing, fall back to index.html for SPA routing.
		clean := strings.TrimPrefix(r.URL.Path, "/")
		if _, err := fs.Stat(sub, clean); err != nil {
			serveIndex(w, r)
			return
		}
		files.ServeHTTP(w, r)
	})
}

func hasIndex(f fs.FS) bool {
	_, err := fs.Stat(f, "index.html")
	return err == nil
}

type placeholderHandler struct{}

func (placeholderHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" && r.URL.Path != "/index.html" && !strings.HasPrefix(r.URL.Path, "/login") {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(placeholderHTML))
}

const placeholderHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>Okesu Control Plane</title>
<style>
  body { font-family: -apple-system, system-ui, sans-serif; max-width: 720px; margin: 4rem auto; padding: 0 1rem; color: #1a1a2e; line-height: 1.6; }
  code { background: #f3f4f6; padding: 0.1em 0.4em; border-radius: 4px; font-size: 0.9em; }
  h1 { margin-bottom: 0.25em; }
  .muted { color: #6b7280; }
</style>
</head>
<body>
<h1>Okesu Control Plane</h1>
<p class="muted">UI not yet built. The API is running.</p>
<p>To build the React UI:</p>
<pre><code>cd web
npm install
npm run build
go build -o okesu-cp ./cmd/cp</code></pre>
<p>Try the API:</p>
<ul>
  <li><code>POST /api/auth/login</code> — log in</li>
  <li><code>GET  /api/events</code> — recent events (auth required)</li>
  <li><code>GET  /api/events/stream</code> — SSE event stream (auth required)</li>
  <li><code>POST /api/webhooks/events</code> — daemon event ingestion</li>
</ul>
</body>
</html>
`
