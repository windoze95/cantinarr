package instance

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestArrConfigurationClientDoesNotFollowRedirects(t *testing.T) {
	var redirectedRequests atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirectedRequests.Add(1)
		if got := r.Header.Get("X-Api-Key"); got != "" {
			t.Errorf("redirect destination received X-Api-Key %q", got)
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(destination.Close)

	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL+"/credential-sink", http.StatusTemporaryRedirect)
	}))
	t.Cleanup(source.Close)

	client := newArrConfigurationClient(source.URL, "arr-secret")
	if _, err := client.upsertWebhook(context.Background(), "sonarr", "https://cantinarr.example/api/webhooks/arr/1", "webhook-secret"); err == nil {
		t.Fatal("upsertWebhook accepted an upstream redirect")
	}
	if got := redirectedRequests.Load(); got != 0 {
		t.Fatalf("redirect destination received %d requests, want 0", got)
	}
}

func webhookSchema(serviceType string) map[string]any {
	resource := map[string]any{
		"id":                 0,
		"name":               "Webhook",
		"implementation":     "Webhook",
		"implementationName": "Webhook",
		"configContract":     "WebhookSettings",
		"tags":               []any{},
		"onGrab":             false,
		"onDownload":         false,
		"onUpgrade":          false,
		"fields": []any{
			map[string]any{"name": "url", "value": ""},
			map[string]any{"name": "method", "value": 1},
			map[string]any{"name": "username", "value": ""},
			map[string]any{"name": "password", "value": ""},
			map[string]any{"name": "headers", "value": []any{}},
		},
	}
	if serviceType == "radarr" {
		resource["onMovieAdded"] = false
		resource["onMovieDelete"] = false
		resource["onMovieFileDelete"] = false
		resource["onMovieFileDeleteForUpgrade"] = false
	} else {
		resource["onSeriesAdd"] = false
		resource["onSeriesDelete"] = false
		resource["onEpisodeFileDelete"] = false
		resource["onEpisodeFileDeleteForUpgrade"] = false
	}
	return resource
}

func webhookFieldValue(t *testing.T, resource map[string]any, name string) any {
	t.Helper()
	fields, _ := resource["fields"].([]any)
	for _, raw := range fields {
		field, _ := raw.(map[string]any)
		if fieldName, _ := field["name"].(string); strings.EqualFold(fieldName, name) {
			return field["value"]
		}
	}
	t.Fatalf("webhook field %q missing", name)
	return nil
}

