package push

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/db"
)

func TestMediaAccessNotificationsNameServiceAndNextStep(t *testing.T) {
	for _, tc := range []struct{ service, state, next string }{
		{"jellyfin", "granted", "set up or view your account"},
		{"emby", "granted", "set up or view your account"},
		{"audiobookshelf", "granted", "set up or view your account"},
		{"plex", "invite_pending", "Accept the invitation in Plex or check your email"},
		{"plex", "ready", "is ready"},
	} {
		t.Run(tc.service+"/"+tc.state, func(t *testing.T) {
			database, err := dbOpen(t)
			if err != nil {
				t.Fatal(err)
			}
			mustExec(t, database, "INSERT INTO users(id,username,password_hash) VALUES(1,'alice',''),(2,'bob','')")
			manager, capture := newNotifierTestGateway(t, database)
			n := NewNotifier(database, manager, nil)
			n.NotifyUser(1, CategoryMediaServerAccess, map[string]interface{}{
				"instance_id": "home", "server_name": "Family", "service_type": tc.service, "access_state": tc.state,
				"password": "must-not-leak", "email": "private@example.com", "url": "http://private.internal",
			})
			got := capture.waitForNotification(t)
			if !reflect.DeepEqual(userIDsOf(t, got), []string{"1"}) {
				t.Fatalf("audience = %+v", got)
			}
			notification := got["notification"].(map[string]any)
			body := notification["body"].(string)
			if notification["title"] != "Media server access" || !strings.Contains(body, "Family") || !strings.Contains(strings.ToLower(body), tc.service) || !strings.Contains(body, tc.next) {
				t.Fatalf("wrong service or next step: %+v", notification)
			}
			if tc.state != "invite_pending" && (strings.Contains(body, "email") || strings.Contains(body, "invitation")) {
				t.Fatalf("account access incorrectly asks to accept an invite: %s", body)
			}
			data := got["data"].(map[string]any)
			if !reflect.DeepEqual(data, map[string]any{"type": CategoryMediaServerAccess, "instance_id": "home"}) {
				t.Fatalf("unexpected push data: %+v", data)
			}
		})
	}
}

func TestMediaAccessPreferenceMigrationPreservesOptOutOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	database, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, database, "INSERT INTO users(id,username,password_hash) VALUES(1,'alice',''),(2,'bob','')")
	mustExec(t, database, "INSERT INTO notification_prefs(user_id,plex_invite_sent) VALUES(1,0),(2,1)")
	mustExec(t, database, "ALTER TABLE notification_prefs DROP COLUMN media_server_access")
	database.Close()
	database, err = db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	store := NewPrefsStore(database)
	for userID, want := range map[int64]bool{1: false, 2: true} {
		prefs, err := store.Get(userID)
		if err != nil || prefs.MediaServerAccess != want {
			t.Fatalf("migration: %+v, %v", prefs, err)
		}
		prefs.MediaServerAccess = !want
		if err := store.Set(userID, prefs); err != nil {
			t.Fatal(err)
		}
	}
	database.Close()
	database, err = db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	store = NewPrefsStore(database)
	for userID, want := range map[int64]bool{1: true, 2: false} {
		prefs, err := store.Get(userID)
		if err != nil || prefs.MediaServerAccess != want {
			t.Fatalf("restart reset saved choice: %+v, %v", prefs, err)
		}
	}
}

func TestMediaAccessPreferenceAcceptsLegacySavesAndPreservesOmissions(t *testing.T) {
	database, err := dbOpen(t)
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, database, "INSERT INTO users(id,username,password_hash) VALUES(7,'alice','')")
	h := NewHandler(database, nil, nil)
	for _, tc := range []struct {
		body string
		want bool
	}{
		{`{"plex_invite_sent":false}`, false},
		{`{"new_movie":true}`, false},
		{`{"media_server_access":true,"plex_invite_sent":false}`, true},
		{`{"new_movie":false}`, true},
		{`{"media_server_access":false}`, false},
	} {
		w := httptest.NewRecorder()
		h.UpdatePreferences(w, withUser(httptest.NewRequest("PUT", "/", strings.NewReader(tc.body)), 7))
		var got preferencesResponse
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil || got.MediaServerAccess != tc.want || got.PlexInviteSent != tc.want {
			t.Fatalf("save %s: %d %s", tc.body, w.Code, w.Body.String())
		}
	}
}

func TestMediaAccessPolicyPreservesLegacyServerOptOut(t *testing.T) {
	database, err := dbOpen(t)
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, database, `INSERT INTO settings(key,value) VALUES(?,?)`, policyKey, `{"enabled":true,"categories":{"plex_invite_sent":false}}`)
	store := NewPrefsStore(database)
	policy, err := store.Policy()
	if err != nil || policy.Categories[CategoryMediaServerAccess] || !policy.Categories[CategoryNewMovie] {
		t.Fatalf("legacy policy = %+v, %v", policy, err)
	}
	if _, exists := policy.Categories["plex_invite_sent"]; exists {
		t.Fatal("legacy category leaked into policy API")
	}
	if err := store.setPolicy(nil, map[string]bool{CategoryMediaServerAccess: true}); err != nil {
		t.Fatal(err)
	}
	policy, err = store.Policy()
	if err != nil || !policy.Categories[CategoryMediaServerAccess] {
		t.Fatalf("new policy lost: %+v, %v", policy, err)
	}
}
