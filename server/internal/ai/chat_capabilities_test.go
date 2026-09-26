package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/credentials"
	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/mcp"
	"github.com/windoze95/cantinarr-server/internal/request"
	"github.com/windoze95/cantinarr-server/internal/secrets"
)

func TestAISettingsIncludesCurrentCallerCapabilitiesAndNeutralFailure(t *testing.T) {
	h, creds, database, userID := newResolverTestHandler(t)
	if err := creds.SetUserAIProfile(userID, credentials.AIProviderAnthropic, "personal-model", "personal-secret"); err != nil {
		t.Fatal(err)
	}
	cipher, err := secrets.NewCipher(bytes.Repeat([]byte{0x27}, 32))
	if err != nil {
		t.Fatal(err)
	}
	store := instance.NewStore(database, cipher)
	book := &instance.Instance{ServiceType: "chaptarr", Name: "private-books", URL: "http://private-host:8787", APIKey: "private-key"}
	if err := store.Create(book); err != nil {
		t.Fatal(err)
	}
	if err := store.SetUserGrants(userID, map[string][]string{"chaptarr": {book.ID}}); err != nil {
		t.Fatal(err)
	}
	registry := instance.NewRegistry(store)
	h.toolServer = mcp.NewToolServer(creds, request.NewService(database, registry, nil, nil), registry, nil)
	h.toolServer.SetCallAuthorizer(func(_ context.Context, call mcp.CallContext) (string, error) {
		if call.UserID != userID || call.DeviceID != "caller-device" || call.Origin != mcp.OriginInteractiveChat {
			t.Fatalf("wrong capability identity: %+v", call)
		}
		return auth.RoleUser, nil
	})
	read := func() *mcp.ChatCapabilities {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/ai/settings", nil)
		req = req.WithContext(context.WithValue(req.Context(), auth.ClaimsKey, &auth.Claims{UserID: userID, Role: auth.RoleAdmin, DeviceID: "caller-device"}))
		rec := httptest.NewRecorder()
		h.AISettings(rec, req)
		if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("settings: %d %s", rec.Code, rec.Body.String())
		}
		for _, private := range []string{"private-host", "private-key", "private-books", "personal-secret", book.ID} {
			if strings.Contains(rec.Body.String(), private) {
				t.Fatalf("capabilities leaked %q", private)
			}
		}
		var response struct {
			Capabilities *mcp.ChatCapabilities `json:"chat_capabilities"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		return response.Capabilities
	}
	caps := read()
	if caps == nil || !reflect.DeepEqual(caps.DiscoverMediaTypes, []string{"book"}) || !reflect.DeepEqual(caps.RequestMediaTypes, []string{"book"}) || caps.ConfigureServices || caps.ManageDownloads {
		t.Fatalf("caller capabilities = %+v", caps)
	}
	if err := h.toolServer.SetToolEnabled("search_books", false); err != nil {
		t.Fatal(err)
	}
	if len(read().DiscoverMediaTypes) != 0 {
		t.Fatal("disabled search still advertised")
	}
	if _, err := database.Exec(`ALTER TABLE service_instances RENAME TO unavailable_instances`); err != nil {
		t.Fatal(err)
	}
	if caps := read(); caps != nil {
		t.Fatalf("unknown inventory reported capabilities: %+v", caps)
	}
}
