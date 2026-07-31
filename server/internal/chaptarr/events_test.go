package chaptarr

import "testing"

// TestIsImportEventType pins the shared import vocabulary both witnesses key
// on: generous across lineage spellings and separators, and strictly closed to
// rename/retag/delete-class events that must never push a "ready" alert.
func TestIsImportEventType(t *testing.T) {
	for _, name := range []string{
		"Download", "ReleaseImport", "bookFileImported", "BookFileImport",
		"bookImported", "downloadImported", "DownloadFolderImported", "download_imported",
	} {
		if !IsImportEventType(name) {
			t.Errorf("IsImportEventType(%q) = false, want true", name)
		}
	}
	for _, name := range []string{
		"Rename", "Retag", "BookRetag", "BookFileDelete", "BookDelete",
		"AuthorDelete", "Grab", "Test", "Health", "", "downloadFailed",
	} {
		if IsImportEventType(name) {
			t.Errorf("IsImportEventType(%q) = true, want false", name)
		}
	}
	// Absurdly long names are rejected outright rather than normalized.
	long := make([]byte, 65)
	for i := range long {
		long[i] = 'a'
	}
	if NormalizeEventType(string(long)) != "" {
		t.Error("NormalizeEventType accepted a 65-byte name")
	}
}
