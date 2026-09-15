package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/config"
	"github.com/windoze95/cantinarr-server/internal/credentials"
	"github.com/windoze95/cantinarr-server/internal/db"
	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/secrets"
	"github.com/windoze95/cantinarr-server/internal/serversettings"
)

func TestBuildSetupItemsNothingConfigured(t *testing.T) {
	items := buildSetupItems(setupFacts{})
	if len(items) != 14 {
		t.Fatalf("items = %d, want 14", len(items))
	}
	for _, item := range items {
		if item.Configured {
			t.Errorf("%s: configured with empty facts", item.Key)
		}
		if item.Key == "" || item.Title == "" || item.Description == "" {
			t.Errorf("item missing display fields: %+v", item)
		}
	}
	for _, item := range items {
		if !item.Optional {
			t.Errorf("%s must advertise skip support", item.Key)
		}
	}

}

func TestBuildSetupItemsMapsFacts(t *testing.T) {
	items := buildSetupItems(setupFacts{
		HasRadarr:         true,
		HasDownloadClient: true,
		MediaDownloads:    true,
		TMDB:              true,
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
		"media_servers":   false,
		"download_client": true,
		"media_downloads": true,
		"tautulli":        false,
		"trakt":           false,
		"discovery_prefs": false,
		"books":           false,
		"music":           false,
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

// The one genuinely broken remediation shape gets called out in the item copy:
// detection on, nothing configured to investigate.
func TestRemediationSetupItemWarnsWhenProviderless(t *testing.T) {
	broken := remediationDescription(setupFacts{RemediationEnabled: true, AI: false})
	if !strings.Contains(broken, "no shared AI provider") {
		t.Fatalf("providerless copy = %q, want the warning", broken)
	}
	fine := remediationDescription(setupFacts{RemediationEnabled: true, AI: true})
	if strings.Contains(fine, "no shared AI provider") {
		t.Fatalf("healthy copy still warns: %q", fine)
	}
}

func TestSetupSkipsPersistForEveryItem(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cantinarr.db")
	database, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	cipher, err := secrets.NewCipher(bytes.Repeat([]byte{0x31}, 32))
	if err != nil {
		t.Fatal(err)
	}
	settings := serversettings.NewService(database, nil)
	if _, err := settings.SetExternalURL("https://cantinarr.example.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := settings.SetDiscovery(serversettings.DiscoverySourceTMDBTrending, false); err != nil {
		t.Fatal(err)
	}
	put := func(body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		setupSkipHandler(settings)(rec, httptest.NewRequest(http.MethodPut, "/api/admin/setup-status/skips", strings.NewReader(body)))
		return rec
	}
	for _, item := range buildSetupItems(setupFacts{}) {
		body, _ := json.Marshal(map[string]any{"key": item.Key, "skipped": true})
		if rec := put(string(body)); rec.Code != http.StatusOK {
			t.Fatalf("skip %s = %d: %s", item.Key, rec.Code, rec.Body.String())
		}
	}
	for _, body := range []string{`{"key":"unknown","skipped":true}`, `{`} {
		if rec := put(body); rec.Code != http.StatusBadRequest {
			t.Fatalf("invalid write = %d", rec.Code)
		}
	}

	// Reopen the database, not just the settings service, to prove persistence.
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	database, err = db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	settings = serversettings.NewService(database, nil)
	stored := settings.Get()
	if stored.ExternalURL != "https://cantinarr.example.com" || stored.DiscoverySource != serversettings.DiscoverySourceTMDBTrending || stored.DiscoveryEnglishOnly {
		t.Fatalf("skips changed unrelated preferences: %+v", stored)
	}
	store := instance.NewStore(database, cipher)
	creds := credentials.NewRegistry(database, cipher)
	get := func() []setupItem {
		t.Helper()
		rec := httptest.NewRecorder()
		setupStatusHandler(&config.Config{}, store, creds, nil, settings, nil)(rec, httptest.NewRequest(http.MethodGet, "/api/admin/setup-status", nil))
		var response struct {
			Items []setupItem `json:"items"`
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("GET status = %d: %s", rec.Code, rec.Body.String())
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		return response.Items
	}
	items := get()
	if len(items) != 14 {
		t.Fatalf("read %d items", len(items))
	}
	for _, item := range items {
		if !item.Optional || !item.Skipped {
			t.Errorf("skip not restored for %s: %+v", item.Key, item)
		}
		if item.Key == "radarr" && item.Configured {
			t.Error("skipping configured Radarr")
		}
	}

	// An existing but unreachable instance is still configured, even if skipped.
	if err := store.Create(&instance.Instance{ServiceType: "radarr", Name: "Offline movies", URL: "http://127.0.0.1:1", APIKey: "test-key"}); err != nil {
		t.Fatal(err)
	}
	for _, item := range get() {
		if item.Key == "radarr" && (!item.Configured || !item.Skipped) {
			t.Errorf("configured skipped Radarr = %+v", item)
		}
	}
	if rec := put(`{"key":"radarr","skipped":false}`); rec.Code != http.StatusOK {
		t.Fatalf("restore = %d", rec.Code)
	}
	for _, item := range get() {
		if item.Skipped != (item.Key != "radarr") {
			t.Errorf("restoring Radarr changed %s: %+v", item.Key, item)
		}
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if rec := put(`{"key":"push","skipped":true}`); rec.Code != http.StatusInternalServerError {
		t.Fatalf("failed save = %d", rec.Code)
	}
}

func TestSetupSkipWritesRequireAdmin(t *testing.T) {
	h := newRBACRouterHarness(t, false)
	for _, tc := range []struct {
		token string
		want  int
	}{
		{"", http.StatusUnauthorized},
		{h.requesterToken, http.StatusForbidden},
		{h.adminToken, http.StatusOK},
	} {
		rec := serveRBACRequestWithBody(h.router, http.MethodPut, "/api/admin/setup-status/skips", tc.token, `{"key":"radarr","skipped":true}`)
		if rec.Code != tc.want {
			t.Errorf("write = %d, want %d: %s", rec.Code, tc.want, rec.Body.String())
		}
	}
}
