package audiobookshelf

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/mediaserver"
)

func testUser() map[string]any {
	return map[string]any{"id": "user-1", "username": "alice", "type": "user", "isActive": true,
		"permissions":         map[string]any{"download": true, "update": false, "delete": false, "upload": false, "createEreader": false, "accessAllLibraries": false, "accessAllTags": true, "accessExplicitContent": false, "selectedTagsNotAccessible": false},
		"librariesAccessible": []string{"books"}, "itemTagsSelected": []string{}}
}

func sendJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func TestCreateAccountAppliesRestrictionsAndReconciles(t *testing.T) {
	u := testUser()
	created, deleted := false, false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sentinel" {
			t.Error("missing admin key")
		}
		switch r.Method + " " + r.URL.Path {
		case "GET /api/users":
			if created {
				sendJSON(w, map[string]any{"users": []any{u}})
			} else {
				sendJSON(w, map[string]any{"users": []any{}})
			}
		case "POST /api/users":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			permissions := body["permissions"].(map[string]any)
			for _, key := range []string{"update", "delete", "upload", "createEreader", "accessAllLibraries", "accessExplicitContent"} {
				if permissions[key] != false {
					t.Errorf("unsafe permission %s", key)
				}
			}
			if body["password"] != "private-password" || body["isActive"] != true || body["type"] != "user" || !reflect.DeepEqual(body["librariesAccessible"], []any{"books"}) {
				t.Error("invalid provisioning body")
			}
			created = true
			sendJSON(w, map[string]any{"user": u})
		case "GET /api/users/user-1":
			sendJSON(w, u)
		case "PATCH /api/users/user-1":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["isActive"] != nil {
				u["isActive"] = body["isActive"]
			}
			if body["permissions"] != nil {
				p := body["permissions"].(map[string]any)
				if len(p) != 1 {
					t.Error("library update changed other permissions")
				}
				u["permissions"].(map[string]any)["accessAllLibraries"] = p["accessAllLibraries"]
				u["librariesAccessible"] = body["librariesAccessible"]
			}
			sendJSON(w, u)
		case "DELETE /api/users/user-1":
			deleted = true
			w.WriteHeader(200)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	c, ctx := NewClient(server.URL, "sentinel"), context.Background()
	remote, err := c.CreateUser(ctx, "alice", "private-password", []string{"books"})
	if err != nil || remote.IsAdministrator || remote.IsDisabled {
		t.Fatalf("creation: %+v %v", remote, err)
	}
	if _, err := c.CreateUser(ctx, "ALICE", "another-password", nil); !errors.Is(err, mediaserver.ErrUserExists) {
		t.Fatalf("name conflict: %v", err)
	}
	for _, disabled := range []bool{true, false} {
		if err := c.SetDisabled(ctx, remote.ID, disabled); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.SetLibraries(ctx, remote.ID, nil); err != nil {
		t.Fatal(err)
	}
	u["type"] = "admin"
	if err := c.SetDisabled(ctx, remote.ID, true); err != nil {
		t.Fatal(err)
	}
	if u["isActive"] != true {
		t.Fatal("administrator disabled")
	}
	if err := c.DeleteUser(ctx, remote.ID); err == nil || deleted {
		t.Fatal("administrator deleted")
	}
}

func TestProvisioningRollbackAndRedaction(t *testing.T) {
	deleted := false
	u := testUser()
	u["permissions"].(map[string]any)["accessAllLibraries"] = true // Remote silently ignored the restriction.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /api/users":
			sendJSON(w, map[string]any{"users": []any{}})
		case "POST /api/users":
			sendJSON(w, map[string]any{"user": u})
		case "GET /api/users/user-1":
			sendJSON(w, u)
		case "DELETE /api/users/user-1":
			deleted = true
		default:
			w.WriteHeader(500)
			_, _ = w.Write([]byte("private-password http://private-host/ sentinel"))
		}
	}))
	defer server.Close()
	c := NewClient(server.URL, "sentinel")
	if _, err := c.CreateUser(context.Background(), "alice", "private-password", []string{"books"}); err == nil || !deleted {
		t.Fatalf("rollback: %v, deleted=%v", err, deleted)
	}
	_, err := c.Libraries(context.Background())
	if err == nil || strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "sentinel") {
		t.Fatalf("redaction: %v", err)
	}
}

func TestAuthenticationClosesOnlyTemporarySession(t *testing.T) {
	logout := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("stored administrator credential sent to sign-in")
		}
		if r.URL.Path == "/login" {
			if r.Header.Get("X-Return-Tokens") != "true" {
				t.Error("session not requested")
			}
			u := testUser()
			u["refreshToken"] = "temporary-session"
			sendJSON(w, map[string]any{"user": u})
		} else if r.URL.Path == "/logout" {
			logout = true
			if r.Header.Get("X-Refresh-Token") != "temporary-session" || r.URL.RawQuery != "" {
				t.Error("wrong logout scope")
			}
		} else {
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	u, err := NewClient(server.URL, "sentinel").Authenticate(context.Background(), "alice", "private-password")
	if err != nil || u.ID != "user-1" || !logout {
		t.Fatalf("sign in: %+v, %v, logout=%v", u, err, logout)
	}
}

