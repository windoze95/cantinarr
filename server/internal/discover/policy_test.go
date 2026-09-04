package discover

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/contentpolicy"
	"github.com/windoze95/cantinarr-server/internal/serversettings"
)

// Kids accounts through the discover routes. The fake upstream answers the
// certification schemes, per-title ratings, and whatever list a test hands
// it, so the filter's decisions and its cache discipline are observable as
// upstream hits.

const (
	certMovieList = `{"certifications":{"US":[{"certification":"NR","order":0},{"certification":"G","order":1},{"certification":"PG","order":2},{"certification":"PG-13","order":3},{"certification":"R","order":4},{"certification":"NC-17","order":5}]}}`
	certTVList    = `{"certifications":{"US":[{"certification":"NR","order":0},{"certification":"TV-Y","order":1},{"certification":"TV-Y7","order":2},{"certification":"TV-G","order":3},{"certification":"TV-PG","order":4},{"certification":"TV-14","order":5},{"certification":"TV-MA","order":6}]}}`
)

// kidsUpstream is a path table for the fake transport: ratings per title,
// the schemes, and the list bodies a test registers. Unknown paths are 404
// (TMDB's answer for an unknown title).
type kidsUpstream struct {
	bodies map[string]string
	status map[string]int
}

func newKidsUpstream() *kidsUpstream {
	u := &kidsUpstream{bodies: map[string]string{}, status: map[string]int{}}
	u.bodies["/3/certification/movie/list"] = certMovieList
	u.bodies["/3/certification/tv/list"] = certTVList
	u.bodies["/3/genre/movie/list"] = `{"genres":[{"id":18,"name":"Drama"},{"id":27,"name":"Horror"},{"id":35,"name":"Comedy"}]}`
	u.bodies["/3/genre/tv/list"] = `{"genres":[{"id":18,"name":"Drama"},{"id":10768,"name":"War & Politics"},{"id":10762,"name":"Kids"}]}`
	return u
}

func (u *kidsUpstream) movie(id int, cert string) {
	u.bodies[fmt.Sprintf("/3/movie/%d/release_dates", id)] = fmt.Sprintf(`{"id":%d,"results":[{"iso_3166_1":"US","release_dates":[{"certification":%q,"type":3}]}]}`, id, cert)
}

func (u *kidsUpstream) tv(id int, rating string) {
	u.bodies[fmt.Sprintf("/3/tv/%d/content_ratings", id)] = fmt.Sprintf(`{"id":%d,"results":[{"iso_3166_1":"US","rating":%q}]}`, id, rating)
}

func (u *kidsUpstream) respond(req *http.Request) (int, string) {
	if status, ok := u.status[req.URL.Path]; ok {
		return status, `{"error":"upstream"}`
	}
	if body, ok := u.bodies[req.URL.Path]; ok {
		return http.StatusOK, body
	}
	return http.StatusNotFound, `{"status_code":34,"status_message":"not found"}`
}

// kidsEnv is the discover env with a kids account and an adult signed in
// through the same fake identity injection AuthMiddleware performs.
type kidsEnv struct {
	*env
	upstreamTable *kidsUpstream
	kid           *auth.User
	adult         *auth.User
	admin         *auth.User
	policies      *contentpolicy.Service
}

func newKidsEnv(t *testing.T) *kidsEnv {
	t.Helper()
	e := newEnv(t, true)
	table := newKidsUpstream()
	e.upstream.setRespond(table.respond)
	policies := contentpolicy.New(e.database, func() contentpolicy.RawGetter {
		if client := e.creds.TMDB(); client != nil {
			return client
		}
		return nil
	}, e.cache)
	e.handler.SetContentPolicy(policies)

	insert := func(name, role string) *auth.User {
		res, err := e.database.Exec("INSERT INTO users (username, password_hash, role) VALUES (?, '', ?)", name, role)
		if err != nil {
			t.Fatalf("insert %s: %v", name, err)
		}
		id, _ := res.LastInsertId()
		return &auth.User{ID: id, Username: name, Role: role}
	}
	kid := insert("kid", auth.RoleUser)
	if err := policies.Store.Set(kid.ID, contentpolicy.Policy{MaxMovieRating: "PG", MaxTVRating: "TV-PG", RatingRegion: "US", BlockUnrated: true, BlockedMovieGenres: []int{27}, BlockedTVGenres: []int{10768}}); err != nil {
		t.Fatalf("set policy: %v", err)
	}
	kid.Child = true
	return &kidsEnv{
		env:           e,
		upstreamTable: table,
		kid:           kid,
		adult:         insert("adult", auth.RoleUser),
		admin:         insert("admin", auth.RoleAdmin),
		policies:      policies,
	}
}

