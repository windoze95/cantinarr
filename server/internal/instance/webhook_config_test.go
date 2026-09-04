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
	switch serviceType {
	case "radarr":
		resource["onMovieAdded"] = false
		resource["onMovieDelete"] = false
		resource["onMovieFileDelete"] = false
		resource["onMovieFileDeleteForUpgrade"] = false
	case "chaptarr":
		// The Readarr lineage names the import event onReleaseImport and its
		// settings carry no headers field.
		resource["onReleaseImport"] = false
		resource["onAuthorAdded"] = false
		resource["onBookAdded"] = false
		resource["onBookDelete"] = false
		resource["onAuthorDelete"] = false
		resource["onBookFileDelete"] = false
		resource["onBookFileDeleteForUpgrade"] = false
		resource["fields"] = []any{
			map[string]any{"name": "url", "value": ""},
			map[string]any{"name": "method", "value": 1},
			map[string]any{"name": "username", "value": ""},
			map[string]any{"name": "password", "value": ""},
		}
	case "lidarr":
		// Lidarr names the import event onReleaseImport too, and exposes no
		// track-file-delete toggle at all; its settings carry no headers field.
		resource["onReleaseImport"] = false
		resource["onArtistAdd"] = false
		resource["onAlbumDelete"] = false
		resource["onArtistDelete"] = false
		resource["onTrackRetag"] = false
		resource["onDownloadFailure"] = false
		resource["onImportFailure"] = false
		resource["fields"] = []any{
			map[string]any{"name": "url", "value": ""},
			map[string]any{"name": "method", "value": 1},
			map[string]any{"name": "username", "value": ""},
			map[string]any{"name": "password", "value": ""},
		}
	default:
		resource["onSeriesAdd"] = false
		resource["onSeriesDelete"] = false
		resource["onEpisodeFileDelete"] = false
		resource["onEpisodeFileDeleteForUpgrade"] = false
	}
	return resource
}

