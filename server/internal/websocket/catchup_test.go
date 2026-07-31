package websocket

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/windoze95/cantinarr-server/internal/chaptarr"
	"github.com/windoze95/cantinarr-server/internal/radarr"
	"github.com/windoze95/cantinarr-server/internal/sonarr"
)

// videoBackend is a minimal Radarr/Sonarr API double for the catch-up tests:
// a mutable queue and history, fixed movie/series tables, and counters for the
// endpoints whose cost the tests pin.
type videoBackend struct {
	mu           sync.Mutex
	apiPrefix    string // "/api/v3"
	queue        string // JSON records array
	history      string // JSON records array; "" serves an empty complete page
	historyTotal int    // overrides totalRecords when > 0 (overflow simulation)
	movies       map[int]string
	series       map[int]string
	historyHits  int
	mediaHits    int
}

func (b *videoBackend) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b.mu.Lock()
		defer b.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == b.apiPrefix+"/queue":
			fmt.Fprintf(w, `{"records":%s}`, b.queue)
		case r.URL.Path == b.apiPrefix+"/history":
			b.historyHits++
			records := b.history
			if records == "" {
				records = "[]"
			}
			total := strings.Count(records, `"eventType"`)
			if b.historyTotal > 0 {
				total = b.historyTotal
			}
			fmt.Fprintf(w, `{"page":1,"pageSize":200,"totalRecords":%d,"records":%s}`, total, records)
		case r.URL.Path == b.apiPrefix+"/episode":
			fmt.Fprint(w, `[]`)
		default:
			var id int
			if _, err := fmt.Sscanf(r.URL.Path, b.apiPrefix+"/movie/%d", &id); err == nil {
				b.mediaHits++
				if body, ok := b.movies[id]; ok {
					fmt.Fprint(w, body)
					return
				}
			}
			if _, err := fmt.Sscanf(r.URL.Path, b.apiPrefix+"/series/%d", &id); err == nil {
				b.mediaHits++
				if body, ok := b.series[id]; ok {
					fmt.Fprint(w, body)
					return
				}
			}
			http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
		}
	}
}

func (b *videoBackend) set(field *string, value string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	*field = value
}

func (b *videoBackend) counts() (history, media int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.historyHits, b.mediaHits
}

// historyDate renders a history record timestamp the given duration in the past.
func historyDate(ago time.Duration) string {
	return time.Now().Add(-ago).UTC().Format(time.RFC3339)
}

// TestResumedPollCatchesImportsMissedEntirely is the regression test for the
// full-downtime hole: content added to the arr outside Cantinarr, grabbed AND
// imported entirely while the process was down, appears in no queue snapshot —
// the old witness could never announce it and the arr's webhook fired into the
// void exactly once. The import-history catch-up recovers it on the first poll
// after boot, and only once.
func TestResumedPollCatchesImportsMissedEntirely(t *testing.T) {
	database := witnessDB(t)
	backend := &videoBackend{apiPrefix: "/api/v3", movies: map[int]string{
		42: `{"id":42,"title":"The Matrix","tmdbId":603,"hasFile":true,"monitored":true}`,
	}}
	srv := httptest.NewServer(backend.handler())
	t.Cleanup(srv.Close)
	client := radarr.NewClient(srv.URL, "test-key")

	// Process 1 last saw an empty queue: nothing in flight, nothing witnessed.
	backend.set(&backend.queue, `[]`)
	hubA := NewHub(nil, nil, nil, database, &recordingContent{}, nil)
	hubA.pollRadarrInstance("movies-a", client)

	// While the process was down, movie 42 was added directly in Radarr,
	// grabbed, and fully imported: it exists only as an import-history record.
	if _, err := database.Exec(
		`UPDATE arr_queue_witness SET observed_at = ? WHERE instance_id = 'movies-a'`,
		time.Now().Add(-30*time.Minute).UTC(),
	); err != nil {
		t.Fatalf("backdate witness: %v", err)
	}
	backend.set(&backend.history, fmt.Sprintf(
		`[{"id":900,"movieId":42,"eventType":"downloadFolderImported","date":"%s"}]`,
		historyDate(10*time.Minute)))

	after := &recordingContent{}
	hubB := NewHub(nil, nil, nil, database, after, nil)
	hubB.restoreQueueWitness()
	hubB.pollRadarrInstance("movies-a", client)

	if got := after.movieCalls(); len(got) != 1 || got[0] != "The Matrix|603" {
		t.Fatalf("caught-up alerts = %v, want exactly The Matrix|603", got)
	}

	// The next tick is steady state: the same history record must not repeat.
	hubB.pollRadarrInstance("movies-a", client)
	if got := after.movieCalls(); len(got) != 1 {
		t.Errorf("catch-up re-announced on a steady tick: %v", got)
	}
}

