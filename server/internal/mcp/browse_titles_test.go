package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/credentials"
	"github.com/windoze95/cantinarr-server/internal/db"
	"github.com/windoze95/cantinarr-server/internal/serversettings"
)

// fakeBrowseTMDB serves the catalog tables, the two lookups, and a discover
// page, recording every discover query so a test can read what was sent.
type fakeBrowseTMDB struct {
	server   *httptest.Server
	mu       sync.Mutex
	discover []url.Values
}

func newFakeBrowseTMDB(t *testing.T) *fakeBrowseTMDB {
	t.Helper()
	f := &fakeBrowseTMDB{}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		q := r.URL.Query()
		switch {
		case r.URL.Path == "/genre/movie/list":
			_, _ = io.WriteString(w, `{"genres":[{"id":28,"name":"Action"},{"id":35,"name":"Comedy"},{"id":878,"name":"Science Fiction"}]}`)
		case r.URL.Path == "/genre/tv/list":
			_, _ = io.WriteString(w, `{"genres":[{"id":35,"name":"Comedy"},{"id":10765,"name":"Sci-Fi & Fantasy"}]}`)
		case r.URL.Path == "/configuration/languages":
			_, _ = io.WriteString(w, `[{"iso_639_1":"en","english_name":"English","name":"English"},{"iso_639_1":"ko","english_name":"Korean","name":"한국어"}]`)
		case strings.HasPrefix(r.URL.Path, "/watch/providers/"):
			if q.Get("watch_region") == "GB" {
				_, _ = io.WriteString(w, `{"results":[{"provider_id":8,"provider_name":"Netflix","display_priority":0},{"provider_id":39,"provider_name":"Sky Go","display_priority":1}]}`)
			} else {
				_, _ = io.WriteString(w, `{"results":[{"provider_id":8,"provider_name":"Netflix","display_priority":0},{"provider_id":337,"provider_name":"Disney Plus","display_priority":1},{"provider_id":9,"provider_name":"Amazon Prime Video","display_priority":2}]}`)
			}
		case r.URL.Path == "/search/keyword":
			if strings.Contains(strings.ToLower(q.Get("query")), "super") {
				_, _ = io.WriteString(w, `{"results":[{"id":9715,"name":"superhero"}]}`)
			} else {
				_, _ = io.WriteString(w, `{"results":[]}`)
			}
		case r.URL.Path == "/search/company":
			if strings.Contains(strings.ToLower(q.Get("query")), "marvel") {
				_, _ = io.WriteString(w, `{"results":[{"id":420,"name":"Marvel Studios","origin_country":"US"}]}`)
			} else {
				_, _ = io.WriteString(w, `{"results":[]}`)
			}
		case strings.HasPrefix(r.URL.Path, "/discover/"):
			f.mu.Lock()
			f.discover = append(f.discover, q)
			f.mu.Unlock()
			if q.Get("vote_count.gte") == "99999" {
				_, _ = io.WriteString(w, `{"page":1,"total_pages":0,"total_results":0,"results":[]}`)
				return
			}
			_, _ = io.WriteString(w, `{"page":2,"total_pages":37,"total_results":733,"results":[{"id":603,"title":"The Matrix","overview":"Wake up.","release_date":"1999-03-31","vote_average":8.2},{"id":1399,"name":"Game of Thrones","first_air_date":"2011-04-17","vote_average":8.4}]}`)
		default:
			t.Errorf("unexpected TMDB request %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeBrowseTMDB) lastDiscover(t *testing.T) url.Values {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.discover) == 0 {
		t.Fatal("no discover request reached TMDB")
	}
	return f.discover[len(f.discover)-1]
}

func (f *fakeBrowseTMDB) discoverCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.discover)
}

type stubDiscoveryPrefs struct{ englishOnly bool }

func (p stubDiscoveryPrefs) Get() serversettings.Settings {
	return serversettings.Settings{DiscoveryEnglishOnly: p.englishOnly}
}

