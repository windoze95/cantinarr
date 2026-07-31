package websocket

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/chaptarr"
)

// recordingContent captures ContentNotifier calls so the Chaptarr departure
// witness can be pinned without a push gateway.
type recordingContent struct {
	mu       sync.Mutex
	books    []string // "title|foreignID|instanceID|format"
	movies   []string // "title|tmdbID"
	episodes []string // "seriesTitle|tmdbID"
}

func (r *recordingContent) NotifyNewMovie(title string, tmdbID int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.movies = append(r.movies, fmt.Sprintf("%s|%d", title, tmdbID))
}
func (r *recordingContent) NotifyNewEpisode(seriesTitle string, tmdbID int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.episodes = append(r.episodes, fmt.Sprintf("%s|%d", seriesTitle, tmdbID))
}
func (r *recordingContent) NotifyNewBook(title, foreignID, instanceID, format string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.books = append(r.books, fmt.Sprintf("%s|%s|%s|%s", title, foreignID, instanceID, format))
}

func (r *recordingContent) calls() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.books...)
}

func (r *recordingContent) movieCalls() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.movies...)
}

func (r *recordingContent) episodeCalls() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.episodes...)
}

// chaptarrBackend is a minimal Chaptarr API double: a mutable queue plus a
// fixed book table, recording every book lookup it serves. history holds the
// records array for /api/v1/history; empty means no history at all, so the
// catch-up reader sees a provably complete empty window.
type chaptarrBackend struct {
	mu        sync.Mutex
	queue     string         // JSON records array for /api/v1/queue
	history   string         // JSON records array for /api/v1/history
	books     map[int]string // id -> JSON body for /api/v1/book/{id}
	bookAsked []int          // ids requested via /api/v1/book/{id}
}

func (b *chaptarrBackend) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b.mu.Lock()
		defer b.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/queue":
			fmt.Fprintf(w, `{"records":%s}`, b.queue)
		case r.URL.Path == "/api/v1/history":
			records := b.history
			if records == "" {
				records = "[]"
			}
			count := strings.Count(records, `"id":`)
			fmt.Fprintf(w, `{"page":1,"pageSize":200,"totalRecords":%d,"records":%s}`, count, records)
		default:
			var id int
			if _, err := fmt.Sscanf(r.URL.Path, "/api/v1/book/%d", &id); err == nil {
				b.bookAsked = append(b.bookAsked, id)
				if body, ok := b.books[id]; ok {
					fmt.Fprint(w, body)
					return
				}
			}
			http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
		}
	}
}

func (b *chaptarrBackend) setQueue(records string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.queue = records
}

func (b *chaptarrBackend) setHistory(records string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.history = records
}

func (b *chaptarrBackend) asked() []int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]int(nil), b.bookAsked...)
}

// TestPollChaptarrWitnessesImportedDepartures proves the queue-departure
// witness: a record that leaves the queue with a file on disk pushes exactly
// one new_book alert carrying its identity; a departure without a file (failed
// or removed download) says nothing; unmapped queue rows (bookId 0) are never
// looked up.
func TestPollChaptarrWitnessesImportedDepartures(t *testing.T) {
	backend := &chaptarrBackend{
		books: map[int]string{
			// Record 7 imported (file on disk); record 8's download failed.
			7: `{"id":7,"title":"Ahsoka (Star Wars)","foreignBookId":"29749107","mediaType":"ebook","statistics":{"bookFileCount":1}}`,
			8: `{"id":8,"title":"Flock","foreignBookId":"55520734","mediaType":"audiobook","statistics":{"bookFileCount":0}}`,
		},
	}
	srv := httptest.NewServer(backend.handler())
	t.Cleanup(srv.Close)
	client := chaptarr.NewClient(srv.URL, "test-key")

	content := &recordingContent{}
	hub := NewHub(nil, nil, nil, nil, content, nil)

	// First poll: three tracked downloads (one unmapped). No previous state,
	// so nothing can have departed yet.
	backend.setQueue(`[
		{"id":100,"bookId":7,"status":"downloading","sizeleft":1000},
		{"id":101,"bookId":8,"status":"downloading","sizeleft":2000},
		{"id":102,"bookId":0,"status":"queued","sizeleft":3000}
	]`)
	hub.pollChaptarrInstance("books-a", client)
	if calls := content.calls(); len(calls) != 0 {
		t.Fatalf("first poll notified %v, want none (no previous state)", calls)
	}

	// Second poll: everything left the queue. Only the record with a file may
	// alert, and the unmapped id must not be fetched at all.
	backend.setQueue(`[]`)
	hub.pollChaptarrInstance("books-a", client)

	calls := content.calls()
	if len(calls) != 1 || calls[0] != "Ahsoka (Star Wars)|29749107|books-a|ebook" {
		t.Errorf("witness calls = %v, want exactly the imported ebook", calls)
	}
	for _, id := range backend.asked() {
		if id == 0 {
			t.Error("unmapped queue row (bookId 0) was looked up")
		}
	}

	// Third poll: an empty queue again — no repeat alerts for old departures.
	hub.pollChaptarrInstance("books-a", client)
	if calls := content.calls(); len(calls) != 1 {
		t.Errorf("witness re-alerted on an unchanged empty queue: %v", calls)
	}
}
