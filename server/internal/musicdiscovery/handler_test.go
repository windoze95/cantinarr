package musicdiscovery

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/contentpolicy"
	"github.com/windoze95/cantinarr-server/internal/db"
	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/secrets"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type accessEnv struct {
	h           *Handler
	router      http.Handler
	db          *sql.DB
	a, b, wrong string
	hits        atomic.Int32
}

func newAccessEnv(t *testing.T) *accessEnv {
	t.Helper()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	for _, user := range []struct{ name, role string }{{"requester", "user"}, {"ungranted", "user"}, {"kid", "user"}, {"ungranted-kid", "user"}, {"admin", "admin"}} {
		if _, err := database.Exec("INSERT INTO users(username,password_hash,role) VALUES (?,'',?)", user.name, user.role); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []int64{3, 4} {
		if err := contentpolicy.NewStore(database).Set(id, contentpolicy.Policy{MaxMovieRating: "G", MaxTVRating: "TV-Y", RatingRegion: "US", BlockUnrated: true}); err != nil {
			t.Fatal(err)
		}
	}
	cipher, err := secrets.NewCipher(bytes.Repeat([]byte{42}, 32))
	if err != nil {
		t.Fatal(err)
	}
	store := instance.NewStore(database, cipher)
	create := func(kind, name string) string {
		inst := &instance.Instance{ServiceType: kind, Name: name, URL: "http://not-contacted.invalid", APIKey: "test"}
		if err := store.Create(inst); err != nil {
			t.Fatal(err)
		}
		return inst.ID
	}
	e := &accessEnv{db: database, a: create("lidarr", "A"), b: create("lidarr", "B"), wrong: create("radarr", "Movies")}
	for _, user := range []int64{1, 3} {
		if err := store.SetUserGrants(user, map[string][]string{"lidarr": {e.a}}); err != nil {
			t.Fatal(err)
		}
	}
	e.h = NewHandler(store)
	e.h.service = testService(t, func(w http.ResponseWriter, r *http.Request) {
		e.hits.Add(1)
		if strings.Contains(r.URL.Path, "fresh-releases") {
			jsonResponse(w, map[string]any{"payload": map[string]any{"releases": []freshRelease{{aID, "Album", "Artist", "2026-09-05", "Album"}}}})
		} else if r.URL.Path == "/artist/" {
			jsonResponse(w, map[string]any{"count": 1, "offset": 0, "artists": []map[string]string{{"id": aID, "name": "Artist"}}})
		} else if strings.Contains(r.URL.Path, "/artist/") {
			jsonResponse(w, map[string]string{"id": aID, "name": "Artist"})
		} else if r.URL.Query().Get("artist") != "" {
			jsonResponse(w, map[string]any{"release-group-count": 1, "release-group-offset": 0, "release-groups": groups(aID)})
		} else if r.URL.Query().Get("query") != "" {
			jsonResponse(w, map[string]any{"count": 1, "offset": 0, "release-groups": groups(aID)})
		} else {
			jsonResponse(w, groups(aID)[0])
		}
	})
	e.h.service.(*Service).art.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		e.hits.Add(1)
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader([]byte{137, 80, 78, 71, 13, 10, 26, 10})), Request: r}, nil
	})
	r := chi.NewRouter()
	r.Get("/api/discover/music/search", e.h.Search)
	r.Get("/api/discover/music/artists", e.h.Artists)
	r.Get("/api/media/music/artists/{mbid}", e.h.Artist)
	r.Get("/api/media/music/artists/{mbid}/albums", e.h.ArtistAlbums)
	r.Get("/api/discover/music/{feed}", e.h.Feed)
	r.Get("/api/discover/music/artwork/{mbid}", e.h.Artwork)
	r.Get("/api/media/music/{mbid}", e.h.Album)
	r.Get("/api/genres/music", e.h.Genres)
	e.router = r
	return e
}

