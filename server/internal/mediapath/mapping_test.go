package mediapath

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestValidateNormalizesSourceNamespacesWithoutMutatingInput(t *testing.T) {
	root := t.TempDir()
	targets := []string{
		mkdir(t, filepath.Join(root, "posix")),
		mkdir(t, filepath.Join(root, "drive")),
		mkdir(t, filepath.Join(root, "unc")),
	}
	input := []Mapping{
		{ArrPath: "/ebooks//library/", CantinarrPath: targets[0] + string(filepath.Separator)},
		{ArrPath: `z:/Ebooks\Audio/`, CantinarrPath: targets[1]},
		{ArrPath: `//Server/Share/Books//`, CantinarrPath: targets[2]},
	}
	original := append([]Mapping(nil), input...)

	got, err := Validate(input, []string{root})
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	want := []Mapping{
		{ArrPath: "/ebooks/library", CantinarrPath: targets[0]},
		{ArrPath: `Z:\Ebooks\Audio`, CantinarrPath: targets[1]},
		{ArrPath: `\\Server\Share\Books`, CantinarrPath: targets[2]},
	}
	if len(got) != len(want) {
		t.Fatalf("Validate() returned %d mappings, want %d", len(got), len(want))
	}
	for index := range want {
		if got[index] != want[index] {
			t.Errorf("mapping %d = %#v, want %#v", index, got[index], want[index])
		}
		if input[index] != original[index] {
			t.Errorf("input mapping %d mutated from %#v to %#v", index, original[index], input[index])
		}
	}
}

func TestValidateRejectsInvalidSourcePaths(t *testing.T) {
	root := t.TempDir()
	target := mkdir(t, filepath.Join(root, "target"))
	invalidUTF8 := string([]byte{'/', 'b', 0xff})
	tests := map[string]string{
		"empty":                  "",
		"relative POSIX":         "books/file.epub",
		"drive relative":         `C:Books\file.epub`,
		"single rooted slash":    `\Books\file.epub`,
		"UNC without share":      `\\server`,
		"POSIX traversal":        "/books/../secrets",
		"drive traversal":        `C:\Books\..\Secrets`,
		"UNC traversal":          `\\server\share\..\Secrets`,
		"control":                "/books/evil\nname",
		"invalid UTF-8":          invalidUTF8,
		"Windows alternate data": `C:\Books\file.epub:secret`,
	}
	for name, source := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := Validate([]Mapping{{ArrPath: source, CantinarrPath: target}}, []string{root})
			if err == nil {
				t.Fatalf("Validate(%q) unexpectedly succeeded", source)
			}
		})
	}
}

func TestValidateRejectsDuplicateNormalizedSources(t *testing.T) {
	root := t.TempDir()
	first := mkdir(t, filepath.Join(root, "first"))
	second := mkdir(t, filepath.Join(root, "second"))
	tests := map[string][]Mapping{
		"POSIX cleaned duplicate": {
			{ArrPath: "/books", CantinarrPath: first},
			{ArrPath: "/books/", CantinarrPath: second},
		},
		"drive case and separator duplicate": {
			{ArrPath: `C:\Books\Audio`, CantinarrPath: first},
			{ArrPath: `c:/books/audio/`, CantinarrPath: second},
		},
		"UNC case duplicate": {
			{ArrPath: `\\Server\Share\Books`, CantinarrPath: first},
			{ArrPath: `//server/share/books/`, CantinarrPath: second},
		},
	}
	for name, mappings := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := Validate(mappings, []string{root})
			if err == nil || !strings.Contains(err.Error(), "duplicates") {
				t.Fatalf("Validate() error = %v, want duplicate rejection", err)
			}
		})
	}
}

func TestValidateRequiresNativeDirectoryInsideAllowedRoot(t *testing.T) {
	rootParent := t.TempDir()
	root := mkdir(t, filepath.Join(rootParent, "media"))
	inside := mkdir(t, filepath.Join(root, "inside"))
	outside := mkdir(t, filepath.Join(t.TempDir(), "outside"))
	prefixSibling := mkdir(t, filepath.Join(rootParent, "media-other"))
	regularFile := filepath.Join(root, "not-a-directory")
	if err := os.WriteFile(regularFile, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(root, "missing")
	traversal := inside + string(filepath.Separator) + ".." + string(filepath.Separator) + filepath.Base(inside)

	tests := map[string]string{
		"relative":       "inside",
		"outside":        outside,
		"prefix sibling": prefixSibling,
		"regular file":   regularFile,
		"missing":        missing,
		"traversal":      traversal,
		"control":        inside + "\n",
	}
	for name, target := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := Validate([]Mapping{{ArrPath: "/books", CantinarrPath: target}}, []string{root})
			if err == nil {
				t.Fatalf("Validate(%q) unexpectedly succeeded", target)
			}
		})
	}

	got, err := Validate([]Mapping{{ArrPath: "/books", CantinarrPath: inside}}, []string{root})
	if err != nil {
		t.Fatalf("Validate(inside) error = %v", err)
	}
	if got[0].CantinarrPath != filepath.Clean(inside) {
		t.Fatalf("CantinarrPath = %q, want %q", got[0].CantinarrPath, filepath.Clean(inside))
	}
}

