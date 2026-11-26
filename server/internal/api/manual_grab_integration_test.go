package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/instance"
)

// Use the authenticated route and the same two fields as the app. The arr's
// cache lookup requires the original GUID spelling and indexer ID together.
func TestManualGrabThroughInstanceProxy(t *testing.T) {
	type release struct {
		GUID      string `json:"guid"`
		IndexerID int    `json:"indexerId"`
	}
	const key = "test-indexer-credential"
	rows := []release{
		{"plain-release-id", 3},
		{"https://indexer.invalid/get?id=42&apikey=" + key, 7},
		{"https://indexer.invalid/get?id=42&apikey=" + key + "-other", 7},
	}
	harness := newRBACRouterHarness(t, false)
	store := instance.NewStore(harness.database, harness.cipher)
	for _, tc := range []struct{ service, query string }{
		{"radarr", "movieId=42"},
		{"sonarr", "seriesId=42&seasonNumber=10"},
	} {
		t.Run(tc.service, func(t *testing.T) {
			var searches, grabs atomic.Int32
			var lastGrab atomic.Pointer[release]
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path != "/base/api/v3/release" {
					http.NotFound(w, r)
					return
				}
				switch r.Method {
				case http.MethodGet:
					searches.Add(1)
					if r.URL.RawQuery != tc.query {
						t.Errorf("search query = %q, want %q", r.URL.RawQuery, tc.query)
					}
					_ = json.NewEncoder(w).Encode(rows)
				case http.MethodPost:
					grabs.Add(1)
					var got release
					if json.NewDecoder(r.Body).Decode(&got) != nil || !slices.Contains(rows, got) {
						w.WriteHeader(http.StatusNotFound)
						_, _ = io.WriteString(w, `{"message":"Couldn't find requested release in cache, try searching again"}`)
						return
					}
					lastGrab.Store(&got)
					// The real arrs echo the submitted release on success.
					_ = json.NewEncoder(w).Encode(got)
				default:
					http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
				}
			}))
			defer upstream.Close()
			inst := &instance.Instance{ServiceType: tc.service, Name: tc.service, URL: upstream.URL + "/base", APIKey: "test-key"}
			if err := store.Create(inst); err != nil {
				t.Fatal(err)
			}
			path := "/api/instances/" + inst.ID + "/api/v3/release"
			search := serveRBACRequestWithBody(harness.router, http.MethodGet, path+"?"+tc.query, harness.adminToken, "")
			if search.Code != http.StatusOK || strings.Contains(search.Body.String(), key) {
				t.Fatalf("search status %d or exposed credential", search.Code)
			}
			var results []release
			if err := json.Unmarshal(search.Body.Bytes(), &results); err != nil || len(results) != len(rows) {
				t.Fatalf("search returned %d releases, err %v", len(results), err)
			}
			if results[0] != rows[0] {
				t.Fatal("ordinary release changed on its way to the app")
			}
			for i, selected := range results {
				body, err := json.Marshal(selected)
				if err != nil {
					t.Fatal(err)
				}
				grab := serveRBACRequestWithBody(harness.router, http.MethodPost, path, harness.adminToken, string(body))
				if strings.Contains(grab.Body.String(), key) {
					t.Fatal("grab response exposed the credential")
				}
				if grab.Code != http.StatusOK {
					t.Fatalf("grab result %d: HTTP %d: %s", i, grab.Code, grab.Body.String())
				}
				if got := lastGrab.Load(); got == nil || *got != rows[i] {
					t.Fatalf("grab result %d did not forward the selected GUID and indexer", i)
				}
			}
			// Even two instance records pointing to the same host cannot
			// exchange references. Neither mismatch may send another grab.
			sibling := &instance.Instance{ServiceType: tc.service, Name: "Other", URL: inst.URL, APIKey: inst.APIKey}
			if err := store.Create(sibling); err != nil {
				t.Fatal(err)
			}
			for _, mismatch := range []struct {
				instanceID string
				indexerID  int
			}{{sibling.ID, results[1].IndexerID}, {inst.ID, results[1].IndexerID + 1}} {
				body, err := json.Marshal(release{results[1].GUID, mismatch.indexerID})
				if err != nil {
					t.Fatal(err)
				}
				path := "/api/instances/" + mismatch.instanceID + "/api/v3/release"
				grab := serveRBACRequestWithBody(harness.router, http.MethodPost, path, harness.adminToken, string(body))
				if grab.Code != http.StatusGone || !strings.Contains(grab.Body.String(), "Search again") {
					t.Fatalf("mismatched grab: HTTP %d; want 410 with search guidance", grab.Code)
				}
			}
			if searches.Load() != 1 || int(grabs.Load()) != len(rows) {
				t.Errorf("got %d searches and %d grabs for %d selections", searches.Load(), grabs.Load(), len(rows))
			}
		})
	}
}
