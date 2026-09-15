package instance

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/windoze95/cantinarr-server/internal/tdarr"
)

func TestTdarrCredentialsTestSaveAndCacheInvalidation(t *testing.T) {
	var key string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != key {
			w.WriteHeader(401)
			return
		}
		switch r.URL.Path {
		case "/api/v2/status":
			io.WriteString(w, `{"status":"good","version":"2.62.01"}`)
		case "/api/v2/get-nodes":
			io.WriteString(w, `{}`)
		case "/api/v2/cruddb":
			var body struct{ Data struct{ Collection string } }
			json.NewDecoder(r.Body).Decode(&body)
			if body.Data.Collection == "LibrarySettingsJSONDB" {
				io.WriteString(w, `[]`)
			} else {
				io.WriteString(w, `{"totalFileCount":0}`)
			}
		default:
			t.Errorf("unexpected upstream path %s", r.URL.Path)
		}
	}))
	defer upstream.Close()
	s := newTestStore(t)
	registry := NewRegistry(s)
	h := NewHandler(s, registry)
	r := chi.NewRouter()
	r.Post("/instances", h.Create)
	r.Post("/instances/test", h.TestConnection)
	r.Put("/instances/{instanceID}", h.Update)
	td := tdarr.NewHandler(s, registry)
	r.Get("/tdarr/{instanceID}/activity", td.Serve)
	do := func(method, path string, body map[string]any) *httptest.ResponseRecorder {
		b, _ := json.Marshal(body)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(string(b))))
		return rec
	}
	body := map[string]any{"service_type": "tdarr", "name": "Tdarr", "url": upstream.URL}
	created := do("POST", "/instances", body)
	if created.Code != 201 {
		t.Fatalf("create %d %s", created.Code, created.Body.String())
	}
	var info instanceResponse
	json.Unmarshal(created.Body.Bytes(), &info)
	body["id"] = info.ID
	old, err := registry.GetTdarrClient(info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = old.Activity(context.Background()); err != nil {
		t.Fatal(err)
	}
	key = "TDARR_SECRET_SENTINEL"
	body["api_key"] = key
	if rec := do("PUT", "/instances/"+info.ID, body); rec.Code != 200 || strings.Contains(rec.Body.String(), key) {
		t.Fatalf("save %d %s", rec.Code, rec.Body.String())
	}
	newClient, err := registry.GetTdarrClient(info.ID)
	if err != nil || newClient == old {
		t.Fatal("connection cache survived key change")
	}
	body["api_key"] = ""
	if rec := do("POST", "/instances/test", body); rec.Code != 204 {
		t.Fatalf("blank edit lost stored key: %d %s", rec.Code, rec.Body.String())
	}
	var encrypted string
	s.db.QueryRow("SELECT api_key FROM service_instances WHERE id=?", info.ID).Scan(&encrypted)
	if encrypted == key || encrypted == "" {
		t.Fatal("key not encrypted")
	}
	key = ""
	body["clear_api_key"] = true
	if rec := do("POST", "/instances/test", body); rec.Code != 204 {
		t.Fatalf("test did not clear key: %d %s", rec.Code, rec.Body.String())
	}
	stored, _ := s.Get(info.ID)
	if stored.APIKey == "" {
		t.Fatal("test persisted key removal")
	}
	if rec := do("PUT", "/instances/"+info.ID, body); rec.Code != 200 {
		t.Fatalf("clear %d %s", rec.Code, rec.Body.String())
	}
	stored, _ = s.Get(info.ID)
	if stored.APIKey != "" {
		t.Fatal("save retained removed key")
	}
	wrong := mkInstance(t, s, "radarr", "Movies")
	for _, tc := range []struct {
		id     string
		status int
	}{{wrong, 400}, {"missing", 404}, {info.ID, 200}} {
		if rec := do("GET", "/tdarr/"+tc.id+"/activity", nil); rec.Code != tc.status {
			t.Fatalf("lookup %s = %d", tc.id, rec.Code)
		}
	}
}

func TestTdarrClearKeyIsExplicitAndServiceScoped(t *testing.T) {
	for _, tc := range []struct {
		typ, key    string
		clear, fail bool
	}{
		{"tdarr", "", true, false}, {"tdarr", "replacement", true, true}, {"radarr", "", true, true}, {"radarr", "", false, false},
	} {
		inst := &Instance{ServiceType: tc.typ, APIKey: "stored"}
		err := applyClearAPIKey(inst, &instanceRequest{Instance: Instance{APIKey: tc.key}, ClearAPIKey: tc.clear})
		if (err != nil) != tc.fail {
			t.Errorf("%+v: %v", tc, err)
		}
		if !tc.clear && inst.APIKey != "stored" {
			t.Fatal("implicit key removal")
		}
	}
}

func TestTdarrInFlightReadCannotRepopulateEditedOrDeletedInstance(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	oldServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		io.WriteString(w, `{"old":{"nodeName":"Old server","workers":{}}}`)
	}))
	defer oldServer.Close()
	newServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"new":{"nodeName":"New server","workers":{}}}`)
	}))
	defer newServer.Close()
	s := newTestStore(t)
	inst := &Instance{ID: "tdarr", ServiceType: "tdarr", Name: "Tdarr", URL: oldServer.URL}
	if err := s.Create(inst); err != nil {
		t.Fatal(err)
	}
	r := NewRegistry(s)
	old, err := r.GetTdarrClient(inst.ID)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := old.Activity(context.Background()); done <- err }()
	<-entered
	inst.URL = newServer.URL
	if err := s.Update(inst); err != nil {
		close(release)
		t.Fatal(err)
	}
	r.InvalidateClient(inst.ID)
	current, err := r.GetTdarrClient(inst.ID)
	close(release)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	activity, err := current.Activity(context.Background())
	if err != nil || len(activity.Nodes) != 1 || activity.Nodes[0].Name != "New server" {
		t.Fatalf("old in-flight read poisoned new client: %v %v", activity, err)
	}
	if err := s.Delete(inst.ID); err != nil {
		t.Fatal(err)
	}
	r.InvalidateClient(inst.ID)
	if _, err := r.GetTdarrClient(inst.ID); err == nil {
		t.Fatal("deleted instance retained a client")
	}
}
