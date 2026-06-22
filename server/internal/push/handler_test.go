package push

import (
	"context"
	"encoding/json"
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
	want := Prefs{RequestDecision: false, RequestPending: true, NewMovie: true, NewEpisode: true}
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
