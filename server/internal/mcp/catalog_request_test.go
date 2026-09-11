package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/windoze95/cantinarr-server/internal/db"
	"github.com/windoze95/cantinarr-server/internal/discordnotify"
	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/push"
	"github.com/windoze95/cantinarr-server/internal/request"
	"github.com/windoze95/cantinarr-server/internal/secrets"
)

func TestBookToolsUseNativeIdentityAndRetirePublicRequests(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/book/lookup" {
			w.Write([]byte(`[{"foreignBookId":"gr:48297245","title":"The Subtle Art of Not Giving a Fuck","authorName":"Mark Manson"}]`))
			return
		}
		t.Errorf("unexpected upstream call: %s", r.URL.Path)
		w.WriteHeader(503)
	}))
	defer upstream.Close()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	res, err := database.Exec(`INSERT INTO users(username,password_hash,role) VALUES('reader','','user')`)
	if err != nil {
		t.Fatal(err)
	}
	uid, _ := res.LastInsertId()
	cipher, _ := secrets.NewCipher(bytes.Repeat([]byte{3}, 32))
	store := instance.NewStore(database, cipher)
	inst := &instance.Instance{ServiceType: "chaptarr", Name: "Selected books", URL: upstream.URL, APIKey: "fixture"}
	if err = store.Create(inst); err != nil {
		t.Fatal(err)
	}
	if err = store.SetUserDefault(uid, "chaptarr", inst.ID); err != nil {
		t.Fatal(err)
	}
	registry := instance.NewRegistry(store)
	service := request.NewService(database, registry, nil, nil)

	server := NewToolServer(nil, service, registry, nil)
	search, err := server.searchBooks(json.RawMessage(`{"query":"The Subtle Art of Not Giving a Fuck","catalog":"all"}`), uid)
	if err != nil || !strings.Contains(search.Text, "gr:48297245") || !strings.Contains(search.Text, inst.ID) || strings.Contains(search.Text, "openlibrary") {
		t.Fatalf("native identity lost: %+v %v", search, err)
	}
	for name, call := range map[string]func() (*ToolResult, error){
		"search": func() (*ToolResult, error) {
			return server.searchBooks(json.RawMessage(`{"query":"book","catalog":"public"}`), uid)
		},
		"display": func() (*ToolResult, error) {
			return server.displayMedia(context.Background(), json.RawMessage(`{"items":[{"media_type":"book","title":"Book","catalog_ref":{"provider":"openlibrary","id":"OL1W"}}]}`), uid, nil)
		},
		"request": func() (*ToolResult, error) {
			return server.requestMedia(json.RawMessage(`{"media_type":"book","title":"Book","catalog_ref":{"provider":"openlibrary","id":"OL1W"}}`), uid)
		},
		"confirm": func() (*ToolResult, error) {
			return server.requestMedia(json.RawMessage(`{"media_type":"book","action":"confirm","request_id":1,"foreign_id":"summary"}`), uid)
		},
	} {
		t.Run(name, func(t *testing.T) {
			out, err := call()
			if err != nil {
				t.Fatal(err)
			}
			var data map[string]string
			if json.Unmarshal([]byte(out.Text), &data) != nil || data["code"] != "catalog_retired" {
				t.Fatalf("retirement missing: %+v", out)
			}
		})
	}
}

func TestMCPRequestQueuesOneDiscordAlert(t *testing.T) {
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	res, err := database.Exec(`INSERT INTO users(username,password_hash,role) VALUES('requester','','user')`)
	if err != nil {
		t.Fatal(err)
	}
	uid, _ := res.LastInsertId()
	cipher, _ := secrets.NewCipher(bytes.Repeat([]byte{3}, 32))
	service := request.NewService(database, nil, nil, nil)
	if err = service.SetGlobalSettings(request.GlobalSettings{RequireApproval: true}); err != nil {
		t.Fatal(err)
	}
	discord := discordnotify.NewService(database, cipher, nil)
	include := true
	if err = discord.Save(true, "https://discord.com/api/webhooks/123/test-token", false, &include); err != nil {
		t.Fatal(err)
	}
	service.SetCreationObserver(discord)
	server := NewToolServer(nil, service, nil, nil)
	for i := 0; i < 2; i++ {
		result, err := server.requestMedia(json.RawMessage(`{"media_type":"movie","tmdb_id":550,"title":"A movie"}`), uid)
		if err != nil || !strings.Contains(result.Text, `"success":true`) {
			t.Fatalf("request: %+v %v", result, err)
		}
	}
	var count int
	if err = database.QueryRow(`SELECT COUNT(*) FROM discord_notifications`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("receipts: %d %v", count, err)
	}
}

func TestAutomaticMCPRequestNotifiesOnceWithDiscordDisabled(t *testing.T) {
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	for _, statement := range []string{
		`INSERT INTO users(id,username,password_hash,role) VALUES(1,'requester','','user'),(2,'admin','','admin')`,
		`INSERT INTO notification_prefs(user_id,request_auto_approved) VALUES(2,1)`,
	} {
		if _, err := database.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	arr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == "/api/v3/movie" {
			w.Write([]byte(`[{"id":42,"tmdbId":550,"title":"Canonical movie","monitored":true,"hasFile":false}]`))
			return
		}
		t.Errorf("unexpected arr call: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(500)
	}))
	defer arr.Close()
	cipher, _ := secrets.NewCipher(bytes.Repeat([]byte{3}, 32))
	store := instance.NewStore(database, cipher)
	inst := &instance.Instance{ServiceType: "radarr", Name: "Movies", URL: arr.URL, APIKey: "fixture"}
	if err := store.Create(inst); err != nil {
		t.Fatal(err)
	}
	if err := store.SetUserDefault(1, "radarr", inst.ID); err != nil {
		t.Fatal(err)
	}
	captured := make(chan map[string]any, 4)
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/notifications" {
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			captured <- body
		}
		w.Write([]byte(`{"sent":1,"failed":0}`))
	}))
	defer gateway.Close()
	manager := push.NewManager(database, nil, gateway.URL, "fixture", "", "Cantinarr", nil)
	if manager.Ensure(context.Background()) == nil {
		t.Fatal("push manager not ready")
	}
	registry := instance.NewRegistry(store)
	service := request.NewService(database, registry, nil, nil)
	service.SetCreationObserver(request.CreationObservers{
		discordnotify.NewService(database, cipher, nil),
		push.NewNotifier(database, manager, nil),
	})
	server := NewToolServer(nil, service, registry, nil)
	for i := 0; i < 2; i++ {
		result, err := server.requestMedia(json.RawMessage(`{"media_type":"movie","tmdb_id":550,"title":"Supplied title"}`), 1)
		if err != nil || !strings.Contains(result.Text, `"success":true`) {
			t.Fatalf("request: %+v %v", result, err)
		}
	}
	select {
	case body := <-captured:
		data := body["data"].(map[string]any)
		if data["type"] != "request_auto_approved" || data["title"] != "Canonical movie" || data["instance_id"] != inst.ID {
			t.Fatalf("wrong event identity: %v", body)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("automatic request push not sent")
	}
	select {
	case body := <-captured:
		t.Fatalf("retry produced another alert: %v", body)
	case <-time.After(50 * time.Millisecond):
	}
	var queued int
	if err := database.QueryRow(`SELECT COUNT(*) FROM discord_notifications`).Scan(&queued); err != nil || queued != 0 {
		t.Fatalf("disabled Discord queued %d alerts: %v", queued, err)
	}
}
