package request

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/windoze95/cantinarr-server/internal/db"
	"github.com/windoze95/cantinarr-server/internal/tmdb"
)

// fakePosterSource counts what the approval queue asks TMDB for and can stall,
// standing in for a slow or unreachable API.
type fakePosterSource struct {
	mu      sync.Mutex
	movies  []int
	shows   []int
	stall   time.Duration
	failIDs map[int]bool
}

func (f *fakePosterSource) GetMovieDetails(tmdbID int) (*tmdb.MovieDetails, error) {
	f.mu.Lock()
	f.movies = append(f.movies, tmdbID)
	stall, fail := f.stall, f.failIDs[tmdbID]
	f.mu.Unlock()
	if stall > 0 {
		time.Sleep(stall)
	}
	if fail {
		return nil, fmt.Errorf("tmdb unavailable")
	}
	return &tmdb.MovieDetails{ID: tmdbID, PosterPath: fmt.Sprintf("/movie-%d.jpg", tmdbID)}, nil
}

func (f *fakePosterSource) GetTVDetails(tmdbID int) (*tmdb.TVDetails, error) {
	f.mu.Lock()
	f.shows = append(f.shows, tmdbID)
	stall := f.stall
	f.mu.Unlock()
	if stall > 0 {
		time.Sleep(stall)
	}
	return &tmdb.TVDetails{ID: tmdbID, PosterPath: fmt.Sprintf("/tv-%d.jpg", tmdbID)}, nil
}

func (f *fakePosterSource) lookups() (movies, shows int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.movies), len(f.shows)
}

