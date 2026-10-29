package mediametadata

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/windoze95/cantinarr-server/internal/cache"
	"github.com/windoze95/cantinarr-server/internal/credentials"
	"github.com/windoze95/cantinarr-server/internal/db"
	"github.com/windoze95/cantinarr-server/internal/secrets"
)

func testService(t *testing.T, handler http.HandlerFunc) *Service {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	cipher, err := secrets.NewCipher(bytes.Repeat([]byte{42}, 32))
	if err != nil {
		t.Fatal(err)
	}
	creds := credentials.NewRegistry(database, cipher, credentials.WithDefaultTMDBToken("test"), credentials.WithTMDBBaseURL(server.URL))
	c := cache.New()
	t.Cleanup(c.Close)
	return New(creds, c)
}
func TestPresentationLocaleFallbackAndCacheIsolation(t *testing.T) {
	var hits atomic.Int32
	s := testService(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if len(r.URL.Query()["language"]) != 1 {
			t.Error("duplicate language")
		}
		if r.URL.Query().Get("language") == "fr-FR" {
			fmt.Fprint(w, `{"id":7,"title":"Titre français","overview":"","poster_path":null,"adult":true}`)
		} else {
			fmt.Fprint(w, `{"id":7,"title":"English title","overview":"English overview","poster_path":"/english.jpg","adult":false}`)
		}
	})
	p, err := s.Presentation(context.Background(), "movie", 7, "fr-FR")
	if err != nil {
		t.Fatal(err)
	}
	if p.Title != "Titre français" || p.SourceTitle != "English title" || p.Overview != "English overview" || p.PosterPath != "/english.jpg" {
		t.Fatalf("fallback: %+v", p)
	}
	en, err := s.Presentation(context.Background(), "movie", 7, "en-US")
	if err != nil || en.Title != "English title" {
		t.Fatalf("English: %+v %v", en, err)
	}
	_, _ = s.Presentation(context.Background(), "movie", 7, "fr-FR")
	if hits.Load() != 3 {
		t.Fatalf("cache hits: %d", hits.Load())
	}
}
func TestRawFallbackNeverChangesMembershipOrderOrPolicyFields(t *testing.T) {
	s := testService(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("language") == "fr-FR" {
			fmt.Fprint(w, `{"results":[{"id":2,"media_type":"movie","title":"Deux","overview":"","adult":true},{"id":1,"media_type":"tv","name":"Un","overview":""}]}`)
		} else {
			fmt.Fprint(w, `{"results":[{"id":1,"media_type":"movie","title":"Different identity","overview":"Wrong"},{"id":2,"media_type":"movie","title":"Two","overview":"Right","adult":false},{"id":3,"title":"Never add"}]}`)
		}
	})
	raw, err := s.Raw(context.Background(), "/search/multi", url.Values{"query": {"test"}}, "fr-FR")
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Results []map[string]any `json:"results"`
	}
	if err = json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Results) != 2 || body.Results[0]["id"] != float64(2) || body.Results[0]["overview"] != "Right" || body.Results[0]["adult"] != true || body.Results[1]["overview"] != "" {
		t.Fatalf("unsafe merge: %s", raw)
	}
}
func TestPresentationCoalescesAndLimitsConcurrency(t *testing.T) {
	var hits, active, maximum atomic.Int32
	s := testService(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		n := active.Add(1)
		defer active.Add(-1)
		for old := maximum.Load(); n > old; old = maximum.Load() {
			if maximum.CompareAndSwap(old, n) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		var id int
		fmt.Sscanf(r.URL.Path, "/movie/%d", &id)
		fmt.Fprintf(w, `{"id":%d,"title":"Title"}`, id)
	})
	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := s.Presentation(context.Background(), "movie", i%8+1, "en-US"); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	if hits.Load() != 8 || maximum.Load() > 4 {
		t.Fatalf("hits %d max concurrency %d", hits.Load(), maximum.Load())
	}
}
func TestEpisodeRequiresExactExternalIdentity(t *testing.T) {
	s := testService(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/tv/1/season/2/episode/3/external_ids" {
			fmt.Fprint(w, `{"id":99,"tvdb_id":900}`)
			return
		}
		fmt.Fprint(w, `{"id":99,"name":"Épisode","season_number":2,"episode_number":3}`)
	})
	if _, err := s.Episode(context.Background(), 1, 2, 3, 901, "fr-FR"); err == nil {
		t.Fatal("accepted different episode")
	}
	p, err := s.Episode(context.Background(), 1, 2, 3, 900, "fr-FR")
	if err != nil || p.Title != "Épisode" {
		t.Fatalf("episode %+v %v", p, err)
	}
}
func TestLanguageIsExplicitAndNormalizesUnsupported(t *testing.T) {
	r := httptest.NewRequest("GET", "/media/movie/1", nil)
	r.Header.Set("Accept-Language", "fr-FR")
	if Language(r) != "" {
		t.Fatal("old browser changed language")
	}
	for raw, want := range map[string]string{
		"": "", "fr": "fr-FR", "fr-FR": "fr-FR", "en": "en-US", "en-US": "en-US",
		"de": "de", "DE-de": "de-DE", "es": "es", " es-es ": "es-ES",
		"pt-BR": "pt-BR", "pt-PT": "pt-PT", "fr-CA": "fr-CA", "en-gb": "en-GB",
		"tl": "tl", "tl-PH": "tl-PH", "iw": "he", "IW-il": "he-IL",
		"no": "no", "nb": "nb", "\ten-us\n": "en-US",
	} {
		if got := NormalizeLanguage(raw); got != want {
			t.Errorf("%q => %q", raw, got)
		}
	}
	for _, raw := range []string{
		"zz", "zz-US", "en-ZZ", "en-AA", "en-XK", "en-EU", "de-419",
		"zh-Hant", "zh-Hant-TW", "en-US-x-private", "i-klingon", "pt_BR",
		"fil-PH", "qaa", "en-XX", "e1", "1a", "éé", "en-USx", "en--US",
		"en-US\nsuffix", strings.Repeat("a", 4096),
	} {
		if got := NormalizeLanguage(raw); got != "en-US" {
			t.Errorf("unsupported %q => %q", raw, got)
		}
	}
}

