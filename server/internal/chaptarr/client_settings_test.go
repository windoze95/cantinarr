package chaptarr

import (
	"errors"
	"net/http"
	"net/http/httptest"
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

func TestGetCustomFormatsRawMaps404ToNotFound(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(server.Close)

	_, err := NewClient(server.URL, "key").GetCustomFormatsRaw()
	if !errors.Is(err, ErrCustomFormatsNotFound) {
		t.Fatalf("err = %v, want ErrCustomFormatsNotFound", err)
	}
}
