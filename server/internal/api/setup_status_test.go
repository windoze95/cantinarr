package api

import "testing"

func TestBuildSetupItemsNothingConfigured(t *testing.T) {
	items := buildSetupItems(setupFacts{})
	if len(items) != 10 {
		t.Fatalf("items = %d, want 10", len(items))
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
		"tautulli":        false,
		"trakt":           false,
		"books":           false,
		"ai":              false,
	}
	for key, expect := range want {
		if got[key] != expect {
			t.Errorf("%s configured = %v, want %v", key, got[key], expect)
		}
	}
}
