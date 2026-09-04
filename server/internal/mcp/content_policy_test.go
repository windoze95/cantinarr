package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/contentpolicy"
	"github.com/windoze95/cantinarr-server/internal/credentials"
	"github.com/windoze95/cantinarr-server/internal/db"
)

// fakeKidsTMDB serves what the discover tools and the kids-account lookups
// ask for: searches with adult/genre flags, per-title ratings, details, a
// collection, a discover page, and the schemes.
type fakeKidsTMDB struct {
	server   *httptest.Server
	mu       sync.Mutex
	discover []url.Values
}

func newFakeKidsTMDB(t *testing.T) *fakeKidsTMDB {
	t.Helper()
	f := &fakeKidsTMDB{}
	ratings := map[string]string{
		"/movie/1/release_dates": `{"id":1,"results":[{"iso_3166_1":"US","release_dates":[{"certification":"G","type":3}]}]}`,
		"/movie/2/release_dates": `{"id":2,"results":[{"iso_3166_1":"US","release_dates":[{"certification":"R","type":3}]}]}`,
		"/movie/7/release_dates": `{"id":7,"results":[{"iso_3166_1":"US","release_dates":[{"certification":"PG","type":3}]}]}`,
		"/tv/5/content_ratings":  `{"id":5,"results":[{"iso_3166_1":"US","rating":"TV-Y"}]}`,
		"/tv/6/content_ratings":  `{"id":6,"results":[{"iso_3166_1":"US","rating":"TV-MA"}]}`,
	}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		path := r.URL.Path
		if body, ok := ratings[path]; ok {
			_, _ = io.WriteString(w, body)
			return
		}
		switch {
		case path == "/certification/movie/list":
			_, _ = io.WriteString(w, `{"certifications":{"US":[{"certification":"NR","order":0},{"certification":"G","order":1},{"certification":"PG","order":2},{"certification":"PG-13","order":3},{"certification":"R","order":4}]}}`)
		case path == "/certification/tv/list":
			_, _ = io.WriteString(w, `{"certifications":{"US":[{"certification":"NR","order":0},{"certification":"TV-Y","order":1},{"certification":"TV-G","order":3},{"certification":"TV-PG","order":4},{"certification":"TV-MA","order":6}]}}`)
		case path == "/genre/movie/list":
			_, _ = io.WriteString(w, `{"genres":[{"id":18,"name":"Drama"},{"id":27,"name":"Horror"}]}`)
		case path == "/genre/tv/list":
			_, _ = io.WriteString(w, `{"genres":[{"id":18,"name":"Drama"}]}`)
		case path == "/search/movie":
			_, _ = io.WriteString(w, `{"results":[
				{"id":1,"title":"Gentle","release_date":"2020-01-01","genre_ids":[18]},
				{"id":2,"title":"Grown","release_date":"2021-01-01","genre_ids":[18]},
				{"id":3,"title":"Gory","release_date":"2022-01-01","genre_ids":[27]},
				{"id":4,"title":"Blue","release_date":"2023-01-01","adult":true}
			]}`)
		case path == "/search/tv":
			_, _ = io.WriteString(w, `{"results":[{"id":5,"name":"Soft","first_air_date":"2020-01-01"},{"id":6,"name":"Hard","first_air_date":"2020-01-01"}]}`)
		case path == "/movie/1":
			_, _ = io.WriteString(w, `{"id":1,"title":"Gentle","release_date":"2020-01-01","genres":[{"id":18,"name":"Drama"}]}`)
		case path == "/movie/2":
			_, _ = io.WriteString(w, `{"id":2,"title":"Grown","release_date":"2021-01-01","genres":[{"id":18,"name":"Drama"}]}`)
		case path == "/movie/3":
			_, _ = io.WriteString(w, `{"id":3,"title":"Gory","release_date":"2022-01-01","genres":[{"id":27,"name":"Horror"}]}`)
		case path == "/tv/6":
			_, _ = io.WriteString(w, `{"id":6,"name":"Hard","first_air_date":"2020-01-01","genres":[{"id":18,"name":"Drama"}]}`)
		case path == "/movie/1/recommendations":
			_, _ = io.WriteString(w, `{"results":[{"id":7,"title":"Fine","release_date":"2020-01-01","genre_ids":[18]},{"id":2,"title":"Grown","release_date":"2021-01-01","genre_ids":[18]}]}`)
		case path == "/search/collection":
			_, _ = io.WriteString(w, `{"results":[{"id":100,"name":"Mixed Collection"}]}`)
		case path == "/collection/100":
			_, _ = io.WriteString(w, `{"id":100,"name":"Mixed Collection","parts":[{"id":1,"title":"Gentle","release_date":"2020-01-01","genre_ids":[18]},{"id":2,"title":"Grown","release_date":"2021-01-01","genre_ids":[18]}]}`)
		case strings.HasPrefix(path, "/discover/"):
			f.mu.Lock()
			f.discover = append(f.discover, r.URL.Query())
			f.mu.Unlock()
			_, _ = io.WriteString(w, `{"page":1,"total_pages":1,"total_results":2,"results":[{"id":1,"title":"Gentle","release_date":"2020-01-01","genre_ids":[18]},{"id":2,"title":"Grown","release_date":"2021-01-01","genre_ids":[18]}]}`)
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"status_code":34}`)
		}
	}))
	t.Cleanup(f.server.Close)
	return f
}

type kidsToolEnv struct {
	server *ToolServer
	db     *sql.DB
	fake   *fakeKidsTMDB
	kid    CallContext
	adult  CallContext
	admin  CallContext
}

func newKidsToolEnv(t *testing.T) *kidsToolEnv {
	t.Helper()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	fake := newFakeKidsTMDB(t)
	registry := credentials.NewRegistry(database, nil,
		credentials.WithDefaultTMDBToken("test-token"),
		credentials.WithTMDBBaseURL(fake.server.URL),
	)
	server := NewToolServer(registry, nil, nil, nil)
	server.SetCallAuthorizer(func(_ context.Context, callCtx CallContext) (string, error) {
		return callCtx.Role, nil
	})
	policies := contentpolicy.New(database, func() contentpolicy.RawGetter {
		if client := registry.TMDB(); client != nil {
			return client
		}
		return nil
	}, nil)
	server.SetContentPolicy(policies)

	insert := func(name, role string) int64 {
		res, err := database.Exec("INSERT INTO users (username, password_hash, role) VALUES (?, '', ?)", name, role)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := res.LastInsertId()
		return id
	}
	kid := insert("kid", auth.RoleUser)
	adult := insert("adult", auth.RoleUser)
	admin := insert("admin", auth.RoleAdmin)
	if err := policies.Store.Set(kid, contentpolicy.Policy{MaxMovieRating: "PG", MaxTVRating: "TV-PG", RatingRegion: "US", BlockUnrated: true, BlockedMovieGenres: []int{27}}); err != nil {
		t.Fatal(err)
	}
	return &kidsToolEnv{
		server: server,
		db:     database,
		fake:   fake,
		kid:    CallContext{UserID: kid, Role: auth.RoleUser, DeviceID: "kid-device"},
		adult:  CallContext{UserID: adult, Role: auth.RoleUser, DeviceID: "adult-device"},
		admin:  CallContext{UserID: admin, Role: auth.RoleAdmin, DeviceID: "admin-device"},
	}
}

func (e *kidsToolEnv) run(t *testing.T, callCtx CallContext, tool, input string) *ToolResult {
	t.Helper()
	result, err := e.server.ExecuteTool(context.Background(), tool, json.RawMessage(input), callCtx)
	if err != nil {
		t.Fatalf("%s: %v", tool, err)
	}
	return result
}

// itemIDs reads the carousel items back through JSON: ExecuteTool's
// sanitizer round-trips structured data, so the concrete type is gone.
func itemIDs(t *testing.T, result *ToolResult) []int {
	t.Helper()
	raw, err := json.Marshal(result.StructuredData)
	if err != nil {
		t.Fatal(err)
	}
	var items []MediaResultItem
	if err := json.Unmarshal(raw, &items); err != nil {
		t.Fatalf("structured data = %s: %v", raw, err)
	}
	ids := make([]int, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	return ids
}

func TestSearchMoviesHidesBlockedTitlesAndSaysHowMany(t *testing.T) {
	e := newKidsToolEnv(t)

	kid := e.run(t, e.kid, "search_movies", `{"query":"g"}`)
	if ids := itemIDs(t, kid); len(ids) != 1 || ids[0] != 1 {
		t.Fatalf("kid sees %v, want only the G title", ids)
	}
	if !strings.Contains(kid.Text, "3 titles hidden by this account's content limits") {
		t.Fatalf("kid text = %q", kid.Text)
	}
	if strings.Contains(kid.Text, "Grown") || strings.Contains(kid.Text, "Gory") || strings.Contains(kid.Text, "Blue") {
		t.Fatalf("hidden titles named in the text: %q", kid.Text)
	}

	adult := e.run(t, e.adult, "search_movies", `{"query":"g"}`)
	if ids := itemIDs(t, adult); len(ids) != 4 {
		t.Fatalf("adult sees %v", ids)
	}
	if strings.Contains(adult.Text, "hidden by") {
		t.Fatalf("adult text carries a hidden note: %q", adult.Text)
	}
	admin := e.run(t, e.admin, "search_movies", `{"query":"g"}`)
	if ids := itemIDs(t, admin); len(ids) != 4 {
		t.Fatalf("admin sees %v", ids)
	}
	trusted := e.run(t, CallContext{Role: auth.RoleAdmin, TrustedInternal: true}, "search_movies", `{"query":"g"}`)
	if ids := itemIDs(t, trusted); len(ids) != 4 {
		t.Fatalf("trusted internal sees %v", ids)
	}

	tv := e.run(t, e.kid, "search_tv_shows", `{"query":"s"}`)
	if ids := itemIDs(t, tv); len(ids) != 1 || ids[0] != 5 {
		t.Fatalf("kid tv sees %v", ids)
	}
	if !strings.Contains(tv.Text, "1 title hidden") {
		t.Fatalf("kid tv text = %q", tv.Text)
	}
}

func TestDetailsAndRecommendationsRespectTheLimits(t *testing.T) {
	e := newKidsToolEnv(t)

	blocked := e.run(t, e.kid, "get_movie_details", `{"tmdb_id":2}`)
	if blocked.Text != titleNotAvailableText {
		t.Fatalf("kid details of an R title = %q", blocked.Text)
	}
	if strings.Contains(blocked.Text, "Grown") {
		t.Fatal("a hidden title was described")
	}
	genre := e.run(t, e.kid, "get_movie_details", `{"tmdb_id":3}`)
	if genre.Text != titleNotAvailableText {
		t.Fatalf("kid details of a hidden-genre title = %q", genre.Text)
	}
	allowed := e.run(t, e.kid, "get_movie_details", `{"tmdb_id":1}`)
	if !strings.Contains(allowed.Text, "Gentle") {
		t.Fatalf("kid details of a G title = %q", allowed.Text)
	}
	if adult := e.run(t, e.adult, "get_movie_details", `{"tmdb_id":2}`); !strings.Contains(adult.Text, "Grown") {
		t.Fatalf("adult details = %q", adult.Text)
	}
	if tv := e.run(t, e.kid, "get_tv_details", `{"tmdb_id":6}`); tv.Text != titleNotAvailableText {
		t.Fatalf("kid details of a TV-MA show = %q", tv.Text)
	}

	recs := e.run(t, e.kid, "get_recommendations", `{"tmdb_id":1,"media_type":"movie"}`)
	if ids := itemIDs(t, recs); len(ids) != 1 || ids[0] != 7 {
		t.Fatalf("kid recommendations = %v", ids)
	}
}

func TestDisplayMediaRejectsBlockedItems(t *testing.T) {
	e := newKidsToolEnv(t)
	result := e.run(t, e.kid, "display_media", `{"items":[{"tmdb_id":1,"media_type":"movie","title":"Gentle","year":"2020"},{"tmdb_id":2,"media_type":"movie","title":"Grown","year":"2021"},{"tmdb_id":6,"media_type":"tv","title":"Hard","year":"2020"}]}`)
	if ids := itemIDs(t, result); len(ids) != 1 || ids[0] != 1 {
		t.Fatalf("kid carousel = %v", ids)
	}
	if !strings.Contains(result.Text, "movie 2: not available for this account") || !strings.Contains(result.Text, "tv 6: not available for this account") {
		t.Fatalf("display_media text = %q", result.Text)
	}
	adult := e.run(t, e.adult, "display_media", `{"items":[{"tmdb_id":2,"media_type":"movie","title":"Grown","year":"2021"}]}`)
	if ids := itemIDs(t, adult); len(ids) != 1 {
		t.Fatalf("adult carousel = %v", ids)
	}
}

func TestSearchMovieCollectionsFiltersParts(t *testing.T) {
	e := newKidsToolEnv(t)
	kid := e.run(t, e.kid, "search_movie_collections", `{"query":"mixed"}`)
	if !strings.Contains(kid.Text, "1 movie(s)") || strings.Contains(kid.Text, "Grown") || !strings.Contains(kid.Text, "1 title hidden") {
		t.Fatalf("kid collections = %q", kid.Text)
	}
	adult := e.run(t, e.adult, "search_movie_collections", `{"query":"mixed"}`)
	if !strings.Contains(adult.Text, "2 movie(s)") || strings.Contains(adult.Text, "hidden") {
		t.Fatalf("adult collections = %q", adult.Text)
	}
}

func TestBrowseTitlesPushesTheLimitsUpstreamAndFilters(t *testing.T) {
	e := newKidsToolEnv(t)
	kid := e.run(t, e.kid, "browse_titles", `{"media_type":"movie","genres":["Drama"]}`)
	if ids := itemIDs(t, kid); len(ids) != 1 || ids[0] != 1 {
		t.Fatalf("kid browse = %v", ids)
	}
	if !strings.Contains(kid.Text, "content limits") {
		t.Fatalf("kid browse text = %q", kid.Text)
	}
	e.fake.mu.Lock()
	q := e.fake.discover[len(e.fake.discover)-1]
	e.fake.mu.Unlock()
	if q.Get("certification_country") != "US" || q.Get("certification.lte") != "PG" || q.Get("without_genres") != "27" || q.Get("include_adult") != "false" {
		t.Fatalf("kid browse query = %v", q)
	}

	adult := e.run(t, e.adult, "browse_titles", `{"media_type":"movie","genres":["Drama"]}`)
	if ids := itemIDs(t, adult); len(ids) != 2 {
		t.Fatalf("adult browse = %v", ids)
	}
	e.fake.mu.Lock()
	q = e.fake.discover[len(e.fake.discover)-1]
	e.fake.mu.Unlock()
	if q.Get("certification.lte") != "" || q.Get("include_adult") != "" {
		t.Fatalf("adult browse query carries kids limits: %v", q)
	}
}

func TestPolicyReadFailureFailsTheCall(t *testing.T) {
	e := newKidsToolEnv(t)
	if err := e.db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := e.server.ExecuteTool(context.Background(), "search_movies", json.RawMessage(`{"query":"g"}`), e.kid); err == nil {
		t.Fatal("a policy that cannot be read must fail the call rather than answer unfiltered")
	} else if !strings.Contains(err.Error(), "content limits") {
		t.Fatalf("err = %v", err)
	}
}

func TestCallerPolicySkipsAdminsTrustedAndUnwired(t *testing.T) {
	e := newKidsToolEnv(t)
	if p, err := e.server.callerPolicy(e.admin); err != nil || p != nil {
		t.Fatalf("admin policy = %v, %v", p, err)
	}
	if p, err := e.server.callerPolicy(CallContext{TrustedInternal: true, Role: auth.RoleAdmin}); err != nil || p != nil {
		t.Fatalf("trusted policy = %v, %v", p, err)
	}
	if p, err := e.server.callerPolicy(e.kid); err != nil || p == nil {
		t.Fatalf("kid policy = %v, %v", p, err)
	}
	e.server.SetContentPolicy(nil)
	if p, err := e.server.callerPolicy(e.kid); err != nil || p != nil {
		t.Fatalf("unwired policy = %v, %v", p, err)
	}
	_ = fmt.Sprintf
}
