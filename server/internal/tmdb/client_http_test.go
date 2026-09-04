package tmdb

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

// testHTTPClient returns a Client pointed at a fake TMDB server.
func testHTTPClient(t *testing.T, handler http.Handler) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c := NewClient("tmdb-secret")
	c.baseURL = srv.URL
	return c
}

func TestDoGetRawSendsAuthAndParams(t *testing.T) {
	c := testHTTPClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/discover/movie" {
			t.Errorf("path = %q, want /discover/movie", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer tmdb-secret" {
			t.Errorf("Authorization = %q, want Bearer token", got)
		}
		if got := r.Header.Get("Accept"); got != "application/json" {
			t.Errorf("Accept = %q", got)
		}
		q := r.URL.Query()
		if q.Get("language") != "en-US" {
			t.Errorf("language = %q, want en-US", q.Get("language"))
		}
		if q.Get("page") != "2" {
			t.Errorf("page = %q, want 2", q.Get("page"))
		}
		_, _ = w.Write([]byte(`{"results":[{"id":7}]}`))
	}))

	body, err := c.DoGetRaw("/discover/movie", url.Values{"page": []string{"2"}})
	if err != nil {
		t.Fatalf("DoGetRaw: %v", err)
	}
	if string(body) != `{"results":[{"id":7}]}` {
		t.Fatalf("body = %s, want the raw upstream JSON", body)
	}
}

func TestSearchMoviesEscapesQueryAndStampsMediaType(t *testing.T) {
	c := testHTTPClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search/movie" {
			t.Errorf("path = %q, want /search/movie", r.URL.Path)
		}
		if got := r.URL.Query().Get("query"); got != "the matrix & co" {
			t.Errorf("query = %q, want the raw query decoded", got)
		}
		_, _ = w.Write([]byte(`{"results":[{"id":603,"title":"The Matrix"},{"id":604,"title":"Reloaded"}]}`))
	}))

	got, err := c.SearchMovies("the matrix & co")
	if err != nil {
		t.Fatalf("SearchMovies: %v", err)
	}
	if len(got) != 2 || got[0].ID != 603 || got[0].Title != "The Matrix" {
		t.Fatalf("results = %+v", got)
	}
	for i, r := range got {
		if r.MediaType != "movie" {
			t.Fatalf("result %d media type = %q, want movie", i, r.MediaType)
		}
	}
}

func TestSearchTVStampsMediaType(t *testing.T) {
	c := testHTTPClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search/tv" {
			t.Errorf("path = %q, want /search/tv", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"results":[{"id":1399,"name":"Game of Thrones"}]}`))
	}))

	got, err := c.SearchTV("game of thrones")
	if err != nil {
		t.Fatalf("SearchTV: %v", err)
	}
	if len(got) != 1 || got[0].ID != 1399 || got[0].Name != "Game of Thrones" || got[0].MediaType != "tv" {
		t.Fatalf("results = %+v", got)
	}
}

func TestGetTrendingNormalizesTypeAndWindow(t *testing.T) {
	var path atomic.Value
	c := testHTTPClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path.Store(r.URL.Path)
		_, _ = w.Write([]byte(`{"results":[{"id":1399,"name":"Game of Thrones"}]}`))
	}))

	// "shows" normalizes to tv, and an unknown window falls back to day.
	got, err := c.GetTrending("shows", "fortnight")
	if err != nil {
		t.Fatalf("GetTrending: %v", err)
	}
	if p := path.Load(); p != "/trending/tv/day" {
		t.Fatalf("request path = %v, want /trending/tv/day", p)
	}
	if len(got) != 1 || got[0].MediaType != "tv" {
		t.Fatalf("results = %+v", got)
	}
}

func TestGetTrendingAllInterleavesBothFeeds(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/trending/movie/week", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"results":[{"id":1},{"id":2},{"id":3},{"id":4},{"id":5},{"id":6}]}`))
	})
	mux.HandleFunc("/trending/tv/week", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"results":[{"id":101},{"id":102},{"id":103},{"id":104},{"id":105},{"id":106}]}`))
	})
	c := testHTTPClient(t, mux)

	got, err := c.GetTrending("all", "week")
	if err != nil {
		t.Fatalf("GetTrending: %v", err)
	}
	if len(got) != 10 {
		t.Fatalf("expected 10 balanced results, got %d", len(got))
	}
	for i, item := range got {
		want := "movie"
		if i%2 == 1 {
			want = "tv"
		}
		if item.MediaType != want {
			t.Fatalf("result %d media type = %q, want %q", i, item.MediaType, want)
		}
	}
}

func TestGetMovieAndTVDetails(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/movie/603", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":603,"title":"The Matrix","release_date":"1999-03-31","imdb_id":"tt0133093"}`))
	})
	mux.HandleFunc("/tv/1399", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":1399,"name":"Game of Thrones","first_air_date":"2011-04-17"}`))
	})
	c := testHTTPClient(t, mux)

	movie, err := c.GetMovieDetails(603)
	if err != nil {
		t.Fatalf("GetMovieDetails: %v", err)
	}
	if movie.ID != 603 || movie.Title != "The Matrix" || movie.IMDBID != "tt0133093" {
		t.Fatalf("movie = %+v", movie)
	}

	tv, err := c.GetTVDetails(1399)
	if err != nil {
		t.Fatalf("GetTVDetails: %v", err)
	}
	if tv.ID != 1399 || tv.Name != "Game of Thrones" || tv.FirstAir != "2011-04-17" {
		t.Fatalf("tv = %+v", tv)
	}
}