func newBrowseToolServer(t *testing.T, fake *fakeBrowseTMDB) *ToolServer {
	t.Helper()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	registry := credentials.NewRegistry(database, nil,
		credentials.WithDefaultTMDBToken("test-token"),
		credentials.WithTMDBBaseURL(fake.server.URL),
	)
	return NewToolServer(registry, nil, nil, nil)
}

func browse(t *testing.T, s *ToolServer, input string) *ToolResult {
	t.Helper()
	result, err := s.browseTitles(context.Background(), json.RawMessage(input), nil)
	if err != nil {
		t.Fatalf("browse_titles(%s): %v", input, err)
	}
	return result
}

func TestBrowseTitlesForwardsEveryFilter(t *testing.T) {
	fake := newFakeBrowseTMDB(t)
	s := newBrowseToolServer(t, fake)
	s.SetDiscoveryPrefs(stubDiscoveryPrefs{englishOnly: true})

	result := browse(t, s, `{"media_type":"movie","genres":["Science Fiction","Comedy"],"year_from":2015,"year_to":2020,
		"min_rating":7,"min_votes":100,"sort":"top_rated","original_language":"Korean",
		"streaming_services":["Netflix","disney+"],"region":"us","keywords":["super hero"],"studios":["Marvel"],"page":2}`)

	q := fake.lastDiscover(t)
	for key, want := range map[string]string{
		"with_genres":              "878,35",
		"primary_release_date.gte": "2015-01-01",
		"primary_release_date.lte": "2020-12-31",
		"vote_average.gte":         "7",
		"vote_count.gte":           "100",
		"sort_by":                  "vote_average.desc",
		"with_original_language":   "ko",
		"with_watch_providers":     "8|337",
		"watch_region":             "US",
		"with_keywords":            "9715",
		"with_companies":           "420",
		"page":                     "2",
	} {
		if got := q.Get(key); got != want {
			t.Errorf("upstream %s = %q, want %q", key, got, want)
		}
	}
	if got := q["language"]; len(got) != 1 || got[0] != "en-US" {
		t.Errorf("upstream language = %v, want exactly [en-US]", got)
	}
	for _, want := range []string{"Science Fiction + Comedy", "years 2015 to 2020", "rating 7+", "in Korean",
		"on Netflix or Disney Plus (US)", "about superhero", "from Marvel Studios", "Page 2 of 37 (733 titles)",
		`keyword "super hero" matched as "superhero"`, `studio "Marvel" matched as "Marvel Studios"`, "[TMDB ID: 603]"} {
		if !strings.Contains(result.Text, want) {
			t.Errorf("text lacks %q:\n%s", want, result.Text)
		}
	}
	if strings.Contains(result.Text, "English-language originals only") {
		t.Errorf("a named language must switch the English-only note off:\n%s", result.Text)
	}
	items, ok := result.StructuredData.([]MediaResultItem)
	if !ok || len(items) != 2 || items[0].MediaType != "movie" || items[0].ID != 603 {
		t.Fatalf("structured data = %#v, want two movie items led by 603", result.StructuredData)
	}
}

func TestBrowseTitlesResolvesLooseNames(t *testing.T) {
	fake := newFakeBrowseTMDB(t)
	s := newBrowseToolServer(t, fake)

	browse(t, s, `{"media_type":"movie","genres":"sci-fi, COMEDY","streaming_services":"prime video","original_language":"ko"}`)
	q := fake.lastDiscover(t)
	if q.Get("with_genres") != "878,35" {
		t.Errorf("with_genres = %q, want sci-fi and comedy resolved to 878,35", q.Get("with_genres"))
	}
	if q.Get("with_watch_providers") != "9" {
		t.Errorf("with_watch_providers = %q, want prime video resolved to 9", q.Get("with_watch_providers"))
	}
	if q.Get("with_original_language") != "ko" {
		t.Errorf("with_original_language = %q, want the code kept", q.Get("with_original_language"))
	}

	browse(t, s, `{"media_type":"tv","genres":["sci fi"],"sort":"title"}`)
	q = fake.lastDiscover(t)
	if q.Get("with_genres") != "10765" || q.Get("sort_by") != "name.asc" {
		t.Errorf("tv query = %v, want the TV Sci-Fi & Fantasy genre and name.asc", q)
	}
}