// arrWebhookStub serves the notification schema/list/create endpoints at the
// given API version and captures the resource Cantinarr writes.
func arrWebhookStub(t *testing.T, apiVersion string, schema map[string]any, captured *map[string]any) *httptest.Server {
	t.Helper()
	base := "/api/" + apiVersion + "/notification"
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == base+"/schema":
			_ = json.NewEncoder(w).Encode([]any{schema})
		case r.Method == http.MethodGet && r.URL.Path == base:
			_, _ = w.Write([]byte(`[]`))
		case r.Method == http.MethodPost && r.URL.Path == base:
			if got := r.URL.Query().Get("forceSave"); got != "true" {
				t.Errorf("forceSave = %q, want true", got)
			}
			if err := json.NewDecoder(r.Body).Decode(captured); err != nil {
				t.Errorf("decode webhook request: %v", err)
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{}`))
		default:
			http.NotFound(w, r)
		}
	}))
}

func mkArrInstance(t *testing.T, store *Store, serviceType, url string) *Instance {
	t.Helper()
	inst := &Instance{ServiceType: serviceType, Name: "Media", URL: url, APIKey: "synthetic-arr-key"}
	if err := store.Create(inst); err != nil {
		t.Fatalf("create instance: %v", err)
	}
	return inst
}

// TestConfigureWebhookRegistersChaptarrAgainstV1 pins that a Chaptarr webhook is
// installed through the Readarr-lineage v1 notification API and enables an
// import event. Without an import toggle the record would save cleanly and never
// fire, which reads to an admin as working instant updates.
func TestConfigureWebhookRegistersChaptarrAgainstV1(t *testing.T) {
	var captured map[string]any
	arr := arrWebhookStub(t, "v1", webhookSchema("chaptarr"), &captured)
	defer arr.Close()

	store := newTestStore(t)
	inst := mkArrInstance(t, store, "chaptarr", arr.URL)
	if rec := postWebhook(t, NewHandler(store, nil, "http://192.168.35.150:8585"), inst.ID); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if captured == nil {
		t.Fatal("no webhook resource was written to Chaptarr")
	}
	if got, _ := captured["onReleaseImport"].(bool); !got {
		t.Error("onReleaseImport was not enabled, so book imports would never alert")
	}
	// The Readarr lineage sends no import callback for a book that replaces an
	// existing file unless onUpgrade is on. The callback must still arrive —
	// it invalidates availability caches and feeds the admin content_upgraded
	// alert; the notifier, not this webhook, decides who is paged for it.
	if got, _ := captured["onUpgrade"].(bool); !got {
		t.Error("onUpgrade was not enabled, so upgrade imports would never reach the server")
	}
	// onAuthorAdded is what lets the receiver resume author-import parks the
	// moment the arr finishes a queued import, instead of on the next tick.
	if got, _ := captured["onAuthorAdded"].(bool); !got {
		t.Error("onAuthorAdded was not enabled, so parked book requests would only resume by poll")
	}
	if _, present := captured["onEpisodeFileDelete"]; present {
		t.Error("a Sonarr-only event flag leaked onto a Chaptarr resource")
	}
	if got, _ := captured["onBookFileDeleteForUpgrade"].(bool); got {
		t.Error("onBookFileDeleteForUpgrade must stay false")
	}
	// Readarr-lineage webhook settings have no headers field; inventing one
	// would post a phantom setting.
	for _, raw := range captured["fields"].([]any) {
		field, _ := raw.(map[string]any)
		if name, _ := field["name"].(string); strings.EqualFold(name, "headers") {
			t.Error("a headers field was invented for a schema that does not declare one")
		}
	}
	if got := webhookFieldValue(t, captured, "username"); got != managedWebhookUsername {
		t.Errorf("username = %v, want %s", got, managedWebhookUsername)
	}
	if got := webhookFieldValue(t, captured, "url"); got != "http://192.168.35.150:8585/api/webhooks/arr/"+inst.ID {
		t.Errorf("private-LAN callback URL = %v", got)
	}
}

// TestConfigureWebhookRegistersLidarrAgainstV1 pins that a Lidarr webhook is
// installed through the v1 notification API with its import event enabled,
// under the same schema-is-authority rule as Chaptarr: only declared toggles
// are written, and no track-file-delete flag is invented (Lidarr has none —
// upgrade-deletes surface via the import payload and history instead).
func TestConfigureWebhookRegistersLidarrAgainstV1(t *testing.T) {
	var captured map[string]any
	arr := arrWebhookStub(t, "v1", webhookSchema("lidarr"), &captured)
	defer arr.Close()

	store := newTestStore(t)
	inst := mkArrInstance(t, store, "lidarr", arr.URL)
	if rec := postWebhook(t, NewHandler(store, nil, "http://192.168.35.150:8585"), inst.ID); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if captured == nil {
		t.Fatal("no webhook resource was written to Lidarr")
	}
	if got, _ := captured["onReleaseImport"].(bool); !got {
		t.Error("onReleaseImport was not enabled, so music imports would never invalidate")
	}
	if got, _ := captured["onUpgrade"].(bool); !got {
		t.Error("onUpgrade was not enabled, so upgrade imports would never reach the server")
	}
	if got, _ := captured["onArtistAdd"].(bool); !got {
		t.Error("onArtistAdd was not enabled")
	}
	for _, invented := range []string{"onTrackFileDelete", "onBookFileDelete", "onEpisodeFileDelete"} {
		if _, present := captured[invented]; present {
			t.Errorf("%s leaked onto a Lidarr resource that never declared it", invented)
		}
	}
	for _, raw := range captured["fields"].([]any) {
		field, _ := raw.(map[string]any)
		if name, _ := field["name"].(string); strings.EqualFold(name, "headers") {
			t.Error("a headers field was invented for a schema that does not declare one")
		}
	}
	if got := webhookFieldValue(t, captured, "url"); got != "http://192.168.35.150:8585/api/webhooks/arr/"+inst.ID {
		t.Errorf("callback URL = %v", got)
	}
}

// TestConfigureWebhookFailsWhenLidarrDeclaresNoImportEvent mirrors the
// Chaptarr fail-loud guarantee for the music arm.
func TestConfigureWebhookFailsWhenLidarrDeclaresNoImportEvent(t *testing.T) {
	schema := webhookSchema("lidarr")
	delete(schema, "onReleaseImport")
	var captured map[string]any
	arr := arrWebhookStub(t, "v1", schema, &captured)
	defer arr.Close()

	store := newTestStore(t)
	inst := mkArrInstance(t, store, "lidarr", arr.URL)
	rec := postWebhook(t, NewHandler(store, nil, "http://192.168.35.150:8585"), inst.ID)
	if rec.Code == http.StatusOK {
		t.Fatalf("webhook configured without any import toggle; body=%s", rec.Body.String())
	}
	if captured != nil {
		t.Error("a useless webhook resource was still written")
	}
}

// TestConfigureWebhookFailsWhenChaptarrDeclaresNoImportEvent is the fail-loud
// guarantee. The import toggle is onReleaseImport (verified against
// Chaptarr's open source), but the schema template stays the authority at
// configure time; if it declares no import toggle the admin must see an
// error instead of a silently useless webhook.
func TestConfigureWebhookFailsWhenChaptarrDeclaresNoImportEvent(t *testing.T) {
	schema := webhookSchema("chaptarr")
	delete(schema, "onReleaseImport")
	delete(schema, "onDownload")

	var captured map[string]any
	arr := arrWebhookStub(t, "v1", schema, &captured)
	defer arr.Close()

	store := newTestStore(t)
	inst := mkArrInstance(t, store, "chaptarr", arr.URL)
	rec := postWebhook(t, NewHandler(store, nil), inst.ID)
	if rec.Code == http.StatusOK {
		t.Fatal("configuring a webhook with no import event reported success")
	}
	if captured != nil {
		t.Error("a webhook was written to the arr despite having no import event")
	}
}

// TestConfigureWebhookHonorsUnsupportedEventFlags pins that a flag the fork
// reports unsupported is not written, while an undeclared supports* key is
// treated as permission rather than refusal.
func TestConfigureWebhookHonorsUnsupportedEventFlags(t *testing.T) {
	schema := webhookSchema("chaptarr")
	schema["supportsOnBookDelete"] = false

	var captured map[string]any
	arr := arrWebhookStub(t, "v1", schema, &captured)
	defer arr.Close()

	store := newTestStore(t)
	inst := mkArrInstance(t, store, "chaptarr", arr.URL)
	if rec := postWebhook(t, NewHandler(store, nil), inst.ID); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if got, _ := captured["onBookDelete"].(bool); got {
		t.Error("an event the schema reports unsupported was enabled anyway")
	}
	if got, _ := captured["onReleaseImport"].(bool); !got {
		t.Error("the supported import event should still have been enabled")
	}
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
			var capturedMethod, capturedPath, capturedForceSave string
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
					capturedForceSave = r.URL.Query().Get("forceSave")
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
			if capturedForceSave != "true" {
				t.Errorf("forceSave = %q, want true", capturedForceSave)
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
	var tested, captured map[string]any
	var callbackPath string
	testedBeforeSave := false
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
		case r.Method == http.MethodPost && r.URL.Path == "/api/v3/notification/test":
			if got := r.URL.Query().Get("forceTest"); got != "true" {
				t.Errorf("forceTest = %q, want true", got)
			}
			if err := json.NewDecoder(r.Body).Decode(&tested); err != nil {
				t.Errorf("decode webhook test request: %v", err)
			}
			testedBeforeSave = true
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodPut && r.URL.Path == "/api/v3/notification/42":
			if !testedBeforeSave {
				t.Error("webhook was saved before the explicit callback test")
			}
			if got := r.URL.Query().Get("forceSave"); got != "true" {
				t.Errorf("forceSave = %q, want true", got)
			}
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
	if rec.Code != http.StatusOK || tested == nil || captured == nil {
		t.Fatalf("configure = %d %s", rec.Code, rec.Body.String())
	}
	if got := tested["id"]; got != float64(42) {
		t.Errorf("tested resource id = %v, want 42", got)
	}
	if got := captured["id"]; got != float64(42) {
		t.Errorf("updated resource id = %v, want 42", got)
	}
	if got, want := webhookFieldValue(t, tested, "password"), webhookFieldValue(t, captured, "password"); got != want {
		t.Error("the tested and saved resources used different credentials")
	}
	if got := webhookFieldValue(t, captured, "url"); strings.Contains(got.(string), legacyQueryToken) || strings.Contains(got.(string), "token=") {
		t.Error("adopted webhook retained its legacy query credential")
	}
	var response map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil || response["action"] != "updated" {
		t.Errorf("response = %v, want updated action", response)
	}
}

// TestConfigureWebhookUpdateStopsWhenExplicitTestFails protects the forced
// update sequence: forceSave acknowledges warnings but must never let a real
// callback failure reach PUT or promote the pending credential.
func TestConfigureWebhookUpdateStopsWhenExplicitTestFails(t *testing.T) {
	const verdict = "Unable to send test message: Connection refused"
	base := "/api/v1/notification"
	var putCalls atomic.Int32
	var sentToken string
	arr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == base+"/schema":
			_ = json.NewEncoder(w).Encode([]any{webhookSchema("chaptarr")})
		case r.Method == http.MethodGet && r.URL.Path == base:
			existing := webhookSchema("chaptarr")
			existing["id"] = 17
			existing["name"] = managedWebhookName
			_ = json.NewEncoder(w).Encode([]any{existing})
		case r.Method == http.MethodPost && r.URL.Path == base+"/test":
			if got := r.URL.Query().Get("forceTest"); got != "true" {
				t.Errorf("forceTest = %q, want true", got)
			}
			var resource map[string]any
			if err := json.NewDecoder(r.Body).Decode(&resource); err != nil {
				t.Errorf("decode webhook test request: %v", err)
			}
			sentToken, _ = webhookFieldValue(t, resource, "password").(string)
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode([]any{map[string]any{
				"propertyName": "Url", "errorMessage": verdict,
				"attemptedValue": sentToken, "severity": "error",
			}})
		case r.Method == http.MethodPut:
			putCalls.Add(1)
			http.Error(w, "unexpected save", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	defer arr.Close()

	store := newTestStore(t)
	inst := mkArrInstance(t, store, "chaptarr", arr.URL)
	oldToken, err := store.WebhookToken(inst.ID)
	if err != nil {
		t.Fatalf("seed current webhook credential: %v", err)
	}
	rec := postWebhook(t, NewHandler(store, nil), inst.ID)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body=%s", rec.Code, rec.Body.String())
	}
	if got := putCalls.Load(); got != 0 {
		t.Fatalf("PUT calls = %d, want 0 after failed callback test", got)
	}
	if !strings.Contains(rec.Body.String(), verdict) {
		t.Errorf("error omitted the arr's test verdict: %s", rec.Body.String())
	}
	if sentToken == "" || strings.Contains(rec.Body.String(), sentToken) {
		t.Fatal("error did not capture and safely redact the pending credential")
	}
	current, err := store.WebhookToken(inst.ID)
	if err != nil || current != oldToken {
		t.Fatalf("failed update replaced current credential: got %q err=%v", current, err)
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
	// A download client has no Connect surface at all. Chaptarr does, so it is
	// no longer the unsupported case.
	downloadID := mkInstance(t, store, "sabnzbd", "Downloads")
	handler := NewHandler(store, nil)
	if rec := postWebhook(t, handler, downloadID); rec.Code != http.StatusBadRequest {
		t.Errorf("sabnzbd status = %d, want 400", rec.Code)
	}
	if rec := postWebhook(t, handler, "missing-instance"); rec.Code != http.StatusNotFound {
		t.Errorf("missing status = %d, want 404", rec.Code)
	}
}

func TestConfigureWebhookRejectsInvalidConfiguredCallbackURL(t *testing.T) {
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

func TestConfiguredCallbackURLWinsOverRequestOrigin(t *testing.T) {
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

// TestConfigureWebhookSurfacesArrTestFailure pins the diagnosability of the
// most common configure failure: the arr saves a webhook only after testing
// it, and its 400 carries the verdict ("Unable to send test message: …").
// That verdict must reach the admin — extracted from the validation shape
// only, with the submitted credential redacted even when the arr echoes it
// back in attemptedValue — together with the exact callback URL to check.
func TestConfigureWebhookSurfacesArrTestFailure(t *testing.T) {
	var sentToken string
	base := "/api/v1/notification"
	arr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == base+"/schema":
			_ = json.NewEncoder(w).Encode([]any{webhookSchema("chaptarr")})
		case r.Method == http.MethodGet && r.URL.Path == base:
			_, _ = w.Write([]byte(`[]`))
		case r.Method == http.MethodPost && r.URL.Path == base:
			if got := r.URL.Query().Get("forceSave"); got != "true" {
				t.Errorf("forceSave = %q, want true", got)
			}
			var resource map[string]any
			_ = json.NewDecoder(r.Body).Decode(&resource)
			for _, raw := range resource["fields"].([]any) {
				field, _ := raw.(map[string]any)
				if name, _ := field["name"].(string); strings.EqualFold(name, "password") {
					sentToken, _ = field["value"].(string)
				}
			}
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode([]any{
				map[string]any{
					"propertyName":   "Url",
					"errorMessage":   "Unable to send test message: Connection refused",
					"attemptedValue": sentToken,
					"severity":       "error",
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer arr.Close()

	store := newTestStore(t)
	inst := mkArrInstance(t, store, "chaptarr", arr.URL)
	rec := postWebhook(t, NewHandler(store, nil), inst.ID)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Unable to send test message: Connection refused") {
		t.Errorf("error omitted the arr's verdict: %s", body)
	}
	if !strings.Contains(body, "/api/webhooks/arr/") {
		t.Errorf("error omitted the callback URL to check: %s", body)
	}
	if sentToken == "" {
		t.Fatal("stub captured no webhook credential")
	}
	if strings.Contains(body, sentToken) {
		t.Fatal("error reflected the webhook credential echoed by the arr")
	}
	var decoded map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("error body is not valid JSON despite carrying arr text: %v — %s", err, body)
	}
}

// TestConfigureWebhookErrorOmitsUnrecognizedBody pins that only the known
// validation shapes are ever extracted: an arbitrary 400 body (HTML, secrets
// under other keys) is not reflected, and the reachability hint still guides.
func TestConfigureWebhookErrorOmitsUnrecognizedBody(t *testing.T) {
	const upstreamSecret = "html-body-secret-sentinel"
	base := "/api/v3/notification"
	arr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == base+"/schema":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]any{webhookSchema("radarr")})
		case r.Method == http.MethodGet && r.URL.Path == base:
			_, _ = w.Write([]byte(`[]`))
		case r.Method == http.MethodPost && r.URL.Path == base:
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("<html>" + upstreamSecret + "</html>"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer arr.Close()

	store := newTestStore(t)
	inst := mkArrInstance(t, store, "radarr", arr.URL)
	rec := postWebhook(t, NewHandler(store, nil), inst.ID)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, upstreamSecret) {
		t.Fatal("error reflected an unrecognized arr response body")
	}
	if !strings.Contains(body, "the arr tests the webhook during configuration") {
		t.Errorf("400 without extractable detail lost the reachability hint: %s", body)
	}
}

// getWebhookStatus mirrors postWebhook: the expected callback must derive from
// the trusted request origin, never from client-controlled forwarded headers.
func getWebhookStatus(t *testing.T, handler *Handler, instanceID string) *httptest.ResponseRecorder {
	t.Helper()
	router := chi.NewRouter()
	router.Get("/instances/{instanceID}/webhook", handler.WebhookStatus)
	req := httptest.NewRequest(http.MethodGet, "https://cantinarr.example:8443/instances/"+instanceID+"/webhook", nil)
	req.Header.Set("X-Forwarded-Proto", "javascript")
	req.Header.Set("X-Forwarded-Host", "attacker.example")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func decodeWebhookStatus(t *testing.T, rec *httptest.ResponseRecorder) (supported, configured bool, state string) {
	t.Helper()
	var body struct {
		Supported  bool   `json:"supported"`
		Configured bool   `json:"configured"`
		State      string `json:"state"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode status: %v — %s", err, rec.Body.String())
	}
	return body.Supported, body.Configured, body.State
}