func TestGetTVExternalIDs(t *testing.T) {
	c := testHTTPClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tv/1399/external_ids" {
			t.Errorf("path = %q, want /tv/1399/external_ids", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"id":1399,"imdb_id":"tt0944947","tvdb_id":121361}`))
	}))

	ids, err := c.GetTVExternalIDs(1399)
	if err != nil {
		t.Fatalf("GetTVExternalIDs: %v", err)
	}
	if ids.TVDBID == nil || *ids.TVDBID != 121361 {
		t.Fatalf("tvdb id = %v, want 121361", ids.TVDBID)
	}
	if ids.IMDBID == nil || *ids.IMDBID != "tt0944947" {
		t.Fatalf("imdb id = %v, want tt0944947", ids.IMDBID)
	}
}

func TestGetMovieCollectionSortsPartsByReleaseDate(t *testing.T) {
	c := testHTTPClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/collection/2344" {
			t.Errorf("path = %q, want /collection/2344", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"id":2344,"name":"The Matrix Collection","parts":[` +
			`{"id":605,"title":"Revolutions","release_date":"2003-11-05"},` +
			`{"id":999,"title":"Unreleased"},` +
			`{"id":603,"title":"The Matrix","release_date":"1999-03-31"}]}`))
	}))

	got, err := c.GetMovieCollection(2344)
	if err != nil {
		t.Fatalf("GetMovieCollection: %v", err)
	}
	if got.Name != "The Matrix Collection" || len(got.Parts) != 3 {
		t.Fatalf("collection = %+v", got)
	}
	// Dated parts sort ascending; undated parts sink to the end.
	if got.Parts[0].ID != 603 || got.Parts[1].ID != 605 || got.Parts[2].ID != 999 {
		t.Fatalf("part order = %d, %d, %d; want 603, 605, 999", got.Parts[0].ID, got.Parts[1].ID, got.Parts[2].ID)
	}
	if got.Parts[0].MediaType != "movie" {
		t.Fatalf("part media type = %q, want movie", got.Parts[0].MediaType)
	}
}

func TestNon200SurfacesStatusWithoutEchoingToken(t *testing.T) {
	c := testHTTPClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "upstream broke", http.StatusInternalServerError)
	}))

	calls := map[string]func() error{
		"DoGetRaw":           func() error { _, err := c.DoGetRaw("/discover/movie", nil); return err },
		"GetTVExternalIDs":   func() error { _, err := c.GetTVExternalIDs(1); return err },
		"GetMovieDetails":    func() error { _, err := c.GetMovieDetails(1); return err },
		"GetTVDetails":       func() error { _, err := c.GetTVDetails(1); return err },
		"SearchMovies":       func() error { _, err := c.SearchMovies("q"); return err },
		"SearchTV":           func() error { _, err := c.SearchTV("q"); return err },
		"GetTrending":        func() error { _, err := c.GetTrending("movie", "day"); return err },
		"GetRecommendations": func() error { _, err := c.GetRecommendations(1, "movie"); return err },
	}
	for name, call := range calls {
		err := call()
		if err == nil {
			t.Fatalf("%s accepted a 500 response", name)
		}
		if !strings.Contains(err.Error(), "TMDB API returned status 500") {
			t.Fatalf("%s error = %v, want the upstream status surfaced", name, err)
		}
		if strings.Contains(err.Error(), "tmdb-secret") {
			t.Fatalf("%s error echoed the access token: %v", name, err)
		}
	}
}

