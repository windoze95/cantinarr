package mediaaccess

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/windoze95/cantinarr-server/internal/auth"
	"golang.org/x/oauth2"
)

// Runs both sides over HTTP with the production PIN client, directory adapter,
// auth handlers, encrypted instance store and SQLite transactions. The simulated
// friend starts without a share, accepts one, loses it, and is finally unlinked.
func TestPlexSimulatedFriendLifecycle(t *testing.T) {
	e := newEnv(t)
	tv, plexServer := newFakePlexTV(t)
	instanceID := e.plexServer("Test Plex", plexServer.URL)
	s := auth.NewService(e.db, "simulation-secret")
	s.SetPlexBaseURL(plexServer.URL)
	e.svc.SetPlexAuth(s)
	if err := s.EnsureAdmin("simulation-password"); err != nil {
		t.Fatal(err)
	}
	admin, err := s.Login("admin", "simulation-password", "simulation", "")
	if err != nil {
		t.Fatal(err)
	}
	h := auth.NewHandler(s)
	r := chi.NewRouter()
	r.Post("/api/auth/plex/begin", h.PlexBegin)
	r.Post("/api/auth/plex/check", h.PlexCheck)
	r.Post("/api/auth/plex/exchange", h.PlexExchange)
	r.Group(func(r chi.Router) {
		r.Use(s.AuthMiddleware)
		r.Put("/api/admin/plex-auth", h.PlexConfigSave)
		r.Delete("/api/admin/users/{userID}/plex", h.PlexUnlink)
	})
	backend := httptest.NewServer(r)
	defer backend.Close()
	request := func(method, path, token string, body any) (int, map[string]any) {
		t.Helper()
		raw, _ := json.Marshal(body)
		req, err := http.NewRequest(method, backend.URL+path, bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var result map[string]any
		if json.NewDecoder(resp.Body).Decode(&result) != nil {
			t.Fatal("invalid response")
		}
		return resp.StatusCode, result
	}
	status, out := request("PUT", "/api/admin/plex-auth", admin.AccessToken, map[string]any{"enabled": true, "auto_create": true})
	if status != 200 {
		t.Fatal(status, out)
	}
	tv.approve(userToken)
	login := func() (int, map[string]any) {
		t.Helper()
		v := oauth2.GenerateVerifier()
		status, b := request("POST", "/api/auth/plex/begin", "", map[string]string{"client": "web", "challenge": oauth2.S256ChallengeFromVerifier(v)})
		if status != 200 {
			t.Fatal(status, b)
		}
		status, c := request("POST", "/api/auth/plex/check", "", map[string]any{"flow": b["flow"], "verifier": v})
		if status != 200 {
			t.Fatal(status, c)
		}
		encoded, _ := json.Marshal(c)
		if strings.Contains(string(encoded), userToken) {
			t.Fatal("Plex token escaped in check response")
		}
		return request("POST", "/api/auth/plex/exchange", "", map[string]any{"flow": b["flow"], "verifier": v, "code": c["code"]})
	}
	for _, share := range []string{"", `<SharedServer id="5" userID="2" email="Rey@Example.com" username="rey" accepted="0"/>`} {
		tv.mu.Lock()
		tv.shares = share
		tv.mu.Unlock()
		status, out = login()
		if status != 403 {
			t.Fatalf("friend/pending share qualified: %d %#v", status, out)
		}
	}
	tv.mu.Lock()
	tv.shares = `<SharedServer id="5" userID="2" email="Rey@Example.com" username="rey" accepted="1"/>`
	tv.mu.Unlock()
	status, out = login()
	if status != 200 {
		t.Fatal(status, out)
	}
	userID := int64(out["user"].(map[string]any)["id"].(float64))
	refresh := out["refresh_token"].(string)
	var grants int
	e.db.QueryRow("SELECT COUNT(*) FROM user_instance_grants WHERE user_id=? AND instance_id=?", userID, instanceID).Scan(&grants)
	if grants != 1 {
		t.Fatal("accepted share not adopted")
	}
	tv.mu.Lock()
	tv.shares = ""
	tv.mu.Unlock()
	status, out = login()
	if status != 200 {
		t.Fatal("existing identity depended on removed share", status, out)
	}
	if _, err = s.Refresh(refresh); err != nil {
		t.Fatal(err)
	}
	status, out = request("DELETE", "/api/admin/users/"+itoa(userID)+"/plex", admin.AccessToken, nil)
	if status != 200 {
		t.Fatal(status, out)
	}
	if _, err = s.Refresh(refresh); err == nil {
		t.Fatal("unlinked session survived")
	}
	status, out = login()
	if status != 403 {
		t.Fatal("removed share permitted new signup", status, out)
	}
	// Fresh ownership, even though the owner is absent from the share list.
	tv.approve(ownerToken)
	status, out = login()
	if status != 200 {
		t.Fatal("verified owner could not sign up", status, out)
	}
	if tv.count("POST /api/v2/shared_servers") != 0 || tv.count("DELETE /api/servers") != 0 || tv.count("PUT /api/servers") != 0 {
		t.Fatal("signup mutated Plex sharing")
	}
	if tv.count("DELETE /devices/") != 6 {
		t.Fatal("temporary tokens not cleaned up", tv.requests)
	}
}

func TestPlexLegacyPINRequiresInitiatingSessionAndAdminImportsRecordIdentity(t *testing.T) {
	e := newEnv(t)
	tv, server := newFakePlexTV(t)
	instanceID := e.plexServer("Test Plex", server.URL)
	s := auth.NewService(e.db, "simulation-secret")
	e.svc.SetPlexAuth(s)
	e.svc.SetUserCreator(s)
	e.svc.SetPlexBaseURL(server.URL)
	if err := s.EnsureAdmin("simulation-password"); err != nil {
		t.Fatal(err)
	}
	session, err := s.Login("admin", "simulation-password", "simulation", "")
	if err != nil {
		t.Fatal(err)
	}
	claims := &auth.Claims{UserID: session.User.ID, DeviceID: session.DeviceID}
	start, err := e.svc.PlexSignInBegin(context.Background(), claims.UserID, claims)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.RevokeDevice(claims.DeviceID); err != nil {
		t.Fatal(err)
	}
	tv.approve(userToken)
	if _, err = e.svc.PlexSignInCheck(context.Background(), claims.UserID, start.PinID, "admin"); err == nil {
		t.Fatal("revoked initiating session linked identity")
	}
	tv.mu.Lock()
	tv.shares = `<SharedServer id="5" userID="2" email="rey@example.com" username="rey" accepted="1"/>`
	tv.mu.Unlock()
	rows, err := e.svc.ImportAccounts(context.Background(), claims.UserID, instanceID, "http://localhost", []string{"rey@example.com"})
	if err != nil || len(rows) != 1 || !rows[0].Linked || rows[0].PlexIdentityError != "" {
		t.Fatal(rows, err)
	}
	var id int64
	if e.db.QueryRow("SELECT plex_account_id FROM plex_identities WHERE user_id=?", rows[0].UserID).Scan(&id) != nil || id != 2 {
		t.Fatal("fresh import did not establish numeric identity")
	}
	// A namesake has no authority over another account selected from Plex.
	tv.mu.Lock()
	tv.shares = `<SharedServer id="6" userID="3" email="different@example.com" username="rey" accepted="1"/>`
	tv.mu.Unlock()
	rows, err = e.svc.ImportAccounts(context.Background(), claims.UserID, instanceID, "http://localhost", []string{"different@example.com"})
	if err != nil || len(rows) != 1 || rows[0].Error != "username_conflict" {
		t.Fatal("import took over a namesake", rows, err)
	}
}
