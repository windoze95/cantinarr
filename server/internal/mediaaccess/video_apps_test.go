package mediaaccess

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/mediaserver"
)

func TestVideoAppPreferencesArePersonalPersistentAndAtomic(t *testing.T) {
	e := newEnv(t)
	reader, admin := e.user("reader"), e.user("administrator")
	if _, err := e.db.Exec("UPDATE users SET role='admin' WHERE id=?", admin); err != nil {
		t.Fatal(err)
	}
	h := http.HandlerFunc(NewHandler(e.svc, nil).VideoAppPreferences)
	for _, method := range []string{"GET", "PUT"} {
		if rec := serve(h, method, "/api/me/video-apps", 0, `{}`); rec.Code != 401 {
			t.Fatalf("unauthenticated %s: %d", method, rec.Code)
		}
	}
	read := func(id int64) videoAppPreferences {
		t.Helper()
		rec := serve(h, "GET", "/api/me/video-apps", id, "")
		var apps videoAppPreferences
		if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &apps) != nil || rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("read: %d %s", rec.Code, rec.Body.String())
		}
		return apps
	}
	if got := read(reader); len(got) != 3 || got["plex"].IOS != "" {
		t.Fatalf("new user does not inherit: %+v", got)
	}
	for _, id := range []int64{reader, admin} {
		if rec := serve(h, "PUT", "/api/me/video-apps", id, `{"plex":{"ios":"infuse"},"jellyfin":{"ios":"browser"},"emby":{"ios":"service"}}`); rec.Code != 200 {
			t.Fatal(rec.Body.String())
		}
	}
	for _, bad := range []string{`null`, `[]`, `{} {}`, `{"user_id":2}`, `{"plex":null}`, `{"audiobookshelf":{"ios":"infuse"}}`, `{"plex":{"android":"infuse"}}`, `{"plex":{"ios":"shelfplayer"}}`, `{"plex":{"ios":"infuse://"}}`} {
		if rec := serve(h, "PUT", "/api/me/video-apps", reader, bad); rec.Code != 400 {
			t.Fatalf("accepted %s: %d", bad, rec.Code)
		}
	}
	if got := read(reader); got["plex"].IOS != "infuse" || got["jellyfin"].IOS != "browser" {
		t.Fatalf("invalid save changed preferences: %+v", got)
	}
	if rec := serve(h, "PUT", "/api/me/video-apps", reader, `{"plex":{"ios":"browser"}}`); rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	if got := read(reader); got["plex"].IOS != "browser" || got["jellyfin"].IOS != "" {
		t.Fatalf("replacement/reset failed: %+v", got)
	}
	if got := read(admin); got["plex"].IOS != "infuse" || got["jellyfin"].IOS != "browser" {
		t.Fatalf("cross-account write: %+v", got)
	}
	fresh := NewService(e.db, e.store, nil, nil)
	if got, err := fresh.videoAppPreferences(reader); err != nil || got["plex"].IOS != "browser" {
		t.Fatalf("reload: %+v %v", got, err)
	}
	if _, err := e.db.Exec("CREATE TRIGGER reject_video_app_save BEFORE UPDATE ON video_app_preferences WHEN NEW.service_type='jellyfin' BEGIN SELECT RAISE(ABORT, 'save rejected'); END"); err != nil {
		t.Fatal(err)
	}
	if rec := serve(h, "PUT", "/api/me/video-apps", reader, `{}`); rec.Code != 503 {
		t.Fatalf("failed save: %d", rec.Code)
	}
	if got := read(reader); got["plex"].IOS != "browser" {
		t.Fatalf("failed save lost stored preferences: %+v", got)
	}
	if _, err := e.db.Exec("DELETE FROM users WHERE id=?", reader); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := e.db.QueryRow("SELECT COUNT(*) FROM video_app_preferences WHERE user_id=?", reader).Scan(&count); err != nil || count != 0 {
		t.Fatalf("deleted user preferences: %d %v", count, err)
	}
}

func TestVideoAppsResolvePerInstanceAndServiceWithoutChangingWatchAccess(t *testing.T) {
	e := newEnv(t)
	reader := e.user("reader")
	ids := map[string][]string{}
	for _, serviceType := range []string{"plex", "jellyfin", "emby"} {
		for _, app := range []string{"infuse", "service"} {
			id := e.mediaServer(serviceType, serviceType+" "+app, instance.MediaServerConfig{PublicAddress: "https://watch.example.com", LibraryIDs: []string{"main"}, VideoApps: &instance.VideoApps{IOS: app}})
			ids[serviceType] = append(ids[serviceType], id)
			p := &finderProvider{fakeProvider: newFakeProvider(), find: func(remoteID string, q mediaserver.ItemQuery) (mediaserver.Item, error) {
				return mediaserver.Item{ID: "item", WebPath: "/web/item"}, nil
			}}
			e.providers[id] = p
			if _, err := e.svc.insertAccount(accountRow{UserID: reader, InstanceID: id, RemoteUserID: "remote", RemoteUsername: "reader"}, false); err != nil {
				t.Fatal(err)
			}
		}
		e.grantType(reader, serviceType, ids[serviceType]...)
	}
	q := mediaserver.ItemQuery{MediaType: "movie", TMDBID: 10378}
	before, err := e.svc.WatchLinks(context.Background(), reader, q)
	if err != nil || len(before) != 6 {
		t.Fatalf("links: %+v %v", before, err)
	}
	defaults := map[string]string{}
	for _, servers := range ids {
		defaults[servers[0]], defaults[servers[1]] = "infuse", "service"
	}
	for _, link := range before {
		if link.VideoApps.IOS != defaults[link.InstanceID] || link.State != WatchFound {
			t.Fatalf("instance default: %+v", link)
		}
	}
	if err := e.svc.saveVideoAppPreferences(reader, videoAppPreferences{"plex": {IOS: "browser"}, "emby": {IOS: "infuse"}}); err != nil {
		t.Fatal(err)
	}
	after, err := e.svc.WatchLinks(context.Background(), reader, q)
	if err != nil || len(after) != len(before) {
		t.Fatalf("override links: %+v %v", after, err)
	}
	for i, link := range after {
		want := defaults[link.InstanceID]
		if link.ServiceType == "plex" {
			want = "browser"
		}
		if link.ServiceType == "emby" {
			want = "infuse"
		}
		if link.VideoApps.IOS != want || link.URL != before[i].URL || link.State != before[i].State {
			t.Fatalf("override or access changed: %+v", link)
		}
	}
	views, err := e.svc.ListForUser(context.Background(), reader)
	if err != nil || len(views) != 6 {
		t.Fatalf("guide: %+v %v", views, err)
	}
	for _, view := range views {
		for _, link := range after {
			if view.InstanceID == link.InstanceID && (view.VideoApps == nil || *view.VideoApps != link.VideoApps) {
				t.Fatalf("guide choice differs: %+v", view)
			}
		}
	}
	// A preference never confers server access or changes grants.
	other := e.user("other-reader")
	if err := e.svc.saveVideoAppPreferences(other, videoAppPreferences{"plex": {IOS: "infuse"}}); err != nil {
		t.Fatal(err)
	}
	if links, err := e.svc.WatchLinks(context.Background(), other, q); err != nil || len(links) != 0 {
		t.Fatalf("preference granted access: %+v %v", links, err)
	}
}