func TestBrowseTitlesReportsWhatItCannotResolve(t *testing.T) {
	fake := newFakeBrowseTMDB(t)
	s := newBrowseToolServer(t, fake)

	// One genre unknown: the rest still runs, and the options are listed.
	result := browse(t, s, `{"media_type":"movie","genres":["Comedy","Foo"]}`)
	if fake.lastDiscover(t).Get("with_genres") != "35" {
		t.Errorf("with_genres = %q, want the resolved genre alone", fake.lastDiscover(t).Get("with_genres"))
	}
	if !strings.Contains(result.Text, `genre "Foo"`) || !strings.Contains(result.Text, "Action, Comedy, Science Fiction") {
		t.Errorf("text does not name the unknown genre with the options:\n%s", result.Text)
	}

	// Nothing named resolves: no query, since dropping the filter would
	// answer a different question.
	before := fake.discoverCount()
	result = browse(t, s, `{"media_type":"movie","genres":["Foo"],"studios":["Foo Films"]}`)
	if fake.discoverCount() != before {
		t.Error("a query ran although the named genre resolved to nothing")
	}
	if !strings.Contains(result.Text, "Nothing was searched") || !strings.Contains(result.Text, `genre "Foo"`) {
		t.Errorf("text = %q, want the unresolved report", result.Text)
	}
	if result.StructuredData != nil {
		t.Error("an unresolved browse carried structured data")
	}
}

func TestBrowseTitlesHonorsEnglishOnlyUnlessALanguageIsNamed(t *testing.T) {
	fake := newFakeBrowseTMDB(t)
	s := newBrowseToolServer(t, fake)

	// The shipped default is on, and nil prefs read as that default.
	result := browse(t, s, `{"media_type":"movie"}`)
	if got := fake.lastDiscover(t).Get("with_original_language"); got != "en" {
		t.Errorf("default with_original_language = %q, want en", got)
	}
	if !strings.Contains(result.Text, "English-language originals only") {
		t.Errorf("text does not mention the preference:\n%s", result.Text)
	}

	s.SetDiscoveryPrefs(stubDiscoveryPrefs{englishOnly: false})
	browse(t, s, `{"media_type":"movie"}`)
	if _, ok := fake.lastDiscover(t)["with_original_language"]; ok {
		t.Error("with_original_language sent with the preference off")
	}

	s.SetDiscoveryPrefs(stubDiscoveryPrefs{englishOnly: true})
	browse(t, s, `{"media_type":"movie","original_language":"Korean"}`)
	if got := fake.lastDiscover(t).Get("with_original_language"); got != "ko" {
		t.Errorf("named language sent as %q, want ko", got)
	}
}

func TestBrowseTitlesEmptyPageNamesTheFilters(t *testing.T) {
	fake := newFakeBrowseTMDB(t)
	s := newBrowseToolServer(t, fake)
	s.SetDiscoveryPrefs(stubDiscoveryPrefs{englishOnly: true})

	result := browse(t, s, `{"media_type":"tv","genres":["Comedy"],"year_from":1950,"year_to":1955,"min_votes":99999}`)
	for _, want := range []string{"No TV shows matched", "genres Comedy", "years 1950 to 1955", "votes 99999+",
		"rules out nothing outside them", "Only English-language originals were searched"} {
		if !strings.Contains(result.Text, want) {
			t.Errorf("text lacks %q:\n%s", want, result.Text)
		}
	}
	if result.StructuredData != nil {
		t.Error("an empty page carried structured data, which would owe a carousel")
	}
}

