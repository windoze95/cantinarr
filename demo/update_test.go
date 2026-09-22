package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func demoTestLogin(t *testing.T, router http.Handler, username string) string {
	t.Helper()
	body := bytes.NewBufferString(`{"username":"` + username + `","password":"demo","device_name":"test","hardware_id":"test-` + username + `","platform":"test"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", body)
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("login status = %d: %s", res.Code, res.Body.String())
	}
	var payload map[string]any
	if json.Unmarshal(res.Body.Bytes(), &payload) != nil {
		t.Fatal("login returned invalid JSON")
	}
	token, _ := payload["access_token"].(string)
	if token == "" {
		t.Fatal("login returned no access_token")
	}
	return token
}

func demoTestRequest(t *testing.T, router http.Handler, method, path, token string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)
	return res
}

func TestCurrentAppDemoSurfaces(t *testing.T) {
	router := buildRouter()
	admin := demoTestLogin(t, router, "admin")
	user := demoTestLogin(t, router, "user")

	res := demoTestRequest(t, router, http.MethodGet, "/api/config", user, nil)
	if res.Code != http.StatusOK {
		t.Fatalf("config status = %d: %s", res.Code, res.Body.String())
	}
	var config map[string]any
	_ = json.Unmarshal(res.Body.Bytes(), &config)
	if config["downloads_activity"] != true || config["tv_library_navigation"] != true {
		t.Fatalf("current capabilities missing: %s", res.Body.String())
	}

	res = demoTestRequest(t, router, http.MethodGet, "/api/downloads/activity?scope=mine", user, nil)
	if res.Code != http.StatusOK {
		t.Fatalf("download activity status = %d: %s", res.Code, res.Body.String())
	}
	var activity struct {
		Count  *int  `json:"count"`
		Groups []any `json:"groups"`
		Jobs   []any `json:"jobs"`
	}
	_ = json.Unmarshal(res.Body.Bytes(), &activity)
	if activity.Count == nil || *activity.Count == 0 || len(activity.Groups) == 0 || len(activity.Jobs) == 0 {
		t.Fatalf("download activity is empty: %s", res.Body.String())
	}

	res = demoTestRequest(t, router, http.MethodGet, "/api/tdarr/"+instTdarr+"/activity", admin, nil)
	if res.Code != http.StatusOK || !bytes.Contains(res.Body.Bytes(), []byte(`"nodes"`)) {
		t.Fatalf("Tdarr activity status = %d: %s", res.Code, res.Body.String())
	}

	res = demoTestRequest(t, router, http.MethodGet, "/api/requests/tv-library?instance_id="+instSonarr+"&series_id=5", user, nil)
	if res.Code != http.StatusOK || !bytes.Contains(res.Body.Bytes(), []byte(`"season_number":3`)) || !bytes.Contains(res.Body.Bytes(), []byte(`"revision"`)) {
		t.Fatalf("TV library detail status = %d: %s", res.Code, res.Body.String())
	}

	res = demoTestRequest(t, router, http.MethodGet, "/api/issues/1", admin, nil)
	if res.Code != http.StatusOK || !bytes.Contains(res.Body.Bytes(), []byte(`"can_reopen":true`)) {
		t.Fatalf("issue detail status = %d: %s", res.Code, res.Body.String())
	}
	res = demoTestRequest(t, router, http.MethodPost, "/api/admin/issues/1/reopen", admin, nil)
	if res.Code != http.StatusOK || !bytes.Contains(res.Body.Bytes(), []byte(`"status":"needs_admin"`)) {
		t.Fatalf("issue reopen status = %d: %s", res.Code, res.Body.String())
	}
}

func TestSeerrCompatibleDemoKeyAndStatus(t *testing.T) {
	router := buildRouter()
	admin := demoTestLogin(t, router, "admin")
	res := demoTestRequest(t, router, http.MethodPost, "/api/admin/seerr-api", admin, nil)
	if res.Code != http.StatusOK {
		t.Fatalf("issue key status = %d: %s", res.Code, res.Body.String())
	}
	var issued struct {
		Configured bool   `json:"configured"`
		APIKey     string `json:"api_key"`
	}
	_ = json.Unmarshal(res.Body.Bytes(), &issued)
	if !issued.Configured || issued.APIKey == "" {
		t.Fatalf("key not issued: %s", res.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	req.Header.Set("X-Api-Key", issued.APIKey)
	res = httptest.NewRecorder()
	router.ServeHTTP(res, req)
	if res.Code != http.StatusOK || !bytes.Contains(res.Body.Bytes(), []byte(`"version":"demo"`)) {
		t.Fatalf("Seerr status = %d: %s", res.Code, res.Body.String())
	}

	res = demoTestRequest(t, router, http.MethodGet, "/api/v1/request/count", "", nil)
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("Seerr API without key status = %d, want 401", res.Code)
	}
}