func (k *kidsEnv) as(user *auth.User) *kidsEnv {
	k.identity = user
	return k
}

func resultIDs(t *testing.T, body string) []int {
	t.Helper()
	var envelope struct {
		Results []struct {
			ID int `json:"id"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		t.Fatalf("decode results: %v (%s)", err, body)
	}
	ids := make([]int, 0, len(envelope.Results))
	for _, r := range envelope.Results {
		ids = append(ids, r.ID)
	}
	return ids
}

func TestChildRowsAreFilteredAfterTheSharedCache(t *testing.T) {
	k := newKidsEnv(t)
	k.upstreamTable.bodies["/3/movie/popular"] = `{"page":1,"results":[{"id":1,"title":"Gentle","genre_ids":[18]},{"id":2,"title":"Gory","genre_ids":[27]},{"id":3,"title":"Grown","genre_ids":[18]},{"id":4,"title":"Blue","adult":true},{"id":5,"title":"Unrated"}],"total_pages":3,"total_results":100}`
	k.upstreamTable.movie(1, "G")
	k.upstreamTable.movie(3, "R")
	// 2 is a hidden genre and 4 is adult: neither is ever looked up. 5 has
	// no rating on TMDB: 404 there, unrated here, hidden.

	// The adult warms the shared cache with the verbatim page.
	adultBody := k.as(k.adult).doOK(t, "/discover/movies/popular")
	if ids := resultIDs(t, adultBody); len(ids) != 5 {
		t.Fatalf("adult sees %v, want all five", ids)
	}
	popularHits := func() int {
		n := 0
		for i := 0; i < k.upstream.hitCount(); i++ {
			if k.upstream.hit(t, i).path == "/3/movie/popular" {
				n++
			}
		}
		return n
	}
	if popularHits() != 1 {
		t.Fatalf("popular hits = %d after the adult", popularHits())
	}

	kidBody := k.as(k.kid).doOK(t, "/discover/movies/popular")
	if ids := resultIDs(t, kidBody); len(ids) != 1 || ids[0] != 1 {
		t.Fatalf("kid sees %v, want only the G-rated title", ids)
	}
	if popularHits() != 1 {
		t.Fatalf("popular hits = %d: the kid's read must come from the shared cache", popularHits())
	}
	for i := 0; i < k.upstream.hitCount(); i++ {
		path := k.upstream.hit(t, i).path
		if path == "/3/movie/2/release_dates" || path == "/3/movie/4/release_dates" {
			t.Fatalf("%s was looked up: hidden genres and adult titles need no rating", path)
		}
	}
	if !strings.Contains(kidBody, `"total_pages":3`) {
		t.Fatalf("paging fields must describe the upstream feed: %s", kidBody)
	}

	// The adult after the kid still gets the verbatim page: nothing the
	// kid's filter did reached the cache.
	if ids := resultIDs(t, k.as(k.adult).doOK(t, "/discover/movies/popular")); len(ids) != 5 {
		t.Fatalf("adult after kid sees %v", ids)
	}
	if ids := resultIDs(t, k.as(k.admin).doOK(t, "/discover/movies/popular")); len(ids) != 5 {
		t.Fatalf("admin sees %v", ids)
	}
}

func TestChildDetailIs404AndPrimesTheRatingCache(t *testing.T) {
	k := newKidsEnv(t)
	k.upstreamTable.bodies["/3/movie/603"] = `{"id":603,"title":"The Matrix","adult":false,"genres":[{"id":28}],"release_dates":{"results":[{"iso_3166_1":"US","release_dates":[{"certification":"R","type":3}]}]}}`
	k.upstreamTable.bodies["/3/movie/10"] = `{"id":10,"title":"Toy Story","genres":[{"id":16}],"release_dates":{"results":[{"iso_3166_1":"US","release_dates":[{"certification":"G","type":3}]}]}}`
	k.upstreamTable.bodies["/3/tv/1399"] = `{"id":1399,"name":"Game of Thrones","genres":[{"id":18}],"content_ratings":{"results":[{"iso_3166_1":"US","rating":"TV-MA"}]}}`
	k.upstreamTable.bodies["/3/tv/2"] = `{"id":2,"name":"Bluey","genres":[{"id":16}],"content_ratings":{"results":[{"iso_3166_1":"US","rating":"TV-Y"}]}}`

	rec := k.as(k.kid).do(t, "/media/movie/603")
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "not available") {
		t.Fatalf("kid detail of an R title: %d %s", rec.Code, rec.Body.String())
	}
	if rec := k.as(k.admin).do(t, "/media/movie/603"); rec.Code != http.StatusOK {
		t.Fatalf("admin detail: %d", rec.Code)
	}
	if rec := k.as(k.kid).do(t, "/media/movie/10"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Toy Story") {
		t.Fatalf("kid detail of a G title: %d %s", rec.Code, rec.Body.String())
	}
	if rec := k.as(k.kid).do(t, "/media/tv/1399"); rec.Code != http.StatusNotFound {
		t.Fatalf("kid detail of a TV-MA show: %d", rec.Code)
	}
	if rec := k.as(k.kid).do(t, "/media/tv/2"); rec.Code != http.StatusOK {
		t.Fatalf("kid detail of a TV-Y show: %d %s", rec.Code, rec.Body.String())
	}
	// The TV detail now asks for content_ratings alongside the rest.
	var tvHit bool
	for i := 0; i < k.upstream.hitCount(); i++ {
		hit := k.upstream.hit(t, i)
		if hit.path == "/3/tv/2" {
			tvHit = true
			if !strings.Contains(hit.query.Get("append_to_response"), "content_ratings") {
				t.Fatalf("tv detail append = %q, want content_ratings", hit.query.Get("append_to_response"))
			}
		}
		if hit.path == "/3/movie/603/release_dates" || hit.path == "/3/movie/10/release_dates" || hit.path == "/3/tv/2/content_ratings" {
			t.Fatalf("%s was fetched: the detail body already carried the rating", hit.path)
		}
	}
	if !tvHit {
		t.Fatal("tv detail never reached upstream")
	}

	// A row that names the primed titles needs no lookup either.
	k.upstreamTable.bodies["/3/movie/10/similar"] = `{"page":1,"results":[{"id":603,"title":"The Matrix"},{"id":10,"title":"Toy Story"}],"total_pages":1}`
	before := k.upstream.hitCount()
	if ids := resultIDs(t, k.as(k.kid).doOK(t, "/media/movie/10/similar")); len(ids) != 1 || ids[0] != 10 {
		t.Fatalf("similar for kid = %v", ids)
	}
	for i := before; i < k.upstream.hitCount(); i++ {
		if strings.HasSuffix(k.upstream.hit(t, i).path, "/release_dates") {
			t.Fatal("primed titles were looked up again")
		}
	}
}

func TestChildMultiSearchDropsAdultPersonsAndFiltersKnownFor(t *testing.T) {
	k := newKidsEnv(t)
	k.upstreamTable.bodies["/3/search/multi"] = `{"page":1,"results":[
		{"id":1,"media_type":"movie","title":"Gentle","genre_ids":[18]},
		{"id":2,"media_type":"tv","name":"Harsh","genre_ids":[18]},
		{"id":3,"media_type":"person","name":"Star","adult":false,"known_for":[{"id":1,"media_type":"movie","genre_ids":[18]},{"id":2,"media_type":"tv","genre_ids":[18]}]},
		{"id":4,"media_type":"person","name":"Adult","adult":true,"known_for":[]},
		{"id":5,"title":"Typeless"}
	],"total_pages":1,"total_results":5}`
	k.upstreamTable.movie(1, "G")
	k.upstreamTable.tv(2, "TV-MA")

	body := k.as(k.kid).doOK(t, "/search?query=x")
	var envelope struct {
		Results []struct {
			ID        int    `json:"id"`
			MediaType string `json:"media_type"`
			KnownFor  []struct {
				ID int `json:"id"`
			} `json:"known_for"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Results) != 2 {
		t.Fatalf("kid search results = %+v, want the G movie and the person", envelope.Results)
	}
	if envelope.Results[0].ID != 1 || envelope.Results[1].ID != 3 {
		t.Fatalf("kid search results = %+v", envelope.Results)
	}
	if len(envelope.Results[1].KnownFor) != 1 || envelope.Results[1].KnownFor[0].ID != 1 {
		t.Fatalf("known_for = %+v, want only the G movie", envelope.Results[1].KnownFor)
	}
	if ids := resultIDs(t, k.as(k.adult).doOK(t, "/search?query=x")); len(ids) != 5 {
		t.Fatalf("adult search sees %v", ids)
	}
}

func TestChildPersonRoutesFilterCreditsAndHideAdultPerformers(t *testing.T) {
	k := newKidsEnv(t)
	k.upstreamTable.bodies["/3/person/7"] = `{"id":7,"name":"Star","adult":false}`
	k.upstreamTable.bodies["/3/person/8"] = `{"id":8,"name":"Performer","adult":true}`
	k.upstreamTable.bodies["/3/person/7/combined_credits"] = `{"id":7,"cast":[{"id":1,"media_type":"movie","genre_ids":[18]},{"id":2,"media_type":"movie","genre_ids":[27]},{"id":3,"media_type":"tv","genre_ids":[18]}],"crew":[{"id":4,"media_type":"movie","job":"Director","genre_ids":[18]}]}`
	k.upstreamTable.movie(1, "PG")
	k.upstreamTable.tv(3, "TV-14")
	k.upstreamTable.movie(4, "G")

	if rec := k.as(k.kid).do(t, "/media/person/7"); rec.Code != http.StatusOK {
		t.Fatalf("person: %d", rec.Code)
	}
	if rec := k.as(k.kid).do(t, "/media/person/8"); rec.Code != http.StatusNotFound {
		t.Fatalf("adult performer for kid: %d", rec.Code)
	}
	if rec := k.as(k.adult).do(t, "/media/person/8"); rec.Code != http.StatusOK {
		t.Fatalf("adult performer for adult: %d", rec.Code)
	}
	body := k.as(k.kid).doOK(t, "/media/person/7/credits")
	var credits struct {
		Cast []struct {
			ID int `json:"id"`
		} `json:"cast"`
		Crew []struct {
			ID int `json:"id"`
		} `json:"crew"`
	}
	if err := json.Unmarshal([]byte(body), &credits); err != nil {
		t.Fatal(err)
	}
	if len(credits.Cast) != 1 || credits.Cast[0].ID != 1 || len(credits.Crew) != 1 || credits.Crew[0].ID != 4 {
		t.Fatalf("kid credits = %+v", credits)
	}
}

func TestChildGenreListsDropHiddenGenres(t *testing.T) {
	k := newKidsEnv(t)
	body := k.as(k.kid).doOK(t, "/genres/movie")
	if strings.Contains(body, "Horror") || !strings.Contains(body, "Drama") {
		t.Fatalf("kid movie genres = %s", body)
	}
	body = k.as(k.kid).doOK(t, "/genres/tv")
	if strings.Contains(body, "War") || !strings.Contains(body, "Kids") {
		t.Fatalf("kid tv genres = %s", body)
	}
	if body := k.as(k.adult).doOK(t, "/genres/movie"); !strings.Contains(body, "Horror") {
		t.Fatalf("adult movie genres = %s", body)
	}
}

func TestChildFeaturedRowIsFilteredForBothSources(t *testing.T) {
	k := newKidsEnv(t)
	k.upstreamTable.bodies["/3/trending/movie/week"] = `{"page":1,"results":[{"id":1,"title":"Gentle","genre_ids":[18],"original_language":"en"},{"id":3,"title":"Grown","genre_ids":[18],"original_language":"en"}],"total_pages":1}`
	k.upstreamTable.movie(1, "G")
	k.upstreamTable.movie(3, "R")

	adultBody := k.as(k.adult).doOK(t, "/discover/movies/featured")
	if ids := resultIDs(t, adultBody); len(ids) != 2 {
		t.Fatalf("adult featured = %v", ids)
	}
	kidBody := k.as(k.kid).doOK(t, "/discover/movies/featured")
	if ids := resultIDs(t, kidBody); len(ids) != 1 || ids[0] != 1 {
		t.Fatalf("kid featured = %v", ids)
	}
	if !strings.Contains(kidBody, `"total_results":1`) || !strings.Contains(kidBody, `"source":"`) {
		t.Fatalf("headline page reports the served count: %s", kidBody)
	}

	// Trakt source: the mapped items carry only a TMDB id, so every one is
	// rated by lookup.
	k.prefs.set(serversettings.DiscoverySourceTraktTrending, false)
	k.upstreamTable.bodies["/movies/trending"] = `[{"watchers":1,"movie":{"title":"Gentle","year":2020,"language":"en","ids":{"tmdb":1}}},{"watchers":2,"movie":{"title":"Grown","year":2020,"language":"en","ids":{"tmdb":3}}}]`
	if ids := resultIDs(t, k.as(k.kid).doOK(t, "/discover/movies/featured")); len(ids) != 1 || ids[0] != 1 {
		t.Fatalf("kid trakt featured = %v", ids)
	}
	if ids := resultIDs(t, k.as(k.adult).doOK(t, "/discover/movies/featured")); len(ids) != 2 {
		t.Fatalf("adult trakt featured = %v", ids)
	}
}

func TestChildTraktPassthroughsHandleEveryShape(t *testing.T) {
	k := newKidsEnv(t)
	k.upstreamTable.movie(1, "G")
	k.upstreamTable.movie(3, "R")
	k.upstreamTable.tv(5, "TV-Y")
	k.upstreamTable.tv(6, "TV-MA")
	count := func(body string) int {
		var items []json.RawMessage
		if err := json.Unmarshal([]byte(body), &items); err != nil {
			t.Fatalf("decode trakt body: %v (%s)", err, body)
		}
		return len(items)
	}

	// Wrapped entries (anticipated), plus one with no TMDB id.
	k.upstreamTable.bodies["/movies/anticipated"] = `[{"list_count":9,"movie":{"title":"Gentle","ids":{"tmdb":1}}},{"list_count":8,"movie":{"title":"Grown","ids":{"tmdb":3}}},{"list_count":7,"movie":{"title":"Nowhere","ids":{"tmdb":0}}}]`
	if n := count(k.as(k.kid).doOK(t, "/trakt/anticipated?type=movies")); n != 1 {
		t.Fatalf("anticipated for kid = %d items", n)
	}
	if n := count(k.as(k.adult).doOK(t, "/trakt/anticipated?type=movies")); n != 3 {
		t.Fatalf("anticipated for adult = %d items", n)
	}
	// Bare entries (popular).
	k.upstreamTable.bodies["/shows/popular"] = `[{"title":"Soft","ids":{"tmdb":5}},{"title":"Hard","ids":{"tmdb":6}}]`
	if n := count(k.as(k.kid).doOK(t, "/trakt/popular?type=shows")); n != 1 {
		t.Fatalf("popular shows for kid = %d items", n)
	}
	// Calendar pairs a show with an episode.
	calendarPath := "/calendars/all/shows/" + time.Now().Format("2006-01-02") + "/3"
	k.upstreamTable.bodies[calendarPath] = `[{"first_aired":"2026-09-04T00:00:00Z","episode":{"season":1,"number":1},"show":{"title":"Soft","ids":{"tmdb":5}}},{"first_aired":"2026-09-04T00:00:00Z","episode":{"season":1,"number":2},"show":{"title":"Hard","ids":{"tmdb":6}}}]`
	if n := count(k.as(k.kid).doOK(t, "/trakt/calendar?days=3")); n != 1 {
		t.Fatalf("calendar for kid = %d items", n)
	}
	if n := count(k.as(k.adult).doOK(t, "/trakt/calendar?days=3")); n != 2 {
		t.Fatalf("calendar for adult = %d items", n)
	}
	// List items name a type; a person entry cannot be judged and is dropped.
	k.upstreamTable.bodies["/users/u/lists/l/items"] = `[{"rank":1,"type":"movie","movie":{"ids":{"tmdb":1}}},{"rank":2,"type":"show","show":{"ids":{"tmdb":6}}},{"rank":3,"type":"person","person":{"ids":{"tmdb":9}}}]`
	if n := count(k.as(k.kid).doOK(t, "/trakt/lists/u/l/items")); n != 1 {
		t.Fatalf("list items for kid = %d items", n)
	}
	// List metadata carries no titles and is opaque.
	k.upstreamTable.bodies["/lists/popular"] = `[{"like_count":3,"list":{"name":"Best horror","ids":{"trakt":1}}}]`
	if n := count(k.as(k.kid).doOK(t, "/trakt/lists")); n != 1 {
		t.Fatalf("popular lists for kid = %d items, want verbatim", n)
	}
}

func TestChildBrowseQueryPushesLimitsUpstreamAndKeysItsOwnCache(t *testing.T) {
	k := newKidsEnv(t)
	k.upstreamTable.bodies["/3/discover/movie"] = `{"page":1,"results":[{"id":1,"title":"Gentle","genre_ids":[18]}],"total_pages":1}`
	k.upstreamTable.bodies["/3/discover/tv"] = `{"page":1,"results":[{"id":5,"name":"Soft","genre_ids":[18]}],"total_pages":1}`
	k.upstreamTable.movie(1, "G")
	k.upstreamTable.tv(5, "TV-Y")

	k.as(k.adult).doOK(t, "/discover/movies?with_genres=18")
	adultHit := k.upstream.hit(t, k.upstream.hitCount()-1)
	if adultHit.query.Get("certification.lte") != "" || adultHit.query.Get("include_adult") != "" {
		t.Fatalf("adult query carries kids limits: %v", adultHit.query)
	}

	before := k.upstream.hitCount()
	k.as(k.kid).doOK(t, "/discover/movies?with_genres=18")
	var kidHit *upstreamHit
	for i := before; i < k.upstream.hitCount(); i++ {
		hit := k.upstream.hit(t, i)
		if hit.path == "/3/discover/movie" {
			kidHit = &hit
		}
	}
	if kidHit == nil {
		t.Fatal("the kid's browse must be its own upstream query, not the adult's cached page")
	}
	q := kidHit.query
	if q.Get("include_adult") != "false" || q.Get("without_genres") != "27" || q.Get("certification_country") != "US" || q.Get("certification.lte") != "PG" || q.Get("with_genres") != "18" {
		t.Fatalf("kid movie query = %v", q)
	}

	before = k.upstream.hitCount()
	k.as(k.kid).doOK(t, "/discover/tv?with_genres=18")
	for i := before; i < k.upstream.hitCount(); i++ {
		hit := k.upstream.hit(t, i)
		if hit.path == "/3/discover/tv" {
			if hit.query.Get("certification.lte") != "" || hit.query.Get("without_genres") != "10768" || hit.query.Get("include_adult") != "false" {
				t.Fatalf("kid tv query = %v", hit.query)
			}
		}
	}

	// With unrated titles allowed, the certification filter stays off
	// upstream (TMDB would drop every uncertified title), and the genre
	// and adult limits still go.
	lenient := contentpolicy.Policy{MaxMovieRating: "PG", MaxTVRating: "TV-PG", RatingRegion: "US", BlockUnrated: false, BlockedMovieGenres: []int{27}}
	if err := k.policies.Store.Set(k.kid.ID, lenient); err != nil {
		t.Fatal(err)
	}
	before = k.upstream.hitCount()
	k.as(k.kid).doOK(t, "/discover/movies?with_genres=18")
	for i := before; i < k.upstream.hitCount(); i++ {
		hit := k.upstream.hit(t, i)
		if hit.path == "/3/discover/movie" && (hit.query.Get("certification.lte") != "" || hit.query.Get("without_genres") != "27") {
			t.Fatalf("lenient kid query = %v", hit.query)
		}
	}

	// The builder the assistant uses says the same thing.
	params, _, err := BuildBrowseQuery("movie", nil, 1, false, &contentpolicy.Policy{MaxMovieRating: "PG-13", RatingRegion: "GB", BlockUnrated: true, BlockedMovieGenres: []int{27, 53}})
	if err != nil {
		t.Fatal(err)
	}
	if params.Get("certification_country") != "GB" || params.Get("certification.lte") != "PG-13" || params.Get("without_genres") != "27,53" || params.Get("include_adult") != "false" {
		t.Fatalf("BuildBrowseQuery with policy = %v", params)
	}
	params, _, err = BuildBrowseQuery("movie", nil, 1, false, nil)
	if err != nil || params.Get("include_adult") != "" {
		t.Fatalf("BuildBrowseQuery without policy = %v, %v", params, err)
	}
}

func TestChildFailuresAreErrorsNeverEmptyRows(t *testing.T) {
	k := newKidsEnv(t)

	// A body the filter cannot read is 502, not an empty list.
	k.upstreamTable.bodies["/3/movie/popular"] = `["not","an","envelope"]`
	if rec := k.as(k.kid).do(t, "/discover/movies/popular"); rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "content limits could not be applied") {
		t.Fatalf("unparseable for kid: %d %s", rec.Code, rec.Body.String())
	}
	if rec := k.as(k.adult).do(t, "/discover/movies/popular"); rec.Code != http.StatusOK {
		t.Fatalf("unparseable for adult passes through: %d", rec.Code)
	}

	// A rating lookup outage is 502 too, even though block_unrated would
	// otherwise decide.
	k.upstreamTable.bodies["/3/tv/popular"] = `{"page":1,"results":[{"id":5,"name":"Soft"},{"id":6,"name":"Down"}],"total_pages":1}`
	k.upstreamTable.tv(5, "TV-Y")
	k.upstreamTable.status["/3/tv/6/content_ratings"] = http.StatusBadGateway
	if rec := k.as(k.kid).do(t, "/discover/tv/popular"); rec.Code != http.StatusBadGateway {
		t.Fatalf("lookup outage for kid: %d %s", rec.Code, rec.Body.String())
	}
	delete(k.upstreamTable.status, "/3/tv/6/content_ratings")
	k.upstreamTable.tv(6, "TV-MA")
	if ids := resultIDs(t, k.as(k.kid).doOK(t, "/discover/tv/popular")); len(ids) != 1 || ids[0] != 5 {
		t.Fatalf("after recovery = %v", ids)
	}

	// A kids account on a handler with no policy service wired is 503.
	k.handler.SetContentPolicy(nil)
	if rec := k.as(k.kid).do(t, "/discover/tv/popular"); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("unwired for kid: %d", rec.Code)
	}
	if rec := k.as(k.adult).do(t, "/discover/tv/popular"); rec.Code != http.StatusOK {
		t.Fatalf("unwired for adult: %d", rec.Code)
	}
}