func TestUnreadableCreationCleansUpOnlyPasswordVerifiedNewAccount(t *testing.T) {
	for _, owned := range []bool{true, false} {
		t.Run(fmt.Sprintf("our_account=%v", owned), func(t *testing.T) {
			created, deleted, loggedOut := false, false, false
			u := testUser()
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.Method + " " + r.URL.Path {
				case "GET /api/users":
					users := []any{}
					if created {
						users = append(users, u)
					}
					sendJSON(w, map[string]any{"users": users})
				case "POST /api/users":
					created = true
					_, _ = w.Write([]byte("unreadable"))
				case "POST /login":
					if !owned {
						w.WriteHeader(http.StatusUnauthorized)
						return
					}
					u["refreshToken"] = "temporary-session"
					sendJSON(w, map[string]any{"user": u})
				case "POST /logout":
					loggedOut = true
				case "GET /api/users/user-1":
					sendJSON(w, u)
				case "DELETE /api/users/user-1":
					deleted = true
				default:
					w.WriteHeader(404)
				}
			}))
			defer s.Close()
			_, err := NewClient(s.URL, "sentinel").CreateUser(context.Background(), "alice", "private-password", []string{"books"})
			if err == nil || deleted != owned || loggedOut != owned {
				t.Fatalf("unconfirmed creation: error=%v deleted=%v logout=%v", err, deleted, loggedOut)
			}
		})
	}
}

func TestRejectsRedirectsAndIncompleteResponses(t *testing.T) {
	for _, body := range []string{"{}", "null", "[]"} {
		t.Run(body, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
			defer s.Close()
			if _, err := NewClient(s.URL, "sentinel").GetUser(context.Background(), "user-1"); err == nil {
				t.Fatal("incomplete user accepted")
			}
		})
	}
	visits := 0
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { visits++ }))
	defer destination.Close()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, destination.URL, 302) }))
	defer s.Close()
	if _, err := NewClient(s.URL, "sentinel").Libraries(context.Background()); err == nil || visits != 0 {
		t.Fatal("redirect followed")
	}
}

// Run only against a disposable Audiobookshelf 2.36.0 server. This exercises
// real provisioning/authentication semantics without keeping any credentials
// or touching a user's media server. The test creates and removes its account.
func TestAudiobookshelfLiveSmoke(t *testing.T) {
	path := os.Getenv("CANTINARR_ABS_SMOKE_CONFIG")
	if path == "" {
		t.Skip("disposable Audiobookshelf config not supplied")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		URL       string `json:"url"`
		Key       string `json:"key"`
		LibraryID string `json:"library_id"`
		BookID    string `json:"book_id"`
	}
	if json.Unmarshal(data, &cfg) != nil || cfg.URL == "" || cfg.Key == "" {
		t.Fatal("invalid smoke configuration")
	}
	c, ctx := NewClient(cfg.URL, cfg.Key), context.Background()
	info, err := c.SystemInfo(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Verified Audiobookshelf %s administrator API key", info.Version)
	libraries, err := c.Libraries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, lib := range libraries {
		ids = append(ids, lib.ID)
	}
	u, err := c.CreateUser(ctx, "cantinarr-smoke-user", "smoke-only-disposable-password", ids)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := c.DeleteUser(context.Background(), u.ID); err != nil {
			t.Error(err)
		}
	})
	if _, err := c.Authenticate(ctx, u.Name, "smoke-only-disposable-password"); err != nil {
		t.Fatal(err)
	}
	for _, disabled := range []bool{true, false} {
		if err := c.SetDisabled(ctx, u.ID, disabled); err != nil {
			t.Fatal(err)
		}
		if _, err := c.Authenticate(ctx, u.Name, "smoke-only-disposable-password"); (err != nil) != disabled {
			t.Fatalf("disabled=%v authentication=%v", disabled, err)
		}
	}
	if err := c.SetLibraries(ctx, u.ID, ids); err != nil {
		t.Fatal(err)
	}
	t.Log("Verified account creation, linking authentication, disable/restore, and library policy")
	if cfg.LibraryID == "" || cfg.BookID == "" {
		t.Fatal("scanned smoke libraries not supplied")
	}
	if err := c.SetLibraries(ctx, u.ID, []string{cfg.LibraryID}); err != nil {
		t.Fatal(err)
	}
	query := mediaserver.BookQuery{Available: true, ASINs: []string{"B012345678"}, ISBNs: []string{"0306406152"}}
	items, err := c.FindBooks(ctx, u.ID, query)
	if err != nil || len(items) != 1 || items[0].ID != cfg.BookID {
		t.Fatalf("restricted exact matching: %d items, %v", len(items), err)
	}
	if err := c.SetLibraries(ctx, u.ID, nil); err != nil {
		t.Fatal(err)
	}
	items, err = c.FindBooks(ctx, u.ID, query)
	if err != nil || len(items) != 2 || items[0].ID == items[1].ID {
		t.Fatalf("distinct copies: %d items, %v", len(items), err)
	}
	for _, policy := range []struct {
		permissions map[string]bool
		wantMatch   bool
	}{
		{map[string]bool{"accessAllTags": false, "selectedTagsNotAccessible": true}, false},
		{map[string]bool{"accessAllTags": false, "selectedTagsNotAccessible": false}, true},
	} {
		if err := c.do(ctx, "PATCH", "/api/users/"+u.ID, map[string]any{"permissions": policy.permissions, "itemTagsSelected": []string{"smoke"}}, nil); err != nil {
			t.Fatal(err)
		}
		items, err = c.FindBooks(ctx, u.ID, query)
		if policy.wantMatch && (err != nil || len(items) != 2) || !policy.wantMatch && !errors.Is(err, mediaserver.ErrItemUnverified) {
			t.Fatalf("live tag policy: %d items, %v", len(items), err)
		}
	}
	resp, err := c.httpClient.Get(cfg.URL + "/item/" + cfg.BookID)
	if err != nil {
		t.Fatal("browser item URL could not be reached")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(resp.Header.Get("Content-Type"), "text/html") {
		t.Fatal("browser item URL did not serve the Audiobookshelf web client")
	}
	t.Log("Verified scanned audio, exact ISBN/ASIN matching, distinct copies, live library/tag restrictions, and browser item route")
}
