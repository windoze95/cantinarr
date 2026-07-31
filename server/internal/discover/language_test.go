package discover

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/serversettings"
)

const mixedLanguageFeed = `{"page":1,"total_pages":42,"total_results":840,"results":[` +
	`{"id":1,"name":"The Pitt","original_language":"en"},` +
	`{"id":2,"name":"La Rosa de Guadalupe","original_language":"es"},` +
	`{"id":3,"name":"Severance","original_language":"en"}]}`

// TestFeedsHonorEnglishOnly covers the admin's language switch on an ordinary
// discovery feed, and pins that the rest of the envelope survives the filter.
func TestFeedsHonorEnglishOnly(t *testing.T) {
	e := newEnv(t, true)
	e.prefs.set(serversettings.DefaultDiscoverySource, true)
	e.upstream.setRespond(func(*http.Request) (int, string) {
		return http.StatusOK, mixedLanguageFeed
	})

	var page struct {
		Page         int              `json:"page"`
		TotalPages   int              `json:"total_pages"`
		TotalResults int              `json:"total_results"`
		Results      []map[string]any `json:"results"`
	}
	body := e.doOK(t, "/discover/tv/popular")
	if err := json.Unmarshal([]byte(body), &page); err != nil {
		t.Fatalf("decode filtered feed: %v (body = %s)", err, body)
	}

	if len(page.Results) != 2 {
		t.Fatalf("results = %d, want 2 with the Spanish entry dropped", len(page.Results))
	}
	if page.Results[0]["name"] != "The Pitt" || page.Results[1]["name"] != "Severance" {
		t.Errorf("kept %v, want the two English entries in order", page.Results)
	}
	// The counts still describe the upstream feed, so paging keeps working —
	// each page just arrives thinner.
	if page.Page != 1 || page.TotalPages != 42 || page.TotalResults != 840 {
		t.Errorf("envelope = page %d of %d (%d results), want the upstream values preserved",
			page.Page, page.TotalPages, page.TotalResults)
	}
}

// TestSearchAndDetailAreNeverLanguageFiltered is the boundary that keeps the
// preference an editorial choice about rows rather than a hole in the catalogue:
// a title you go looking for is always findable.
func TestSearchAndDetailAreNeverLanguageFiltered(t *testing.T) {
	e := newEnv(t, true)
	e.prefs.set(serversettings.DefaultDiscoverySource, true)
	e.upstream.setRespond(func(*http.Request) (int, string) {
		return http.StatusOK, mixedLanguageFeed
	})

	for _, path := range []string{"/search?query=guadalupe", "/media/tv/2"} {
		if body := e.doOK(t, path); body != mixedLanguageFeed {
			t.Errorf("GET %s returned a filtered body, want the upstream payload verbatim:\n%s", path, body)
		}
	}
}

// TestFeedCacheSeparatesFilteredFromUnfiltered guards the moment the admin
// flips the switch: the row must not keep serving the other variant.
func TestFeedCacheSeparatesFilteredFromUnfiltered(t *testing.T) {
	e := newEnv(t, true)
	e.upstream.setRespond(func(*http.Request) (int, string) {
		return http.StatusOK, mixedLanguageFeed
	})

	if body := e.doOK(t, "/discover/tv/popular"); body != mixedLanguageFeed {
		t.Fatalf("unfiltered body = %s, want the upstream payload verbatim", body)
	}

	e.prefs.set(serversettings.DefaultDiscoverySource, true)
	if body := e.doOK(t, "/discover/tv/popular"); body == mixedLanguageFeed {
		t.Error("filtered read served the cached unfiltered payload")
	}

	e.prefs.set(serversettings.DefaultDiscoverySource, false)
	if body := e.doOK(t, "/discover/tv/popular"); body != mixedLanguageFeed {
		t.Error("unfiltered read served the cached filtered payload")
	}
}

// TestFilterEnglishFeedDegradesToUnfiltered pins the failure direction: a
// payload the filter cannot read passes through whole, so an upstream shape
// change costs the filter, never the row.
func TestFilterEnglishFeedDegradesToUnfiltered(t *testing.T) {
	for name, payload := range map[string]string{
		"not json":           `<html>upstream error page</html>`,
		"no results key":     `{"page":1}`,
		"results not a list": `{"results":{"id":1}}`,
	} {
		t.Run(name, func(t *testing.T) {
			if got := string(filterEnglishFeed([]byte(payload))); got != payload {
				t.Errorf("filterEnglishFeed(%s) = %s, want it untouched", payload, got)
			}
		})
	}
}

func TestIsEnglishLanguage(t *testing.T) {
	english := []string{"en", "EN", "en-US", "en_GB", "", "  "}
	for _, code := range english {
		if !isEnglishLanguage(code) {
			t.Errorf("isEnglishLanguage(%q) = false, want true", code)
		}
	}

	foreign := []string{"es", "ko", "ja", "de", "pt-BR"}
	for _, code := range foreign {
		if isEnglishLanguage(code) {
			t.Errorf("isEnglishLanguage(%q) = true, want false", code)
		}
	}
}
