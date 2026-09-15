package serversettings

import (
	"path/filepath"
	"sync"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/db"
)

func TestDiscoveryPartialWritesAreAtomicAndPreserveOtherSettings(t *testing.T) {
	s := newTestService(t, true)
	start := make(chan struct{})
	var wg sync.WaitGroup
	write := func(f func() (Settings, error)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if _, err := f(); err != nil {
				t.Error(err)
			}
		}()
	}
	for mediaType := range DiscoverServices() {
		write(func() (Settings, error) {
			return s.UpdateDiscovery(DiscoveryPatch{HiddenWhenUnconfigured: map[string]bool{mediaType: true}})
		})
	}
	write(func() (Settings, error) { return s.SetManagementURL("https://management.example") })
	write(func() (Settings, error) { return s.SetExternalURL("https://server.example") })
	write(func() (Settings, error) { return s.SetSetupItemSkipped("ai", true) })
	close(start)
	wg.Wait()
	got := s.Get()
	for mediaType := range DiscoverServices() {
		if !got.HiddenWhenUnconfigured[mediaType] {
			t.Errorf("lost %s preference", mediaType)
		}
	}
	if got.ManagementURL != "https://management.example" || got.ExternalURL != "https://server.example" || len(got.SetupSkippedItems) != 1 {
		t.Fatalf("lost unrelated settings: %+v", got)
	}
	if s.DiscoveryChosen() || !got.DiscoveryEnglishOnly || got.DiscoverySource != DiscoverySourceTraktTrending {
		t.Fatal("hide-only writes must preserve automatic discovery defaults")
	}
	off := false
	if _, err := s.UpdateDiscovery(DiscoveryPatch{EnglishOnly: &off}); err != nil {
		t.Fatal(err)
	}
	got = s.Get()
	if got.DiscoveryEnglishOnly || got.DiscoverySource != DiscoverySourceTraktTrending || len(got.HiddenWhenUnconfigured) != 4 {
		t.Fatalf("partial language update lost preferences: %+v", got)
	}
}

func TestDiscoverVisibilitySurvivesRestartAndOldClientWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cantinarr.db")
	database, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s := NewService(database, nil)
	if _, err := s.UpdateDiscovery(DiscoveryPatch{HiddenWhenUnconfigured: map[string]bool{"movie": true, "book": true}}); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	database, err = db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	s = NewService(database, nil)
	if _, err := s.SetDiscovery(DiscoverySourceTMDBPopular, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateDiscovery(DiscoveryPatch{HiddenWhenUnconfigured: map[string]bool{"movie": false}}); err != nil {
		t.Fatal(err)
	}
	got := s.Get()
	if !got.HiddenWhenUnconfigured["book"] || got.HiddenWhenUnconfigured["movie"] || got.DiscoveryEnglishOnly || got.DiscoverySource != DiscoverySourceTMDBPopular {
		t.Fatalf("restart/legacy write lost preferences: %+v", got)
	}
}

func TestDiscoveryUnreadableSettingsCannotBeOverwritten(t *testing.T) {
	s := newTestService(t, false)
	if _, err := s.db.Exec("INSERT INTO settings (key, value) VALUES (?, ?)", settingsKey, "invalid-json"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateDiscovery(DiscoveryPatch{HiddenWhenUnconfigured: map[string]bool{"tv": true}}); err == nil {
		t.Fatal("unreadable preferences were replaced")
	}
}
