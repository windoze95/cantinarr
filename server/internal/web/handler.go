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

	fileServer := http.FileServer(http.FS(distFS))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip API and WebSocket routes
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}

		// Try to serve the file directly
		// If it doesn't exist (SPA route), serve index.html
		path := r.URL.Path
		if path == "/" {
			path = "/index.html"
		}

		// Check if file exists
		f, err := distFS.Open(strings.TrimPrefix(path, "/"))
		if err != nil {
			// SPA fallback: serve index.html for client-side routing
			r.URL.Path = "/index.html"
		} else {
			f.Close()
		}

		fileServer.ServeHTTP(w, r)
	})
}
