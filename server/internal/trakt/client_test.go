package trakt

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

// testClient returns a Client pointed at a fake Trakt server.
func testClient(t *testing.T, handler http.Handler) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c := NewClient("trakt-client-id")
	c.baseURL = srv.URL
	return c
}

// requireTraktHeaders asserts the API headers Trakt demands on every call,
// including the client id key.
func requireTraktHeaders(t *testing.T, r *http.Request) {
	t.Helper()
	if got := r.Header.Get("trakt-api-key"); got != "trakt-client-id" {
		t.Errorf("trakt-api-key = %q, want the client id", got)
	}
	if got := r.Header.Get("trakt-api-version"); got != "2" {
		t.Errorf("trakt-api-version = %q, want 2", got)
	}
	if got := r.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
}

func TestSearchByTMDBSendsHeadersAndParsesShow(t *testing.T) {
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requireTraktHeaders(t, r)
		if r.URL.Path != "/search/tmdb/1399" {
			t.Errorf("path = %q, want /search/tmdb/1399", r.URL.Path)
		}
		if got := r.URL.Query().Get("type"); got != "show" {
			t.Errorf("type = %q, want show", got)
		}
		_, _ = w.Write([]byte(`[{"show":{"ids":{"tvdb":121361,"imdb":"tt0944947"}}}]`))
	}))

	got, err := c.SearchByTMDB(1399, "show")
	if err != nil {
		t.Fatalf("SearchByTMDB: %v", err)
	}
	if got.TVDBID != 121361 || got.IMDBID != "tt0944947" {
		t.Fatalf("result = %+v", got)
	}
}

func TestSearchByTMDBNoUsableResult(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"empty result set", `[]`},
		{"first result is not a show", `[{"movie":{"ids":{"tmdb":603}}}]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(tc.body))
			}))
			_, err := c.SearchByTMDB(603, "show")
			if err == nil {
				t.Fatal("SearchByTMDB accepted a result set without a show")
			}
			if !strings.Contains(err.Error(), "no results found") {
				t.Fatalf("error = %v, want no-results message", err)
			}
		})
	}
}

func TestSearchByTMDBNon200WithoutEchoingClientID(t *testing.T) {
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "slow down", http.StatusTooManyRequests)
	}))

	_, err := c.SearchByTMDB(1399, "show")
	if err == nil {
		t.Fatal("SearchByTMDB accepted a 429 response")
	}
	if !strings.Contains(err.Error(), "trakt API returned status 429") {
		t.Fatalf("error = %v, want the upstream status surfaced", err)
	}
	if strings.Contains(err.Error(), "trakt-client-id") {
		t.Fatalf("error echoed the client id: %v", err)
	}
}

func TestDoGetRawPassthrough(t *testing.T) {
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requireTraktHeaders(t, r)
		if r.URL.Path != "/movies/trending" {
			t.Errorf("path = %q, want /movies/trending", r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("page") != "2" || q.Get("limit") != "10" {
			t.Errorf("query = %q, want page=2 and limit=10", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`[{"watchers":42}]`))
	}))

	body, err := c.DoGetRaw("/movies/trending", url.Values{"page": []string{"2"}, "limit": []string{"10"}})
	if err != nil {
		t.Fatalf("DoGetRaw: %v", err)
	}
	if string(body) != `[{"watchers":42}]` {
		t.Fatalf("body = %s, want the raw upstream JSON", body)
	}
}

func TestDoGetRawNon200WithoutEchoingClientID(t *testing.T) {
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "broken", http.StatusInternalServerError)
	}))

	_, err := c.DoGetRaw("/movies/trending", nil)
	if err == nil {
		t.Fatal("DoGetRaw accepted a 500 response")
	}
	if !strings.Contains(err.Error(), "trakt API returned status 500") {
		t.Fatalf("error = %v, want the upstream status surfaced", err)
	}
	if strings.Contains(err.Error(), "trakt-client-id") {
		t.Fatalf("error echoed the client id: %v", err)
	}
}

func TestClientDoesNotFollowRedirects(t *testing.T) {
	var redirectedRequests atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirectedRequests.Add(1)
		if got := r.Header.Get("trakt-api-key"); got != "" {
			t.Errorf("redirect destination received trakt-api-key %q", got)
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(destination.Close)

	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL+"/credential-sink", http.StatusTemporaryRedirect)
	}))

	if _, err := c.SearchByTMDB(1399, "show"); err == nil {
		t.Fatal("SearchByTMDB accepted an upstream redirect")
	}
	if _, err := c.DoGetRaw("/movies/trending", nil); err == nil {
		t.Fatal("DoGetRaw accepted an upstream redirect")
	}
	if got := redirectedRequests.Load(); got != 0 {
		t.Fatalf("redirect destination received %d requests, want 0", got)
	}
}
