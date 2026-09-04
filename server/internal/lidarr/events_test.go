package lidarr

import "testing"

// The vocabulary is verified against Lidarr's open source: the webhook import
// event serializes as "Download" and the history enum's import events as
// "trackFileImported" and "downloadImported".
func TestIsImportEventType(t *testing.T) {
	for _, name := range []string{"Download", "download", "trackFileImported", "TrackFileImported", "downloadImported", "ReleaseImport", "AlbumImport"} {
		if !IsImportEventType(name) {
			t.Errorf("IsImportEventType(%q) = false, want true", name)
		}
	}
	// Rename, retag and delete are deliberately NOT import events: announcing
	// "your album is ready" because a file was renamed would be worse than
	// announcing it late.
	for _, name := range []string{"Grab", "Rename", "Retag", "TrackRetag", "AlbumDelete", "ArtistDelete", "TrackFileDelete", "Test", "Health", ""} {
		if IsImportEventType(name) {
			t.Errorf("IsImportEventType(%q) = true, want false", name)
		}
	}
}

func TestNormalizeEventTypeFoldsCaseAndSeparators(t *testing.T) {
	if got := NormalizeEventType("Track-File_Imported"); got != "trackfileimported" {
		t.Fatalf("NormalizeEventType = %q", got)
	}
	long := make([]byte, 65)
	for i := range long {
		long[i] = 'a'
	}
	if got := NormalizeEventType(string(long)); got != "" {
		t.Fatalf("oversized input normalized to %q, want rejection", got)
	}
}