func TestBrowseTitlesClampsThePage(t *testing.T) {
	fake := newFakeBrowseTMDB(t)
	s := newBrowseToolServer(t, fake)

	result := browse(t, s, `{"media_type":"movie","page":9999}`)
	if got := fake.lastDiscover(t).Get("page"); got != "500" {
		t.Errorf("page 9999 reached TMDB as %q, want 500", got)
	}
	if !strings.Contains(result.Text, "Page 9999 clamped to 500") {
		t.Errorf("text does not say the page was clamped:\n%s", result.Text)
	}
	browse(t, s, `{"media_type":"movie","page":0}`)
	if got := fake.lastDiscover(t).Get("page"); got != "1" {
		t.Errorf("page 0 reached TMDB as %q, want 1", got)
	}
}

func TestBrowseTitlesFloorsRatingSortsAndCapsNewest(t *testing.T) {
	fake := newFakeBrowseTMDB(t)
	s := newBrowseToolServer(t, fake)

	result := browse(t, s, `{"media_type":"movie","sort":"top_rated"}`)
	if got := fake.lastDiscover(t).Get("vote_count.gte"); got != "200" {
		t.Errorf("top_rated without min_votes sent vote_count.gte=%q, want the 200 floor", got)
	}
	if !strings.Contains(result.Text, "votes 200+ (top_rated floor)") {
		t.Errorf("text does not explain the floor:\n%s", result.Text)
	}

	browse(t, s, `{"media_type":"tv","sort":"newest"}`)
	q := fake.lastDiscover(t)
	if q.Get("sort_by") != "first_air_date.desc" || q.Get("first_air_date.lte") == "" {
		t.Errorf("newest TV query = %v, want first_air_date.desc capped at today", q)
	}
	browse(t, s, `{"media_type":"movie","sort":"newest","year_to":2020}`)
	if got := fake.lastDiscover(t).Get("primary_release_date.lte"); got != "2020-12-31" {
		t.Errorf("newest with year_to sent lte=%q, want the named year kept", got)
	}
}

func TestBrowseTitlesRejectsBadInputWithoutDialingOut(t *testing.T) {
	fake := newFakeBrowseTMDB(t)
	s := newBrowseToolServer(t, fake)

	for input, want := range map[string]string{
		`{}`:                                     `needs media_type "movie" or "tv"`,
		`{"media_type":"all"}`:                   `no "all"`,
		`{"media_type":"movie","sort":"random"}`: `does not know sort "random"`,
	} {
		result := browse(t, s, input)
		if !strings.Contains(result.Text, want) {
			t.Errorf("browse_titles(%s) = %q, want %q", input, result.Text, want)
		}
	}
	if fake.discoverCount() != 0 {
		t.Errorf("upstream discover hits = %d, want 0 for rejected input", fake.discoverCount())
	}

	// A tool server with no registry at all answers rather than panics.
	bare := NewToolServer(nil, nil, nil, nil)
	result, err := bare.browseTitles(context.Background(), json.RawMessage(`{}`), nil)
	if err != nil || !strings.Contains(result.Text, "not configured") {
		t.Errorf("nil registry: result = %v, err = %v; want the not-configured text", result, err)
	}
}

func TestBrowseTitlesDispatchesThroughExecuteTool(t *testing.T) {
	fake := newFakeBrowseTMDB(t)
	s := newBrowseToolServer(t, fake)
	s.SetCallAuthorizer(func(_ context.Context, callCtx CallContext) (string, error) { return callCtx.Role, nil })

	result, err := s.ExecuteTool(context.Background(), "browse_titles",
		json.RawMessage(`{"media_type":"movie","genres":["Action"]}`), CallContext{Role: auth.RoleUser})
	if err != nil {
		t.Fatalf("ExecuteTool: %v", err)
	}
	if fake.lastDiscover(t).Get("with_genres") != "28" {
		t.Errorf("dispatch did not reach the executor: %v", fake.lastDiscover(t))
	}
	// ExecuteTool serializes structured data for the wire; what matters is
	// that the browse carried items through it at all.
	if result.StructuredData == nil || !strings.Contains(result.Text, "[TMDB ID: 603]") {
		t.Errorf("dispatched result = %q with structured data %#v, want the browsed titles", result.Text, result.StructuredData)
	}
}