func TestValidateChecksLexicalAndResolvedSymlinkContainment(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks requires privileges on some Windows builders")
	}
	root := t.TempDir()
	inside := mkdir(t, filepath.Join(root, "inside"))
	outside := t.TempDir()

	insideLink := filepath.Join(root, "inside-link")
	if err := os.Symlink(filepath.Base(inside), insideLink); err != nil {
		t.Fatal(err)
	}
	if _, err := Validate([]Mapping{{ArrPath: "/inside", CantinarrPath: insideLink}}, []string{root}); err != nil {
		t.Fatalf("Validate(in-root symlink) error = %v", err)
	}

	escapeLink := filepath.Join(root, "escape-link")
	if err := os.Symlink(outside, escapeLink); err != nil {
		t.Fatal(err)
	}
	if _, err := Validate([]Mapping{{ArrPath: "/escape", CantinarrPath: escapeLink}}, []string{root}); err == nil {
		t.Fatal("Validate() accepted a lexically contained symlink resolving outside the root")
	}

	aliasParent := t.TempDir()
	rootAlias := filepath.Join(aliasParent, "root-alias")
	if err := os.Symlink(root, rootAlias); err != nil {
		t.Fatal(err)
	}
	aliasedTarget := filepath.Join(rootAlias, filepath.Base(inside))
	got, err := Validate([]Mapping{{ArrPath: "/alias", CantinarrPath: aliasedTarget}}, []string{rootAlias})
	if err != nil {
		t.Fatalf("Validate(root alias) error = %v", err)
	}
	if got[0].CantinarrPath != filepath.Clean(aliasedTarget) {
		t.Fatalf("alias target = %q, want lexical path %q", got[0].CantinarrPath, aliasedTarget)
	}

	// Resolved containment alone is insufficient: a target spelled through
	// the real root is not reachable relative to an allowlist entry spelled
	// through a different lexical alias.
	if _, err := Validate([]Mapping{{ArrPath: "/real", CantinarrPath: inside}}, []string{rootAlias}); err == nil {
		t.Fatal("Validate() accepted a target outside the allowed root's lexical namespace")
	}
}

func TestValidateRejectsInvalidAllowedRoots(t *testing.T) {
	root := t.TempDir()
	target := mkdir(t, filepath.Join(root, "target"))
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	tests := map[string]string{
		"relative": "relative-root",
		"missing":  filepath.Join(root, "missing"),
		"file":     file,
		"control":  root + "\n",
	}
	if runtime.GOOS != "windows" {
		tests["filesystem root"] = string(filepath.Separator)
	}
	for name, allowed := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := Validate([]Mapping{{ArrPath: "/books", CantinarrPath: target}}, []string{allowed})
			if err == nil {
				t.Fatalf("Validate() accepted allowed root %q", allowed)
			}
		})
	}
}

func TestTranslateUsesLongestComponentBoundPOSIXPrefix(t *testing.T) {
	root := t.TempDir()
	general := filepath.Join(root, "general")
	audio := filepath.Join(root, "audio")
	mappings := []Mapping{
		{ArrPath: "/books", CantinarrPath: general},
		{ArrPath: "/books/audio", CantinarrPath: audio},
	}

	got, ok := Translate("/books/audio/Author/Title/chapter01.m4b", mappings)
	want := filepath.Join(audio, "Author", "Title", "chapter01.m4b")
	if !ok || got != want {
		t.Fatalf("Translate(nested) = (%q, %v), want (%q, true)", got, ok, want)
	}
	got, ok = Translate("/books/ebooks/Author/Title.epub", mappings)
	want = filepath.Join(general, "ebooks", "Author", "Title.epub")
	if !ok || got != want {
		t.Fatalf("Translate(general) = (%q, %v), want (%q, true)", got, ok, want)
	}
	if _, ok := Translate("/books-old/title.epub", mappings); ok {
		t.Fatal("Translate() matched a prefix sibling")
	}
	if _, ok := Translate("/Books/title.epub", mappings); ok {
		t.Fatal("Translate() treated a POSIX prefix as case-insensitive")
	}
	got, ok = Translate("/books", mappings)
	if !ok || got != filepath.Clean(general) {
		t.Fatalf("Translate(exact prefix) = (%q, %v), want (%q, true)", got, ok, general)
	}
}

