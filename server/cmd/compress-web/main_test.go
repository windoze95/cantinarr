package main

import (
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestCompressDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "canvaskit"), 0755); err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{
		"main.dart.js":             bytes.Repeat([]byte("const app = 'Cantinarr';\n"), 100),
		"canvaskit/canvaskit.wasm": bytes.Repeat([]byte{0, 97, 115, 109, 1, 0, 0, 0}, 100),
		"poster.webp":              bytes.Repeat([]byte("already an image"), 100),
		"version.json":             []byte("{}"),
		".gitkeep":                 nil,
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := compressDir(dir); err != nil {
		t.Fatal(err)
	}
	compressedFiles := map[string][]byte{}
	for name, original := range files {
		path := filepath.Join(dir, name)
		unchanged, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(unchanged, original) {
			t.Fatalf("original %s changed: %v", name, err)
		}
		compressed, err := os.ReadFile(path + ".gz")
		if name != "main.dart.js" && name != "canvaskit/canvaskit.wasm" {
			if !os.IsNotExist(err) {
				t.Fatalf("unhelpful gzip for %s: %v", name, err)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if len(compressed) >= len(original) {
			t.Fatalf("%s did not shrink", name)
		}
		reader, err := gzip.NewReader(bytes.NewReader(compressed))
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := io.ReadAll(reader)
		reader.Close()
		if err != nil || !bytes.Equal(decoded, original) {
			t.Fatalf("%s did not round trip: %v", name, err)
		}
		compressedFiles[name] = compressed
	}
	// Rebuilding is deterministic and never compresses its own outputs.
	if err := compressDir(dir); err != nil {
		t.Fatal(err)
	}
	for name, previous := range compressedFiles {
		path := filepath.Join(dir, name) + ".gz"
		current, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(current, previous) {
			t.Fatalf("%s changed on rebuild: %v", name, err)
		}
		if _, err := os.Stat(path + ".gz"); !os.IsNotExist(err) {
			t.Fatalf("compressed %s twice: %v", name, err)
		}
	}
	// A changed original must replace the old gzip, even when the new file
	// is too small to benefit from compression.
	for _, content := range [][]byte{bytes.Repeat([]byte("updated bundle\n"), 200), []byte("small")} {
		path := filepath.Join(dir, "main.dart.js")
		if err := os.WriteFile(path, content, 0644); err != nil {
			t.Fatal(err)
		}
		if err := compressDir(dir); err != nil {
			t.Fatal(err)
		}
		compressed, err := os.ReadFile(path + ".gz")
		if len(content) == 5 {
			if !os.IsNotExist(err) {
				t.Fatalf("stale gzip retained: %v", err)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		reader, err := gzip.NewReader(bytes.NewReader(compressed))
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := io.ReadAll(reader)
		reader.Close()
		if err != nil || !bytes.Equal(decoded, content) {
			t.Fatalf("stale gzip content: %v", err)
		}
	}
}

func TestCompressDirWithoutWebBuild(t *testing.T) {
	if err := compressDir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if err := compressDir(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing input directory must fail the build")
	}
}