// notificationListStub serves only the notification list; the status read must
// never write to the arr, so everything else 404s loudly.
func notificationListStub(t *testing.T, apiVersion string, records func() []any) *httptest.Server {
	t.Helper()
	base := "/api/" + apiVersion + "/notification"
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == base {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(records())
			return
		}
		t.Errorf("status read sent %s %s to the arr", r.Method, r.URL.Path)
		http.NotFound(w, r)
	}))
}

func managedWebhookRecord(name, url string) map[string]any {
	return map[string]any{
		"id": 7, "name": name, "implementation": "Webhook", "configContract": "WebhookSettings",
		"fields": []any{map[string]any{"name": "url", "value": url}},
	}
}

// TestWebhookStatusReportsLiveArrState pins that the answer comes from the
// arr's Connect list at read time: the same store reads "ok", "missing", or
// "stale" purely by what the arr reports right now — a stored flag would keep
// claiming configured after an admin deleted the record or the public URL
// moved.
func TestWebhookStatusReportsLiveArrState(t *testing.T) {
	var records []any
	arr := notificationListStub(t, "v3", func() []any { return records })
	defer arr.Close()

	store := newTestStore(t)
	inst := mkArrInstance(t, store, "sonarr", arr.URL)
	if _, err := store.WebhookToken(inst.ID); err != nil {
		t.Fatalf("seed webhook credential: %v", err)
	}
	handler := NewHandler(store, nil)
	callback := "https://cantinarr.example:8443/api/webhooks/arr/" + inst.ID

	cases := []struct {
		name           string
		records        []any
		wantConfigured bool
		wantState      string
	}{
		{"managed record targets the expected callback",
			[]any{managedWebhookRecord(managedWebhookName, callback)}, true, webhookStateOK},
		{"no webhook installed", []any{}, false, webhookStateMissing},
		{"record targets another Cantinarr address",
			[]any{managedWebhookRecord(managedWebhookName, "http://old-host/api/webhooks/arr/" + inst.ID)}, false, webhookStateStale},
		{"adopted legacy record still carries its query credential",
			[]any{managedWebhookRecord("Old manual hook", callback+"?token=legacy")}, false, webhookStateStale},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			records = tc.records
			rec := getWebhookStatus(t, handler, inst.ID)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
			}
			supported, configured, state := decodeWebhookStatus(t, rec)
			if !supported || configured != tc.wantConfigured || state != tc.wantState {
				t.Fatalf("got supported=%v configured=%v state=%q, want supported=true configured=%v state=%q",
					supported, configured, state, tc.wantConfigured, tc.wantState)
			}
		})
	}
}

