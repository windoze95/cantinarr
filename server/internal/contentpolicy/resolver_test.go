package contentpolicy

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestResolverPicksTheatricalCertThenAnyForRegion(t *testing.T) {
	env := newTestEnv(t)
	env.tmdb.set("/movie/1/release_dates", movieReleaseDates(map[string][][2]string{
		"US": {{"", "1"}, {"R", "4"}, {"PG-13", "3"}},
		"GB": {{"15", "4"}, {"12A", "5"}},
	}))
	m, err := env.svc.ratings(context.Background(), MediaMovie, 1)
	if err != nil {
		t.Fatalf("ratings: %v", err)
	}
	if m["US"] != "PG-13" {
		t.Fatalf("US = %q, want the theatrical PG-13 over the earlier R", m["US"])
	}
	if m["GB"] != "15" {
		t.Fatalf("GB = %q, want the first non-empty certification", m["GB"])
	}

	env.tmdb.set("/tv/2/content_ratings", tvContentRatings(map[string]string{"US": "TV-MA", "DE": "16"}))
	m, err = env.svc.ratings(context.Background(), MediaTV, 2)
	if err != nil {
		t.Fatalf("tv ratings: %v", err)
	}
	if m["US"] != "TV-MA" || m["DE"] != "16" {
		t.Fatalf("tv ratings = %v", m)
	}
}

func TestResolverTreats404AsUnratedAnd5xxAsError(t *testing.T) {
	env := newTestEnv(t)
	m, err := env.svc.ratings(context.Background(), MediaMovie, 404)
	if err != nil || len(m) != 0 {
		t.Fatalf("404 should be an empty map, got %v, %v", m, err)
	}
	if env.tmdb.hitCount("/movie/404/release_dates") != 1 {
		t.Fatal("expected one upstream hit")
	}
	// The absence is cached: a second read costs nothing upstream.
	if _, err := env.svc.ratings(context.Background(), MediaMovie, 404); err != nil {
		t.Fatal(err)
	}
	if env.tmdb.hitCount("/movie/404/release_dates") != 1 {
		t.Fatal("404 should have been cached")
	}

	env.tmdb.fail("/movie/500/release_dates", &fakeStatusError{status: http.StatusBadGateway})
	env.tmdb.set("/movie/500/release_dates", movieReleaseDates(map[string][][2]string{"US": {{"G", "3"}}}))
	if _, err := env.svc.ratings(context.Background(), MediaMovie, 500); !errors.Is(err, errLookup) {
		t.Fatalf("5xx should be a lookup error, got %v", err)
	}
	// Errors are never cached: the next read tries upstream again.
	m, err = env.svc.ratings(context.Background(), MediaMovie, 500)
	if err != nil || m["US"] != "G" {
		t.Fatalf("recovered read = %v, %v", m, err)
	}
}

func TestResolverRetriesRateLimitOnce(t *testing.T) {
	env := newTestEnv(t)
	var slept time.Duration
	env.svc.sleep = func(_ context.Context, d time.Duration) error { slept = d; return nil }
	env.tmdb.fail("/movie/7/release_dates", &fakeStatusError{status: http.StatusTooManyRequests, retryAfter: 500 * time.Millisecond})
	env.tmdb.set("/movie/7/release_dates", movieReleaseDates(map[string][][2]string{"US": {{"PG", "3"}}}))
	m, err := env.svc.ratings(context.Background(), MediaMovie, 7)
	if err != nil || m["US"] != "PG" {
		t.Fatalf("after retry = %v, %v", m, err)
	}
	if slept != 500*time.Millisecond {
		t.Fatalf("slept %v, want the upstream Retry-After", slept)
	}
	if env.tmdb.hitCount("/movie/7/release_dates") != 2 {
		t.Fatalf("hits = %d, want one retry", env.tmdb.hitCount("/movie/7/release_dates"))
	}

	// A Retry-After beyond the cap waits only the cap.
	env.tmdb.fail("/movie/8/release_dates", &fakeStatusError{status: http.StatusTooManyRequests, retryAfter: time.Minute})
	env.tmdb.set("/movie/8/release_dates", movieReleaseDates(nil))
	if _, err := env.svc.ratings(context.Background(), MediaMovie, 8); err != nil {
		t.Fatal(err)
	}
	if slept != maxRetryAfter {
		t.Fatalf("slept %v, want the cap %v", slept, maxRetryAfter)
	}
}

