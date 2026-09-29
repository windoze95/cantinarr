// compress-web creates gzip alternatives before the web assets are embedded.
// Keeping compression in the build avoids CPU and allocation costs on the NAS.
package main

import (
	"bytes"
	"compress/gzip"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	dir := flag.String("dir", "internal/web/dist", "directory containing the built web app")
	flag.Parse()
	if err := compressDir(*dir); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func compressDir(dir string) error {
	return filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.Type().IsRegular() || !compressible(path) {
			return nil
		}
		original, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var compressed bytes.Buffer
		writer, err := gzip.NewWriterLevel(&compressed, gzip.BestCompression)
		if err != nil {
			return err
		}
		if _, err := writer.Write(original); err != nil {
			return err
		}
		if err := writer.Close(); err != nil {
			return err
		}
		if compressed.Len() >= len(original) {
			// A rebuild may have replaced a formerly compressible file.
			if err := os.Remove(path + ".gz"); err != nil && !os.IsNotExist(err) {
				return err
			}
			return nil
		}
		if err := os.WriteFile(path+".gz", compressed.Bytes(), 0644); err != nil {
			return err
		}
		fmt.Printf("%s: %d -> %d bytes\n", path, len(original), compressed.Len())
		return nil
	})
}

func compressible(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".css", ".html", ".js", ".json", ".mjs", ".svg", ".txt", ".wasm", ".xml":
		return true
	default:
		return false
	}
}
