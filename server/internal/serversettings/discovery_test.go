package serversettings

import (
	"testing"

	"github.com/windoze95/cantinarr-server/internal/db"
)

// newTestService builds a service over an empty database. trakt is what the
// availability probe answers, since it decides the default row source.
func newTestService(t *testing.T, trakt bool) *Service {
	t.Helper()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return NewService(database, func() bool { return trakt })
}

// TestGetDefaultsToATrendingSource pins the shipped defaults: a server nobody
// has configured serves a short-window feed, not TMDB's lifetime popularity
// ranking, and hides non-English titles from the rows.
func TestGetDefaultsToATrendingSource(t *testing.T) {
	s := newTestService(t, false)
	got := s.Get()
	if got.DiscoverySource != DiscoverySourceTMDBTrending {
		t.Errorf("DiscoverySource = %q, want %q", got.DiscoverySource, DiscoverySourceTMDBTrending)
	}
	if !got.DiscoveryEnglishOnly {
		t.Error("DiscoveryEnglishOnly = false, want the shipped default true")
	}
}

// TestDefaultSourceFollowsTraktAvailability covers the auto-adoption: adding a
// Trakt client ID is itself the statement that Trakt should be used, so the
// rows move without anyone visiting the discovery screen — and move back if the
// credential goes away, since nothing was written.
func TestDefaultSourceFollowsTraktAvailability(t *testing.T) {
	trakt := false
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	s := NewService(database, func() bool { return trakt })

	if got := s.Get().DiscoverySource; got != DiscoverySourceTMDBTrending {
		t.Errorf("DiscoverySource = %q without Trakt, want %q", got, DiscoverySourceTMDBTrending)
	}
	trakt = true
	if got := s.Get().DiscoverySource; got != DiscoverySourceTraktTrending {
		t.Errorf("DiscoverySource = %q with Trakt, want %q", got, DiscoverySourceTraktTrending)
	}
	trakt = false
	if got := s.Get().DiscoverySource; got != DiscoverySourceTMDBTrending {
		t.Errorf("DiscoverySource = %q after Trakt went away, want %q", got, DiscoverySourceTMDBTrending)
	}
}

// TestAdoptingTraktIsNotAnAdminDecision is the guard that keeps auto-adoption
// honest in both directions: it must not tick the setup-checklist item off on
// an admin's behalf, and it must not survive an admin who picks something else.
func TestAdoptingTraktIsNotAnAdminDecision(t *testing.T) {
	s := newTestService(t, true)
	if s.DiscoveryChosen() {
		t.Error("DiscoveryChosen = true from a Trakt credential alone, want false")
	}
	if got := s.Get().DiscoverySource; got != DiscoverySourceTraktTrending {
		t.Fatalf("DiscoverySource = %q, want the adopted %q", got, DiscoverySourceTraktTrending)
	}

	if _, err := s.SetDiscovery(DiscoverySourceTMDBPopular, false); err != nil {
		t.Fatalf("SetDiscovery: %v", err)
	}
	if got := s.Get().DiscoverySource; got != DiscoverySourceTMDBPopular {
		t.Errorf("DiscoverySource = %q, want the admin's %q to outrank the Trakt default", got, DiscoverySourceTMDBPopular)
	}
}

// TestEnglishOnlyHoldsItsDefaultUntilADecision covers the half of the default
// pair a bool cannot express on its own: false means "off" only once an admin
// has saved, so an explicit opt-out has to survive being read back.
func TestEnglishOnlyHoldsItsDefaultUntilADecision(t *testing.T) {
	s := newTestService(t, false)
	if !s.Get().DiscoveryEnglishOnly {
		t.Error("DiscoveryEnglishOnly = false on a fresh server, want the default true")
	}

	if _, err := s.SetDiscovery(DiscoverySourceTMDBTrending, false); err != nil {
		t.Fatalf("SetDiscovery: %v", err)
	}
	if s.Get().DiscoveryEnglishOnly {
		t.Error("DiscoveryEnglishOnly = true after an admin turned it off, want the opt-out to stick")
	}
}