// TestResumedCatchUpMergesWithDepartures pins the merge: a completion the
// witness saw depart and a completion only history knows about announce as one
// deduplicated batch, each verified live, each exactly once.
func TestResumedCatchUpMergesWithDepartures(t *testing.T) {
	database := witnessDB(t)
	backend := &videoBackend{apiPrefix: "/api/v3", movies: map[int]string{
		42: `{"id":42,"title":"The Matrix","tmdbId":603,"hasFile":true,"monitored":true}`,
		77: `{"id":77,"title":"Heat","tmdbId":949,"hasFile":true,"monitored":true}`,
	}}
	srv := httptest.NewServer(backend.handler())
	t.Cleanup(srv.Close)
	client := radarr.NewClient(srv.URL, "test-key")

	backend.set(&backend.queue, `[{"id":1,"movieId":42,"status":"downloading","size":100,"sizeleft":50}]`)
	hubA := NewHub(nil, nil, nil, database, &recordingContent{}, nil)
	hubA.pollRadarrInstance("movies-a", client)

	if _, err := database.Exec(
		`UPDATE arr_queue_witness SET observed_at = ? WHERE instance_id = 'movies-a'`,
		time.Now().Add(-30*time.Minute).UTC(),
	); err != nil {
		t.Fatalf("backdate witness: %v", err)
	}
	// Movie 42 finished (a departure history also records); movie 77 was
	// grabbed and imported entirely unwatched.
	backend.set(&backend.queue, `[]`)
	backend.set(&backend.history, fmt.Sprintf(
		`[{"id":901,"movieId":77,"eventType":"downloadFolderImported","date":"%s"},
		  {"id":900,"movieId":42,"eventType":"downloadFolderImported","date":"%s"}]`,
		historyDate(5*time.Minute), historyDate(10*time.Minute)))

	after := &recordingContent{}
	hubB := NewHub(nil, nil, nil, database, after, nil)
	hubB.restoreQueueWitness()
	hubB.pollRadarrInstance("movies-a", client)

	got := after.movieCalls()
	if len(got) != 2 || got[0] != "The Matrix|603" || got[1] != "Heat|949" {
		t.Fatalf("merged alerts = %v, want The Matrix then Heat, once each", got)
	}
}

// TestResumedCatchUpDropsMergedBatchOverCap pins that the burst cap covers the
// merged batch: more than restoredAlertCap imports while unwatched drop whole,
// with zero per-title lookups.
func TestResumedCatchUpDropsMergedBatchOverCap(t *testing.T) {
	database := witnessDB(t)
	backend := &videoBackend{apiPrefix: "/api/v3", movies: map[int]string{}}
	records := make([]string, 0, restoredAlertCap+1)
	for i := 1; i <= restoredAlertCap+1; i++ {
		records = append(records, fmt.Sprintf(
			`{"id":%d,"movieId":%d,"eventType":"downloadFolderImported","date":"%s"}`,
			900+i, i, historyDate(10*time.Minute)))
	}
	srv := httptest.NewServer(backend.handler())
	t.Cleanup(srv.Close)
	client := radarr.NewClient(srv.URL, "test-key")

	backend.set(&backend.queue, `[]`)
	hubA := NewHub(nil, nil, nil, database, &recordingContent{}, nil)
	hubA.pollRadarrInstance("movies-a", client)

	if _, err := database.Exec(
		`UPDATE arr_queue_witness SET observed_at = ? WHERE instance_id = 'movies-a'`,
		time.Now().Add(-30*time.Minute).UTC(),
	); err != nil {
		t.Fatalf("backdate witness: %v", err)
	}
	backend.set(&backend.history, "["+strings.Join(records, ",")+"]")

	after := &recordingContent{}
	hubB := NewHub(nil, nil, nil, database, after, nil)
	hubB.restoreQueueWitness()
	hubB.pollRadarrInstance("movies-a", client)

	if got := after.movieCalls(); len(got) != 0 {
		t.Errorf("over-cap merged batch announced %v, want silence", got)
	}
	if _, media := backend.counts(); media != 0 {
		t.Errorf("over-cap batch cost %d media lookups, want 0", media)
	}
}

