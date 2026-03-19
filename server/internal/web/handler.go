package web

import (
	"io/fs"
	"net/http"
	"strings"
)

// Handler returns an http.Handler that serves the embedded Flutter web app.
// It handles SPA routing by falling back to index.html for non-file paths.
func Handler() http.Handler {
	// Get the dist subdirectory from the embedded filesystem
	distFS, err := fs.Sub(Assets, "dist")
	if err != nil {
		// Return a handler that shows a message if web assets aren't available
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte("<h1>Cantinarr</h1><p>Web UI not available. Use the mobile app.</p>"))
		})
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip API and WebSocket routes
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}

		// Determine which file to serve
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}

		// Check if file exists; fall back to index.html for SPA routing
		f, err := distFS.Open(path)
		if err != nil {
			path = "index.html"
			f, err = distFS.Open(path)
			if err != nil {
				http.NotFound(w, r)
				return
			}
		}
		f.Close()

		// Serve the file directly from the filesystem to avoid
		// http.FileServer's redirect behavior for "/" and "/index.html"
		http.ServeFileFS(w, r, distFS, path)
	})
}
