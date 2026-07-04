package push

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/auth"
)

// withUser returns a request carrying authenticated claims for userID.
func withUser(r *http.Request, userID int64) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), auth.ClaimsKey, &auth.Claims{UserID: userID, Role: "user"}))
}

func TestGetPreferencesDefaults(t *testing.T) {
	database, err := dbOpen(t)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (7, 'alice', '', 'user')")
	h := NewHandler(database, nil, nil)

	rr := httptest.NewRecorder()
	req := withUser(httptest.NewRequest(http.MethodGet, "/api/notifications/preferences", nil), 7)
	h.GetPreferences(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var got Prefs
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := Prefs{RequestDecision: false, RequestPending: true, NewMovie: true, NewEpisode: true, IssueCreated: true, AgentActionPending: true, PlexAccessRequest: true, PlexInviteSent: true}
	if got != want {
		t.Errorf("prefs = %+v, want %+v", got, want)
	}
}

func TestGetPreferencesUnauthorized(t *testing.T) {
	database, err := dbOpen(t)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	h := NewHandler(database, nil, nil)

	rr := httptest.NewRecorder()
	// No claims in context.
	req := httptest.NewRequest(http.MethodGet, "/api/notifications/preferences", nil)
	h.GetPreferences(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rr.Code)
	}
}

func TestUpdatePreferencesRoundTrip(t *testing.T) {
	database, err := dbOpen(t)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (7, 'alice', '', 'user')")
	h := NewHandler(database, nil, nil)

	body := `{"request_decision":true,"request_pending":false,"new_movie":false,"new_episode":true}`
	rr := httptest.NewRecorder()
	req := withUser(httptest.NewRequest(http.MethodPut, "/api/notifications/preferences", strings.NewReader(body)), 7)
	h.UpdatePreferences(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var got Prefs
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := Prefs{RequestDecision: true, RequestPending: false, NewMovie: false, NewEpisode: true}
	if got != want {
		t.Errorf("echoed prefs = %+v, want %+v", got, want)
	}

	// The change must persist for a subsequent GET.
	rr2 := httptest.NewRecorder()
	h.GetPreferences(rr2, withUser(httptest.NewRequest(http.MethodGet, "/", nil), 7))
	var stored Prefs
	if err := json.Unmarshal(rr2.Body.Bytes(), &stored); err != nil {
		t.Fatalf("decode stored: %v", err)
	}
	if stored != want {
		t.Errorf("stored prefs = %+v, want %+v", stored, want)
	}
}

func TestRegisterPushDisabledStoresLocallyAndIsNoop(t *testing.T) {
	database, err := dbOpen(t)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	// A user owning a device, but push disabled (nil manager).
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (7, 'alice', '', 'user')")
	mustExec(t, database, "INSERT INTO devices (id, user_id, device_name) VALUES ('dev-1', 7, 'iPhone')")
	h := NewHandler(database, nil, nil)

	body := `{"device_id":"dev-1","apns_token":"tok-1","platform":"ios"}`
	rr := httptest.NewRecorder()
	req := withUser(httptest.NewRequest(http.MethodPost, "/api/devices/push-token", strings.NewReader(body)), 7)
	h.Register(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	// The local row must be stored even with push disabled.
	var count int
	if err := database.QueryRow("SELECT COUNT(*) FROM push_tokens WHERE device_id = 'dev-1' AND token = 'tok-1'").Scan(&count); err != nil {
		t.Fatalf("count tokens: %v", err)
	}
	if count != 1 {
		t.Errorf("stored token rows = %d, want 1", count)
	}
}

func TestTestPushSendsToCaller(t *testing.T) {
	database, err := dbOpen(t)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (7, 'alice', '', 'user')")

	// Mock gateway that records the send body and reports one delivery.
	gotBody := make(chan map[string]any, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/notifications" {
			raw, _ := io.ReadAll(r.Body)
			body := map[string]any{}
			_ = json.Unmarshal(raw, &body)
			gotBody <- body
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"sent":1,"failed":0}`)
	}))
	t.Cleanup(srv.Close)

	mgr := NewManager(database, nil, srv.URL, "pgk_test", "", "Cantinarr", nil)
	h := NewHandler(database, mgr, nil)

	rr := httptest.NewRecorder()
	req := withUser(httptest.NewRequest(http.MethodPost, "/api/notifications/test", nil), 7)
	h.TestPush(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var counts map[string]int
	if err := json.Unmarshal(rr.Body.Bytes(), &counts); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if counts["sent"] != 1 || counts["failed"] != 0 {
		t.Errorf("counts = %v, want sent=1 failed=0", counts)
	}

	// The push must target the caller's own user id.
	select {
	case body := <-gotBody:
		to, _ := body["to"].(map[string]any)
		ids, _ := to["user_ids"].([]any)
		if len(ids) != 1 || ids[0] != "7" {
			t.Errorf("user_ids = %v, want [\"7\"]", ids)
		}
	default:
		t.Fatal("gateway never received a notification")
	}
}

func TestTestPushNotConfigured(t *testing.T) {
	database, err := dbOpen(t)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (7, 'alice', '', 'user')")
	// nil manager => push disabled.
	h := NewHandler(database, nil, nil)

	rr := httptest.NewRecorder()
	req := withUser(httptest.NewRequest(http.MethodPost, "/api/notifications/test", nil), 7)
	h.TestPush(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%s", rr.Code, rr.Body.String())
	}
	var resp map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["error"] != "push not configured" {
		t.Errorf("error = %q, want \"push not configured\"", resp["error"])
	}
}

func TestTestPushUnauthorized(t *testing.T) {
	database, err := dbOpen(t)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	h := NewHandler(database, nil, nil)

	rr := httptest.NewRecorder()
	// No claims in context.
	req := httptest.NewRequest(http.MethodPost, "/api/notifications/test", nil)
	h.TestPush(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rr.Code)
	}
}

func TestUpdatePreferencesInvalidBody(t *testing.T) {
	database, err := dbOpen(t)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	mustExec(t, database, "INSERT INTO users (id, username, password_hash, role) VALUES (7, 'alice', '', 'user')")
	h := NewHandler(database, nil, nil)

	rr := httptest.NewRecorder()
	req := withUser(httptest.NewRequest(http.MethodPut, "/", strings.NewReader("not json")), 7)
	h.UpdatePreferences(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}