// newPendingQueueService builds a service whose queue holds the given rows,
// each already pending. Column values mirror what the request flows write.
func newPendingQueueService(t *testing.T, rows ...PendingRequest) *Service {
	t.Helper()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	res, err := database.Exec(
		"INSERT INTO users (username, password_hash, role) VALUES ('reader', '', 'user')",
	)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	uid, _ := res.LastInsertId()
	for _, row := range rows {
		if _, err := database.Exec(
			`INSERT INTO request_log (user_id, tmdb_id, foreign_id, media_type, title, status, book_format)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			uid, row.TmdbID, row.ForeignID, row.MediaType, row.Title, StatusPending, row.BookFormat,
		); err != nil {
			t.Fatalf("insert pending %s: %v", row.MediaType, err)
		}
	}
	return NewService(database, nil, nil, nil)
}

func TestListPendingSurfacesBookForeignIDForTapThrough(t *testing.T) {
	svc := newPendingQueueService(t, PendingRequest{
		MediaType: "book", Title: "Flock", ForeignID: "OL123W", BookFormat: BookFormatEbook,
	})

	pending, err := svc.ListPending()
	if err != nil || len(pending) != 1 {
		t.Fatalf("ListPending = %+v err=%v, want one row", pending, err)
	}
	// The queue row is the admin's only route to the book: /detail/book keys on
	// this id, and nothing else in the payload identifies the record.
	if pending[0].ForeignID != "OL123W" {
		t.Fatalf("ForeignID = %q, want the stored Chaptarr identity", pending[0].ForeignID)
	}
}

func TestListPendingResolvesArtworkPerMediaTypeAndSkipsBooks(t *testing.T) {
	svc := newPendingQueueService(t,
		PendingRequest{MediaType: "movie", TmdbID: 11, Title: "Arrival"},
		PendingRequest{MediaType: "tv", TmdbID: 11, Title: "Severance"},
		PendingRequest{MediaType: "book", TmdbID: 0, ForeignID: "OL9W", Title: "Flock", BookFormat: BookFormatEbook},
	)
	source := &fakePosterSource{}
	svc.posterLookupOverride = source

	pending, err := svc.ListPending()
	if err != nil || len(pending) != 3 {
		t.Fatalf("ListPending = %+v err=%v, want three rows", pending, err)
	}
	byType := map[string]PendingRequest{}
	for _, row := range pending {
		byType[row.MediaType] = row
	}
	// Same id, different media type: TMDB's number spaces are unrelated, so the
	// TV row must not inherit the movie's artwork.
	if got := byType["movie"].PosterPath; got != "/movie-11.jpg" {
		t.Fatalf("movie PosterPath = %q, want the movie lookup", got)
	}
	if got := byType["tv"].PosterPath; got != "/tv-11.jpg" {
		t.Fatalf("tv PosterPath = %q, want the series lookup", got)
	}
	// A pending book is not in the library yet; resolving a cover would cost an
	// arr metadata call per row to usually return nothing.
	if got := byType["book"].PosterPath; got != "" {
		t.Fatalf("book PosterPath = %q, want no arr lookup for books", got)
	}
	if movies, shows := source.lookups(); movies != 1 || shows != 1 {
		t.Fatalf("lookups = %d movie / %d tv, want exactly one each", movies, shows)
	}
}

func TestListPendingReusesCachedArtworkAndAsksOncePerTitle(t *testing.T) {
	svc := newPendingQueueService(t,
		PendingRequest{MediaType: "movie", TmdbID: 42, Title: "Arrival"},
		PendingRequest{MediaType: "movie", TmdbID: 42, Title: "Arrival"},
	)
	source := &fakePosterSource{}
	svc.posterLookupOverride = source

	for round := 1; round <= 2; round++ {
		pending, err := svc.ListPending()
		if err != nil || len(pending) != 2 {
			t.Fatalf("round %d: ListPending = %+v err=%v, want two rows", round, pending, err)
		}
		for _, row := range pending {
			if row.PosterPath != "/movie-42.jpg" {
				t.Fatalf("round %d: PosterPath = %q, want artwork on every row", round, row.PosterPath)
			}
		}
	}
	// Two rows for one title across two loads is still one title: the queue
	// dedupes within a load and caches across them.
	if movies, _ := source.lookups(); movies != 1 {
		t.Fatalf("movie lookups = %d, want 1 (deduped within a load, cached across loads)", movies)
	}
}

func TestListPendingSurvivesUnreachableTMDB(t *testing.T) {
	svc := newPendingQueueService(t,
		PendingRequest{MediaType: "movie", TmdbID: 7, Title: "Arrival"},
	)
	source := &fakePosterSource{failIDs: map[int]bool{7: true}}
	svc.posterLookupOverride = source

	pending, err := svc.ListPending()
	if err != nil || len(pending) != 1 {
		t.Fatalf("ListPending = %+v err=%v, want the row despite the TMDB failure", pending, err)
	}
	if pending[0].PosterPath != "" {
		t.Fatalf("PosterPath = %q, want empty when the lookup failed", pending[0].PosterPath)
	}
	if pending[0].Title != "Arrival" {
		t.Fatalf("Title = %q, want the row itself intact", pending[0].Title)
	}
}

func TestListPendingDoesNotWaitPastItsArtworkBudget(t *testing.T) {
	svc := newPendingQueueService(t,
		PendingRequest{MediaType: "movie", TmdbID: 3, Title: "Arrival"},
	)
	source := &fakePosterSource{stall: time.Second}
	svc.posterLookupOverride = source
	restore := posterLookupBudget
	posterLookupBudget = 20 * time.Millisecond
	t.Cleanup(func() { posterLookupBudget = restore })

	start := time.Now()
	pending, err := svc.ListPending()
	elapsed := time.Since(start)

	if err != nil || len(pending) != 1 {
		t.Fatalf("ListPending = %+v err=%v, want the row without waiting on TMDB", pending, err)
	}
	if pending[0].PosterPath != "" {
		t.Fatalf("PosterPath = %q, want empty when the lookup overran the budget", pending[0].PosterPath)
	}
	// An unreachable TMDB must not hold an admin's queue open for its own timeout.
	if elapsed > 500*time.Millisecond {
		t.Fatalf("ListPending took %s, want it to abandon the lookup near the budget", elapsed)
	}
}

func TestListPendingWithoutTMDBReturnsRowsUnadorned(t *testing.T) {
	// bridge is nil here, exactly as it is before an admin configures TMDB.
	svc := newPendingQueueService(t,
		PendingRequest{MediaType: "movie", TmdbID: 5, Title: "Arrival"},
	)

	pending, err := svc.ListPending()
	if err != nil || len(pending) != 1 {
		t.Fatalf("ListPending = %+v err=%v, want the row with no TMDB configured", pending, err)
	}
	if pending[0].PosterPath != "" {
		t.Fatalf("PosterPath = %q, want empty without a TMDB client", pending[0].PosterPath)
	}
}
