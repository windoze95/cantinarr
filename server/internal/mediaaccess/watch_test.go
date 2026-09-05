package mediaaccess

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/mediaserver"
)

// finderProvider is a fakeProvider that can also find items. The bare
// fakeProvider deliberately cannot, which is how a client without the
// lookup is stood in for.
type finderProvider struct {
	*fakeProvider
	find  func(remoteID string, q mediaserver.ItemQuery) (mediaserver.Item, error)
	calls atomic.Int32
}

func (f *finderProvider) FindItem(_ context.Context, remoteID string, q mediaserver.ItemQuery) (mediaserver.Item, error) {
	f.calls.Add(1)
	return f.find(remoteID, q)
}

func TestWatchLinksForUser(t *testing.T) {
	e := newEnv(t)
	alice := e.user("alice")
	found := e.jellyfin("A Found", instance.MediaServerConfig{PublicAddress: "https://jf.example.com/"})
	missing := e.mediaServer("emby", "B Missing", instance.MediaServerConfig{PublicAddress: "https://emby.example.com"})
	dead := e.jellyfin("C Dead", instance.MediaServerConfig{PublicAddress: "https://dead.example.com"})
	noFinder := e.jellyfin("D No finder", instance.MediaServerConfig{PublicAddress: "https://old.example.com"})
	noAddress := e.jellyfin("E No address", instance.MediaServerConfig{})
	noRow := e.jellyfin("F No account", instance.MediaServerConfig{PublicAddress: "https://f.example.com"})
	off := e.jellyfin("G Switched off", instance.MediaServerConfig{PublicAddress: "https://g.example.com"})
	plex := e.mediaServer("plex", "H Plex", instance.MediaServerConfig{PublicAddress: instance.PlexPublicAddress})
	e.grant(alice, found, dead, noFinder, noAddress, noRow, off)
	e.grantType(alice, "emby", missing)
	e.grantType(alice, "plex", plex)

	want := mediaserver.ItemQuery{MediaType: "movie", TMDBID: 10378, Year: 2008, Title: "Big Buck Bunny"}
	// The lookups run concurrently; what each was asked as is recorded
	// under a lock and read once they have all answered.
	var mu sync.Mutex
	asked := map[string]string{}
	finder := func(name string, item mediaserver.Item, err error) *finderProvider {
		p := &finderProvider{fakeProvider: newFakeProvider()}
		p.find = func(remoteID string, q mediaserver.ItemQuery) (mediaserver.Item, error) {
			if q != want {
				t.Errorf("%s asked %+v, want %+v", name, q, want)
			}
			mu.Lock()
			asked[name] = remoteID
			mu.Unlock()
			return item, err
		}
		return p
	}
	foundP := finder("found", mediaserver.Item{ID: "i-1", WebPath: "/web/#/details?id=i-1&serverId=s-1"}, nil)
	missingP := finder("missing", mediaserver.Item{}, mediaserver.ErrItemNotFound)
	deadP := finder("dead", mediaserver.Item{}, errors.New("jellyfin find item: connection refused"))
	noFinderP := newFakeProvider()
	noAddressP := finder("no address", mediaserver.Item{ID: "x", WebPath: "/x"}, nil)
	noRowP := finder("no row", mediaserver.Item{ID: "x", WebPath: "/x"}, nil)
	offP := finder("off", mediaserver.Item{ID: "x", WebPath: "/x"}, nil)
	e.providers[found], e.providers[missing], e.providers[dead], e.providers[noFinder] = foundP, missingP, deadP, noFinderP
	e.providers[noAddress], e.providers[noRow], e.providers[off], e.providers[plex] = noAddressP, noRowP, offP, newFakeInviteProvider()
	remotes := map[string]string{}
	for _, inst := range []string{found, missing, dead, noFinder, noAddress, off} {
		remotes[inst] = "remote-" + inst
		if _, err := e.svc.insertAccount(accountRow{UserID: alice, InstanceID: inst, RemoteUserID: remotes[inst], RemoteUsername: "alice", CreatedByCantinarr: true}, false); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.svc.setDisabledAt(alice, off, true); err != nil {
		t.Fatal(err)
	}

	links, err := e.svc.WatchLinks(context.Background(), alice, want)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]WatchLink{}
	for _, l := range links {
		byName[l.Name] = l
	}
	if len(links) != 4 {
		t.Fatalf("links = %+v, want the three lookups and Plex's generic shortcut", links)
	}
	if l := byName["H Plex"]; l.State != WatchUnverified || l.URL != "" || l.FallbackURL != instance.PlexPublicAddress {
		t.Fatalf("unlinked Plex = %+v", l)
	}
	if l := byName["A Found"]; l.State != WatchFound || l.URL != "https://jf.example.com/web/#/details?id=i-1&serverId=s-1" || l.ServiceType != "jellyfin" || l.InstanceID != found {
		t.Fatalf("found = %+v, want the item's page at the public address", l)
	}
	if l := byName["B Missing"]; l.State != WatchMissing || l.URL != "" {
		t.Fatalf("missing = %+v, want confirmed absence without a URL", l)
	}
	if l := byName["C Dead"]; l.State != WatchUnreachable || l.URL != "" {
		t.Fatalf("dead = %+v, want unreachable, never absence", l)
	}
	mu.Lock()
	defer mu.Unlock()
	for name, inst := range map[string]string{"found": found, "missing": missing, "dead": dead} {
		if asked[name] != remotes[inst] {
			t.Errorf("%s was asked as %q, want the linked account %q", name, asked[name], remotes[inst])
		}
	}
	for name, p := range map[string]*finderProvider{"no address": noAddressP, "no row": noRowP, "off": offP} {
		if p.calls.Load() != 0 {
			t.Errorf("%s was asked %d times, want never", name, p.calls.Load())
		}
	}

	raw, _ := json.Marshal(links)
	if strings.Contains(string(raw), ".internal") {
		t.Fatalf("links carry an instance host: %s", raw)
	}
	logs := e.logs.String()
	if !strings.Contains(logs, "could not look a title up") || strings.Contains(logs, "Big Buck Bunny") || strings.Contains(logs, ".internal") {
		t.Fatalf("logs = %q, want the unreachable server noted with ids only", logs)
	}
}