func TestResolverCachesRatingsAndPrimesFromDetail(t *testing.T) {
	env := newTestEnv(t)
	env.tmdb.set("/movie/3/release_dates", movieReleaseDates(map[string][][2]string{"US": {{"G", "3"}}}))
	for i := 0; i < 3; i++ {
		if _, err := env.svc.ratings(context.Background(), MediaMovie, 3); err != nil {
			t.Fatal(err)
		}
	}
	if env.tmdb.hitCount("/movie/3/release_dates") != 1 {
		t.Fatalf("hits = %d, want 1", env.tmdb.hitCount("/movie/3/release_dates"))
	}

	detail := `{"id":9,"title":"Primed","release_dates":` + movieReleaseDates(map[string][][2]string{"US": {{"R", "3"}}}) + `}`
	env.svc.Prime(MediaMovie, 9, []byte(detail))
	m, err := env.svc.ratings(context.Background(), MediaMovie, 9)
	if err != nil || m["US"] != "R" {
		t.Fatalf("primed = %v, %v", m, err)
	}
	if env.tmdb.hitCount("/movie/9/release_dates") != 0 {
		t.Fatal("a primed title should not be fetched")
	}

	tvDetail := `{"id":10,"name":"Show","content_ratings":` + tvContentRatings(map[string]string{"US": "TV-14"}) + `}`
	env.svc.Prime(MediaTV, 10, []byte(tvDetail))
	m, err = env.svc.ratings(context.Background(), MediaTV, 10)
	if err != nil || m["US"] != "TV-14" {
		t.Fatalf("primed tv = %v, %v", m, err)
	}
}

func TestFilterShortCircuitsWithoutLookupsAndDedupes(t *testing.T) {
	env := newTestEnv(t)
	env.tmdb.set("/movie/1/release_dates", movieReleaseDates(map[string][][2]string{"US": {{"G", "3"}}}))
	env.tmdb.set("/movie/2/release_dates", movieReleaseDates(map[string][][2]string{"US": {{"R", "3"}}}))
	env.tmdb.set("/tv/3/content_ratings", tvContentRatings(map[string]string{"US": "TV-Y"}))
	p := usPolicy()
	cands := []Candidate{
		{MediaType: MediaMovie, TMDBID: 1},                      // G: kept
		{MediaType: MediaMovie, TMDBID: 2},                      // R: hidden
		{MediaType: MediaMovie, TMDBID: 1},                      // duplicate: one lookup
		{MediaType: MediaMovie, TMDBID: 5, Adult: true},         // no lookup
		{MediaType: MediaMovie, TMDBID: 6, GenreIDs: []int{27}}, // hidden genre, no lookup
		{MediaType: MediaMovie, TMDBID: 0},                      // no identity, no lookup
		{MediaType: MediaTV, TMDBID: 3},                         // TV-Y: kept
		{MediaType: "person", TMDBID: 4},                        // unknown type
		{MediaType: MediaMovie, TMDBID: 99},                     // TMDB 404: unrated, hidden
	}
	keep, err := env.svc.Filter(context.Background(), &p, cands)
	if err != nil {
		t.Fatalf("Filter: %v", err)
	}
	want := []bool{true, false, true, false, false, false, true, false, false}
	for i := range want {
		if keep[i] != want[i] {
			t.Fatalf("keep[%d] = %v, want %v (%v)", i, keep[i], want[i], keep)
		}
	}
	for _, path := range []string{"/movie/5/release_dates", "/movie/6/release_dates", "/movie/0/release_dates"} {
		if env.tmdb.hitCount(path) != 0 {
			t.Fatalf("%s should not have been looked up", path)
		}
	}
	if env.tmdb.hitCount("/movie/1/release_dates") != 1 {
		t.Fatal("duplicate candidates share one lookup")
	}
}

func TestFilterReportsTransportErrorsAsBlockedPlusError(t *testing.T) {
	env := newTestEnv(t)
	env.tmdb.set("/movie/1/release_dates", movieReleaseDates(map[string][][2]string{"US": {{"G", "3"}}}))
	env.tmdb.fail("/movie/2/release_dates", errors.New("connection reset"))
	p := usPolicy()
	p.BlockUnrated = false
	keep, err := env.svc.Filter(context.Background(), &p, []Candidate{{MediaType: MediaMovie, TMDBID: 1}, {MediaType: MediaMovie, TMDBID: 2}})
	if err == nil {
		t.Fatal("a failed lookup must surface as an error")
	}
	if !keep[0] || keep[1] {
		t.Fatalf("keep = %v: the readable title stays, the unreadable one is hidden even with block_unrated off", keep)
	}
}

