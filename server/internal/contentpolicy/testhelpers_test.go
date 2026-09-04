package contentpolicy

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/windoze95/cantinarr-server/internal/cache"
	"github.com/windoze95/cantinarr-server/internal/db"
)

// fakeStatusError mirrors tmdb.StatusError without importing it: the
// resolver reads status and Retry-After through interfaces.
type fakeStatusError struct {
	status     int
	retryAfter time.Duration
}

func (e *fakeStatusError) Error() string                     { return fmt.Sprintf("TMDB API returned status %d", e.status) }
func (e *fakeStatusError) HTTPStatus() int                   { return e.status }
func (e *fakeStatusError) RetryAfterDuration() time.Duration { return e.retryAfter }

// fakeTMDB answers DoGetRaw from a path table and records every hit. A
// path with no entry answers 404 like TMDB does for an unknown title.
type fakeTMDB struct {
	mu        sync.Mutex
	bodies    map[string]string
	errors    map[string]error
	hits      []string
	inFlight  int
	maxFlight int
	block     chan struct{} // when set, every call waits here (concurrency probe)
}

func newFakeTMDB() *fakeTMDB {
	return &fakeTMDB{bodies: map[string]string{}, errors: map[string]error{}}
}

func (f *fakeTMDB) DoGetRaw(path string, _ url.Values) ([]byte, error) {
	f.mu.Lock()
	f.hits = append(f.hits, path)
	f.inFlight++
	if f.inFlight > f.maxFlight {
		f.maxFlight = f.inFlight
	}
	block := f.block
	body, ok := f.bodies[path]
	err, failed := f.errors[path]
	f.mu.Unlock()
	if block != nil {
		<-block
	}
	f.mu.Lock()
	f.inFlight--
	f.mu.Unlock()
	if failed {
		// A one-shot error clears itself so a retry can succeed.
		f.mu.Lock()
		delete(f.errors, path)
		f.mu.Unlock()
		return nil, err
	}
	if !ok {
		return nil, &fakeStatusError{status: http.StatusNotFound}
	}
	return []byte(body), nil
}

func (f *fakeTMDB) set(path, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.bodies[path] = body
}

func (f *fakeTMDB) fail(path string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.errors[path] = err
}

func (f *fakeTMDB) hitCount(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, h := range f.hits {
		if h == path {
			n++
		}
	}
	return n
}

func (f *fakeTMDB) totalHits() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.hits)
}

const (
	usMovieList = `{"certifications":{"US":[{"certification":"NR","meaning":"","order":0},{"certification":"G","meaning":"All ages","order":1},{"certification":"PG","meaning":"Guidance","order":2},{"certification":"PG-13","meaning":"13","order":3},{"certification":"R","meaning":"Restricted","order":4},{"certification":"NC-17","meaning":"Adults","order":5}],"GB":[{"certification":"U","meaning":"","order":1},{"certification":"PG","meaning":"","order":2},{"certification":"12A","meaning":"","order":3},{"certification":"12","meaning":"","order":4},{"certification":"15","meaning":"","order":5},{"certification":"18","meaning":"","order":6}]}}`
	usTVList    = `{"certifications":{"US":[{"certification":"NR","meaning":"","order":0},{"certification":"TV-Y","meaning":"","order":1},{"certification":"TV-Y7","meaning":"","order":2},{"certification":"TV-G","meaning":"","order":3},{"certification":"TV-PG","meaning":"","order":4},{"certification":"TV-14","meaning":"","order":5},{"certification":"TV-MA","meaning":"","order":6}],"GB":[{"certification":"U","meaning":"","order":1},{"certification":"PG","meaning":"","order":2},{"certification":"12","meaning":"","order":3},{"certification":"15","meaning":"","order":4},{"certification":"18","meaning":"","order":5}]}}`
)

func (f *fakeTMDB) withLists() *fakeTMDB {
	f.set("/certification/movie/list", usMovieList)
	f.set("/certification/tv/list", usTVList)
	return f
}

func movieReleaseDates(entries map[string][][2]string) string {
	var b bytes.Buffer
	b.WriteString(`{"results":[`)
	first := true
	for region, releases := range entries {
		if !first {
			b.WriteString(",")
		}
		first = false
		fmt.Fprintf(&b, `{"iso_3166_1":%q,"release_dates":[`, region)
		for i, rel := range releases {
			if i > 0 {
				b.WriteString(",")
			}
			fmt.Fprintf(&b, `{"certification":%q,"type":%s}`, rel[0], rel[1])
		}
		b.WriteString("]}")
	}
	b.WriteString("]}")
	return b.String()
}

func tvContentRatings(entries map[string]string) string {
	var b bytes.Buffer
	b.WriteString(`{"results":[`)
	first := true
	for region, rating := range entries {
		if !first {
			b.WriteString(",")
		}
		first = false
		fmt.Fprintf(&b, `{"iso_3166_1":%q,"rating":%q}`, region, rating)
	}
	b.WriteString("]}")
	return b.String()
}

type testEnv struct {
	db    *sql.DB
	tmdb  *fakeTMDB
	cache *cache.Cache
	svc   *Service
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	c := cache.New()
	t.Cleanup(c.Close)
	fake := newFakeTMDB().withLists()
	svc := New(database, func() RawGetter { return fake }, c)
	svc.sleep = func(context.Context, time.Duration) error { return nil }
	return &testEnv{db: database, tmdb: fake, cache: c, svc: svc}
}

func (e *testEnv) user(t *testing.T, name, role string) int64 {
	t.Helper()
	res, err := e.db.Exec("INSERT INTO users (username, password_hash, role) VALUES (?, '', ?)", name, role)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

func usPolicy() Policy {
	return Policy{MaxMovieRating: "PG", MaxTVRating: "TV-PG", RatingRegion: "US", BlockUnrated: true, BlockedMovieGenres: []int{27}, BlockedTVGenres: []int{10768}}
}
