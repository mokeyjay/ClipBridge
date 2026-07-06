// Package web embeds the built React management console and serves it as a
// single-page app. The Go server mounts this under "/" on the Web port; the
// explicit /api/v1 and /healthz routes take precedence over this catch-all.
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"
)

//go:embed all:dist
var dist embed.FS

// Handler returns an http.Handler serving the embedded console. Unknown paths
// that are not asset files fall back to index.html so client-side routing works.
// Returns (nil, false) if the console was not built into the binary.
func Handler() (http.Handler, bool) {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		return nil, false
	}
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return nil, false // dist present but empty (e.g. console not built)
	}
	fileServer := http.FileServer(http.FS(sub))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Serve real asset files directly; otherwise return index.html so the SPA
		// can handle the route on the client.
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name != "" {
			if f, err := sub.Open(name); err == nil {
				_ = f.Close()
				fileServer.ServeHTTP(w, r)
				return
			}
		}
		serveIndex(w, r, sub)
	}), true
}

// serveIndex writes index.html for SPA fallback routes.
func serveIndex(w http.ResponseWriter, r *http.Request, sub fs.FS) {
	data, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		http.Error(w, "console not available", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	http.ServeContent(w, r, "index.html", time.Time{}, strings.NewReader(string(data)))
}