func TestFilterBoundsConcurrency(t *testing.T) {
	env := newTestEnv(t)
	env.tmdb.block = make(chan struct{})
	var cands []Candidate
	for i := 1; i <= 20; i++ {
		env.tmdb.set(ratingPath(MediaMovie, i), movieReleaseDates(map[string][][2]string{"US": {{"G", "3"}}}))
		cands = append(cands, Candidate{MediaType: MediaMovie, TMDBID: i})
	}
	p := usPolicy()
	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := env.svc.Filter(context.Background(), &p, cands); err != nil {
			t.Errorf("Filter: %v", err)
		}
	}()
	// Let the workers saturate the bound, then release everything.
	deadline := time.Now().Add(2 * time.Second)
	for {
		env.tmdb.mu.Lock()
		inFlight := env.tmdb.inFlight
		env.tmdb.mu.Unlock()
		if inFlight == lookupWorkers || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	close(env.tmdb.block)
	<-done
	if env.tmdb.maxFlight > lookupWorkers {
		t.Fatalf("max in flight = %d, want at most %d", env.tmdb.maxFlight, lookupWorkers)
	}
	if env.tmdb.maxFlight == 0 {
		t.Fatal("no lookups ran")
	}
}

func ratingPath(mediaType string, id int) string {
	if mediaType == MediaTV {
		return "/tv/" + itoa(id) + "/content_ratings"
	}
	return "/movie/" + itoa(id) + "/release_dates"
}

func itoa(i int) string {
	return string(rune('0'+i/10)) + string(rune('0'+i%10))
}

func TestFilterWithoutTMDBIsUnavailable(t *testing.T) {
	env := newTestEnv(t)
	env.svc.getter = func() RawGetter { return nil }
	p := usPolicy()
	keep, err := env.svc.Filter(context.Background(), &p, []Candidate{{MediaType: MediaMovie, TMDBID: 1}})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
	if keep[0] {
		t.Fatal("nothing is allowed while ratings cannot be read")
	}
}

func TestCertListsFallBackStaleThenBuiltinUS(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	list, source := env.svc.certLists(ctx, MediaMovie)
	if source != SourceTMDB || len(list["GB"]) == 0 {
		t.Fatalf("fresh list: source %q, GB entries %d", source, len(list["GB"]))
	}
	// Expire the day copy; the month copy answers while TMDB is down.
	env.cache.Delete("certlist:movie")
	env.tmdb.fail("/certification/movie/list", errors.New("down"))
	list, source = env.svc.certLists(ctx, MediaMovie)
	if source != SourceCached || len(list["GB"]) == 0 {
		t.Fatalf("stale list: source %q, GB entries %d", source, len(list["GB"]))
	}
	// Nothing cached at all: the built-in US scheme, US only.
	env.cache.Delete("certlist:movie")
	env.cache.Delete("certlist:movie:last")
	env.tmdb.fail("/certification/movie/list", errors.New("down"))
	list, source = env.svc.certLists(ctx, MediaMovie)
	if source != SourceBuiltin || len(list["US"]) != 6 || len(list["GB"]) != 0 {
		t.Fatalf("builtin list: source %q, US %d, GB %d", source, len(list["US"]), len(list["GB"]))
	}

	// A GB policy cannot be evaluated on the built-in scheme; a US one can.
	env.cache.Delete("certlist:tv")
	env.cache.Delete("certlist:tv:last")
	env.tmdb.fail("/certification/movie/list", errors.New("down"))
	env.tmdb.fail("/certification/tv/list", errors.New("down"))
	gb := Policy{MaxMovieRating: "PG", MaxTVRating: "PG", RatingRegion: "GB"}
	if _, err := env.svc.EvaluatorFor(ctx, &gb); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("GB on builtin lists: %v, want ErrUnavailable", err)
	}
	env.tmdb.fail("/certification/movie/list", errors.New("down"))
	env.tmdb.fail("/certification/tv/list", errors.New("down"))
	us := usPolicy()
	ev, err := env.svc.EvaluatorFor(ctx, &us)
	if err != nil || ev == nil {
		t.Fatalf("US on builtin lists: %v", err)
	}
	if !ev.Allows(MediaMovie, Rating{Certification: "G", Known: true}, false, nil) {
		t.Fatal("built-in US scheme ranks G under PG")
	}
}

