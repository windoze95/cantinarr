package discover

import (
	"net/url"
	"strings"
	"testing"
)

// TestBuildBrowseQueryMirrorsTheRoute pins the request-free builder the
// assistant's browse tool shares with the HTTP browse routes: one allowlist,
// one sort and date validation, one rating floor, one English-only rule.
func TestBuildBrowseQueryMirrorsTheRoute(t *testing.T) {
	in := url.Values{
		"with_genres":      {"28,12"},
		"with_keywords":    {"1,2"},
		"with_companies":   {"3|4"},
		"include_adult":    {"true"},
		"language":         {"fr"},
		"vote_average.gte": {"7"},
	}
	params, explicit, err := BuildBrowseQuery("movie", in, 9999, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{
		"with_genres":            "28,12",
		"with_keywords":          "1,2",
		"with_companies":         "3|4",
		"vote_average.gte":       "7",
		"page":                   "500",
		"with_original_language": "en",
	} {
		if got := params.Get(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	for _, key := range []string{"include_adult", "language"} {
		if _, ok := params[key]; ok {
			t.Errorf("%s was forwarded, want it dropped", key)
		}
	}
	if explicit {
		t.Error("explicitLanguage = true with no language named")
	}

	params, explicit, err = BuildBrowseQuery("tv", url.Values{"with_original_language": {"ko"}, "sort_by": {"vote_average.desc"}}, 0, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !explicit || params.Get("with_original_language") != "ko" {
		t.Errorf("named language: explicit=%v value=%q, want true and ko", explicit, params.Get("with_original_language"))
	}
	if params.Get("vote_count.gte") != ratingSortMinVotes || params.Get("page") != "1" {
		t.Errorf("params = %v, want the rating floor and page 1", params)
	}

	if _, _, err := BuildBrowseQuery("podcast", nil, 1, false, nil); err == nil || !strings.Contains(err.Error(), "podcast") {
		t.Errorf("unknown media type err = %v, want it named", err)
	}
	if _, _, err := BuildBrowseQuery("movie", url.Values{"sort_by": {"rating.desc"}}, 1, false, nil); err == nil {
		t.Error("invalid sort_by accepted")
	}
	if _, _, err := BuildBrowseQuery("movie", url.Values{"primary_release_date.gte": {"2019"}}, 1, false, nil); err == nil {
		t.Error("malformed date accepted")
	}
}

func TestClampPage(t *testing.T) {
	for in, want := range map[int]int{-3: 1, 0: 1, 1: 1, 250: 250, MaxTMDBPage: MaxTMDBPage, 9999: MaxTMDBPage} {
		if got := ClampPage(in); got != want {
			t.Errorf("ClampPage(%d) = %d, want %d", in, got, want)
		}
	}
}

func TestEnglishOnlyDefaultsWithoutPrefs(t *testing.T) {
	if !EnglishOnly(nil) {
		t.Error("EnglishOnly(nil) = false, want the shipped default of true")
	}
	prefs := &stubPrefs{}
	prefs.set("tmdb_trending", false)
	if EnglishOnly(prefs) {
		t.Error("EnglishOnly = true with the preference off")
	}
}
