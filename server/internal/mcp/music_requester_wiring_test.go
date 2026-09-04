package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/db"
	"github.com/windoze95/cantinarr-server/internal/instance"
	requestsvc "github.com/windoze95/cantinarr-server/internal/request"
	"github.com/windoze95/cantinarr-server/internal/secrets"
)

// TestMusicRequesterToolWiring drives the music requester loop end to end:
// search_music surfaces addressable albums with client-reachable covers,
// display_media verifies against the same user-scoped lookup (an invented
// MBID never reaches the carousel), and the request/status tools demand the
// foreign id instead of falling into the tmdb pipeline at 0.
func TestMusicRequesterToolWiring(t *testing.T) {
	lidarrSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/album/lookup":
			// fa-1 carries a proper external remoteUrl; fa-2 mirrors the
			// arr-relative shape, which must be dropped rather than leak an
			// instance-origin path to clients.
			fmt.Fprint(w, `[{"title":"Example Album","artist":{"artistName":"The Artist"},"foreignAlbumId":"fa-1","releaseDate":"2020-05-01T00:00:00Z","overview":"About sounds.","images":[{"coverType":"cover","url":"/mediacover/1.jpg","remoteUrl":"https://covers.example.org/1.jpg"}]},{"title":"Relative Cover","foreignAlbumId":"fa-2","remoteCover":"/MediaCover/Albums/2/cover.jpg","images":[{"coverType":"cover","url":"/MediaCover/Albums/2/cover.jpg"}]}]`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(lidarrSrv.Close)

	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	res, err := database.Exec("INSERT INTO users (username, password_hash, role) VALUES ('listener', '', 'user')")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	uid, _ := res.LastInsertId()

	cipher, err := secrets.NewCipher(bytes.Repeat([]byte{0x21}, 32))
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	store := instance.NewStore(database, cipher)
	inst := &instance.Instance{ServiceType: "lidarr", Name: "Music", URL: lidarrSrv.URL, APIKey: "key"}
	if err := store.Create(inst); err != nil {
		t.Fatalf("create instance: %v", err)
	}
	if err := store.SetUserDefault(uid, "lidarr", inst.ID); err != nil {
		t.Fatalf("grant lidarr: %v", err)
	}
	registry := instance.NewRegistry(store)
	service := requestsvc.NewService(database, registry, nil, nil)
	server := NewToolServer(nil, service, registry, nil)
	server.SetCallAuthorizer(func(context.Context, CallContext) (string, error) {
		return auth.RoleUser, nil
	})
	callCtx := CallContext{UserID: uid, Role: auth.RoleUser, DeviceID: "device-1", Reauthorize: true}

	search, err := server.ExecuteTool(context.Background(), "search_music",
		json.RawMessage(`{"query":"example"}`), callCtx)
	if err != nil {
		t.Fatalf("search_music: %v", err)
	}
	if !strings.Contains(search.Text, "foreign_album_id: fa-1") {
		t.Fatalf("search text lacks the foreign id: %q", search.Text)
	}
	if !strings.Contains(search.Text, "Example Album by The Artist (2020)") {
		t.Fatalf("search text lacks the artist-labelled row: %q", search.Text)
	}
	searchJSON, _ := json.Marshal(search.StructuredData)
	for _, want := range []string{`"media_type":"music"`, `"foreign_id":"fa-1"`, `"poster_url":"https://covers.example.org/1.jpg"`} {
		if !strings.Contains(string(searchJSON), want) {
			t.Fatalf("search structured data lacks %s: %s", want, searchJSON)
		}
	}
	if strings.Contains(string(searchJSON), "/MediaCover/") {
		t.Fatalf("an arr-relative cover path reached the carousel: %s", searchJSON)
	}

	display, err := server.ExecuteTool(context.Background(), "display_media",
		json.RawMessage(`{"items":[{"media_type":"music","title":"Example Album","foreign_id":"fa-1"}]}`), callCtx)
	if err != nil {
		t.Fatalf("display_media: %v", err)
	}
	displayJSON, _ := json.Marshal(display.StructuredData)
	if !strings.Contains(string(displayJSON), `"foreign_id":"fa-1"`) ||
		!strings.Contains(string(displayJSON), `"poster_url":"https://covers.example.org/1.jpg"`) {
		t.Fatalf("display structured data = %s (text %q)", displayJSON, display.Text)
	}

	invented, err := server.ExecuteTool(context.Background(), "display_media",
		json.RawMessage(`{"items":[{"media_type":"music","title":"Example Album","foreign_id":"fa-nope"}]}`), callCtx)
	if err != nil {
		t.Fatalf("display_media invented id: %v", err)
	}
	inventedJSON, _ := json.Marshal(invented.StructuredData)
	if strings.Contains(string(inventedJSON), "fa-nope") {
		t.Fatalf("invented foreign id reached the carousel: %s", inventedJSON)
	}
	if !strings.Contains(invented.Text, "did not match") {
		t.Fatalf("invented foreign id text = %q", invented.Text)
	}

	if res, err := server.ExecuteTool(context.Background(), "request_media",
		json.RawMessage(`{"media_type":"music"}`), callCtx); err != nil || !strings.Contains(res.Text, "requires the foreign_id") {
		t.Fatalf("music request without foreign id = %v / %v", res, err)
	}
	if res, err := server.ExecuteTool(context.Background(), "check_request_status",
		json.RawMessage(`{"media_type":"music"}`), callCtx); err != nil || !strings.Contains(res.Text, "requires the foreign_id") {
		t.Fatalf("music status without foreign id = %v / %v", res, err)
	}
}

// TestSearchMusicWithoutAccessSaysSo pins the absence sentence: no configured
// or granted Lidarr must read as "not available for this account", never as an
// empty result set.
func TestSearchMusicWithoutAccessSaysSo(t *testing.T) {
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	res, err := database.Exec("INSERT INTO users (username, password_hash, role) VALUES ('nobody', '', 'user')")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	uid, _ := res.LastInsertId()

	cipher, err := secrets.NewCipher(bytes.Repeat([]byte{0x21}, 32))
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	registry := instance.NewRegistry(instance.NewStore(database, cipher))
	service := requestsvc.NewService(database, registry, nil, nil)
	server := NewToolServer(nil, service, registry, nil)
	server.SetCallAuthorizer(func(context.Context, CallContext) (string, error) {
		return auth.RoleUser, nil
	})
	callCtx := CallContext{UserID: uid, Role: auth.RoleUser, DeviceID: "device-1", Reauthorize: true}

	out, err := server.ExecuteTool(context.Background(), "search_music",
		json.RawMessage(`{"query":"anything"}`), callCtx)
	if err != nil {
		t.Fatalf("search_music: %v", err)
	}
	if !strings.Contains(out.Text, "Music is not available for this account") {
		t.Fatalf("no-access text = %q", out.Text)
	}
}
