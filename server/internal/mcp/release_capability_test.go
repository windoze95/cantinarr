package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/chaptarr"
	"github.com/windoze95/cantinarr-server/internal/db"
	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/radarr"
	"github.com/windoze95/cantinarr-server/internal/secrets"
	"github.com/windoze95/cantinarr-server/internal/sonarr"
)

func TestAllReleaseRenderersHideOpaqueAndSignedCapabilities(t *testing.T) {
	radarrRelease := radarr.Release{GUID: "opaque-radarr-release-capability", IndexerID: 1, Title: "Radarr Candidate"}
	sonarrRelease := sonarr.Release{GUID: "https://indexer.invalid/signed/sonarr-path-sentinel", IndexerID: 2, Title: "Sonarr Candidate"}
	chaptarrRelease := chaptarr.Release{GUID: "https://indexer.invalid/book?signature=chaptarr-query-sentinel", IndexerID: 3, Title: "Chaptarr Candidate"}

	tests := []struct {
		name       string
		rawGUID    string
		text       string
		candidates []ReleaseCandidate
	}{
		{name: "radarr opaque", rawGUID: radarrRelease.GUID, text: formatRadarrReleases([]radarr.Release{radarrRelease}), candidates: radarrReleaseCandidates([]radarr.Release{radarrRelease})},
		{name: "sonarr signed path", rawGUID: sonarrRelease.GUID, text: formatSonarrReleases([]sonarr.Release{sonarrRelease}), candidates: sonarrReleaseCandidates([]sonarr.Release{sonarrRelease})},
		{name: "chaptarr signed query", rawGUID: chaptarrRelease.GUID, text: formatChaptarrReleases([]chaptarr.Release{chaptarrRelease}), candidates: chaptarrReleaseCandidates([]chaptarr.Release{chaptarrRelease})},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidateJSON, err := json.Marshal(test.candidates)
			if err != nil {
				t.Fatalf("encode candidates: %v", err)
			}
			combined := test.text + string(candidateJSON)
			if strings.Contains(combined, test.rawGUID) {
				t.Fatalf("release capability leaked: %s", combined)
			}
			if len(test.candidates) != 1 || test.candidates[0].Reference != releaseGUIDReference(test.rawGUID) ||
				!strings.Contains(test.text, releaseGUIDReference(test.rawGUID)) {
				t.Fatalf("canonical reference missing: text=%q candidates=%+v", test.text, test.candidates)
			}
		})
	}
}