func postWebhook(t *testing.T, handler *Handler, instanceID string) *httptest.ResponseRecorder {
	t.Helper()
	router := chi.NewRouter()
	router.Post("/instances/{instanceID}/webhook", handler.ConfigureWebhook)
	req := httptest.NewRequest(http.MethodPost, "https://cantinarr.example:8443/instances/"+instanceID+"/webhook", nil)
	// A client-controlled forwarded origin must never influence the callback
	// address that will receive a server-held credential.
	req.Header.Set("X-Forwarded-Proto", "javascript")
	req.Header.Set("X-Forwarded-Host", "attacker.example")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestConfigureWebhookCreatesServerManagedArrRecord(t *testing.T) {
	for _, serviceType := range []string{"radarr", "sonarr"} {
		t.Run(serviceType, func(t *testing.T) {
			var captured map[string]any
			var capturedMethod, capturedPath string
			arr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("X-Api-Key") != "synthetic-arr-key" {
					t.Errorf("arr request did not carry the stored API key")
				}
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/api/v3/notification/schema":
					_ = json.NewEncoder(w).Encode([]any{webhookSchema(serviceType)})
				case r.Method == http.MethodGet && r.URL.Path == "/api/v3/notification":
					_, _ = w.Write([]byte(`[]`))
				case r.Method == http.MethodPost && r.URL.Path == "/api/v3/notification":
					capturedMethod, capturedPath = r.Method, r.URL.Path
					if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
						t.Errorf("decode webhook request: %v", err)
					}
					w.WriteHeader(http.StatusCreated)
					_, _ = w.Write([]byte(`{}`))
				default:
					http.NotFound(w, r)
				}
			}))
			defer arr.Close()

			store := newTestStore(t)
			inst := &Instance{ServiceType: serviceType, Name: "Media", URL: arr.URL, APIKey: "synthetic-arr-key"}
			if err := store.Create(inst); err != nil {
				t.Fatalf("create instance: %v", err)
			}
			oldToken, err := store.WebhookToken(inst.ID)
			if err != nil {
				t.Fatalf("seed current webhook credential: %v", err)
			}
			rec := postWebhook(t, NewHandler(store, nil), inst.ID)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
			}
			if capturedMethod != http.MethodPost || capturedPath != "/api/v3/notification" || captured == nil {
				t.Fatalf("arr write = %s %s, want POST /api/v3/notification", capturedMethod, capturedPath)
			}

			token, err := store.WebhookToken(inst.ID)
			if err != nil {
				t.Fatalf("WebhookToken: %v", err)
			}
			if token == oldToken {
				t.Fatal("successful configuration did not rotate the webhook credential")
			}
			accepted, err := store.WebhookTokens(inst.ID)
			if err != nil || len(accepted) != 1 || accepted[0] != token {
				t.Fatalf("accepted credentials after promotion = %v, err=%v; want only current", accepted, err)
			}
			if strings.Contains(rec.Body.String(), token) || strings.Contains(rec.Body.String(), "webhook_token") {
				t.Fatal("configuration response exposed the webhook credential")
			}
			if captured["name"] != managedWebhookName || captured["onGrab"] != true || captured["onDownload"] != true || captured["onUpgrade"] != true {
				t.Errorf("managed webhook identity/common events were not configured")
			}
			if got := webhookFieldValue(t, captured, "url"); got != "https://cantinarr.example:8443/api/webhooks/arr/"+inst.ID {
				t.Errorf("callback URL = %v, want public request origin and instance route", got)
			} else if strings.Contains(got.(string), "token=") {
				t.Error("callback URL must not carry its credential in the query string")
			}
			if got := webhookFieldValue(t, captured, "username"); got != managedWebhookUsername {
				t.Errorf("webhook username = %v", got)
			}
			if got := webhookFieldValue(t, captured, "password"); got != token {
				t.Error("arr webhook password did not receive the server-held credential")
			}
			if got := webhookFieldValue(t, captured, "method"); got != float64(1) {
				t.Errorf("webhook method = %v, want POST enum 1", got)
			}
			if serviceType == "radarr" {
				for _, event := range []string{"onMovieAdded", "onMovieDelete", "onMovieFileDelete"} {
					if captured[event] != true {
						t.Errorf("%s was not enabled", event)
					}
				}
			} else {
				for _, event := range []string{"onSeriesAdd", "onSeriesDelete", "onEpisodeFileDelete"} {
					if captured[event] != true {
						t.Errorf("%s was not enabled", event)
					}
				}
			}
		})
	}
}

func TestConfigureWebhookUpdatesExistingManagedRecord(t *testing.T) {
	const legacyQueryToken = "legacy-query-token"
	var captured map[string]any
	var callbackPath string
	arr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/notification/schema":
			_ = json.NewEncoder(w).Encode([]any{webhookSchema("sonarr")})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/notification":
			_ = json.NewEncoder(w).Encode([]any{map[string]any{
				"id": 42, "name": "Old manual Cantinarr hook", "implementation": "Webhook", "configContract": "WebhookSettings",
				"fields": []any{map[string]any{
					"name": "url", "value": "http://old-host" + callbackPath + "?token=" + legacyQueryToken,
				}},
			}})
		case r.Method == http.MethodPut && r.URL.Path == "/api/v3/notification/42":
			if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
				t.Errorf("decode webhook request: %v", err)
			}
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodPost:
			t.Error("existing managed webhook was duplicated instead of updated")
			http.Error(w, "unexpected", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	defer arr.Close()

	store := newTestStore(t)
	inst := &Instance{ServiceType: "sonarr", Name: "TV", URL: arr.URL, APIKey: "synthetic-arr-key"}
	if err := store.Create(inst); err != nil {
		t.Fatalf("create instance: %v", err)
	}
	callbackPath = "/api/webhooks/arr/" + inst.ID
	rec := postWebhook(t, NewHandler(store, nil), inst.ID)
	if rec.Code != http.StatusOK || captured == nil {
		t.Fatalf("configure = %d %s", rec.Code, rec.Body.String())
	}
	if got := captured["id"]; got != float64(42) {
		t.Errorf("updated resource id = %v, want 42", got)
	}
	if got := webhookFieldValue(t, captured, "url"); strings.Contains(got.(string), legacyQueryToken) || strings.Contains(got.(string), "token=") {
		t.Error("adopted webhook retained its legacy query credential")
	}
	var response map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil || response["action"] != "updated" {
		t.Errorf("response = %v, want updated action", response)
	}
}