// TestResumedCatchUpOverflowDropsWholeBatch pins the unproven-window verdict:
// when one page cannot enumerate the gap, nothing is announced — not even the
// witnessed departures — because a partial batch misrepresents a mass job.
func TestResumedCatchUpOverflowDropsWholeBatch(t *testing.T) {
	database := witnessDB(t)
	backend := &videoBackend{apiPrefix: "/api/v3", movies: map[int]string{
		42: `{"id":42,"title":"The Matrix","tmdbId":603,"hasFile":true,"monitored":true}`,
	}}
	srv := httptest.NewServer(backend.handler())
	t.Cleanup(srv.Close)
	client := radarr.NewClient(srv.URL, "test-key")

	backend.set(&backend.queue, `[{"id":1,"movieId":42,"status":"downloading","size":100,"sizeleft":50}]`)
	hubA := NewHub(nil, nil, nil, database, &recordingContent{}, nil)
	hubA.pollRadarrInstance("movies-a", client)

	if _, err := database.Exec(
		`UPDATE arr_queue_witness SET observed_at = ? WHERE instance_id = 'movies-a'`,
		time.Now().Add(-30*time.Minute).UTC(),
	); err != nil {
		t.Fatalf("backdate witness: %v", err)
	}
	backend.set(&backend.queue, `[]`)
	backend.set(&backend.history, fmt.Sprintf(
		`[{"id":900,"movieId":42,"eventType":"downloadFolderImported","date":"%s"}]`,
		historyDate(5*time.Minute)))
	backend.mu.Lock()
	backend.historyTotal = catchUpHistoryPageSize + 50
	backend.mu.Unlock()

	after := &recordingContent{}
	hubB := NewHub(nil, nil, nil, database, after, nil)
	hubB.restoreQueueWitness()
	hubB.pollRadarrInstance("movies-a", client)

	if got := after.movieCalls(); len(got) != 0 {
		t.Errorf("unproven window announced %v, want the whole batch dropped", got)
	}
}

// TestPollGapTriggersCatchUpWithoutRestart pins the outage arm: the process
// never restarted, but the arr (or the network to it) was unreachable long
// enough that completions happened unwatched. The next successful poll runs
// the same capped catch-up a restart does — in memory, no database required.
func TestPollGapTriggersCatchUpWithoutRestart(t *testing.T) {
	backend := &videoBackend{apiPrefix: "/api/v3", movies: map[int]string{
		42: `{"id":42,"title":"The Matrix","tmdbId":603,"hasFile":true,"monitored":true}`,
	}}
	srv := httptest.NewServer(backend.handler())
	t.Cleanup(srv.Close)
	client := radarr.NewClient(srv.URL, "test-key")

	content := &recordingContent{}
	hub := NewHub(nil, nil, nil, nil, content, nil)
	backend.set(&backend.queue, `[]`)
	hub.pollRadarrInstance("movies-a", client)

	// The outage: no successful polls for half an hour, during which movie 42
	// was grabbed and imported.
	hub.lastPollAt["movies-a"] = time.Now().Add(-30 * time.Minute)
	backend.set(&backend.history, fmt.Sprintf(
		`[{"id":900,"movieId":42,"eventType":"downloadFolderImported","date":"%s"}]`,
		historyDate(10*time.Minute)))

	hub.pollRadarrInstance("movies-a", client)
	if got := content.movieCalls(); len(got) != 1 || got[0] != "The Matrix|603" {
		t.Fatalf("outage catch-up alerts = %v, want exactly The Matrix|603", got)
	}
}

