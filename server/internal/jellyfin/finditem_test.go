package jellyfin

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/mediaserver"
)

// fakeLibrary is a Jellyfin that answers /Items with whatever it was given
// and records the query it was asked, plus /System/Info for the server id.
type fakeLibrary struct {
	t       *testing.T
	items   []map[string]any
	status  int
	echo    bool // write the request back in the error body
	queries []url.Values
}

func (f *fakeLibrary) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/System/Info", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(f.t, w, map[string]any{"Id": "server-1", "ServerName": "Home", "Version": "10.11.11"})
	})
	mux.HandleFunc("/Items", func(w http.ResponseWriter, r *http.Request) {
		f.queries = append(f.queries, r.URL.Query())
		if f.status != 0 {
			w.WriteHeader(f.status)
			if f.echo {
				_, _ = io.WriteString(w, "no: "+r.URL.String()+" "+r.Header.Get("X-Emby-Token")+" "+r.Host)
			}
			return
		}
		writeJSON(f.t, w, map[string]any{"Items": f.items, "TotalRecordCount": len(f.items)})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		f.t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusTeapot)
	})
	return mux
}

func item(id string, ids map[string]any) map[string]any {
	return map[string]any{"Id": id, "Name": "whatever", "Type": "Movie", "ProviderIds": ids}
}

func TestFindItemMatchesByProviderIDInsideTheYearWindow(t *testing.T) {
	lib := &fakeLibrary{t: t, items: []map[string]any{
		item("decoy-1", map[string]any{"Tmdb": "1", "Imdb": "tt1"}),
		item("match-1", map[string]any{"Tmdb": "10378", "Imdb": "tt1254207"}),
		item("decoy-2", map[string]any{"Tmdb": "103780"}),
	}}
	c := testClient(t, lib.handler())

	got, err := c.FindItem(context.Background(), "user-1", mediaserver.ItemQuery{MediaType: "movie", TMDBID: 10378, Year: 2008, Title: "Big Buck Bunny"})
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "match-1" || got.WebPath != "/web/#/details?id=match-1&serverId=server-1" {
		t.Fatalf("item = %+v, want match-1 with the web details path", got)
	}
	if len(lib.queries) != 1 {
		t.Fatalf("queries = %d, want one lookup", len(lib.queries))
	}
	q := lib.queries[0]
	for key, want := range map[string]string{
		"userId": "user-1", "recursive": "true", "includeItemTypes": "Movie", "fields": "ProviderIds",
		"years": "2007,2008,2009", "enableImages": "false", "enableUserData": "false",
	} {
		if q.Get(key) != want {
			t.Errorf("query %s = %q, want %q", key, q.Get(key), want)
		}
	}
	if q.Has("searchTerm") {
		t.Error("a known year narrows by year, not by title")
	}
}

func TestFindItemSeriesMatchesTVDBOrTMDBAndFallsBackToTheTitle(t *testing.T) {
	lib := &fakeLibrary{t: t, items: []map[string]any{
		item("by-tvdb", map[string]any{"Tvdb": "81189", "Tmdb": "999"}),
	}}
	c := testClient(t, lib.handler())

	got, err := c.FindItem(context.Background(), "user-1", mediaserver.ItemQuery{MediaType: "tv", TMDBID: 1396, TVDBID: 81189, Title: "Breaking Bad"})
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "by-tvdb" {
		t.Fatalf("item = %+v, want the series matched by its TVDB id", got)
	}
	q := lib.queries[0]
	if q.Get("includeItemTypes") != "Series" || q.Get("searchTerm") != "Breaking Bad" || q.Has("years") {
		t.Fatalf("query = %v, want Series narrowed by the title when no year is known", q)
	}

	// A series with only the TMDB id matching is still the one.
	lib.items = []map[string]any{item("by-tmdb", map[string]any{"Tmdb": "1396"})}
	got, err = c.FindItem(context.Background(), "user-1", mediaserver.ItemQuery{MediaType: "tv", TMDBID: 1396, TVDBID: 81189, Year: 2008})
	if err != nil || got.ID != "by-tmdb" {
		t.Fatalf("item = %+v, err = %v, want the series matched by its TMDB id", got, err)
	}
	// A movie never matches on a TVDB id.
	lib.items = []map[string]any{item("tv-only", map[string]any{"Tvdb": "81189"})}
	if _, err := c.FindItem(context.Background(), "user-1", mediaserver.ItemQuery{MediaType: "movie", TMDBID: 1396, TVDBID: 81189, Year: 2008}); !errors.Is(err, mediaserver.ErrItemNotFound) {
		t.Fatalf("movie with only a tvdb match: err = %v, want ErrItemNotFound", err)
	}
}

func TestFindItemAbsenceAndRefusals(t *testing.T) {
	lib := &fakeLibrary{t: t, items: []map[string]any{item("other", map[string]any{"Tmdb": "1"})}}
	c := testClient(t, lib.handler())
	q := mediaserver.ItemQuery{MediaType: "movie", TMDBID: 10378, Year: 2008}

	if _, err := c.FindItem(context.Background(), "user-1", q); !errors.Is(err, mediaserver.ErrItemNotFound) {
		t.Fatalf("no match: err = %v, want ErrItemNotFound", err)
	}
	if _, err := c.FindItem(context.Background(), "user-1", mediaserver.ItemQuery{MediaType: "book", TMDBID: 1, Year: 2008}); err == nil || errors.Is(err, mediaserver.ErrItemNotFound) {
		t.Fatalf("unsupported type: err = %v, want a refusal that is not absence", err)
	}
	if _, err := c.FindItem(context.Background(), "user-1", mediaserver.ItemQuery{MediaType: "movie", Year: 2008}); err == nil || errors.Is(err, mediaserver.ErrItemNotFound) {
		t.Fatalf("no provider id: err = %v, want a refusal that is not absence", err)
	}
	if _, err := c.FindItem(context.Background(), "user-1", mediaserver.ItemQuery{MediaType: "movie", TMDBID: 1}); err == nil || errors.Is(err, mediaserver.ErrItemNotFound) {
		t.Fatalf("nothing to narrow by: err = %v, want a refusal that is not absence", err)
	}
	if n := len(lib.queries); n != 1 {
		t.Fatalf("queries = %d, want the refusals answered before any request", n)
	}

	// A server error is neither absence nor a leak.
	lib.status, lib.echo = http.StatusInternalServerError, true
	_, err := c.FindItem(context.Background(), "user-1", q)
	if err == nil || errors.Is(err, mediaserver.ErrItemNotFound) {
		t.Fatalf("server error: err = %v, want an error that is not absence", err)
	}
	if msg := err.Error(); strings.Contains(msg, testAPIKey) || strings.Contains(msg, "127.0.0.1") || strings.Contains(msg, "user-1") {
		t.Fatalf("error leaks the key, host, or query: %q", msg)
	}
}