type inviteFinderProvider struct{ *finderProvider }

func (p *inviteFinderProvider) Kind() mediaserver.Kind { return mediaserver.KindInvite }

func TestPlexWatchLinksRequireGrantsAndUseHostedItemLinks(t *testing.T) {
	e := newEnv(t)
	alice, bob := e.user("alice"), e.user("bob")
	id := e.mediaServer("plex", "Plex", instance.MediaServerConfig{PublicAddress: "https://watch.example.com"})
	e.grantType(alice, "plex", id)
	_, err := e.svc.insertAccount(accountRow{UserID: alice, InstanceID: id, RemoteUserID: "alice@example.com"}, false)
	if err != nil {
		t.Fatal(err)
	}
	p := &inviteFinderProvider{&finderProvider{fakeProvider: newFakeProvider()}}
	e.providers[id] = p
	for _, state := range []string{WatchFound, WatchUnverified, WatchUnreachable} {
		p.find = func(remote string, q mediaserver.ItemQuery) (mediaserver.Item, error) {
			if remote != "alice@example.com" {
				t.Errorf("wrong identity: %s", remote)
			}
			switch state {
			case WatchUnverified:
				return mediaserver.Item{}, mediaserver.ErrItemUnverified
			case WatchUnreachable:
				return mediaserver.Item{}, errors.New("plex watch: server did not answer")
			default:
				return mediaserver.Item{ID: "1", WebPath: "/desktop/#!/server/m1/details?key=%2Flibrary%2Fmetadata%2F1"}, nil
			}
		}
		links, err := e.svc.WatchLinks(context.Background(), alice, mediaserver.ItemQuery{MediaType: "movie", TMDBID: 1})
		if err != nil || len(links) != 1 || links[0].State != state {
			t.Fatalf("links = %v, %v", links, err)
		}
		if state == WatchFound {
			if !strings.HasPrefix(links[0].URL, "https://app.plex.tv/desktop/#!/server/m1/") || links[0].FallbackURL != "" {
				t.Fatalf("exact = %+v", links[0])
			}
		} else if links[0].URL != "" || links[0].FallbackURL != "https://watch.example.com" {
			t.Fatalf("fallback = %+v", links[0])
		}
	}
	before := p.calls.Load()
	if links, err := e.svc.WatchLinks(context.Background(), bob, mediaserver.ItemQuery{MediaType: "movie", TMDBID: 1}); err != nil || len(links) != 0 || p.calls.Load() != before {
		t.Fatal("ungranted user received Plex access")
	}
	if err := e.svc.setDisabledAt(alice, id, true); err != nil {
		t.Fatal(err)
	}
	links, err := e.svc.WatchLinks(context.Background(), alice, mediaserver.ItemQuery{MediaType: "movie", TMDBID: 1})
	if err != nil || len(links) != 1 || links[0].State != WatchUnverified || p.calls.Load() != before {
		t.Fatal("disabled account queried")
	}
}

