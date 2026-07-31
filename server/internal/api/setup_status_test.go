package api

import (
	"strings"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/serversettings"
)

func TestBuildSetupItemsNothingConfigured(t *testing.T) {
	items := buildSetupItems(setupFacts{})
	if len(items) != 12 {
		t.Fatalf("items = %d, want 12", len(items))
	}
	for _, item := range items {
		if item.Configured {
			t.Errorf("%s: configured with empty facts", item.Key)
		}
		if item.Key == "" || item.Title == "" || item.Description == "" {
			t.Errorf("item missing display fields: %+v", item)
		}
	}
	// Essentials lead the list so the wizard shows them first.
	for i, key := range []string{"radarr", "sonarr", "tmdb"} {
		if items[i].Key != key {
			t.Errorf("items[%d] = %s, want %s", i, items[i].Key, key)
		}
		if items[i].Optional {
			t.Errorf("%s must not be optional", key)
		}
	}
	for _, item := range items[3:] {
		if !item.Optional {
			t.Errorf("%s should be optional", item.Key)
		}
	}
}

func TestBuildSetupItemsMapsFacts(t *testing.T) {
	items := buildSetupItems(setupFacts{
		HasRadarr:         true,
		HasDownloadClient: true,
		MediaDownloads:    true,
		TMDB:              true,
		PlexInvites:       true,
	})
	got := map[string]bool{}
	for _, item := range items {
		got[item.Key] = item.Configured
	}
	want := map[string]bool{
		"radarr":          true,
		"sonarr":          false,
		"tmdb":            true,
		"push":            false,
		"plex_invites":    true,
		"download_client": true,
		"media_downloads": true,
		"tautulli":        false,
		"trakt":           false,
		"discovery_prefs": false,
		"books":           false,
		"ai":              false,
	}
	for key, expect := range want {
		if got[key] != expect {
			t.Errorf("%s configured = %v, want %v", key, got[key], expect)
		}
	}
}

// TestDiscoveryItemFollowsTrakt keeps the two adjacent: connecting Trakt is
// only half the job, and the row that finishes it has to be the next thing the
// admin's eye lands on.
func TestDiscoveryItemFollowsTrakt(t *testing.T) {
	items := buildSetupItems(setupFacts{})
	for i, item := range items {
		if item.Key != "trakt" {
			continue
		}
		if i+1 >= len(items) || items[i+1].Key != "discovery_prefs" {
			t.Fatalf("item after trakt = %v, want discovery_prefs", items[i+1:])
		}
		return
	}
	t.Fatal("no trakt item in the checklist")
}

// TestDiscoveryItemIsSatisfiedByAnyChoice pins why this item is not graded like
// the rest: TMDB trending is a legitimate answer, so an admin who picks it must
// be able to finish the checklist. Grading on "did you pick Trakt" would leave
// a permanent unconfigured count badged in the menu.
func TestDiscoveryItemIsSatisfiedByAnyChoice(t *testing.T) {
	for _, source := range []string{
		serversettings.DiscoverySourceTMDBTrending,
		serversettings.DiscoverySourceTraktTrending,
		serversettings.DiscoverySourceTMDBPopular,
	} {
		items := buildSetupItems(setupFacts{
			Trakt:           true,
			DiscoveryChosen: true,
			DiscoverySource: source,
		})
		for _, item := range items {
			if item.Key != "discovery_prefs" {
				continue
			}
			if !item.Configured {
				t.Errorf("source %q left discovery_prefs unconfigured", source)
			}
		}
	}
}

// TestDiscoveryDescriptionAnnouncesAdoptedTrakt covers the nudge itself. The
// server switches the rows to Trakt on its own the moment the credential
// exists; this step is where an admin finds that out, so it must say so — and
// only while the choice is still the server's, never as a comment on a decision
// the admin already made.
func TestDiscoveryDescriptionAnnouncesAdoptedTrakt(t *testing.T) {
	adopted := discoveryDescription(setupFacts{
		Trakt:           true,
		DiscoverySource: serversettings.DiscoverySourceTraktTrending,
	})
	if !strings.Contains(adopted, "Trakt is connected") {
		t.Errorf("description = %q, want it to name the adopted Trakt feed", adopted)
	}

	for name, facts := range map[string]setupFacts{
		"admin already decided": {
			Trakt:           true,
			DiscoveryChosen: true,
			DiscoverySource: serversettings.DiscoverySourceTraktTrending,
		},
		"admin chose TMDB over a connected Trakt": {
			Trakt:           true,
			DiscoveryChosen: true,
			DiscoverySource: serversettings.DiscoverySourceTMDBTrending,
		},
		"no trakt": {DiscoverySource: serversettings.DiscoverySourceTMDBTrending},
	} {
		if got := discoveryDescription(facts); strings.Contains(got, "Trakt is connected") {
			t.Errorf("%s: description = %q, want no Trakt nudge", name, got)
		}
	}
}

// TestAdoptedTraktLeavesTheStepUnfinished pins the seam between the two halves
// of this feature: the server picking Trakt for you is a convenience, not an
// answer, so the checklist item stays open until an admin actually saves.
func TestAdoptedTraktLeavesTheStepUnfinished(t *testing.T) {
	items := buildSetupItems(setupFacts{
		Trakt:           true,
		DiscoveryChosen: false,
		DiscoverySource: serversettings.DiscoverySourceTraktTrending,
	})
	for _, item := range items {
		if item.Key == "discovery_prefs" && item.Configured {
			t.Error("discovery_prefs configured by an auto-adopted Trakt source, want it still open")
		}
	}
}