// TestWebhookStatusReportsMissingServerCredential pins the restored-database
// shape: an arr record that looks right while this server holds no accepted
// credential must not read as configured — every delivery would be rejected.
// Chaptarr here also exercises the v1 lineage path.
func TestWebhookStatusReportsMissingServerCredential(t *testing.T) {
	var records []any
	arr := notificationListStub(t, "v1", func() []any { return records })
	defer arr.Close()

	store := newTestStore(t)
	inst := mkArrInstance(t, store, "chaptarr", arr.URL)
	records = []any{managedWebhookRecord(managedWebhookName,
		"https://cantinarr.example:8443/api/webhooks/arr/"+inst.ID)}
	rec := getWebhookStatus(t, NewHandler(store, nil), inst.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
	}
	if _, configured, state := decodeWebhookStatus(t, rec); configured || state != webhookStateCredentialMissing {
		t.Fatalf("configured=%v state=%q, want false/%s", configured, state, webhookStateCredentialMissing)
	}
}

// TestWebhookStatusDistinguishesBlindnessFromAbsence: an unreadable arr is a
// 502, never "missing" — rendered the same, the two would stop an admin from
// looking further.
func TestWebhookStatusDistinguishesBlindnessFromAbsence(t *testing.T) {
	arr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer arr.Close()

	store := newTestStore(t)
	inst := mkArrInstance(t, store, "radarr", arr.URL)
	rec := getWebhookStatus(t, NewHandler(store, nil), inst.ID)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"state"`) {
		t.Fatalf("blindness rendered as a status verdict: %s", rec.Body.String())
	}
}

func TestWebhookStatusUnsupportedAndUnknownInstances(t *testing.T) {
	store := newTestStore(t)
	downloadID := mkInstance(t, store, "sabnzbd", "Downloads")
	handler := NewHandler(store, nil)
	rec := getWebhookStatus(t, handler, downloadID)
	if rec.Code != http.StatusOK {
		t.Fatalf("sabnzbd status = %d, want 200", rec.Code)
	}
	if supported, configured, state := decodeWebhookStatus(t, rec); supported || configured || state != webhookStateUnsupported {
		t.Fatalf("sabnzbd = %v/%v/%q, want unsupported", supported, configured, state)
	}
	if rec := getWebhookStatus(t, handler, "missing-instance"); rec.Code != http.StatusNotFound {
		t.Errorf("missing status = %d, want 404", rec.Code)
	}
}

// TestWebhookStatusReportsUnusableCallbackURL: with a callback URL that can never
// carry a callback, the status is an answer (configure would fail the same
// way), not an error.
func TestWebhookStatusReportsUnusableCallbackURL(t *testing.T) {
	store := newTestStore(t)
	id := mkInstance(t, store, "radarr", "Movies")
	rec := getWebhookStatus(t, NewHandler(store, nil, "javascript://attacker.example"), id)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if _, configured, state := decodeWebhookStatus(t, rec); configured || state != webhookStateNoPublicURL {
		t.Fatalf("configured=%v state=%q, want false/%s", configured, state, webhookStateNoPublicURL)
	}
}

// TestArrValidationDetailReadsOnlyKnownShapes pins the extractor's contract.
func TestArrValidationDetailReadsOnlyKnownShapes(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"validation array", `[{"propertyName":"Url","errorMessage":"Unable to send test message: timeout","attemptedValue":"secret"}]`,
			"Url: Unable to send test message: timeout"},
		{"array without property", `[{"errorMessage":"Invalid"}]`, "Invalid"},
		{"multiple failures", `[{"errorMessage":"A"},{"errorMessage":"B"}]`, "A; B"},
		{"message envelope", `{"message":"NotFound"}`, "NotFound"},
		{"error envelope", `{"error":"boom"}`, "boom"},
		{"secret under other key", `{"apiKey":"secret-sentinel"}`, ""},
		{"html", `<html>oops</html>`, ""},
		{"empty", ``, ""},
	}
	for _, tc := range cases {
		if got := arrValidationDetail([]byte(tc.body)); got != tc.want {
			t.Errorf("%s: arrValidationDetail = %q, want %q", tc.name, got, tc.want)
		}
	}
}