func TestTranslateWindowsDrivePathsOnAnyHost(t *testing.T) {
	target := filepath.Join(t.TempDir(), "drive")
	mappings := []Mapping{{ArrPath: `Z:\Ebooks\Audio`, CantinarrPath: target}}
	got, ok := Translate(`z:/ebooks/audio/Author\Book/01.m4b`, mappings)
	want := filepath.Join(target, "Author", "Book", "01.m4b")
	if !ok || got != want {
		t.Fatalf("Translate() = (%q, %v), want (%q, true)", got, ok, want)
	}
	if _, ok := Translate(`Y:\Ebooks\Audio\Book.m4b`, mappings); ok {
		t.Fatal("Translate() matched a different drive")
	}
	if _, ok := Translate(`Z:\Ebooks\Audiobooks\Book.m4b`, mappings); ok {
		t.Fatal("Translate() matched a Windows prefix sibling")
	}
}

func TestTranslateUNCPathsOnAnyHost(t *testing.T) {
	target := filepath.Join(t.TempDir(), "unc")
	mappings := []Mapping{{ArrPath: `\\MediaServer\Books\Yana-Audio`, CantinarrPath: target}}
	got, ok := Translate(`//mediaserver/books/yana-audio/Author/Book/01.m4b`, mappings)
	want := filepath.Join(target, "Author", "Book", "01.m4b")
	if !ok || got != want {
		t.Fatalf("Translate() = (%q, %v), want (%q, true)", got, ok, want)
	}
	if _, ok := Translate(`\\MediaServer\OtherShare\Yana-Audio\01.m4b`, mappings); ok {
		t.Fatal("Translate() matched a different UNC share")
	}
	if _, ok := Translate(`\\OtherServer\Books\Yana-Audio\01.m4b`, mappings); ok {
		t.Fatal("Translate() matched a different UNC server")
	}
}

func TestTranslateSupportsIndependentChaptarrRoots(t *testing.T) {
	base := t.TempDir()
	mappings := []Mapping{
		{ArrPath: "/ebooks", CantinarrPath: filepath.Join(base, "ebooks")},
		{ArrPath: "/audiobooks", CantinarrPath: filepath.Join(base, "audiobooks")},
		{ArrPath: "/yana-ebooks", CantinarrPath: filepath.Join(base, "yana-ebooks")},
		{ArrPath: "/yana-audiobooks", CantinarrPath: filepath.Join(base, "yana-audiobooks")},
	}
	for _, mapping := range mappings {
		reported := mapping.ArrPath + "/Author/Title/file.bin"
		got, ok := Translate(reported, mappings)
		want := filepath.Join(mapping.CantinarrPath, "Author", "Title", "file.bin")
		if !ok || got != want {
			t.Errorf("Translate(%q) = (%q, %v), want (%q, true)", reported, got, ok, want)
		}
	}
}

func TestTranslateFailsClosedForMalformedOrAmbiguousInput(t *testing.T) {
	target := t.TempDir()
	valid := []Mapping{{ArrPath: "/books", CantinarrPath: target}}
	for _, reported := range []string{
		"relative/book.epub",
		"/books/../secret",
		"/books/evil\nname",
		`C:Books\book.epub`,
		`\\server`,
	} {
		if got, ok := Translate(reported, valid); ok {
			t.Errorf("Translate(%q) = %q, want rejected", reported, got)
		}
	}

	ambiguous := []Mapping{
		{ArrPath: `C:\Books`, CantinarrPath: filepath.Join(target, "first")},
		{ArrPath: `c:/books`, CantinarrPath: filepath.Join(target, "second")},
	}
	if got, ok := Translate(`C:\Books\title.epub`, ambiguous); ok {
		t.Fatalf("Translate(ambiguous) = %q, want rejected", got)
	}
	if got, ok := Translate("/books/title.epub", []Mapping{{ArrPath: "/books", CantinarrPath: "relative"}}); ok {
		t.Fatalf("Translate(relative target) = %q, want rejected", got)
	}
}

func TestTranslateSupportsSourceFilesystemRoots(t *testing.T) {
	posixTarget := filepath.Join(t.TempDir(), "posix")
	got, ok := Translate("/library/title.epub", []Mapping{{ArrPath: "/", CantinarrPath: posixTarget}})
	want := filepath.Join(posixTarget, "library", "title.epub")
	if !ok || got != want {
		t.Fatalf("Translate(POSIX root) = (%q, %v), want (%q, true)", got, ok, want)
	}

	driveTarget := filepath.Join(t.TempDir(), "drive")
	got, ok = Translate(`C:\Library\title.epub`, []Mapping{{ArrPath: `C:\`, CantinarrPath: driveTarget}})
	want = filepath.Join(driveTarget, "Library", "title.epub")
	if !ok || got != want {
		t.Fatalf("Translate(drive root) = (%q, %v), want (%q, true)", got, ok, want)
	}
}

func mkdir(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	return path
}
