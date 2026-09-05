package mediaaccess

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/cache"
	"github.com/windoze95/cantinarr-server/internal/contentpolicy"
	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/mediaserver"
)

type watchMetadataGetter func(string, url.Values) ([]byte, error)

func (f watchMetadataGetter) DoGetRaw(path string, params url.Values) ([]byte, error) {
	return f(path, params)
}

func TestWatchContentPolicy(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
	}{
		{"allowed movie", 200}, {"allowed TV canonical identifiers", 200},
		{"adult", 404}, {"blocked genre", 404}, {"high rating", 404}, {"unrated", 404},
		{"TVDB only", 404}, {"metadata unavailable", 503}, {"ratings unavailable", 503},
		{"malformed metadata", 503}, {"wrong metadata ID", 503}, {"unrestricted", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			user := e.user("viewer")
			id := e.mediaServer("plex", "Plex", instance.MediaServerConfig{PublicAddress: instance.PlexPublicAddress})
			e.grantType(user, "plex", id)
			if _, err := e.svc.insertAccount(accountRow{UserID: user, InstanceID: id, RemoteUserID: "viewer@example.com"}, false); err != nil {
				t.Fatal(err)
			}
			p := &inviteFinderProvider{&finderProvider{fakeProvider: newFakeProvider()}}
			p.find = func(_ string, q mediaserver.ItemQuery) (mediaserver.Item, error) {
				if tc.name != "unrestricted" && (q.Title != "Canonical title" || q.Year != 2008) {
					t.Errorf("trusted client metadata: %+v", q)
				}
				if q.MediaType == "tv" && q.TVDBID != 81189 {
					t.Errorf("trusted forged TVDB id: %+v", q)
				}
				return mediaserver.Item{ID: "1", WebPath: "/desktop/#!/server/m1/details?key=x"}, nil
			}
			e.providers[id] = p
			getter := watchMetadataGetter(func(path string, params url.Values) ([]byte, error) {
				if tc.name == "unrestricted" {
					t.Error("unrestricted account fetched policy metadata")
				}
				switch {
				case strings.HasPrefix(path, "/certification/"), strings.HasPrefix(path, "/genre/"):
					return nil, errors.New("use builtins")
				case strings.HasSuffix(path, "/release_dates"), strings.HasSuffix(path, "/content_ratings"):
					if tc.name == "ratings unavailable" {
						return nil, errors.New("SECRET")
					}
					if tc.name == "unrated" {
						return []byte(`{"results":[]}`), nil
					}
					if strings.Contains(path, "content_ratings") {
						return []byte(`{"results":[{"iso_3166_1":"US","rating":"TV-PG"}]}`), nil
					}
					rating := "PG"
					if tc.name == "high rating" {
						rating = "R"
					}
					return []byte(`{"results":[{"iso_3166_1":"US","release_dates":[{"certification":"` + rating + `","type":3}]}]}`), nil
				default:
					if tc.name == "metadata unavailable" {
						return nil, errors.New("SECRET")
					}
					if tc.name == "malformed metadata" {
						return []byte(`{"id":1}`), nil
					}
					detail := map[string]any{"id": 1, "adult": tc.name == "adult", "genres": []any{}, "title": "Canonical title", "name": "Canonical title", "release_date": "2008-01-01", "first_air_date": "2008-01-01", "external_ids": map[string]int{"tvdb_id": 81189}}
					if tc.name == "blocked genre" {
						detail["genres"] = []any{map[string]int{"id": 27}}
					}
					if tc.name == "wrong metadata ID" {
						detail["id"] = 2
					}
					return json.Marshal(detail)
				}
			})
			c := cache.New()
			defer c.Close()
			source := func() contentpolicy.RawGetter { return getter }
			policies := contentpolicy.New(e.db, source, c)
			if tc.name != "unrestricted" {
				if err := policies.Store.Set(user, contentpolicy.Policy{RatingRegion: "US", MaxMovieRating: "PG", MaxTVRating: "TV-PG", BlockUnrated: true, BlockedMovieGenres: []int{27}}); err != nil {
					t.Fatal(err)
				}
			}
			h := NewHandler(e.svc, nil)
			h.SetWatchContentPolicy(policies, source)
			query := "media_type=movie&tmdb_id=1&tvdb_id=999&year=1999&title=Forged"
			if tc.name == "allowed TV canonical identifiers" {
				query = strings.Replace(query, "media_type=movie", "media_type=tv", 1)
			}
			if tc.name == "TVDB only" {
				query = "media_type=tv&tvdb_id=999"
			}
			rec := serve(http.HandlerFunc(h.Watch), "GET", "/api/media-servers/watch?"+query, user, "")
			if rec.Code != tc.status || rec.Header().Get("Cache-Control") != "no-store" || strings.Contains(rec.Body.String(), "SECRET") {
				t.Fatalf("watch = %d %s", rec.Code, rec.Body.String())
			}
			if tc.status != 200 && p.calls.Load() != 0 {
				t.Fatal("blocked title reached Plex")
			}
			if tc.status == 200 && p.calls.Load() != 1 {
				t.Fatal("allowed title did not reach Plex")
			}
		})
	}
}