func TestOpaqueRoutesAreVerbatimForChildren(t *testing.T) {
	k := newKidsEnv(t)
	k.upstreamTable.bodies["/3/configuration/languages"] = `[{"iso_639_1":"en","english_name":"English"}]`
	k.upstreamTable.bodies["/3/search/keyword"] = `{"results":[{"id":1,"name":"erotic"}]}`
	if body := k.as(k.kid).doOK(t, "/languages"); !strings.Contains(body, "English") {
		t.Fatalf("languages for kid = %s", body)
	}
	if body := k.as(k.kid).doOK(t, "/search/keyword?query=e"); !strings.Contains(body, "erotic") {
		t.Fatalf("keyword search for kid = %s", body)
	}
}

func TestViewerPolicyResolvesFromClaimsWhenNoUserIsInContext(t *testing.T) {
	k := newKidsEnv(t)
	k.upstreamTable.bodies["/3/movie/popular"] = `{"page":1,"results":[{"id":3,"title":"Grown","genre_ids":[18]}],"total_pages":1}`
	k.upstreamTable.movie(3, "R")

	// Claims alone: the store is consulted for a requester, and the kid is
	// still filtered; an admin's claims never are.
	k.claimsOnly = true
	if ids := resultIDs(t, k.as(k.kid).doOK(t, "/discover/movies/popular")); len(ids) != 0 {
		t.Fatalf("kid by claims alone sees %v", ids)
	}
	if ids := resultIDs(t, k.as(k.admin).doOK(t, "/discover/movies/popular")); len(ids) != 1 {
		t.Fatalf("admin by claims alone sees %v", ids)
	}
	if ids := resultIDs(t, k.as(k.adult).doOK(t, "/discover/movies/popular")); len(ids) != 1 {
		t.Fatalf("adult by claims alone sees %v", ids)
	}

	// No identity at all (a harness without auth): unfiltered, as before.
	k.identity = nil
	if ids := resultIDs(t, k.doOK(t, "/discover/movies/popular")); len(ids) != 1 {
		t.Fatalf("anonymous sees %v", ids)
	}
	if errors.Is(errPolicyUnwired, errPolicyPayload) {
		t.Fatal("sentinels must stay distinct")
	}
}
