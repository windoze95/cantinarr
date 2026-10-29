package plex

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"github.com/windoze95/cantinarr-server/internal/mediaserver"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type plexInventoryFixture struct {
	provider      *Provider
	metadata      func(*http.Request) string
	share         func(int32) string
	shareReads    atomic.Int32
	catalogReads  atomic.Int32
	expectedToken string
}

func newPlexInventoryFixture(t *testing.T) *plexInventoryFixture {
	t.Helper()
	f := &plexInventoryFixture{expectedToken: "owner-token"}
	f.share = func(int32) string {
		return `<SharedServer email="reader@example.com" accepted="1" accessToken="reader-token"><Section key="1" shared="1"/><Section key="3" shared="1"/></SharedServer>`
	}
	f.metadata = func(*http.Request) string { return `<MediaContainer size="0" offset="0" totalSize="0"/>` }
	pms := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("inventory attempted mutation: %s", r.Method)
		}
		if strings.Contains(r.URL.RawQuery, "token") {
			t.Error("credential in URL")
		}
		if r.URL.Path == "/identity" {
			if r.Header.Get("X-Plex-Token") != "" {
				t.Error("token sent before identity proof")
			}
			fmt.Fprint(w, `<MediaContainer machineIdentifier="machine"/>`)
			return
		}
		if r.Header.Get("X-Plex-Token") != f.expectedToken {
			t.Errorf("wrong credential for catalog: %q", r.Header.Get("X-Plex-Token"))
		}
		if r.URL.Path == "/library/sections" {
			fmt.Fprint(w, `<MediaContainer><Directory key="1" title="Movies" type="movie"/><Directory key="3" title="TV" type="show"/><Directory key="8" title="Music" type="artist"/></MediaContainer>`)
			return
		}
		f.catalogReads.Add(1)
		fmt.Fprint(w, f.metadata(r))
	}))
	t.Cleanup(pms.Close)
	tv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("inventory changed account: %s", r.Method)
		}
		if r.Header.Get("X-Plex-Token") != "owner-token" {
			t.Error("account discovery wrong credential")
		}
		switch r.URL.Path {
		case "/api/v2/resources":
			json.NewEncoder(w).Encode([]watchResource{{ClientIdentifier: "machine", Provides: "server", Owned: true, Connections: []watchConnection{{URI: pms.URL, Local: true}}}})
		case "/api/servers/machine/shared_servers":
			fmt.Fprint(w, `<MediaContainer>`+f.share(f.shareReads.Add(1))+`</MediaContainer>`)
		default:
			t.Errorf("unexpected cloud call %s", r.URL.Path)
		}
	}))
	t.Cleanup(tv.Close)
	f.provider = NewProvider(NewClientAt(tv.URL), "client", "owner-token", "machine", Account{Email: "owner@example.com"})
	return f
}
func plexMovieXML(id string, tmdb int, media string) string {
	return fmt.Sprintf(`<Video type="movie" ratingKey="%s" librarySectionID="1"><Guid id="tmdb://%d"/>%s</Video>`, id, tmdb, media)
}
func TestPlexInventoryNativeLibrariesPlayableCopiesAndAbsence(t *testing.T) {
	f := newPlexInventoryFixture(t)
	libs, e := f.provider.InventoryLibraries(context.Background())
	if e != nil || len(libs) != 2 || libs[0].ID != "1" {
		t.Fatalf("libraries=%+v err=%v", libs, e)
	}
	media := `<Media width="3840" height="1600"><Part key="/library/parts/7/file"/></Media>`
	f.metadata = func(r *http.Request) string {
		if r.URL.Path != "/library/sections/1/all" || r.URL.Query().Get("includeGuids") != "1" {
			t.Errorf("wrong inventory query %s", r.URL)
		}
		return `<MediaContainer size="3" offset="0" totalSize="3">` + plexMovieXML("11", 10, media) + plexMovieXML("12", 10, `<Media width="1920"><Part key="/library/parts/8/file"/></Media>`) + plexMovieXML("13", 20, `<Media width="3840"><Part key="/library/parts/9/file" exists="0"/></Media>`) + `</MediaContainer>`
	}
	items, e := f.provider.Inventory(context.Background(), []string{"1"}, "", nil)
	if e != nil || len(items) != 3 || !items[0].Movie || !items[0].Is4K || !items[1].Movie || items[1].Is4K || items[2].Movie {
		t.Fatalf("copies=%+v err=%v", items, e)
	}
	f.metadata = func(*http.Request) string { return `<MediaContainer size="0" offset="0" totalSize="0"/>` }
	f.expectedToken = "reader-token"
	items, e = f.provider.InventoryCatalog(context.Background(), []string{"1"}, "reader@example.com")
	if e != nil || len(items) != 0 || f.shareReads.Load() != 2 {
		t.Fatalf("complete absence=%+v err=%v", items, e)
	}
}
func TestPlexInventoryCandidateUsesRecipientAndExactEpisodes(t *testing.T) {
	f := newPlexInventoryFixture(t)
	f.expectedToken = "reader-token"
	f.metadata = func(r *http.Request) string {
		switch r.URL.Path {
		case "/library/metadata/20":
			return `<MediaContainer><Directory type="show" ratingKey="20" librarySectionID="3"><Guid id="tmdb://10"/><Guid id="tvdb://100"/></Directory></MediaContainer>`
		case "/library/metadata/20/allLeaves":
			return `<MediaContainer size="2" offset="0" totalSize="2"><Video type="episode" ratingKey="21" grandparentRatingKey="20" parentIndex="2" index="1"><Guid id="tvdb://1001"/><Guid id="tmdb://2001"/><Media width="1920"><Part key="/library/parts/1/file"/></Media></Video><Video type="episode" ratingKey="22" grandparentRatingKey="20" parentIndex="2" index="2"/></MediaContainer>`
		default:
			t.Errorf("candidate scanned entire catalog: %s", r.URL.Path)
			return `<MediaContainer/>`
		}
	}
	items, e := f.provider.VerifyInventory(context.Background(), []string{"3"}, "reader@example.com", mediaserver.ItemQuery{MediaType: "tv", TMDBID: 10, TVDBID: 100}, []mediaserver.InventoryItem{{LibraryID: "3", ItemID: "20"}})
	if e != nil || len(items) != 1 || len(items[0].Episodes) != 1 || f.catalogReads.Load() != 2 || f.shareReads.Load() != 2 {
		t.Fatalf("inventory=%+v err=%v", items, e)
	}
	ep := items[0].Episodes[0]
	if ep.TVDBID != 1001 || ep.TMDBID != 2001 || ep.SeriesTVDBID != 100 || ep.Numbering != "unknown" {
		t.Fatalf("identity=%+v", ep)
	}
}
func TestPlexInventoryRechecksShareAndNeverReadsUnsharedLibrary(t *testing.T) {
	for _, kind := range []string{"removed", "changed token", "unshared", "pending"} {
		t.Run(kind, func(t *testing.T) {
			f := newPlexInventoryFixture(t)
			f.expectedToken = "reader-token"
			original := f.share
			f.share = func(n int32) string {
				if kind == "unshared" {
					return `<SharedServer email="reader@example.com" accepted="1" accessToken="reader-token"><Section key="3" shared="1"/></SharedServer>`
				}
				if n == 1 {
					return original(n)
				}
				switch kind {
				case "removed":
					return ""
				case "changed token":
					return strings.Replace(original(n), "reader-token", "new-token", 1)
				default:
					return strings.Replace(original(n), `accepted="1"`, `accepted="0"`, 1)
				}
			}
			items, e := f.provider.InventoryCatalog(context.Background(), []string{"1"}, "reader@example.com")
			if kind == "unshared" {
				if e != nil || len(items) != 0 || f.catalogReads.Load() != 0 {
					t.Fatalf("unshared read: %+v %v", items, e)
				}
			} else if !errors.Is(e, mediaserver.ErrItemUnverified) || len(items) > 0 {
				t.Fatalf("obsolete share accepted: %+v %v", items, e)
			}
		})
	}
}
func TestPlexInventoryRejectsIncompletePagesAndMovedCandidates(t *testing.T) {
	for _, kind := range []string{"short", "duplicate", "wrong offset", "moved"} {
		t.Run(kind, func(t *testing.T) {
			f := newPlexInventoryFixture(t)
			f.metadata = func(r *http.Request) string {
				switch kind {
				case "short":
					return `<MediaContainer size="0" offset="0" totalSize="1"/>`
				case "duplicate":
					return `<MediaContainer size="2" offset="0" totalSize="2">` + plexMovieXML("1", 1, "") + plexMovieXML("1", 1, "") + `</MediaContainer>`
				case "wrong offset":
					return `<MediaContainer size="0" offset="1" totalSize="0"/>`
				default:
					return `<MediaContainer><Video ratingKey="1" type="movie" librarySectionID="private"><Guid id="tmdb://1"/></Video></MediaContainer>`
				}
			}
			var e error
			if kind == "moved" {
				_, e = f.provider.VerifyInventory(context.Background(), []string{"1"}, "", mediaserver.ItemQuery{MediaType: "movie", TMDBID: 1}, []mediaserver.InventoryItem{{LibraryID: "1", ItemID: "1"}})
			} else {
				_, e = f.provider.InventoryCatalog(context.Background(), []string{"1"}, "")
			}
			if !errors.Is(e, mediaserver.ErrItemUnverified) {
				t.Fatalf("incomplete trusted: %v", e)
			}
		})
	}
}
func TestPlexIncrementalEpisodeRefreshesParent(t *testing.T) {
	f := newPlexInventoryFixture(t)
	f.metadata = func(r *http.Request) string {
		switch r.URL.Path {
		case "/library/sections/3/all":
			if r.URL.Query().Get("type") != "4" || r.URL.Query().Get("addedAt>>") == "" {
				t.Error("recent episode cursor missing")
			}
			return `<MediaContainer size="1" offset="0" totalSize="1"><Video type="episode" ratingKey="21" grandparentRatingKey="20"/></MediaContainer>`
		case "/library/metadata/20":
			return `<MediaContainer><Directory type="show" ratingKey="20" librarySectionID="3"><Guid id="tmdb://10"/></Directory></MediaContainer>`
		case "/library/metadata/20/allLeaves":
			return `<MediaContainer size="0" offset="0" totalSize="0"/>`
		default:
			t.Errorf("unexpected %s", r.URL)
			return `<MediaContainer/>`
		}
	}
	items, e := f.provider.InventorySince(context.Background(), []string{"3"}, time.Now().Add(-time.Minute))
	if e != nil || len(items) != 1 || items[0].ItemID != "20" || f.catalogReads.Load() != 3 {
		t.Fatalf("incremental=%+v err=%v", items, e)
	}
}