func TestClientDoesNotFollowRedirects(t *testing.T) {
	var redirectedRequests atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirectedRequests.Add(1)
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("redirect destination received Authorization %q", got)
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(destination.Close)

	c := testHTTPClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL+"/credential-sink", http.StatusTemporaryRedirect)
	}))

	if _, err := c.GetMovieDetails(603); err == nil {
		t.Fatal("GetMovieDetails accepted an upstream redirect")
	}
	if got := redirectedRequests.Load(); got != 0 {
		t.Fatalf("redirect destination received %d requests, want 0", got)
	}
}

// The browse tool's typed lookups: each decodes its TMDB shape, and Discover
// carries the page count and stamps the media type.
func TestDiscoverAndLookupsDecode(t *testing.T) {
	c := testHTTPClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/discover/tv":
			if r.URL.Query().Get("with_genres") != "35" {
				t.Errorf("discover query = %v, want with_genres=35", r.URL.Query())
			}
			_, _ = w.Write([]byte(`{"page":2,"total_pages":9,"total_results":180,"results":[{"id":1399,"name":"Game of Thrones"}]}`))
		case "/genre/movie/list":
			_, _ = w.Write([]byte(`{"genres":[{"id":28,"name":"Action"}]}`))
		case "/search/keyword":
			_, _ = w.Write([]byte(`{"results":[{"id":9715,"name":"superhero"}]}`))
		case "/search/company":
			_, _ = w.Write([]byte(`{"results":[{"id":420,"name":"Marvel Studios","origin_country":"US"}]}`))
		case "/watch/providers/movie":
			if r.URL.Query().Get("watch_region") != "GB" {
				t.Errorf("providers query = %v, want watch_region=GB", r.URL.Query())
			}
			_, _ = w.Write([]byte(`{"results":[{"provider_id":8,"provider_name":"Netflix","display_priority":0}]}`))
		case "/configuration/languages":
			_, _ = w.Write([]byte(`[{"iso_639_1":"ko","english_name":"Korean","name":"한국어"}]`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))

	page, err := c.Discover("tv", url.Values{"with_genres": {"35"}})
	if err != nil {
		t.Fatal(err)
	}
	if page.Page != 2 || page.TotalPages != 9 || page.TotalResults != 180 || page.Results[0].MediaType != "tv" || page.Results[0].Name != "Game of Thrones" {
		t.Errorf("Discover = %+v, want page 2 of 9 with the show stamped tv", page)
	}
	if _, err := c.Discover("podcast", nil); err == nil {
		t.Error("Discover accepted an unknown media type")
	}
	if genres, err := c.GenreList("movie"); err != nil || len(genres) != 1 || genres[0].Name != "Action" {
		t.Errorf("GenreList = %v, %v", genres, err)
	}
	if kw, err := c.SearchKeyword("superhero"); err != nil || kw[0].ID != 9715 {
		t.Errorf("SearchKeyword = %v, %v", kw, err)
	}
	if co, err := c.SearchCompany("marvel"); err != nil || co[0].ID != 420 || co[0].OriginCountry != "US" {
		t.Errorf("SearchCompany = %v, %v", co, err)
	}
	if pv, err := c.WatchProviders("movie", "GB"); err != nil || pv[0].ID != 8 {
		t.Errorf("WatchProviders = %v, %v", pv, err)
	}
	if langs, err := c.Languages(); err != nil || langs[0].Code != "ko" || langs[0].EnglishName != "Korean" {
		t.Errorf("Languages = %v, %v", langs, err)
	}
}
