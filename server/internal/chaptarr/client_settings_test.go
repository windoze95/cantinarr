package chaptarr

import (
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
// fields this codebase has never modeled (Chaptarr diverges from stock
// Readarr, so unmodeled fields are the norm here).
func TestSettingsRawReadsAreVerbatim(t *testing.T) {
	const profile = `{"id":1,"name":"eBook","upgradeAllowed":true,"futureField":{"keep":"me"}}`
	const format = `{"id":2,"name":"Retail","specifications":[{"name":"Retail","implementation":"ReleaseTitleSpecification","negate":false,"fields":[{"name":"value","value":"\\bretail\\b"}]}]}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/qualityprofile":
			_, _ = w.Write([]byte(`[` + profile + `]`))
		case "/api/v1/customformat":
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
		wantMethod, wantPath := http.MethodPost, "/api/v1/customformat"
		if calls == 2 {
			wantMethod, wantPath = http.MethodPut, "/api/v1/customformat/8"
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
	const body = `{"id":2,"name":"Audiobook","upgradeAllowed":true,"futureForkField":{"keep":"me"}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/api/v1/qualityprofile/2" {
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
	raw, err := client.UpdateQualityProfileRaw(2, json.RawMessage(body))
	if err != nil || string(raw) != body {
		t.Fatalf("UpdateQualityProfileRaw = %s, %v", raw, err)
	}
}

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
