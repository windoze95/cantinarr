package sonarr

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestSetSeasonsViaSeasonPass asserts the /seasonpass payload Sonarr needs to
// monitor an arbitrary set of seasons: a single series entry with per-season
// {seasonNumber, monitored} flags and monitoringOptions.monitor == "none" so
// Sonarr applies the flags verbatim instead of overriding them with a scope.
func TestSetSeasonsViaSeasonPass(t *testing.T) {
	var gotPath, gotMethod string
	var body map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "key")
	if err := c.SetSeasonsViaSeasonPass(42, map[int]bool{3: true, 5: true, 1: false}); err != nil {
		t.Fatalf("SetSeasonsViaSeasonPass: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("method = %s, want POST", gotMethod)
	}
	if gotPath != "/api/v3/seasonpass" {
		t.Errorf("path = %s, want /api/v3/seasonpass", gotPath)
	}

	mon, ok := body["monitoringOptions"].(map[string]any)
	if !ok || mon["monitor"] != "none" {
		t.Errorf("monitoringOptions.monitor = %v, want \"none\"", body["monitoringOptions"])
	}

	seriesList, ok := body["series"].([]any)
	if !ok || len(seriesList) != 1 {
		t.Fatalf("series = %v, want a single-element array", body["series"])
	}
	series0 := seriesList[0].(map[string]any)
	if int(series0["id"].(float64)) != 42 {
		t.Errorf("series[0].id = %v, want 42", series0["id"])
	}

	seasons, ok := series0["seasons"].([]any)
	if !ok || len(seasons) != 3 {
		t.Fatalf("seasons = %v, want 3 entries", series0["seasons"])
	}
	got := map[int]bool{}
	for _, s := range seasons {
		m := s.(map[string]any)
		got[int(m["seasonNumber"].(float64))] = m["monitored"].(bool)
	}
	want := map[int]bool{1: false, 3: true, 5: true}
	for n, mon := range want {
		if got[n] != mon {
			t.Errorf("season %d monitored = %v, want %v", n, got[n], mon)
		}
	}
}
