package instance

import (
	"reflect"
	"testing"
)

func TestAvailabilityScanSelectionIsIndependentAndOlderEditsPreserveIt(t *testing.T) {
	h := NewHandler(newTestStore(t), nil)
	existing := &Instance{ServiceType: "jellyfin", MediaServerConfig: MediaServerConfig{LibraryIDs: []string{"shared"}, AvailabilityScan: &AvailabilityScanConfig{Enabled: true, LibraryIDs: []string{"scan"}}}}
	next := &Instance{ServiceType: "jellyfin"}
	if err := h.applyMediaServerConfig(next, &MediaServerConfig{LibraryIDs: []string{"different-share"}}, existing); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(next.MediaServerConfig.AvailabilityScan, existing.MediaServerConfig.AvailabilityScan) {
		t.Fatal("older client erased scan selection")
	}
	next.MediaServerConfig.AvailabilityScan.LibraryIDs[0] = "changed"
	if existing.MediaServerConfig.AvailabilityScan.LibraryIDs[0] != "scan" {
		t.Fatal("scan config aliased stored selection")
	}
	provided := &MediaServerConfig{LibraryIDs: []string{"shared"}, AvailabilityScan: &AvailabilityScanConfig{Enabled: false, LibraryIDs: []string{"scan"}}}
	if err := h.applyMediaServerConfig(next, provided, existing); err != nil {
		t.Fatal(err)
	}
	if next.MediaServerConfig.AvailabilityScan.Enabled || !reflect.DeepEqual(next.MediaServerConfig.LibraryIDs, existing.MediaServerConfig.LibraryIDs) {
		t.Fatal("scan disable changed provider sharing")
	}
	for _, bad := range []*AvailabilityScanConfig{{Enabled: true}, {Enabled: true, LibraryIDs: []string{"../path"}}, {Enabled: true, LibraryIDs: []string{"a,b"}}, {Enabled: true, LibraryIDs: []string{"a\nb"}}} {
		if _, err := validateMediaServerConfig(MediaServerConfig{AvailabilityScan: bad}); err == nil {
			t.Fatalf("accepted invalid scan selection %+v", bad)
		}
	}
	if err := h.applyMediaServerConfig(&Instance{ServiceType: "audiobookshelf"}, &MediaServerConfig{AvailabilityScan: &AvailabilityScanConfig{Enabled: true, LibraryIDs: []string{"a"}}}, nil); err == nil {
		t.Fatal("audio server accepted video scan")
	}
}