func TestWatchStatusMapping(t *testing.T) {
	e, router := newHandlerEnv(t)
	alice := e.user("alice")
	jf := e.jellyfin("Home", instance.MediaServerConfig{PublicAddress: "https://jf.example.com"})
	e.grant(alice, jf)
	var got mediaserver.ItemQuery
	p := &finderProvider{fakeProvider: newFakeProvider()}
	p.find = func(_ string, q mediaserver.ItemQuery) (mediaserver.Item, error) {
		got = q
		return mediaserver.Item{ID: "i-1", WebPath: "/web/#/details?id=i-1&serverId=s"}, nil
	}
	e.providers[jf] = p
	if _, err := e.svc.insertAccount(accountRow{UserID: alice, InstanceID: jf, RemoteUserID: "r-1", RemoteUsername: "alice", CreatedByCantinarr: true}, false); err != nil {
		t.Fatal(err)
	}

	if rec := serve(router, "GET", "/api/media-servers/watch?media_type=movie&tmdb_id=1", 0, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous = %d", rec.Code)
	}
	for name, query := range map[string]string{
		"no media type":  "tmdb_id=1",
		"bad media type": "media_type=book&tmdb_id=1",
		"no ids":         "media_type=movie",
		"bad ids":        "media_type=tv&tmdb_id=abc&tvdb_id=-4",
	} {
		if rec := serve(router, "GET", "/api/media-servers/watch?"+query, alice, ""); rec.Code != http.StatusBadRequest {
			t.Errorf("%s = %d, want 400", name, rec.Code)
		}
	}
	if p.calls.Load() != 0 {
		t.Fatalf("malformed queries reached the media server %d times", p.calls.Load())
	}

	rec := serve(router, "GET", "/api/media-servers/watch?media_type=tv&tmdb_id=1396&tvdb_id=81189&year=2008&title=Breaking+Bad", alice, "")
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("watch = %d %s (cache-control %q)", rec.Code, rec.Body.String(), rec.Header().Get("Cache-Control"))
	}
	if want := (mediaserver.ItemQuery{MediaType: "tv", TMDBID: 1396, TVDBID: 81189, Year: 2008, Title: "Breaking Bad"}); got != want {
		t.Fatalf("query = %+v, want %+v", got, want)
	}
	var links []WatchLink
	if err := json.Unmarshal(rec.Body.Bytes(), &links); err != nil || len(links) != 1 || links[0].State != WatchFound || links[0].URL != "https://jf.example.com/web/#/details?id=i-1&serverId=s" {
		t.Fatalf("links = %s (%v)", rec.Body.String(), err)
	}

	// Nothing eligible is an empty list, not null.
	bob := e.user("bob")
	rec = serve(router, "GET", "/api/media-servers/watch?media_type=movie&tmdb_id=1", bob, "")
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Fatalf("nothing eligible = %d %s, want 200 []", rec.Code, rec.Body.String())
	}
}