func TestReleaseSearchHidesCapabilityAndGrabResolvesFreshOnAuthoritativeInstance(t *testing.T) {
	const rawGUID = "https://indexer.invalid/download/opaque-signed-path-sentinel?token=capability-sentinel"
	const targetInstanceID = "radarr-target"

	var targetSearches atomic.Int32
	var targetGrabs atomic.Int32
	grabbedGUID := make(chan string, 1)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/movie":
			if tmdbID := r.URL.Query().Get("tmdbId"); tmdbID != "" && tmdbID != "42" {
				t.Errorf("movie lookup query = %q", r.URL.RawQuery)
			}
			_ = json.NewEncoder(w).Encode([]map[string]any{{"id": 71, "tmdbId": 42, "title": "Scoped Movie " + rawGUID, "year": 2026}})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/release":
			targetSearches.Add(1)
			if r.URL.Query().Get("movieId") != "71" {
				t.Errorf("release search query = %q", r.URL.RawQuery)
			}
			_ = json.NewEncoder(w).Encode([]map[string]any{{
				"guid": rawGUID, "indexerId": 9, "indexer": "Scoped Indexer", "title": "Scoped.Movie.2026 " + rawGUID,
				"size": 1234, "protocol": "usenet", "quality": map[string]any{"quality": map[string]any{"name": "WEBDL-1080p"}},
				"rejected": true, "rejections": []string{"untrusted metadata repeated " + rawGUID},
			}})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v3/release":
			targetGrabs.Add(1)
			var body struct {
				GUID      string `json:"guid"`
				IndexerID int    `json:"indexerId"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode grab body: %v", err)
			}
			if body.IndexerID != 9 {
				t.Errorf("grab indexer_id = %d, want 9", body.IndexerID)
			}
			grabbedGUID <- body.GUID
			w.WriteHeader(http.StatusCreated)
		default:
			http.NotFound(w, r)
		}
	}))
	defer target.Close()

	var decoyRequests atomic.Int32
	decoy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		decoyRequests.Add(1)
		http.Error(w, "wrong instance", http.StatusTeapot)
	}))
	defer decoy.Close()

	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer database.Close()
	cipher, err := secrets.NewCipher(bytes.Repeat([]byte{0x52}, 32))
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	store := instance.NewStore(database, cipher)
	for _, inst := range []*instance.Instance{
		{ID: "radarr-default", ServiceType: "radarr", Name: "Decoy", URL: decoy.URL, APIKey: "decoy", IsDefault: true},
		{ID: targetInstanceID, ServiceType: "radarr", Name: "Target", URL: target.URL, APIKey: "target"},
	} {
		if err := store.Create(inst); err != nil {
			t.Fatalf("create instance %s: %v", inst.ID, err)
		}
	}
	toolServer := NewToolServer(nil, nil, instance.NewRegistry(store), nil)
	callCtx := CallContext{UserID: 1, Role: auth.RoleAdmin, InstanceID: targetInstanceID}

	searchInput := json.RawMessage(`{"media_type":"movie","tmdb_id":42}`)
	searchResult, err := toolServer.ExecuteTool(context.Background(), "search_releases", searchInput, callCtx)
	if err != nil {
		t.Fatalf("search_releases: %v", err)
	}
	if len(searchResult.ReleaseCandidates) != 1 {
		t.Fatalf("release candidates = %+v", searchResult.ReleaseCandidates)
	}
	reference := releaseGUIDReference(rawGUID)
	if searchResult.ReleaseCandidates[0].Reference != reference || !strings.Contains(searchResult.Text, reference) {
		t.Fatalf("search result did not contain canonical reference %q: %+v\n%s", reference, searchResult.ReleaseCandidates, searchResult.Text)
	}
	candidateJSON, err := json.Marshal(searchResult.ReleaseCandidates)
	if err != nil {
		t.Fatalf("encode safe candidates: %v", err)
	}
	combined := searchResult.Text + string(candidateJSON)
	for _, sentinel := range []string{rawGUID, "opaque-signed-path-sentinel", "capability-sentinel"} {
		if strings.Contains(combined, sentinel) {
			t.Fatalf("search ToolResult leaked release capability sentinel %q: %s", sentinel, combined)
		}
	}

	grabInput, err := json.Marshal(map[string]any{
		"media_type": "movie", "guid": reference, "indexer_id": 9, "tmdb_id": 42,
	})
	if err != nil {
		t.Fatalf("encode grab input: %v", err)
	}
	if _, err := toolServer.ExecuteTool(context.Background(), "grab_release", grabInput, callCtx); err != nil {
		t.Fatalf("grab_release: %v", err)
	}
	if got := <-grabbedGUID; got != rawGUID {
		t.Fatalf("dispatched guid = %q, want fresh raw capability", got)
	}
	if got := targetSearches.Load(); got != 2 {
		t.Fatalf("target release searches = %d, want initial + fresh pre-dispatch search", got)
	}
	if got := targetGrabs.Load(); got != 1 {
		t.Fatalf("target grabs = %d, want 1", got)
	}
	if got := decoyRequests.Load(); got != 0 {
		t.Fatalf("default/decoy instance received %d request(s)", got)
	}
}

func TestResolveFreshReleaseGUIDFailsClosed(t *testing.T) {
	const raw = "opaque-capability"
	reference := releaseGUIDReference(raw)

	for name, input := range map[string]struct {
		reference string
		indexerID int
		releases  []releaseCapability
	}{
		"raw reference":    {reference: raw, indexerID: 7, releases: []releaseCapability{{guid: raw, indexerID: 7}}},
		"uppercase digest": {reference: strings.ToUpper(reference), indexerID: 7, releases: []releaseCapability{{guid: raw, indexerID: 7}}},
		"wrong indexer":    {reference: reference, indexerID: 8, releases: []releaseCapability{{guid: raw, indexerID: 7}}},
		"wrong scope":      {reference: reference, indexerID: 7, releases: []releaseCapability{{guid: "other", indexerID: 7}}},
		"ambiguous":        {reference: reference, indexerID: 7, releases: []releaseCapability{{guid: raw, indexerID: 7}, {guid: raw, indexerID: 7}}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := resolveFreshReleaseGUID(input.reference, input.indexerID, input.releases); err == nil {
				t.Fatal("unsafe release selector was accepted")
			}
		})
	}

	got, err := resolveFreshReleaseGUID(reference, 7, []releaseCapability{{guid: raw, indexerID: 7}})
	if err != nil || got != raw {
		t.Fatalf("unique fresh resolution = %q, %v", got, err)
	}
}

func TestSanitizeToolResultCanonicalizesRawReleaseCandidateReference(t *testing.T) {
	const raw = "opaque/signed-path-capability-sentinel"
	result := &ToolResult{
		Text: "untrusted tool text repeated " + raw,
		ReleaseCandidates: []ReleaseCandidate{{
			Reference: raw,
			Title:     "untrusted candidate metadata repeated " + raw,
		}},
	}
	sanitizeToolResult(result)
	if got := result.ReleaseCandidates[0].Reference; got != releaseGUIDReference(raw) || strings.Contains(got, "capability-sentinel") {
		t.Fatalf("candidate boundary reference = %q", got)
	}
	if combined := result.Text + result.ReleaseCandidates[0].Title; strings.Contains(combined, raw) || strings.Contains(combined, "capability-sentinel") {
		t.Fatalf("ToolResult boundary leaked release capability: %s", combined)
	}
}
