package plex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/windoze95/cantinarr-server/internal/mediaserver"
)

type watchFixture struct {
	p             *Provider
	pms           *httptest.Server
	share         string
	ownerEmail    string
	expectToken   string
	machine       string
	sections      string
	items         string
	page          func(*http.Request) string
	connections   []watchConnection
	owned         bool
	status        int
	pmsCalls      atomic.Int32
	metadataCalls atomic.Int32
}

func newWatchFixture(t *testing.T) *watchFixture {
	t.Helper()
	f := &watchFixture{
		share:      `<SharedServer email="alice@example.com" accepted="1" accessToken="share-SECRET"/>`,
		ownerEmail: "owner@example.com", expectToken: "share-SECRET", machine: "m1", owned: true,
		sections: `<Directory key="1" type="movie"/><Directory key="3" type="show"/>`,
		items:    `<Video type="movie" ratingKey="123"><Guid id="tmdb://10378"/></Video>`,
	}
	f.pms = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.pmsCalls.Add(1)
		if r.Method != http.MethodGet {
			t.Errorf("unexpected mutation: %s", r.Method)
		}
		if strings.Contains(r.URL.RawQuery, "SECRET") {
			t.Error("token in URL")
		}
		w.Header().Set("Content-Type", "application/xml")
		if r.URL.Path == "/identity" {
			if r.Header.Get("X-Plex-Token") != "" {
				t.Error("credential sent before machine verification")
			}
			fmt.Fprintf(w, `<MediaContainer machineIdentifier="%s"/>`, f.machine)
			return
		}
		if r.Header.Get("X-Plex-Token") != f.expectToken {
			t.Error("lookup did not use the recipient's token")
		}
		if f.status != 0 {
			w.WriteHeader(f.status)
			fmt.Fprint(w, "token=SECRET host=private.internal")
			return
		}
		if r.URL.Path == "/library/sections" {
			fmt.Fprint(w, `<MediaContainer>`+f.sections+`</MediaContainer>`)
			return
		}
		f.metadataCalls.Add(1)
		q := r.URL.Query()
		if q.Get("includeGuids") != "1" || q.Get("X-Plex-Container-Size") != "100" {
			t.Error("lookup omitted IDs or paging")
		}
		if q.Get("year") != "2007,2008,2009" && q.Get("title") != "Big Buck Bunny" {
			t.Errorf("unbounded search: %v", q)
		}
		if (r.URL.Path == "/library/sections/1/all" && q.Get("type") != "1") ||
			(r.URL.Path == "/library/sections/3/all" && q.Get("type") != "2") {
			t.Error("wrong media type")
		}
		if r.URL.Path != "/library/sections/1/all" && r.URL.Path != "/library/sections/3/all" {
			t.Errorf("queried a library not visible to user: %s", r.URL.Path)
		}
		if f.page != nil {
			fmt.Fprint(w, f.page(r))
			return
		}
		fmt.Fprint(w, `<MediaContainer size="1" offset="0" totalSize="1">`+f.items+`</MediaContainer>`)
	}))
	t.Cleanup(f.pms.Close)
	f.connections = []watchConnection{{URI: f.pms.URL, Local: true}}
	tv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("watch mutated plex.tv: %s", r.Method)
		}
		if r.Header.Get("X-Plex-Token") != "owner-SECRET" {
			t.Error("discovery did not use owner")
		}
		switch r.URL.Path {
		case "/api/v2/resources":
			json.NewEncoder(w).Encode([]watchResource{{ClientIdentifier: "m1", Provides: "server", Owned: f.owned, Connections: f.connections}})
		case "/api/servers/m1/shared_servers":
			fmt.Fprint(w, `<MediaContainer>`+f.share+`</MediaContainer>`)
		case "/api/v2/user":
			json.NewEncoder(w).Encode(Account{Email: f.ownerEmail})
		default:
			t.Errorf("unexpected plex.tv read: %s", r.URL.Path)
		}
	}))
	t.Cleanup(tv.Close)
	f.p = NewProvider(NewClientAt(tv.URL), "cid", "owner-SECRET", "m1", Account{Email: "owner@example.com"})
	return f
}

func movieWatchQuery() mediaserver.ItemQuery {
	return mediaserver.ItemQuery{MediaType: "movie", TMDBID: 10378, Year: 2008, Title: "Big Buck Bunny"}
}

func TestPlexFindItemAsRecipient(t *testing.T) {
	for _, tc := range []struct {
		name, item    string
		tv, titleOnly bool
		found         bool
	}{
		{"movie", `<Video type="movie" ratingKey="123"><Guid id="tmdb://10378"/></Video>`, false, false, true},
		{"show TVDB", `<Directory type="show" ratingKey="123"><Guid id="tvdb://81189"/></Directory>`, true, false, true},
		{"legacy movie", `<Video type="movie" ratingKey="123" guid="com.plexapp.agents.themoviedb://10378?lang=en"/>`, false, true, true},
		{"same title wrong ID", `<Video type="movie" title="Big Buck Bunny" ratingKey="123"><Guid id="tmdb://9"/></Video>`, false, false, false},
		{"missing IDs", `<Video type="movie" ratingKey="123"/>`, false, false, false},
		{"wrong media type", `<Directory type="show" ratingKey="123"><Guid id="tmdb://10378"/></Directory>`, false, false, false},
		{"conflicting show IDs", `<Directory type="show" ratingKey="123"><Guid id="tmdb://9"/><Guid id="tvdb://81189"/></Directory>`, true, false, false},
		{"invalid item key", `<Video type="movie" ratingKey="../../secret"><Guid id="tmdb://10378"/></Video>`, false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newWatchFixture(t)
			f.items = tc.item
			q := movieWatchQuery()
			if tc.tv {
				q.MediaType, q.TVDBID = "tv", 81189
			}
			if tc.titleOnly {
				q.Year = 0
			}
			item, err := f.p.FindItem(context.Background(), "Alice@Example.com", q)
			if !tc.found {
				if !errors.Is(err, mediaserver.ErrItemUnverified) || item.ID != "" {
					t.Fatalf("got %v, %v; want unverified", item, err)
				}
				return
			}
			if err != nil || item.WebPath != "/desktop/#!/server/m1/details?key=%2Flibrary%2Fmetadata%2F123" {
				t.Fatalf("item = %+v, %v", item, err)
			}
			if f.metadataCalls.Load() != 1 {
				t.Fatal("queried unrelated library types")
			}
		})
	}
}