// TestPollGapPastStalenessStaysSilent pins the other half of the outage arm:
// a gap past queueWitnessStaleAfter describes completions the user has long
// since found, so both the held departures and the history window are dropped
// — where the old code announced the departures uncapped — and the witness
// reseeds so the next real completion still alerts.
func TestPollGapPastStalenessStaysSilent(t *testing.T) {
	backend := &videoBackend{apiPrefix: "/api/v3", movies: map[int]string{
		42: `{"id":42,"title":"The Matrix","tmdbId":603,"hasFile":true,"monitored":true}`,
		77: `{"id":77,"title":"Heat","tmdbId":949,"hasFile":true,"monitored":true}`,
	}}
	srv := httptest.NewServer(backend.handler())
	t.Cleanup(srv.Close)
	client := radarr.NewClient(srv.URL, "test-key")

	content := &recordingContent{}
	hub := NewHub(nil, nil, nil, nil, content, nil)
	backend.set(&backend.queue, `[{"id":1,"movieId":42,"status":"downloading","size":100,"sizeleft":50}]`)
	hub.pollRadarrInstance("movies-a", client)

	hub.lastPollAt["movies-a"] = time.Now().Add(-queueWitnessStaleAfter - time.Hour)
	backend.set(&backend.queue, `[]`)
	backend.set(&backend.history, fmt.Sprintf(
		`[{"id":900,"movieId":42,"eventType":"downloadFolderImported","date":"%s"}]`,
		historyDate(30*time.Minute)))

	hub.pollRadarrInstance("movies-a", client)
	if got := content.movieCalls(); len(got) != 0 {
		t.Fatalf("stale gap announced %v, want silence", got)
	}

	// The poller is reseeded, not wedged: a fresh completion still alerts.
	backend.set(&backend.history, `[]`)
	backend.set(&backend.queue, `[{"id":2,"movieId":77,"status":"downloading","size":100,"sizeleft":10}]`)
	hub.pollRadarrInstance("movies-a", client)
	backend.set(&backend.queue, `[]`)
	hub.pollRadarrInstance("movies-a", client)
	if got := content.movieCalls(); len(got) != 1 || got[0] != "Heat|949" {
		t.Errorf("post-reseed completion = %v, want exactly Heat|949", got)
	}
}

// TestSteadyStatePollNeverReadsHistory pins the zero-cost invariant: ordinary
// ticks — including ones that witness a departure — perform no history reads.
// Catch-up is a resumption-only cost.
func TestSteadyStatePollNeverReadsHistory(t *testing.T) {
	backend := &videoBackend{apiPrefix: "/api/v3", movies: map[int]string{
		42: `{"id":42,"title":"The Matrix","tmdbId":603,"hasFile":true,"monitored":true}`,
	}}
	srv := httptest.NewServer(backend.handler())
	t.Cleanup(srv.Close)
	client := radarr.NewClient(srv.URL, "test-key")

	content := &recordingContent{}
	hub := NewHub(nil, nil, nil, nil, content, nil)
	backend.set(&backend.queue, `[{"id":1,"movieId":42,"status":"downloading","size":100,"sizeleft":50}]`)
	hub.pollRadarrInstance("movies-a", client)
	backend.set(&backend.queue, `[]`)
	hub.pollRadarrInstance("movies-a", client)

	if got := content.movieCalls(); len(got) != 1 {
		t.Fatalf("steady-state departure = %v, want exactly one alert", got)
	}
	if history, _ := backend.counts(); history != 0 {
		t.Errorf("steady-state ticks read history %d time(s), want 0", history)
	}
}

// TestCatchUpErrorDegradesToWitnessedDepartures pins that history may only
// ever add alerts: when the history read fails, the departures the witness
// actually saw still announce rather than being silenced behind the failure.
func TestCatchUpErrorDegradesToWitnessedDepartures(t *testing.T) {
	database := witnessDB(t)
	var failHistory bool
	var mu sync.Mutex
	inner := &videoBackend{apiPrefix: "/api/v3", movies: map[int]string{
		42: `{"id":42,"title":"The Matrix","tmdbId":603,"hasFile":true,"monitored":true}`,
	}}
	handler := inner.handler()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		fail := failHistory
		mu.Unlock()
		if fail && r.URL.Path == "/api/v3/history" {
			http.Error(w, `{"error":"boom"}`, http.StatusInternalServerError)
			return
		}
		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	client := radarr.NewClient(srv.URL, "test-key")

	inner.set(&inner.queue, `[{"id":1,"movieId":42,"status":"downloading","size":100,"sizeleft":50}]`)
	hubA := NewHub(nil, nil, nil, database, &recordingContent{}, nil)
	hubA.pollRadarrInstance("movies-a", client)

	if _, err := database.Exec(
		`UPDATE arr_queue_witness SET observed_at = ? WHERE instance_id = 'movies-a'`,
		time.Now().Add(-30*time.Minute).UTC(),
	); err != nil {
		t.Fatalf("backdate witness: %v", err)
	}
	inner.set(&inner.queue, `[]`)
	mu.Lock()
	failHistory = true
	mu.Unlock()

	after := &recordingContent{}
	hubB := NewHub(nil, nil, nil, database, after, nil)
	hubB.restoreQueueWitness()
	hubB.pollRadarrInstance("movies-a", client)

	if got := after.movieCalls(); len(got) != 1 || got[0] != "The Matrix|603" {
		t.Fatalf("degraded resume = %v, want the witnessed departure announced", got)
	}
}