func TestValidateNormalisesAndRejects(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	p := Policy{MaxMovieRating: "pg-13", MaxTVRating: "tv-14", RatingRegion: "us", BlockedMovieGenres: []int{27, 27, 0}}
	if err := env.svc.Validate(ctx, &p); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if p.RatingRegion != "US" || p.MaxMovieRating != "PG-13" || p.MaxTVRating != "TV-14" || len(p.BlockedMovieGenres) != 1 {
		t.Fatalf("normalised = %+v", p)
	}
	if p.BlockedTVGenres == nil {
		t.Fatal("empty genre lists must encode as [], not null")
	}

	var ve *ValidationError
	bad := Policy{MaxMovieRating: "R", MaxTVRating: "TV-PG", RatingRegion: "XX"}
	if err := env.svc.Validate(ctx, &bad); !errors.As(err, &ve) {
		t.Fatalf("unknown region: %v", err)
	}
	bad = Policy{MaxMovieRating: "12A", MaxTVRating: "TV-PG", RatingRegion: "US"}
	if err := env.svc.Validate(ctx, &bad); !errors.As(err, &ve) {
		t.Fatalf("foreign cert: %v", err)
	}
	bad = Policy{MaxMovieRating: "NR", MaxTVRating: "TV-PG", RatingRegion: "US"}
	if err := env.svc.Validate(ctx, &bad); !errors.As(err, &ve) {
		t.Fatalf("NR is not a cap: %v", err)
	}
	empty := Policy{RatingRegion: ""}
	if err := env.svc.Validate(ctx, &empty); !errors.As(err, &ve) {
		t.Fatalf("empty caps: %v", err)
	}

	// Lists down: US still validates on the built-in scheme, GB does not.
	env.cache.Delete("certlist:movie")
	env.cache.Delete("certlist:movie:last")
	env.cache.Delete("certlist:tv")
	env.cache.Delete("certlist:tv:last")
	env.tmdb.fail("/certification/movie/list", errors.New("down"))
	env.tmdb.fail("/certification/tv/list", errors.New("down"))
	gb := Policy{MaxMovieRating: "PG", MaxTVRating: "PG", RatingRegion: "GB"}
	if err := env.svc.Validate(ctx, &gb); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("GB while lists are down: %v", err)
	}
	env.tmdb.fail("/certification/movie/list", errors.New("down"))
	env.tmdb.fail("/certification/tv/list", errors.New("down"))
	us := usPolicy()
	if err := env.svc.Validate(ctx, &us); err != nil {
		t.Fatalf("US while lists are down: %v", err)
	}
}

func TestCertificationsMarksDefaultsAndSource(t *testing.T) {
	env := newTestEnv(t)
	resp := env.svc.Certifications(context.Background())
	if resp.Source != SourceTMDB {
		t.Fatalf("source = %q", resp.Source)
	}
	var movieDefault, tvDefault string
	for _, o := range resp.Movie["US"] {
		if o.Default {
			movieDefault = o.Certification
		}
	}
	for _, o := range resp.TV["US"] {
		if o.Default {
			tvDefault = o.Certification
		}
	}
	if movieDefault != "PG" || tvDefault != "TV-PG" {
		t.Fatalf("defaults = %q / %q", movieDefault, tvDefault)
	}
	for _, o := range resp.Movie["GB"] {
		if o.Default {
			t.Fatal("no default is suggested for GB")
		}
	}
}

func TestDescribeLimitsWorksWithoutLists(t *testing.T) {
	env := newTestEnv(t)
	env.svc.getter = func() RawGetter { return nil }
	gb := Policy{MaxMovieRating: "PG", MaxTVRating: "12", RatingRegion: "GB", BlockUnrated: true}
	got := env.svc.DescribeLimits(context.Background(), &gb)
	if got == "" || got[:len("movies up to PG")] != "movies up to PG" {
		t.Fatalf("DescribeLimits = %q", got)
	}
	if env.svc.DescribeLimits(context.Background(), nil) != "" {
		t.Fatal("nil policy describes as nothing")
	}
}