func (e *accessEnv) get(user int64, role, path, inst string) *httptest.ResponseRecorder {
	if inst != "" {
		sep := "?"
		if strings.Contains(path, "?") {
			sep = "&"
		}
		path += sep + "instance_id=" + inst
	}
	req := httptest.NewRequest("GET", path, nil)
	if user > 0 {
		req = req.WithContext(context.WithValue(req.Context(), auth.ClaimsKey, &auth.Claims{UserID: user, Role: role}))
	}
	w := httptest.NewRecorder()
	e.router.ServeHTTP(w, req)
	return w
}

func TestEveryMusicEndpointChecksGrantsBeforeAndAfterWarmCache(t *testing.T) {
	e := newAccessEnv(t)
	paths := []string{"/api/discover/music/search?query=album&include_singles=true", "/api/discover/music/artists?query=artist", "/api/media/music/artists/" + aID, "/api/media/music/artists/" + aID + "/albums", "/api/discover/music/new-releases", "/api/media/music/" + aID, "/api/genres/music", "/api/discover/music/artwork/" + aID}
	for _, path := range paths {
		for _, tc := range []struct {
			user       int64
			role, inst string
			status     int
		}{
			{1, "user", e.a, 200}, {3, "user", e.a, 200}, {1, "user", "", 200},
			{2, "user", e.a, 403}, {4, "user", e.a, 403}, {2, "user", "", 403},
			{1, "user", e.b, 403}, {1, "user", e.wrong, 403}, {0, "", "", 401},
			{1, "unknown", e.a, 403}, {5, "admin", e.b, 200}, {5, "admin", "", 200},
			{5, "admin", e.wrong, 403}, {5, "admin", "missing", 403},
		} {
			w := e.get(tc.user, tc.role, path, tc.inst)
			if w.Code != tc.status {
				t.Fatalf("%s user %d instance %s: %d %s", path, tc.user, tc.inst, w.Code, w.Body)
			}
			if w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("private data can be shared cached")
			}
		}
	}
	// Metadata was shared between authorized accounts/instances; access wasn't.
	if e.hits.Load() != 7 {
		t.Fatalf("metadata/artwork cache wasn't shared: %d", e.hits.Load())
	}
	if err := e.h.store.SetUserGrants(1, map[string][]string{"lidarr": nil}); err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		if w := e.get(1, "user", path, e.a); w.Code != 403 {
			t.Fatalf("warm cache bypass after revoke: %s %d", path, w.Code)
		}
		if w := e.get(3, "user", path, e.a); w.Code != 200 {
			t.Fatalf("other user's grant lost: %s %d", path, w.Code)
		}
	}
	if e.hits.Load() != 7 {
		t.Fatal("denied caller reached provider")
	}
}

func TestGrantRevocationWhileProviderIsLoading(t *testing.T) {
	e := newAccessEnv(t)
	started, release := make(chan struct{}), make(chan struct{})
	e.h.service = testService(t, func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		jsonResponse(w, groups(aID)[0])
	})
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- e.get(1, "user", "/api/media/music/"+aID, e.a) }()
	<-started
	if err := e.h.store.SetUserGrants(1, map[string][]string{"lidarr": nil}); err != nil {
		t.Fatal(err)
	}
	close(release)
	if w := <-done; w.Code != 403 || strings.Contains(w.Body.String(), "Same title") {
		t.Fatalf("revoked grant received data: %d %s", w.Code, w.Body)
	}
}

func TestMusicInputValidationDoesNotCallProviders(t *testing.T) {
	e := newAccessEnv(t)
	for _, path := range []string{
		"/api/discover/music/popular?page=0", "/api/discover/music/popular?page=999999999999999999999",
		"/api/discover/music/popular?period=all_time", "/api/discover/music/genre?genre=rock%22%20OR%20*",
		"/api/discover/music/new-releases?genre=rock", "/api/media/music/not-a-mbid",
		"/api/discover/music/artwork/https:evil.example", "/api/discover/music/unknown",
	} {
		if w := e.get(1, "user", path, e.a); w.Code != 400 && w.Code != 404 {
			t.Fatalf("%s: %d", path, w.Code)
		}
	}
	if e.hits.Load() != 0 {
		t.Fatal("invalid query reached upstream")
	}
}

