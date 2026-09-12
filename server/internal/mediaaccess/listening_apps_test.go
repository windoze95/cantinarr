package mediaaccess

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/mediaserver"
)

func TestListeningAppPreferencesArePersonalAndPersistent(t *testing.T) {
	e := newEnv(t)
	reader, admin := e.user("reader"), e.user("administrator")
	if _, err := e.db.Exec("UPDATE users SET role='admin' WHERE id=?", admin); err != nil {
		t.Fatal(err)
	}
	h := http.HandlerFunc(NewHandler(e.svc, nil).ListeningAppPreferences)
	for _, method := range []string{"GET", "PUT"} {
		if rec := serve(h, method, "/api/me/listening-apps", 0, `{}`); rec.Code != 401 {
			t.Fatalf("unauthenticated %s = %d", method, rec.Code)
		}
	}
	read := func(id int64) instance.ListeningApps {
		t.Helper()
		rec := serve(h, "GET", "/api/me/listening-apps", id, "")
		var apps instance.ListeningApps
		if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &apps) != nil || rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("read preferences: %d %s", rec.Code, rec.Body.String())
		}
		return apps
	}
	if got := read(reader); got != (instance.ListeningApps{}) {
		t.Fatalf("new account does not inherit: %+v", got)
	}
	for _, id := range []int64{reader, admin} {
		if rec := serve(h, "PUT", "/api/me/listening-apps", id, `{"ios":"shelfplayer","android":"theshelf"}`); rec.Code != 200 {
			t.Fatalf("save %d: %d %s", id, rec.Code, rec.Body.String())
		}
	}
	if rec := serve(h, "PUT", "/api/me/listening-apps", reader, `{"ios":"browser","android":""}`); rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	if got := read(reader); got != (instance.ListeningApps{IOS: "browser"}) {
		t.Fatalf("explicit browser or inherited Android lost: %+v", got)
	}
	if got := read(admin); got != (instance.ListeningApps{IOS: "shelfplayer", Android: "theshelf"}) {
		t.Fatalf("another user's preferences changed: %+v", got)
	}
	for _, bad := range []string{`null`, `[]`, `{} {}`, `{"user_id":2}`, `{"ios":"theshelf"}`, `{"android":"shelfplayer"}`, `{"ios":"javascript:alert(1)"}`} {
		if rec := serve(h, "PUT", "/api/me/listening-apps", reader, bad); rec.Code != 400 {
			t.Fatalf("accepted invalid preferences %s: %d", bad, rec.Code)
		}
	}
	// A fresh service reads the saved choices; they are not device-local state.
	fresh := NewService(e.db, e.store, nil, nil)
	if got, err := fresh.listeningAppPreferences(reader); err != nil || got.IOS != "browser" || got.Android != "" {
		t.Fatalf("reloaded preferences: %+v %v", got, err)
	}
	if _, err := e.db.Exec("DELETE FROM users WHERE id=?", reader); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := e.db.QueryRow("SELECT COUNT(*) FROM listening_app_preferences WHERE user_id=?", reader).Scan(&count); err != nil || count != 0 {
		t.Fatalf("deleted user retained preferences: %d %v", count, err)
	}
}

func TestListeningAppsResolvePerInstanceDefaultsAndPersonalOverrides(t *testing.T) {
	e := newEnv(t)
	reader := e.user("reader")
	a := e.mediaServer("audiobookshelf", "Main books", instance.MediaServerConfig{
		PublicAddress: "https://books.example/main",
		ListeningApps: &instance.ListeningApps{IOS: "shelfplayer", Android: "theshelf"},
	})
	b := e.mediaServer("audiobookshelf", "Other books", instance.MediaServerConfig{
		PublicAddress: "https://books.example/other",
		ListeningApps: &instance.ListeningApps{IOS: "audiobookshelf", Android: "audiobookshelf"},
	})
	e.grantType(reader, "audiobookshelf", a, b)
	links, err := e.svc.ListenLinks(context.Background(), reader, mediaserver.BookQuery{Available: true})
	if err != nil || len(links) != 2 {
		t.Fatalf("links: %+v %v", links, err)
	}
	byID := map[string]instance.ListeningApps{}
	for _, link := range links {
		byID[link.InstanceID] = link.ListeningApps
	}
	if byID[a].IOS != "shelfplayer" || byID[a].Android != "theshelf" || byID[b].IOS != "audiobookshelf" {
		t.Fatalf("defaults crossed instances: %+v", byID)
	}
	h := http.HandlerFunc(NewHandler(e.svc, nil).ListeningAppPreferences)
	if rec := serve(h, "PUT", "/api/me/listening-apps", reader, `{"ios":"browser","android":""}`); rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	links, err = e.svc.ListenLinks(context.Background(), reader, mediaserver.BookQuery{Available: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, link := range links {
		if link.ListeningApps.IOS != "browser" || link.ListeningApps.Android != byID[link.InstanceID].Android {
			t.Fatalf("platform override lost: %+v", link)
		}
	}
	views, err := e.svc.ListForUser(context.Background(), reader)
	if err != nil || len(views) != 2 {
		t.Fatalf("guide: %+v %v", views, err)
	}
	for _, view := range views {
		if view.ListeningApps == nil || view.ListeningApps.IOS != "browser" || view.ListeningApps.Android != byID[view.InstanceID].Android {
			t.Fatalf("guide uses different preferences: %+v", view)
		}
	}
}

func TestListeningAppDefaultsAndUserLibraryPoliciesSaveIndependently(t *testing.T) {
	e, id, _ := absLibraryEnv(t)
	reader, other := e.user("reader"), e.user("other-reader")
	apps := instance.ListeningApps{IOS: "shelfplayer", Android: "theshelf"}
	inst, err := e.store.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	inst.MediaServerConfig.ListeningApps = &apps
	if err := e.store.Update(inst); err != nil {
		t.Fatal(err)
	}
	users := []int64{reader, other}
	saveLibraries(t, e, id, users, []string{"books"}, map[int64]LibraryPolicy{other: selectedLibraries("shared")})
	saveLibraries(t, e, id, users, []string{"shared"}, nil)
	inst, err = e.store.Get(id)
	if err != nil || inst.MediaServerConfig.ListeningApps == nil || *inst.MediaServerConfig.ListeningApps != apps {
		t.Fatalf("library save lost listening defaults: %+v %v", inst, err)
	}
	inst.MediaServerConfig.ListeningApps = &instance.ListeningApps{IOS: "browser", Android: "audiobookshelf"}
	if err := e.store.Update(inst); err != nil {
		t.Fatal(err)
	}
	access, err := e.svc.LibraryAccess(id)
	if err != nil || len(access.UserIDs) != 2 || len(access.DefaultLibraryIDs) != 1 || access.DefaultLibraryIDs[0] != "shared" {
		t.Fatalf("listening save changed access: %+v %v", access, err)
	}
	policy := access.Policies[other]
	if policy.Mode != "selected" || len(policy.LibraryIDs) != 1 || policy.LibraryIDs[0] != "shared" {
		t.Fatalf("listening save changed the individual selection: %+v", policy)
	}
}