func TestConfigureWebhookErrorsNeverReflectArrResponse(t *testing.T) {
	const upstreamSecret = "upstream-secret-sentinel"
	arr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"apiKey":"` + upstreamSecret + `"}`))
	}))
	defer arr.Close()
	store := newTestStore(t)
	inst := &Instance{ServiceType: "radarr", Name: "Movies", URL: arr.URL, APIKey: "synthetic-arr-key"}
	if err := store.Create(inst); err != nil {
		t.Fatalf("create instance: %v", err)
	}
	oldToken, err := store.WebhookToken(inst.ID)
	if err != nil {
		t.Fatalf("seed current webhook credential: %v", err)
	}
	rec := postWebhook(t, NewHandler(store, nil), inst.ID)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
	if strings.Contains(rec.Body.String(), upstreamSecret) {
		t.Fatal("error response reflected the arr response body")
	}
	current, err := store.WebhookToken(inst.ID)
	if err != nil || current != oldToken {
		t.Fatalf("failed configuration replaced current credential: got %q err=%v", current, err)
	}
	accepted, err := store.WebhookTokens(inst.ID)
	if err != nil || len(accepted) != 2 || accepted[0] != oldToken {
		t.Fatalf("accepted credentials after failure = %v, err=%v; want current + retryable pending", accepted, err)
	}
	preparedAgain, err := store.PrepareWebhookToken(inst.ID)
	if err != nil || preparedAgain != accepted[1] {
		t.Fatalf("retry candidate = %q, err=%v; want stable pending candidate", preparedAgain, err)
	}
}

func TestInstanceListNeverExposesWebhookToken(t *testing.T) {
	store := newTestStore(t)
	id := mkInstance(t, store, "sonarr", "TV")
	token, err := store.WebhookToken(id)
	if err != nil {
		t.Fatalf("WebhookToken: %v", err)
	}
	rec := httptest.NewRecorder()
	NewHandler(store, nil).List(rec, httptest.NewRequest(http.MethodGet, "/instances", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if strings.Contains(rec.Body.String(), token) || strings.Contains(rec.Body.String(), "webhook_token") {
		t.Fatal("instance list exposed the webhook credential")
	}
	var instances []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &instances); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(instances) != 1 {
		t.Fatalf("instances = %d, want 1", len(instances))
	}
	if _, present := instances[0]["webhook_token"]; present {
		t.Fatal("instance response retained webhook_token field")
	}
}

func TestConfigureWebhookRejectsUnsupportedAndUnknownInstances(t *testing.T) {
	store := newTestStore(t)
	chaptarrID := mkInstance(t, store, "chaptarr", "Books")
	handler := NewHandler(store, nil)
	if rec := postWebhook(t, handler, chaptarrID); rec.Code != http.StatusBadRequest {
		t.Errorf("chaptarr status = %d, want 400", rec.Code)
	}
	if rec := postWebhook(t, handler, "missing-instance"); rec.Code != http.StatusNotFound {
		t.Errorf("missing status = %d, want 404", rec.Code)
	}
}

func TestConfigureWebhookRejectsInvalidConfiguredPublicURL(t *testing.T) {
	store := newTestStore(t)
	id := mkInstance(t, store, "radarr", "Movies")
	router := chi.NewRouter()
	router.Post("/instances/{instanceID}/webhook", NewHandler(store, nil, "javascript://attacker.example").ConfigureWebhook)
	req := httptest.NewRequest(http.MethodPost, "/instances/"+id+"/webhook", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestConfiguredPublicURLWinsOverRequestOrigin(t *testing.T) {
	h := NewHandler(nil, nil, "https://public.example")
	req := httptest.NewRequest(http.MethodPost, "http://internal.invalid/instances/id/webhook", nil)
	req.Header.Set("X-Forwarded-Host", "attacker.example")
	got, err := h.arrWebhookCallbackURL(req, "sonarr-main")
	if err != nil {
		t.Fatalf("arrWebhookCallbackURL: %v", err)
	}
	if got != "https://public.example/api/webhooks/arr/sonarr-main" {
		t.Fatalf("callback URL = %q", got)
	}
}