func TestArtworkRedirectBoundaryAndRasterValidation(t *testing.T) {
	for _, tc := range []struct {
		target  string
		allowed bool
	}{
		{"https://archive.org/download/cover/cover-500.jpg", true},
		{"https://ia801.example.archive.org/0/items/cover.jpg", true},
		{"https://coverartarchive.org/release-group/" + aID + "/front", true},
		{"https://archive.org.evil.example/art.jpg", false},
		{"https://evil.example/art.jpg", false},
		{"http://archive.org/art.jpg", false},
		{"https://archive.org:8080/art.jpg", false},
		{"https://user:password@archive.org/art.jpg", false},
		{"https://127.0.0.1/art.jpg", false},
	} {
		t.Run(tc.target, func(t *testing.T) {
			req := httptest.NewRequest("GET", tc.target, nil)
			if got := artworkRedirect(req, nil) == nil; got != tc.allowed {
				t.Fatalf("allowed %v", got)
			}
		})
	}
	s := NewService()
	var hits atomic.Int32
	s.art.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		hits.Add(1)
		// A real http.Client redirect must be stopped before the next hop.
		return &http.Response{StatusCode: 302, Header: http.Header{"Location": {"https://evil.example/private"}}, Body: http.NoBody, Request: r}, nil
	})
	if _, err := s.Artwork(context.Background(), aID); err == nil || hits.Load() != 1 {
		t.Fatalf("redirect escaped: %v hits %d", err, hits.Load())
	}
	s.art.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("<svg onload='bad()'/>")), Request: r}, nil
	})
	if _, err := s.Artwork(context.Background(), aID); err == nil {
		t.Fatal("accepted executable artwork")
	}
	s.art.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		hits.Add(1)
		return &http.Response{StatusCode: 404, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("missing")), Request: r}, nil
	})
	for range 2 {
		if b, err := s.Artwork(context.Background(), bID); err != nil || len(b) != 0 {
			t.Fatalf("%s %v", b, err)
		}
	}
	if hits.Load() != 2 {
		t.Fatal(fmt.Sprint("missing artwork not cached: ", hits.Load()))
	}
}

func TestAdminCatalogAndArtworkBeforeLidarrSetup(t *testing.T) {
	e := newAccessEnv(t)
	for _, id := range []string{e.a, e.b} {
		if err := e.h.store.Delete(id); err != nil {
			t.Fatal(err)
		}
	}
	paths := []string{"/api/discover/music/search?query=album&include_singles=true", "/api/discover/music/artists?query=artist", "/api/media/music/artists/" + aID, "/api/media/music/artists/" + aID + "/albums", "/api/discover/music/new-releases", "/api/genres/music", "/api/media/music/" + aID, "/api/discover/music/artwork/" + aID}
	for _, allAbsent := range []bool{false, true} {
		if allAbsent {
			if err := e.h.store.Delete(e.wrong); err != nil {
				t.Fatal(err)
			}
		}
		for _, path := range paths {
			if w := e.get(5, "admin", path, ""); w.Code != 200 {
				t.Fatalf("admin %s: %d %s", path, w.Code, w.Body)
			}
			before := e.hits.Load()
			for _, tc := range []struct {
				user     int64
				role, id string
				status   int
			}{
				{0, "", "", 401}, {2, "user", "", 403}, {4, "user", "", 403},
				{5, "user", "", 403}, {5, "admin", e.a, 403}, {5, "admin", e.wrong, 403},
			} {
				if w := e.get(tc.user, tc.role, path, tc.id); w.Code != tc.status {
					t.Fatalf("%s: %d %s", path, w.Code, w.Body)
				}
			}
			if e.hits.Load() != before {
				t.Fatal("denied access reached metadata/artwork provider")
			}
		}
	}
}
