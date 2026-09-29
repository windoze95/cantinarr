package request

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/instance"
)

// The global editor must read configured defaults without impersonating a
// requester. User 0 has no grants; real requesters still need theirs.
func TestAdminSettingsProfilesUseGlobalDefaults(t *testing.T) {
	radarr := jsonServer(t, map[string]string{
		"/api/v3/qualityprofile": `[{"id":7,"name":"HD-1080p"}]`,
	})
	sonarr := jsonServer(t, map[string]string{
		"/api/v3/qualityprofile": `[{"id":9,"name":"HD TV"}]`,
	})
	personal := jsonServer(t, map[string]string{
		"/api/v3/qualityprofile": `[{"id":42,"name":"Personal library"}]`,
	})
	s, uid, store, radarrID, personalRadarrID := newTwoRadarrTestService(t, radarr.URL, personal.URL)
	globalSonarr := &instance.Instance{ServiceType: "sonarr", Name: "TV", URL: sonarr.URL, APIKey: "key", IsDefault: true}
	personalSonarr := &instance.Instance{ServiceType: "sonarr", Name: "Personal TV", URL: personal.URL, APIKey: "key"}
	for _, inst := range []*instance.Instance{globalSonarr, personalSonarr} {
		if err := store.Create(inst); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.SetUserGrants(uid, map[string][]string{
		"radarr": {personalRadarrID}, "sonarr": {personalSonarr.ID},
	}); err != nil {
		t.Fatal(err)
	}
	for service, id := range map[string]string{"radarr": personalRadarrID, "sonarr": personalSonarr.ID} {
		if err := store.SetUserDefault(uid, service, id); err != nil {
			t.Fatal(err)
		}
	}

	handler := NewHandler(s)
	checkView := func(w *httptest.ResponseRecorder, want GlobalSettings) {
		t.Helper()
		if w.Code != http.StatusOK {
			t.Fatalf("settings status = %d: %s", w.Code, w.Body.String())
		}
		var view AdminSettingsView
		if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil {
			t.Fatal(err)
		}
		if view.Settings != want {
			t.Errorf("settings = %+v, want %+v", view.Settings, want)
		}
		if !reflect.DeepEqual(view.RadarrProfiles, []QualityProfile{{ID: 7, Name: "HD-1080p"}}) {
			t.Errorf("global Radarr profiles = %+v, want [7 HD-1080p]", view.RadarrProfiles)
		}
		if !reflect.DeepEqual(view.SonarrProfiles, []QualityProfile{{ID: 9, Name: "HD TV"}}) {
			t.Errorf("global Sonarr profiles = %+v, want [9 HD TV]", view.SonarrProfiles)
		}
	}
	readSettings := func(want GlobalSettings) {
		t.Helper()
		w := httptest.NewRecorder()
		handler.GetSettings(w, httptest.NewRequest(http.MethodGet, "/api/admin/request-settings", nil))
		checkView(w, want)
	}

	// Profiles remain selectable when requester quality choice is disabled.
	readSettings(defaultGlobalSettings())
	settings := defaultGlobalSettings()
	settings.DefaultQualityRadarr, settings.DefaultQualitySonarr = 7, 9
	settings.AllowQualityChoice = true
	body, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	handler.UpdateSettings(w, httptest.NewRequest(http.MethodPut, "/api/admin/request-settings", strings.NewReader(string(body))))
	checkView(w, settings)
	readSettings(settings)

	// The editor's global read cannot change a requester's source or access.
	for mediaType, globalID := range map[string]string{"movie": radarrID, "tv": globalSonarr.ID} {
		opts, err := s.GetRequestOptions(uid, false, mediaType, "")
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(opts.QualityProfiles, []QualityProfile{{ID: 42, Name: "Personal library"}}) {
			t.Errorf("%s personal profiles = %+v, want [42 Personal library]", mediaType, opts.QualityProfiles)
		}
		if _, err := s.GetRequestOptions(uid, false, mediaType, globalID); !errors.Is(err, ErrArrInstanceForbidden) {
			t.Errorf("%s unassigned global instance error = %v, want forbidden", mediaType, err)
		}
	}
}