// TestSonarrCatchUpCollapsesToSeries pins the TV shape: per-episode-file
// import records collapse to one alert per series, and only records named
// downloadFolderImported count — a renumbered enum surfacing deletions under
// eventType=3 contributes nothing.
func TestSonarrCatchUpCollapsesToSeries(t *testing.T) {
	database := witnessDB(t)
	backend := &videoBackend{apiPrefix: "/api/v3", series: map[int]string{
		6: `{"id":6,"title":"Andor","tmdbId":83867,"statistics":{"episodeFileCount":24,"episodeCount":24}}`,
	}}
	srv := httptest.NewServer(backend.handler())
	t.Cleanup(srv.Close)
	client := sonarr.NewClient(srv.URL, "test-key")

	backend.set(&backend.queue, `[]`)
	hubA := NewHub(nil, nil, nil, database, &recordingContent{}, nil)
	hubA.pollSonarrInstance("tv-a", client)

	if _, err := database.Exec(
		`UPDATE arr_queue_witness SET observed_at = ? WHERE instance_id = 'tv-a'`,
		time.Now().Add(-30*time.Minute).UTC(),
	); err != nil {
		t.Fatalf("backdate witness: %v", err)
	}
	backend.set(&backend.history, fmt.Sprintf(
		`[{"id":903,"episodeId":31,"seriesId":6,"eventType":"downloadFolderImported","date":"%s"},
		  {"id":902,"episodeId":32,"seriesId":6,"eventType":"downloadFolderImported","date":"%s"},
		  {"id":901,"episodeId":40,"seriesId":9,"eventType":"episodeFileDeleted","date":"%s"}]`,
		historyDate(5*time.Minute), historyDate(6*time.Minute), historyDate(7*time.Minute)))

	after := &recordingContent{}
	hubB := NewHub(nil, nil, nil, database, after, nil)
	hubB.restoreQueueWitness()
	hubB.pollSonarrInstance("tv-a", client)

	if got := after.episodeCalls(); len(got) != 1 || got[0] != "Andor|83867" {
		t.Fatalf("sonarr catch-up = %v, want exactly Andor|83867", got)
	}
}

// TestChaptarrCatchUpAppliesImportGuards pins the book shape: only recognized
// Readarr-lineage import events with a dated record and a file on disk
// announce; rename-class events, undated records, and fileless records stay
// silent — the same guards the departure witness applies.
func TestChaptarrCatchUpAppliesImportGuards(t *testing.T) {
	database := witnessDB(t)
	backend := newBookBackend()
	backend.books[8] = `{"id":8,"title":"Flock","foreignBookId":"55520734","mediaType":"audiobook","statistics":{"bookFileCount":0}}`
	srv := httptest.NewServer(backend.handler())
	t.Cleanup(srv.Close)
	client := chaptarr.NewClient(srv.URL, "test-key")

	backend.setQueue(`[]`)
	hubA := NewHub(nil, nil, nil, database, &recordingContent{}, nil)
	hubA.pollChaptarrInstance("books-a", client)

	if _, err := database.Exec(
		`UPDATE arr_queue_witness SET observed_at = ? WHERE instance_id = 'books-a'`,
		time.Now().Add(-30*time.Minute).UTC(),
	); err != nil {
		t.Fatalf("backdate witness: %v", err)
	}
	backend.setHistory(fmt.Sprintf(
		`[{"id":903,"bookId":7,"eventType":"bookFileImported","date":"%s"},
		  {"id":902,"bookId":8,"eventType":"bookFileImported","date":"%s"},
		  {"id":901,"bookId":9,"eventType":"Rename","date":"%s"},
		  {"id":900,"bookId":10,"eventType":"bookFileImported"}]`,
		historyDate(5*time.Minute), historyDate(6*time.Minute), historyDate(7*time.Minute)))

	after := &recordingContent{}
	hubB := NewHub(nil, nil, nil, database, after, nil)
	hubB.restoreQueueWitness()
	hubB.pollChaptarrInstance("books-a", client)

	if got := after.calls(); len(got) != 1 || got[0] != "Ahsoka (Star Wars)|29749107|books-a|ebook" {
		t.Fatalf("chaptarr catch-up = %v, want exactly the imported ebook", got)
	}
	for _, id := range backend.asked() {
		if id == 9 || id == 10 {
			t.Errorf("excluded history record %d was looked up", id)
		}
	}
}
