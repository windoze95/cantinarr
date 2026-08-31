package emby

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

// fakeLibrary is an Emby that answers a user's /Items with whatever it was
// given and records the query it was asked, plus /System/Info.
type fakeLibrary struct {
	t       *testing.T
	items   []map[string]any
	status  int
	echo    bool
	paths   []string
	queries []url.Values
}

func (f *fakeLibrary) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/System/Info", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(f.t, w, map[string]any{"Id": "server-1", "ServerName": "Den", "Version": "4.9.5.0"})
	})
	mux.HandleFunc("/Users/", func(w http.ResponseWriter, r *http.Request) {
		f.paths = append(f.paths, r.URL.Path)
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

func item(id any, ids map[string]any) map[string]any {
	return map[string]any{"Id": id, "Name": "whatever", "ProviderIds": ids}
}

func TestFindItemAsksAsTheUserByProviderIDAndConfirmsTheAnswer(t *testing.T) {
	lib := &fakeLibrary{t: t, items: []map[string]any{
		item(41, map[string]any{"Tmdb": "1"}), // a server that ignored the filter
		item(42, map[string]any{"Tmdb": "10378", "Imdb": "tt1254207"}),
	}}
	c := testClient(t, lib.handler())

	got, err := c.FindItem(context.Background(), "u-1", mediaserver.ItemQuery{MediaType: "movie", TMDBID: 10378, Year: 2008})
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "42" || got.WebPath != "/web/index.html#!/item?id=42&serverId=server-1" {
		t.Fatalf("item = %+v, want item 42 with the web item path", got)
	}
	if len(lib.paths) != 1 || lib.paths[0] != "/Users/u-1/Items" {
		t.Fatalf("paths = %v, want the lookup made as the user", lib.paths)
	}
	q := lib.queries[0]
	for key, want := range map[string]string{
		"Recursive": "true", "IncludeItemTypes": "Movie", "Fields": "ProviderIds", "AnyProviderIdEquals": "tmdb.10378",
	} {
		if q.Get(key) != want {
			t.Errorf("query %s = %q, want %q", key, q.Get(key), want)
		}
	}

	lib.items = []map[string]any{item("s-1", map[string]any{"Tvdb": "81189"})}
	got, err = c.FindItem(context.Background(), "u-1", mediaserver.ItemQuery{MediaType: "tv", TMDBID: 1396, TVDBID: 81189})
	if err != nil || got.ID != "s-1" {
		t.Fatalf("series = %+v, err = %v, want the series by its TVDB id", got, err)
	}
	if q := lib.queries[1]; q.Get("IncludeItemTypes") != "Series" || q.Get("AnyProviderIdEquals") != "tvdb.81189,tmdb.1396" {
		t.Fatalf("series query = %v, want both ids asked for", q)
	}
}

func TestFindItemAbsenceAndRefusals(t *testing.T) {
	lib := &fakeLibrary{t: t}
	c := testClient(t, lib.handler())
	q := mediaserver.ItemQuery{MediaType: "movie", TMDBID: 10378}

	if _, err := c.FindItem(context.Background(), "u-1", q); !errors.Is(err, mediaserver.ErrItemNotFound) {
		t.Fatalf("empty answer: err = %v, want ErrItemNotFound", err)
	}
	if _, err := c.FindItem(context.Background(), "u-1", mediaserver.ItemQuery{MediaType: "book", TMDBID: 1}); err == nil || errors.Is(err, mediaserver.ErrItemNotFound) {
		t.Fatalf("unsupported type: err = %v, want a refusal that is not absence", err)
	}
	if _, err := c.FindItem(context.Background(), "u-1", mediaserver.ItemQuery{MediaType: "movie", TVDBID: 5}); err == nil || errors.Is(err, mediaserver.ErrItemNotFound) {
		t.Fatalf("a movie with only a tvdb id: err = %v, want a refusal that is not absence", err)
	}
	if n := len(lib.queries); n != 1 {
		t.Fatalf("queries = %d, want the refusals answered before any request", n)
	}

	lib.status, lib.echo = http.StatusInternalServerError, true
	_, err := c.FindItem(context.Background(), "u-1", q)
	if err == nil || errors.Is(err, mediaserver.ErrItemNotFound) {
		t.Fatalf("server error: err = %v, want an error that is not absence", err)
	}
	if msg := err.Error(); strings.Contains(msg, testAPIKey) || strings.Contains(msg, "127.0.0.1") || strings.Contains(msg, "u-1") {
		t.Fatalf("error leaks the key, host, or query: %q", msg)
	}
}
