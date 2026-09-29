package web

import (
	"bytes"
	"compress/gzip"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func webFixture(t *testing.T) fstest.MapFS {
	t.Helper()
	files := fstest.MapFS{
		"index.html":               {Data: []byte("<!doctype html><title>Cantinarr</title>")},
		"main.dart.js":             {Data: bytes.Repeat([]byte("const app = 'Cantinarr';\n"), 100)},
		"canvaskit/canvaskit.wasm": {Data: bytes.Repeat([]byte{0, 97, 115, 109, 1, 0, 0, 0}, 100)},
		"assets/poster.webp":       {Data: []byte("uncompressed image response")},
		"without-sidecar.js":       {Data: []byte("const uncompressed = true;")},
		"LICENSE":                  {Data: bytes.Repeat([]byte("Plain text license\n"), 100)},
	}
	for _, name := range []string{"index.html", "main.dart.js", "canvaskit/canvaskit.wasm", "LICENSE"} {
		var compressed bytes.Buffer
		writer := gzip.NewWriter(&compressed)
		if _, err := writer.Write(files[name].Data); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		files[name+".gz"] = &fstest.MapFile{Data: compressed.Bytes()}
	}
	for _, file := range files {
		file.ModTime = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	return files
}

func TestAssetEncodingNegotiation(t *testing.T) {
	files := webFixture(t)
	handler := assetHandler(files)
	for _, tc := range []struct {
		name     string
		accept   []string
		encoding string
		status   int
	}{
		{"absent", nil, "", 200},
		{"empty", []string{""}, "", 200},
		{"browser", []string{"gzip, deflate, br, zstd"}, "gzip", 200},
		{"unsupported", []string{"br"}, "", 200},
		{"case and whitespace", []string{" GZip ; q=1.0 "}, "gzip", 200},
		{"legacy alias", []string{"x-gzip"}, "gzip", 200},
		{"wildcard", []string{"*"}, "gzip", 200},
		{"explicit refusal beats wildcard", []string{"gzip;q=0, *;q=1"}, "", 200},
		{"identity preferred", []string{"gzip;q=0.4, identity;q=0.8"}, "", 200},
		{"gzip preferred", []string{"gzip;q=0.8, identity;q=0.2"}, "gzip", 200},
		{"gzip overrides wildcard refusal", []string{"gzip, *;q=0"}, "gzip", 200},
		{"identity overrides wildcard refusal", []string{"identity, *;q=0"}, "", 200},
		{"everything refused", []string{"gzip;q=0, identity;q=0"}, "", 406},
		{"wildcard refusal", []string{"*;q=0"}, "", 406},
		{"invalid quality", []string{"gzip;q=invalid"}, "", 200},
		{"NaN quality", []string{"gzip;q=NaN"}, "", 200},
		{"out of range quality", []string{"gzip;q=2"}, "", 200},
		{"multiple field lines", []string{"br", "gzip"}, "gzip", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/main.dart.js", nil)
			for _, value := range tc.accept {
				req.Header.Add("Accept-Encoding", value)
			}
			rec := httptest.NewRecorder()
			rec.Header().Add("Vary", "Origin")
			handler.ServeHTTP(rec, req)
			if rec.Code != tc.status || rec.Header().Get("Content-Encoding") != tc.encoding {
				t.Fatalf("status=%d encoding=%q, want %d %q", rec.Code, rec.Header().Get("Content-Encoding"), tc.status, tc.encoding)
			}
			vary := strings.Join(rec.Header().Values("Vary"), ", ")
			if vary != "Origin, Accept-Encoding" {
				t.Fatalf("Vary = %q", vary)
			}
			if tc.status != http.StatusOK {
				return
			}
			want := files["main.dart.js"].Data
			if tc.encoding == "gzip" {
				want = files["main.dart.js.gz"].Data
			}
			if !bytes.Equal(rec.Body.Bytes(), want) || rec.Header().Get("Content-Length") != strconv.Itoa(len(want)) {
				t.Fatal("body or length differs from the selected representation")
			}
			if contentType := rec.Header().Get("Content-Type"); !strings.Contains(contentType, "javascript") {
				t.Fatalf("JavaScript Content-Type = %q", contentType)
			}
		})
	}
}

func TestAssetRoutesAndFallbacks(t *testing.T) {
	files := webFixture(t)
	handler := assetHandler(files)
	for _, tc := range []struct {
		path     string
		file     string
		encoding string
		mime     string
		status   int
	}{
		{"/", "index.html.gz", "gzip", "text/html", 200},
		{"/index.html", "index.html.gz", "gzip", "text/html", 200},
		{"/movies/123", "index.html.gz", "gzip", "text/html", 200},
		{"/movies/", "index.html.gz", "gzip", "text/html", 200},
		{"/canvaskit/canvaskit.wasm", "canvaskit/canvaskit.wasm.gz", "gzip", "application/wasm", 200},
		{"/without-sidecar.js", "without-sidecar.js", "", "", 200},
		{"/assets/poster.webp", "assets/poster.webp", "", "", 200},
		{"/LICENSE", "LICENSE.gz", "gzip", "text/plain", 200},
		{"/api/unknown", "", "", "", 404},
		{"/../main.dart.js", "", "", "", 400},
	} {
		t.Run(tc.path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			req.Header.Set("Accept-Encoding", "gzip")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != tc.status || rec.Header().Get("Content-Encoding") != tc.encoding {
				t.Fatalf("status=%d encoding=%q", rec.Code, rec.Header().Get("Content-Encoding"))
			}
			if tc.file != "" && !bytes.Equal(rec.Body.Bytes(), files[tc.file].Data) {
				t.Fatalf("did not serve %s", tc.file)
			}
			if tc.mime != "" {
				got, _, err := mime.ParseMediaType(rec.Header().Get("Content-Type"))
				if err != nil || got != tc.mime {
					t.Fatalf("Content-Type = %q, want %s", rec.Header().Get("Content-Type"), tc.mime)
				}
			}
		})
	}
	// A source/server-only build without sidecars still serves the app, but
	// cannot send identity to a client that explicitly refuses it.
	req := httptest.NewRequest(http.MethodGet, "/without-sidecar.js", nil)
	req.Header.Set("Accept-Encoding", "gzip, identity;q=0")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotAcceptable || rec.Header().Get("Content-Encoding") != "" {
		t.Fatalf("unavailable encoding: %d %v", rec.Code, rec.Header())
	}
	rec = httptest.NewRecorder()
	assetHandler(fstest.MapFS{}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing web build: %d", rec.Code)
	}
}