func TestNewLanguageTagsPreserveRegionalMetadataAndCanonicalCacheKeys(t *testing.T) {
	titles := map[string]string{
		"en-US": "Original", "es-ES": "Español", "de": "Deutsch",
		"pt-BR": "Brasileiro", "pt-PT": "Português", "tl-PH": "Tagalog", "he-IL": "עברית",
	}
	var mu sync.Mutex
	hits := map[string]int{}
	s := testService(t, func(w http.ResponseWriter, r *http.Request) {
		language := r.URL.Query().Get("language")
		mu.Lock()
		hits[language]++
		mu.Unlock()
		title, ok := titles[language]
		if !ok {
			t.Errorf("unvalidated metadata language reached provider: %q", language)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 7, "title": title})
	})
	for _, query := range []struct{ raw, canonical string }{
		{"en-US", "en-US"}, {"es-es", "es-ES"}, {"ES-ES", "es-ES"},
		{"DE", "de"}, {"de", "de"}, {"pt-BR", "pt-BR"}, {"pt-PT", "pt-PT"},
		{"tl-PH", "tl-PH"}, {"iw-IL", "he-IL"}, {"he-IL", "he-IL"},
	} {
		p, err := s.Presentation(context.Background(), "movie", 7, query.raw)
		if err != nil || p == nil || p.Title != titles[query.canonical] {
			t.Fatalf("%q presentation: %+v, %v", query.raw, p, err)
		}
		if query.canonical != "en-US" && p.SourceTitle != "Original" {
			t.Fatalf("%q lost canonical source title: %+v", query.raw, p)
		}
	}
	mu.Lock()
	for language := range titles {
		if language != "en-US" && hits[language] != 1 {
			t.Errorf("%s provider reads = %d, want one canonical cache entry", language, hits[language])
		}
	}
	englishReads := hits["en-US"]
	mu.Unlock()
	for _, raw := range []string{"zz-ZZ", "en-US-x-unique", "zh-Hant", "qaa"} {
		p, err := s.Presentation(context.Background(), "movie", 7, raw)
		if err != nil || p == nil || p.Title != "Original" {
			t.Fatalf("invalid %q fallback: %+v, %v", raw, p, err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if hits["en-US"] != englishReads || len(hits) != len(titles) {
		t.Fatalf("invalid tags created provider/cache partitions: %+v", hits)
	}
}

func TestLocalizedOutageAndMissingOriginalNameFallback(t *testing.T) {
	for _, mode := range []string{"French unavailable", "English unavailable"} {
		t.Run(mode, func(t *testing.T) {
			s := testService(t, func(w http.ResponseWriter, r *http.Request) {
				french := r.URL.Query().Get("language") == "fr-FR"
				if (mode == "French unavailable" && french) || (mode == "English unavailable" && !french) {
					w.WriteHeader(503)
					return
				}
				if french {
					fmt.Fprint(w, `{"id":1,"name":"","original_name":"Original name"}`)
				} else {
					fmt.Fprint(w, `{"id":1,"name":"English name","overview":"Fallback"}`)
				}
			})
			p, err := s.Presentation(context.Background(), "tv", 1, "fr-FR")
			if err != nil {
				t.Fatal(err)
			}
			want := "English name"
			if mode == "English unavailable" {
				want = "Original name"
			}
			if p.Title != want {
				t.Fatalf("%+v", p)
			}
		})
	}
}

func TestLocalizedAndFallbackRequestsRunTogether(t *testing.T) {
	englishStarted := make(chan struct{})
	s := testService(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("language") == "en-US" {
			close(englishStarted)
			fmt.Fprint(w, `{"id":1,"title":"English"}`)
			return
		}
		select {
		case <-englishStarted:
			fmt.Fprint(w, `{"id":1,"title":"Français"}`)
		case <-r.Context().Done():
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	p, err := s.Presentation(ctx, "movie", 1, "fr-FR")
	if err != nil || p.Title != "Français" || p.SourceTitle != "English" {
		t.Fatalf("parallel fallback %+v %v", p, err)
	}
}

func TestUnknownCanonicalTitleIsAnExplicitEmptySentinel(t *testing.T) {
	s := testService(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("language") == "en-US" {
			w.WriteHeader(503)
			return
		}
		fmt.Fprint(w, `{"id":1,"name":"Titre traduit"}`)
	})
	raw, err := s.Raw(context.Background(), "/tv/1", nil, "fr-FR")
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err = json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	if source, exists := value["source_name"]; !exists || source != "" {
		t.Fatalf("missing canonical sentinel: %s", raw)
	}
	p, err := s.Presentation(context.Background(), "tv", 1, "fr-FR")
	if err != nil || p.Title != "Titre traduit" || p.SourceTitle != "" {
		t.Fatalf("presentation reused translated title: %+v %v", p, err)
	}
}
func TestEmptyLocalizedVideosUseEnglishWithoutBackfillingTitleLists(t *testing.T) {
	s := testService(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("language") == "fr-FR" {
			fmt.Fprint(w, `{"id":1,"title":"Français","videos":{"results":[]},"recommendations":{"results":[]}}`)
			return
		}
		fmt.Fprint(w, `{"id":1,"title":"English","videos":{"results":[{"id":"trailer","key":"youtube-key","name":"Trailer","site":"YouTube"}]},"recommendations":{"results":[{"id":2,"title":"Never add"}]}}`)
	})
	raw, err := s.Raw(context.Background(), "/movie/1", nil, "fr-FR")
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Videos struct {
			Results []any `json:"results"`
		} `json:"videos"`
		Recommendations struct {
			Results []any `json:"results"`
		} `json:"recommendations"`
	}
	if err = json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Videos.Results) != 1 || len(body.Recommendations.Results) != 0 {
		t.Fatalf("unsafe trailer fallback: %s", raw)
	}
}