func TestPlexInventoryRetainsDistinctVersionsAndPartEvidence(t *testing.T) {
	var video inventoryMetadata
	err := xml.Unmarshal([]byte(`<Video><Media id="1" width="1920" height="1080"><Part id="11" key="/library/parts/11/file" file="/private/one.mkv"/></Media><Media id="2" width="3840" height="1600"><Part id="21" key="/library/parts/21/file"/></Media><Media id="3" width="3840" height="2160"><Part id="31" key="/library/parts/31/file" accessible="0"/></Media></Video>`), &video)
	if err != nil {
		t.Fatal(err)
	}
	copies := video.copies()
	if len(copies) != 3 || copies[0].SourceID != "1" || copies[0].PartID != "11" || copies[0].Is4K || !copies[1].Is4K || copies[2].Playable || copies[2].Is4K {
		t.Fatalf("versions=%+v", copies)
	}
	raw, _ := json.Marshal(copies)
	if strings.Contains(string(raw), "private") || strings.Contains(string(raw), "/library/") {
		t.Fatal("part URLs or file paths persisted")
	}
}

func TestPlexInventoryPaginatesCompleteCatalog(t *testing.T) {
	f := newPlexInventoryFixture(t)
	f.metadata = func(r *http.Request) string {
		start, _ := strconv.Atoi(r.URL.Query().Get("X-Plex-Container-Start"))
		size := 100
		if start == 100 {
			size = 1
		} else if start != 0 {
			t.Fatalf("wrong start=%d", start)
		}
		var b strings.Builder
		fmt.Fprintf(&b, `<MediaContainer size="%d" offset="%d" totalSize="101">`, size, start)
		for n := start; n < start+size; n++ {
			b.WriteString(plexMovieXML(strconv.Itoa(n+1), n+1, ""))
		}
		b.WriteString(`</MediaContainer>`)
		return b.String()
	}
	items, err := f.provider.InventoryCatalog(context.Background(), []string{"1"}, "")
	if err != nil || len(items) != 101 || f.catalogReads.Load() != 2 {
		t.Fatalf("items=%d reads=%d err=%v", len(items), f.catalogReads.Load(), err)
	}
}
