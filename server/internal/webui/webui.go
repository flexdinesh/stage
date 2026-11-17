// Package webui serves the embedded SPA build (apps/web/dist copied into ./dist).
package webui

import (
	"embed"
	"errors"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:dist
var dist embed.FS

// Handler serves static files from dist, falling back to index.html for client
// routes. With an empty dist (dev: Vite serves the UI) it returns a hint.
func Handler() http.Handler {
	root, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err) // embed path is static; cannot fail
	}
	index, err := fs.ReadFile(root, "index.html")
	if errors.Is(err, fs.ErrNotExist) {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "web UI not built; use the Vite dev server (mise run dev) or mise run build", http.StatusNotFound)
		})
	}
	files := http.FileServerFS(root)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
		if name != "" && name != "index.html" {
			if info, err := fs.Stat(root, name); err == nil && !info.IsDir() {
				switch name {
				case "sw.js":
					w.Header().Set("Cache-Control", "no-cache") // browsers must see SW updates
				case "manifest.webmanifest":
					w.Header().Set("Cache-Control", "no-cache")
					// No mime.types in distroless; set explicitly.
					w.Header().Set("Content-Type", "application/manifest+json")
				}
				files.ServeHTTP(w, r)
				return
			}
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(index)
	})
}
