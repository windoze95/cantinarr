package sonarr

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The raw settings reads exist so a future write can PUT the object back
// verbatim; the property under test is byte-for-byte fidelity, including
// fields this codebase has never modeled.
func TestSettingsRawReadsAreVerbatim(t *testing.T) {
	const profile = `{"id":1,"name":"Any","upgradeAllowed":true,"futureField":{"keep":"me"}}`
	const format = `{"id":3,"name":"Not English","specifications":[{"name":"Not English","implementation":"LanguageSpecification","negate":true,"fields":[{"name":"value","value":1}]}]}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v3/qualityprofile":
			_, _ = w.Write([]byte(`[` + profile + `]`))
		case "/api/v3/customformat":
			_, _ = w.Write([]byte(`[` + format + `]`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	client := NewClient(server.URL, "key")
	profiles, err := client.GetQualityProfilesRaw()
	if err != nil {
		t.Fatalf("GetQualityProfilesRaw: %v", err)
	}
	if len(profiles) != 1 || string(profiles[0]) != profile {
		t.Fatalf("profiles = %v, want the served object verbatim", profiles)
	}
	formats, err := client.GetCustomFormatsRaw()
	if err != nil {
		t.Fatalf("GetCustomFormatsRaw: %v", err)
	}
	if len(formats) != 1 || string(formats[0]) != format {
		t.Fatalf("formats = %v, want the served object verbatim", formats)
	}
}

func TestCustomFormatRawWritesUseExpectedEndpointsAndBodies(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		wantMethod, wantPath := http.MethodPost, "/api/v3/customformat"
		if calls == 2 {
			wantMethod, wantPath = http.MethodPut, "/api/v3/customformat/8"
		}
		if r.Method != wantMethod || r.URL.Path != wantPath || r.Header.Get("X-Api-Key") != "key" {
			t.Errorf("call %d = %s %s key=%q", calls, r.Method, r.URL.Path, r.Header.Get("X-Api-Key"))
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != `{"name":"x265","specifications":[]}` {
			t.Errorf("body = %s", body)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":8,"name":"x265"}`))
	}))
	t.Cleanup(server.Close)
	client := NewClient(server.URL, "key")
	body := []byte(`{"name":"x265","specifications":[]}`)
	if raw, err := client.CreateCustomFormatRaw(body); err != nil || string(raw) != `{"id":8,"name":"x265"}` {
		t.Fatalf("create = %s, %v", raw, err)
	}
	if raw, err := client.UpdateCustomFormatRaw(8, body); err != nil || string(raw) != `{"id":8,"name":"x265"}` {
		t.Fatalf("update = %s, %v", raw, err)
	}
}

func TestQualityProfileRawWriteUsesExpectedEndpointAndBody(t *testing.T) {
	const body = `{"id":6,"name":"WEB","upgradeAllowed":true,"futureField":{"keep":"me"}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/api/v3/qualityprofile/6" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("X-Api-Key") != "key" || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("headers = key %q content-type %q", r.Header.Get("X-Api-Key"), r.Header.Get("Content-Type"))
		}
		got, _ := io.ReadAll(r.Body)
		if string(got) != body {
			t.Errorf("body = %s, want %s", got, body)
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)

	client := NewClient(server.URL, "key")
	raw, err := client.UpdateQualityProfileRaw(6, json.RawMessage(body))
	if err != nil || string(raw) != body {
		t.Fatalf("UpdateQualityProfileRaw = %s, %v", raw, err)
	}
}

func TestGetLanguagesRawContextReturnsSonarrCatalogUnchanged(t *testing.T) {
	// This synthetic Hindi ID intentionally differs from the Radarr fixture.
	// Together the fixtures prove each client returns its own catalog unchanged;
	// they do not claim that every pair of live instances must differ.
	const catalog = `[{"id":1,"name":"English"},{"id":27,"name":"Hindi"}]`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v3/language" || r.Header.Get("X-Api-Key") != "key" {
			t.Errorf("request = %s %s key=%q", r.Method, r.URL.Path, r.Header.Get("X-Api-Key"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(catalog))
	}))
	t.Cleanup(server.Close)

	client := NewClient(server.URL, "key")
	languages, err := client.GetLanguagesRawContext(context.Background())
	if err != nil {
		t.Fatalf("GetLanguagesRawContext: %v", err)
	}
	if len(languages) != 2 || string(languages[1]) != `{"id":27,"name":"Hindi"}` {
		t.Fatalf("languages = %s", languages)
	}
}

// Sonarr v3 predates custom formats: its API has no /customformat endpoint.
// A wrong URL base 404s identically, so the sentinel says only "404" and the
// tool layer presents both causes.
func TestGetCustomFormatsRawMaps404ToNotFound(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(server.Close)

	client := NewClient(server.URL, "key")
	if _, err := client.GetCustomFormatsRaw(); !errors.Is(err, ErrCustomFormatsNotFound) {
		t.Fatalf("get err = %v, want ErrCustomFormatsNotFound", err)
	}
	for name, call := range map[string]func() error{
		"create": func() error {
			_, err := client.CreateCustomFormatRaw([]byte(`{"name":"x","specifications":[]}`))
			return err
		},
		"update": func() error {
			_, err := client.UpdateCustomFormatRaw(3, []byte(`{"id":3,"name":"x","specifications":[]}`))
			return err
		},
	} {
		if err := call(); err == nil || errors.Is(err, ErrCustomFormatsNotFound) || !strings.Contains(err.Error(), "returned status 404") {
			t.Errorf("%s err = %v, want concrete write 404", name, err)
		}
	}
}
