package web

import (
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path/filepath"
	"strconv"
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

	return assetHandler(distFS)
}

func assetHandler(distFS fs.FS) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip API and WebSocket routes
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}
		for _, part := range strings.FieldsFunc(r.URL.Path, func(c rune) bool { return c == '/' || c == '\\' }) {
			if part == ".." {
				http.Error(w, "invalid URL path", http.StatusBadRequest)
				return
			}
		}

		// Determine which file to serve
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}

		// Check if file exists; fall back to index.html for SPA routing
		f, info, err := openAsset(distFS, path)
		if err != nil {
			path = "index.html"
			f, info, err = openAsset(distFS, path)
			if err != nil {
				http.NotFound(w, r)
				return
			}
		}
		defer f.Close()

		// Both representations vary, including the uncompressed fallback.
		// Add preserves other middleware's Vary fields (such as Origin).
		w.Header().Add("Vary", "Accept-Encoding")
		gzipQuality, identityQuality := encodingQualities(r.Header.Values("Accept-Encoding"))
		// Preserve byte ranges on the original asset when it is acceptable.
		useGzip := gzipQuality > 0 && gzipQuality >= identityQuality &&
			(r.Header.Get("Range") == "" || identityQuality == 0)
		compressed := false
		if useGzip {
			if gz, gzInfo, err := openAsset(distFS, path+".gz"); err == nil {
				defer gz.Close()
				contentType := mime.TypeByExtension(filepath.Ext(path))
				if contentType == "" {
					// Minimal images may have no system MIME database. Sniff
					// the original bytes, never the gzip header.
					var buf [512]byte
					n, _ := io.ReadFull(f, buf[:])
					contentType = http.DetectContentType(buf[:n])
				}
				w.Header().Set("Content-Type", contentType)
				f, info = gz, gzInfo
				compressed = true
			}
		}
		if !compressed && identityQuality == 0 {
			http.Error(w, "no acceptable content encoding", http.StatusNotAcceptable)
			return
		}
		content, ok := f.(io.ReadSeeker)
		if !ok {
			http.Error(w, "web asset is not seekable", http.StatusInternalServerError)
			return
		}
		if compressed {
			w.Header().Set("Content-Encoding", "gzip")
			// ServeContent does not set a full response's length when an
			// encoding is present. Set it only when sending the asset, not
			// on a bodyless 304/412 or an uncompressed error response.
			w = gzipResponseWriter{ResponseWriter: w, size: info.Size()}
			if r.Header.Get("Range") != "" {
				// Identity was refused. HTTP allows ignoring Range; sending
				// the whole gzip avoids a partial or multipart gzip stream.
				r = r.Clone(r.Context())
				r.Header.Del("Range")
			}
		}

		// Use the original name for MIME detection (especially application/wasm),
		// and ServeContent to keep HEAD/conditional requests and SPA routes.
		http.ServeContent(w, r, path, info.ModTime(), content)
	})
}

type gzipResponseWriter struct {
	http.ResponseWriter
	size int64
}

func (w gzipResponseWriter) WriteHeader(status int) {
	if status == http.StatusOK {
		w.Header().Set("Content-Length", strconv.FormatInt(w.size, 10))
	} else {
		w.Header().Del("Content-Encoding")
		w.Header().Del("Content-Length")
	}
	w.ResponseWriter.WriteHeader(status)
}

func openAsset(distFS fs.FS, path string) (fs.File, fs.FileInfo, error) {
	f, err := distFS.Open(path)
	if err != nil {
		return nil, nil, err
	}
	info, err := f.Stat()
	if err != nil || info.IsDir() {
		f.Close()
		return nil, nil, fs.ErrNotExist
	}
	return f, info, nil
}

// encodingQualities applies explicit coding preferences before the wildcard.
// An absent/empty header selects identity; identity remains acceptable unless
// excluded explicitly or by *;q=0 (RFC 9110, section 12.5.3).
func encodingQualities(values []string) (gzipQuality, identityQuality float64) {
	qualities := map[string]float64{}
	for _, value := range values {
		for _, coding := range strings.Split(value, ",") {
			parts := strings.Split(coding, ";")
			name := strings.ToLower(strings.TrimSpace(parts[0]))
			if name == "x-gzip" {
				name = "gzip"
			}
			if name != "gzip" && name != "identity" && name != "*" {
				continue
			}
			quality := 1.0
			for _, param := range parts[1:] {
				key, value, _ := strings.Cut(param, "=")
				if strings.EqualFold(strings.TrimSpace(key), "q") {
					parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
					if err != nil || !(parsed >= 0 && parsed <= 1) {
						quality = 0
					} else {
						quality = parsed
					}
				}
			}
			qualities[name] = quality
		}
	}
	gzipQuality, found := qualities["gzip"]
	if !found {
		gzipQuality = qualities["*"]
	}
	identityQuality, found = qualities["identity"]
	if !found {
		identityQuality = 1
		if wildcard, ok := qualities["*"]; ok && wildcard == 0 {
			identityQuality = 0
		}
	}
	return gzipQuality, identityQuality
}