// TestSettersPreserveEachOthersFields is the regression guard for the one blob:
// each setter knows only its own fields, so a whole-struct write would silently
// clear whatever the caller did not know about.
func TestSettersPreserveEachOthersFields(t *testing.T) {
	s := newTestService(t, false)

	if _, err := s.SetManagementURL("http://tower.local/Docker"); err != nil {
		t.Fatalf("SetManagementURL: %v", err)
	}
	if _, err := s.SetDiscovery(DiscoverySourceTraktTrending, true); err != nil {
		t.Fatalf("SetDiscovery: %v", err)
	}

	got := s.Get()
	if got.ManagementURL != "http://tower.local/Docker" {
		t.Errorf("ManagementURL = %q, want it preserved through the discovery write", got.ManagementURL)
	}
	if got.DiscoverySource != DiscoverySourceTraktTrending || !got.DiscoveryEnglishOnly {
		t.Errorf("discovery = (%q, %t), want (%q, true)",
			got.DiscoverySource, got.DiscoveryEnglishOnly, DiscoverySourceTraktTrending)
	}

	// And the other direction.
	if _, err := s.SetManagementURL("https://portainer.example.com"); err != nil {
		t.Fatalf("SetManagementURL: %v", err)
	}
	got = s.Get()
	if got.DiscoverySource != DiscoverySourceTraktTrending || !got.DiscoveryEnglishOnly {
		t.Errorf("discovery = (%q, %t) after a management-URL write, want it preserved",
			got.DiscoverySource, got.DiscoveryEnglishOnly)
	}
}

// TestSetDiscoveryRejectsUnknownSources fails a typo loudly instead of storing
// it and quietly reverting to the default on the next read.
func TestSetDiscoveryRejectsUnknownSources(t *testing.T) {
	s := newTestService(t, false)
	if _, err := s.SetDiscovery("netflix_top_10", false); err == nil {
		t.Fatal("SetDiscovery accepted an unknown source, want an error")
	}
	if got := s.Get().DiscoverySource; got != DiscoverySourceTMDBTrending {
		t.Errorf("DiscoverySource = %q, want the rejected write to have changed nothing", got)
	}
}

// TestSetDiscoveryAcceptsEmptyAsTheDefault lets a client clear the choice
// without having to know which source is currently the default.
func TestSetDiscoveryAcceptsEmptyAsTheDefault(t *testing.T) {
	s := newTestService(t, false)
	if _, err := s.SetDiscovery(DiscoverySourceTMDBPopular, false); err != nil {
		t.Fatalf("SetDiscovery: %v", err)
	}
	if _, err := s.SetDiscovery("", false); err != nil {
		t.Fatalf("SetDiscovery(\"\"): %v", err)
	}
	if got := s.Get().DiscoverySource; got != DefaultDiscoverySource {
		t.Errorf("DiscoverySource = %q, want %q", got, DefaultDiscoverySource)
	}
}

// TestDiscoveryChosenTracksARealDecision covers what the setup checklist keys
// on. Every discovery answer is valid, so the checklist item can only ask
// whether the admin decided — which means "chosen" has to survive the fact that
// the default and a deliberate pick of the default look identical through Get.
func TestDiscoveryChosenTracksARealDecision(t *testing.T) {
	s := newTestService(t, false)
	if s.DiscoveryChosen() {
		t.Error("DiscoveryChosen = true on a fresh server, want false")
	}

	// Saving the value the screen already loaded is still a decision, so an
	// admin who is happy with the defaults can finish the step in one tap.
	if _, err := s.SetDiscovery(DefaultDiscoverySource, false); err != nil {
		t.Fatalf("SetDiscovery: %v", err)
	}
	if !s.DiscoveryChosen() {
		t.Error("DiscoveryChosen = false after saving the default, want true")
	}
}

// TestManagementURLWriteIsNotADiscoveryDecision is the regression guard for the
// read-modify-write: a setter that round-trips the normalized blob would stamp
// a discovery source nobody picked and silently tick the checklist item off.
func TestManagementURLWriteIsNotADiscoveryDecision(t *testing.T) {
	s := newTestService(t, false)
	if _, err := s.SetManagementURL("http://tower.local/Docker"); err != nil {
		t.Fatalf("SetManagementURL: %v", err)
	}
	if s.DiscoveryChosen() {
		t.Error("DiscoveryChosen = true after a management-URL write, want false")
	}
	if got := s.Get().DiscoverySource; got != DefaultDiscoverySource {
		t.Errorf("DiscoverySource = %q, want reads to still serve %q", got, DefaultDiscoverySource)
	}
}

// TestGetNormalizesAnUnrecognizedStoredSource keeps a hand-edited or
// downgraded database serving a working row rather than an empty one.
func TestGetNormalizesAnUnrecognizedStoredSource(t *testing.T) {
	s := newTestService(t, false)
	if _, err := s.db.Exec(
		"INSERT OR REPLACE INTO settings (key, value) VALUES (?, ?)",
		settingsKey, `{"discovery_source":"from_a_newer_build"}`,
	); err != nil {
		t.Fatalf("seed settings: %v", err)
	}
	if got := s.Get().DiscoverySource; got != DefaultDiscoverySource {
		t.Errorf("DiscoverySource = %q, want the default %q", got, DefaultDiscoverySource)
	}
}
