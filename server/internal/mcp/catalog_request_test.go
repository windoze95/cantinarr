package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/db"
	"github.com/windoze95/cantinarr-server/internal/discordnotify"
	"github.com/windoze95/cantinarr-server/internal/instance"
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
	if err = discord.Save(true, "https://discord.com/api/webhooks/123/test-token", false); err != nil {
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