func TestAssetHeadAndConditionalRequests(t *testing.T) {
	files := webFixture(t)
	handler := assetHandler(files)
	for _, encoding := range []string{"identity", "gzip"} {
		t.Run(encoding, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodHead, "/main.dart.js", nil)
			req.Header.Set("Accept-Encoding", encoding)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			file := "main.dart.js"
			if encoding == "gzip" {
				file += ".gz"
			}
			if rec.Code != http.StatusOK || rec.Body.Len() != 0 || rec.Header().Get("Content-Length") != strconv.Itoa(len(files[file].Data)) {
				t.Fatalf("HEAD: %d %v, body=%d bytes", rec.Code, rec.Header(), rec.Body.Len())
			}
			req = httptest.NewRequest(http.MethodGet, "/main.dart.js", nil)
			req.Header.Set("Accept-Encoding", encoding)
			req.Header.Set("If-Modified-Since", rec.Header().Get("Last-Modified"))
			rec = httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusNotModified || rec.Body.Len() != 0 || rec.Header().Get("Content-Length") != "" || rec.Header().Get("Content-Encoding") != "" {
				t.Fatalf("conditional GET: %d %v, body=%d bytes", rec.Code, rec.Header(), rec.Body.Len())
			}
			if rec.Header().Get("Vary") != "Accept-Encoding" {
				t.Fatal("304 lost cache variation")
			}
			for header, value := range map[string]string{
				"If-Match":            `"old-asset"`,
				"If-Unmodified-Since": "Wed, 01 Jan 2025 00:00:00 GMT",
			} {
				req = httptest.NewRequest(http.MethodGet, "/main.dart.js", nil)
				req.Header.Set("Accept-Encoding", encoding)
				req.Header.Set(header, value)
				rec = httptest.NewRecorder()
				handler.ServeHTTP(rec, req)
				if rec.Code != http.StatusPreconditionFailed || rec.Body.Len() != 0 || rec.Header().Get("Content-Length") != "" || rec.Header().Get("Content-Encoding") != "" {
					t.Fatalf("%s: %d %v, body=%d bytes", header, rec.Code, rec.Header(), rec.Body.Len())
				}
			}
		})
	}
}

func TestAssetRangeRequests(t *testing.T) {
	files := webFixture(t)
	handler := assetHandler(files)
	for _, tc := range []struct {
		name       string
		accept     string
		byteRange  string
		status     int
		compressed bool
	}{
		{"range uses identity", "gzip", "bytes=0-9", 206, false},
		{"unsatisfiable range", "gzip", "bytes=999999-", 416, false},
		{"identity refused ignores range", "gzip, identity;q=0", "bytes=0-9", 200, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/main.dart.js", nil)
			req.Header.Set("Accept-Encoding", tc.accept)
			req.Header.Set("Range", tc.byteRange)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != tc.status || (rec.Header().Get("Content-Encoding") == "gzip") != tc.compressed {
				t.Fatalf("range: %d %v", rec.Code, rec.Header())
			}
			if tc.status == http.StatusPartialContent && !bytes.Equal(rec.Body.Bytes(), files["main.dart.js"].Data[:10]) {
				t.Fatal("range did not return the original bytes")
			}
			if tc.compressed {
				reader, err := gzip.NewReader(rec.Body)
				if err != nil {
					t.Fatal(err)
				}
				decoded, err := io.ReadAll(reader)
				reader.Close()
				if err != nil || !bytes.Equal(decoded, files["main.dart.js"].Data) {
					t.Fatalf("range corrupted gzip: %v", err)
				}
			}
		})
	}
}