func TestPlexFindItemShareEligibility(t *testing.T) {
	for _, tc := range []struct{ name, share string }{
		{"pending", `<SharedServer email="alice@example.com" accepted="0" accessToken="share-SECRET"/>`},
		{"revoked", ""},
		{"no token", `<SharedServer email="alice@example.com" accepted="1"/>`},
		{"other recipient", `<SharedServer email="bob@example.com" accepted="1" accessToken="share-SECRET"/>`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newWatchFixture(t)
			f.share = tc.share
			_, err := f.p.FindItem(context.Background(), "alice@example.com", movieWatchQuery())
			if !errors.Is(err, mediaserver.ErrItemUnverified) || f.pmsCalls.Load() != 0 {
				t.Fatalf("ineligible share queried: %v", err)
			}
		})
	}
	t.Run("owner", func(t *testing.T) {
		f := newWatchFixture(t)
		f.expectToken = "owner-SECRET"
		if _, err := f.p.FindItem(context.Background(), "owner@example.com", movieWatchQuery()); err != nil {
			t.Fatal(err)
		}
		f.ownerEmail = "different@example.com"
		before := f.pmsCalls.Load()
		if _, err := f.p.FindItem(context.Background(), "owner@example.com", movieWatchQuery()); !errors.Is(err, mediaserver.ErrItemUnverified) || f.pmsCalls.Load() != before {
			t.Fatal("trusted stale ownership")
		}
	})
}

func TestPlexFindItemSearchCoverage(t *testing.T) {
	for _, name := range []string{"later page", "ambiguous", "empty", "truncated", "ignored pagination", "bad XML", "no libraries"} {
		t.Run(name, func(t *testing.T) {
			f := newWatchFixture(t)
			f.page = func(r *http.Request) string {
				switch name {
				case "empty":
					return `<MediaContainer size="0" totalSize="0"/>`
				case "ambiguous":
					return `<MediaContainer size="2" totalSize="2">` + f.items + strings.Replace(f.items, "123", "456", 1) + `</MediaContainer>`
				case "truncated":
					return `<MediaContainer size="1" totalSize="2">` + f.items + `</MediaContainer>`
				case "bad XML":
					return `<invalid token="SECRET">`
				}
				if r.URL.Query().Get("X-Plex-Container-Start") == "0" || name == "ignored pagination" {
					return `<MediaContainer size="100" totalSize="101" offset="0">` + strings.Repeat(`<Video type="movie" ratingKey="9"><Guid id="tmdb://9"/></Video>`, 100) + `</MediaContainer>`
				}
				return `<MediaContainer size="1" totalSize="101" offset="100">` + f.items + `</MediaContainer>`
			}
			if name == "no libraries" {
				f.sections = ""
			}
			item, err := f.p.FindItem(context.Background(), "alice@example.com", movieWatchQuery())
			if name == "later page" {
				if err != nil || item.ID != "123" || f.metadataCalls.Load() != 2 {
					t.Fatalf("later match: %v, %v", item, err)
				}
			} else if err == nil || errors.Is(err, mediaserver.ErrItemNotFound) || item.ID != "" {
				t.Fatalf("claimed certainty from incomplete search: %v, %v", item, err)
			}
		})
	}
}

func TestPlexWatchConnectionFailuresAreSafe(t *testing.T) {
	for _, name := range []string{"wrong machine", "not owned", "relay", "bad URL", "redirect", "unauthorized", "canceled"} {
		t.Run(name, func(t *testing.T) {
			f := newWatchFixture(t)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			switch name {
			case "wrong machine":
				f.machine = "other"
			case "not owned":
				f.owned = false
			case "relay":
				f.connections[0].Relay = true
			case "bad URL":
				f.connections[0].URI = "https://user:SECRET@private.internal/path?X-Plex-Token=SECRET"
			case "unauthorized":
				f.status = http.StatusUnauthorized
			case "redirect":
				redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					http.Redirect(w, r, f.pms.URL+"/identity", http.StatusFound)
				}))
				defer redirect.Close()
				f.connections[0].URI = redirect.URL
			case "canceled":
				cancel()
			}
			item, err := f.p.FindItem(ctx, "alice@example.com", movieWatchQuery())
			if err == nil || item.ID != "" || errors.Is(err, mediaserver.ErrItemNotFound) {
				t.Fatalf("failure = %v, %v", item, err)
			}
			if strings.Contains(err.Error(), "SECRET") || strings.Contains(err.Error(), "127.0.0.1") || strings.Contains(err.Error(), "private.internal") {
				t.Fatalf("unsafe error: %v", err)
			}
			if name == "redirect" && f.pmsCalls.Load() != 0 {
				t.Fatal("followed redirect")
			}
		})
	}
}
