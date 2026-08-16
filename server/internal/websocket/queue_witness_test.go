package websocket

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/windoze95/cantinarr-server/internal/chaptarr"
	"github.com/windoze95/cantinarr-server/internal/db"
	"github.com/windoze95/cantinarr-server/internal/radarr"
)

// queueEnvelope wraps a records array in the paged envelope the arr queue
// endpoint serves; the client's bounded-page read fails closed unless
// totalRecords matches the page.
func queueEnvelope(records string) string {
	if records == "" {
		records = "[]"
	}
	var rows []json.RawMessage
	_ = json.Unmarshal([]byte(records), &rows)
	return fmt.Sprintf(`{"totalRecords":%d,"records":%s}`, len(rows), records)
}

// witnessDB opens a file-backed database with the full schema. File-backed
// rather than :memory: so two hubs can share it the way two processes share the
// container's database across a restart.
func witnessDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "cantinarr.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

// readyContent is a recordingContent whose push-readiness can be toggled, so a
// test can stand in for a gateway that has not enrolled yet.
type readyContent struct {
	recordingContent
	mu    sync.Mutex
	ready bool
}

func (r *readyContent) ContentReady() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.ready
}

func (r *readyContent) setReady(v bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ready = v
}

// witnessRowFor reads back one instance's stored membership.
func witnessRowFor(t *testing.T, database *sql.DB, instanceID string) (serviceType, mediaIDs string, ok bool) {
	t.Helper()
	err := database.QueryRow(
		`SELECT service_type, media_ids FROM arr_queue_witness WHERE instance_id = ?`, instanceID,
	).Scan(&serviceType, &mediaIDs)
	if err == sql.ErrNoRows {
		return "", "", false
	}
	if err != nil {
		t.Fatalf("read witness row: %v", err)
	}
	return serviceType, mediaIDs, true
}

// newBookBackend builds the standard Chaptarr double: record 7 imported.
func newBookBackend() *chaptarrBackend {
	return &chaptarrBackend{
		books: map[int]string{
			7: `{"id":7,"title":"Ahsoka (Star Wars)","foreignBookId":"29749107","mediaType":"ebook","statistics":{"bookFileCount":1}}`,
		},
	}
}

// TestQueueWitnessResumesBookDepartureAcrossRestart is the regression test for
// the reported defect, on the surface where it is structural: Chaptarr has no
// webhook path, so the queue poller is the only witness books have. Before this
// change a restart re-seeded from empty and the completion was lost forever.
func TestQueueWitnessResumesBookDepartureAcrossRestart(t *testing.T) {
	database := witnessDB(t)
	backend := newBookBackend()
	srv := httptest.NewServer(backend.handler())
	t.Cleanup(srv.Close)
	client := chaptarr.NewClient(srv.URL, "test-key")

	// Process 1: sees book 7 downloading, then goes away.
	before := &recordingContent{}
	hubA := NewHub(nil, nil, nil, database, before, nil)
	backend.setQueue(`[{"id":100,"bookId":7,"status":"downloading","sizeleft":1000}]`)
	hubA.pollChaptarrInstance("books-a", client)
	if calls := before.calls(); len(calls) != 0 {
		t.Fatalf("seed poll notified %v, want none", calls)
	}

	// The download completes while the process is down.
	backend.setQueue(`[]`)

	// Process 2: a fresh hub over the same database.
	after := &recordingContent{}
	hubB := NewHub(nil, nil, nil, database, after, nil)
	hubB.restoreQueueWitness()
	hubB.pollChaptarrInstance("books-a", client)

	calls := after.calls()
	if len(calls) != 1 || calls[0] != "Ahsoka (Star Wars)|29749107|books-a|ebook" {
		t.Fatalf("resumed witness calls = %v, want exactly the imported ebook", calls)
	}

	// And it must not repeat on the next poll.
	hubB.pollChaptarrInstance("books-a", client)
	if calls := after.calls(); len(calls) != 1 {
		t.Errorf("resumed witness re-alerted: %v", calls)
	}
}

// TestQueueWitnessResumesRadarrDepartureAcrossRestart proves the resume is not
// books-only: the same restart loses movie completions on main.
func TestQueueWitnessResumesRadarrDepartureAcrossRestart(t *testing.T) {
	database := witnessDB(t)
	var queue string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v3/queue":
			fmt.Fprint(w, queueEnvelope(queue))
		case r.URL.Path == "/api/v3/movie/42":
			fmt.Fprint(w, `{"id":42,"title":"The Matrix","tmdbId":603,"hasFile":true,"monitored":true}`)
		default:
			http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	client := radarr.NewClient(srv.URL, "test-key")

	queue = `[{"id":1,"movieId":42,"status":"downloading","size":100,"sizeleft":50}]`
	hubA := NewHub(nil, nil, nil, database, &recordingContent{}, nil)
	hubA.pollRadarrInstance("movies-a", client)

	queue = `[]`
	after := &recordingContent{}
	hubB := NewHub(nil, nil, nil, database, after, nil)
	hubB.restoreQueueWitness()
	hubB.pollRadarrInstance("movies-a", client)

	if got := after.movieCalls(); len(got) != 1 || got[0] != "The Matrix|603" {
		t.Fatalf("resumed movie witness = %v, want exactly The Matrix|603", got)
	}
}

// TestPollSkipsUnknownMediaRows pins the unknown-item contract: a queue row
// the arr could not match to a library record (media id 0) is visible to the
// poller — the read no longer drops it — but it has no identity to look up,
// witness, or announce, so its departure must alert on the matched row only
// and must never fetch /movie/0.
func TestPollSkipsUnknownMediaRows(t *testing.T) {
	database := witnessDB(t)
	var queue string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v3/queue":
			fmt.Fprint(w, queueEnvelope(queue))
		case r.URL.Path == "/api/v3/movie/42":
			fmt.Fprint(w, `{"id":42,"title":"The Matrix","tmdbId":603,"hasFile":true,"monitored":true}`)
		case r.URL.Path == "/api/v3/movie/0":
			t.Error("poller looked up movie id 0 for an unknown-media queue row")
			http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
		default:
			http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	client := radarr.NewClient(srv.URL, "test-key")

	queue = `[{"id":1,"movieId":42,"status":"downloading","size":100,"sizeleft":50},` +
		`{"id":2,"movieId":0,"title":"Unmatched.Release","status":"downloading","size":100,"sizeleft":80}]`
	content := &recordingContent{}
	hub := NewHub(nil, nil, nil, database, content, nil)
	hub.pollRadarrInstance("movies-a", client)

	queue = `[]`
	hub.pollRadarrInstance("movies-a", client)
	if got := content.movieCalls(); len(got) != 1 || got[0] != "The Matrix|603" {
		t.Fatalf("departure alerts = %v, want exactly the matched movie", got)
	}
}

// TestQueueWitnessPersistsBeforeNotifying pins the crashloop guarantee: the
// departed id is already out of the persisted set by the time the alert fires,
// so a crash mid-announce cannot replay it on the next boot.
func TestQueueWitnessPersistsBeforeNotifying(t *testing.T) {
	database := witnessDB(t)
	backend := newBookBackend()
	srv := httptest.NewServer(backend.handler())
	t.Cleanup(srv.Close)
	client := chaptarr.NewClient(srv.URL, "test-key")

	hubA := NewHub(nil, nil, nil, database, &recordingContent{}, nil)
	backend.setQueue(`[{"id":100,"bookId":7,"status":"downloading","sizeleft":1000}]`)
	hubA.pollChaptarrInstance("books-a", client)

	// This notifier reads the persisted membership at the moment it is called.
	observed := make(chan string, 1)
	probe := &probeContent{onBook: func() {
		_, ids, _ := witnessRowFor(t, database, "books-a")
		observed <- ids
	}}

	backend.setQueue(`[]`)
	hubB := NewHub(nil, nil, nil, database, probe, nil)
	hubB.restoreQueueWitness()
	hubB.pollChaptarrInstance("books-a", client)

	select {
	case ids := <-observed:
		if ids != "[]" {
			t.Errorf("persisted membership at notify time = %s, want [] (persist must precede notify)", ids)
		}
	default:
		t.Fatal("no book alert fired")
	}
}

// TestRestoredDiffHeldUntilContentReady answers the boot-window problem: the
// gateway enrolls asynchronously, so announcing the resumed diff immediately
// would drop it into a nil client and overwrite the membership that proves it
// happened — losing exactly the alerts this feature exists to recover.
func TestRestoredDiffHeldUntilContentReady(t *testing.T) {
	database := witnessDB(t)
	backend := newBookBackend()
	srv := httptest.NewServer(backend.handler())
	t.Cleanup(srv.Close)
	client := chaptarr.NewClient(srv.URL, "test-key")

	hubA := NewHub(nil, nil, nil, database, &recordingContent{}, nil)
	backend.setQueue(`[{"id":100,"bookId":7,"status":"downloading","sizeleft":1000}]`)
	hubA.pollChaptarrInstance("books-a", client)

	backend.setQueue(`[]`)
	content := &readyContent{}
	content.setReady(false)
	hubB := NewHub(nil, nil, nil, database, content, nil)
	hubB.restoreQueueWitness()

	hubB.pollChaptarrInstance("books-a", client)
	if calls := content.calls(); len(calls) != 0 {
		t.Fatalf("held diff notified %v, want none while the gateway is unenrolled", calls)
	}
	// Nothing may be persisted while held, or the evidence is destroyed.
	if _, ids, ok := witnessRowFor(t, database, "books-a"); !ok || ids != "[7]" {
		t.Fatalf("held diff overwrote the witness (ids=%q ok=%v), want [7] retained", ids, ok)
	}

	// Gateway comes up; the very next tick delivers.
	content.setReady(true)
	hubB.pollChaptarrInstance("books-a", client)
	if calls := content.calls(); len(calls) != 1 {
		t.Fatalf("after readiness calls = %v, want exactly one", calls)
	}
}

// TestHeldRestoredDiffExpiresAtStalenessCutoff proves the hold cannot wedge: a
// gateway that never enrolls costs one batch, not a permanently stuck poller.
func TestHeldRestoredDiffExpiresAtStalenessCutoff(t *testing.T) {
	database := witnessDB(t)
	backend := newBookBackend()
	srv := httptest.NewServer(backend.handler())
	t.Cleanup(srv.Close)
	client := chaptarr.NewClient(srv.URL, "test-key")

	hubA := NewHub(nil, nil, nil, database, &recordingContent{}, nil)
	backend.setQueue(`[{"id":100,"bookId":7,"status":"downloading","sizeleft":1000}]`)
	hubA.pollChaptarrInstance("books-a", client)

	backend.setQueue(`[]`)
	content := &readyContent{} // never ready
	hubB := NewHub(nil, nil, nil, database, content, nil)
	hubB.restoreQueueWitness()

	hubB.pollChaptarrInstance("books-a", client)
	if len(content.calls()) != 0 {
		t.Fatal("held diff notified while unready")
	}

	// Age the retained snapshot past the cutoff.
	hubB.restoredWitness["books-a"] = time.Now().Add(-queueWitnessStaleAfter - time.Minute)
	hubB.pollChaptarrInstance("books-a", client)

	if calls := content.calls(); len(calls) != 0 {
		t.Errorf("expired hold notified %v, want silence", calls)
	}
	if _, ok := hubB.restoredWitness["books-a"]; ok {
		t.Error("expired hold was not cleared — the poller would stay wedged")
	}
	if _, ids, ok := witnessRowFor(t, database, "books-a"); !ok || ids != "[]" {
		t.Errorf("after expiry witness ids = %q ok=%v, want the current empty queue persisted", ids, ok)
	}
}

// TestQueueWitnessIgnoresStaleSnapshot pins the cutoff, and that it costs no arr
// calls: a snapshot older than the cutoff describes a queue that has since
// turned over, and the user has long since seen the result in the app.
func TestQueueWitnessIgnoresStaleSnapshot(t *testing.T) {
	database := witnessDB(t)
	backend := newBookBackend()
	srv := httptest.NewServer(backend.handler())
	t.Cleanup(srv.Close)
	client := chaptarr.NewClient(srv.URL, "test-key")

	hubA := NewHub(nil, nil, nil, database, &recordingContent{}, nil)
	backend.setQueue(`[{"id":100,"bookId":7,"status":"downloading","sizeleft":1000}]`)
	hubA.pollChaptarrInstance("books-a", client)

	if _, err := database.Exec(
		`UPDATE arr_queue_witness SET observed_at = ? WHERE instance_id = 'books-a'`,
		time.Now().Add(-queueWitnessStaleAfter-time.Hour).UTC(),
	); err != nil {
		t.Fatalf("backdate witness: %v", err)
	}

	backend.setQueue(`[]`)
	content := &recordingContent{}
	hubB := NewHub(nil, nil, nil, database, content, nil)
	hubB.restoreQueueWitness()
	askedBefore := len(backend.asked())
	hubB.pollChaptarrInstance("books-a", client)

	if calls := content.calls(); len(calls) != 0 {
		t.Errorf("stale snapshot notified %v, want silence", calls)
	}
	if got := len(backend.asked()); got != askedBefore {
		t.Errorf("stale snapshot cost %d arr lookups, want 0", got-askedBefore)
	}
}

// TestRestoredDiffDropsBatchOverCap pins the burst guard. New-content alerts fan
// out to the whole household by default, so a partially-delivered pile of stale
// alerts is worse than silence plus the app's live view.
func TestRestoredDiffDropsBatchOverCap(t *testing.T) {
	database := witnessDB(t)
	backend := newBookBackend()
	srv := httptest.NewServer(backend.handler())
	t.Cleanup(srv.Close)
	client := chaptarr.NewClient(srv.URL, "test-key")

	// Seed a snapshot holding one more id than the cap allows.
	ids := make([]string, 0, restoredAlertCap+1)
	records := make([]string, 0, restoredAlertCap+1)
	for i := 1; i <= restoredAlertCap+1; i++ {
		ids = append(ids, fmt.Sprint(i))
		records = append(records, fmt.Sprintf(`{"id":%d,"bookId":%d,"status":"downloading","sizeleft":1}`, 100+i, i))
		backend.books[i] = fmt.Sprintf(
			`{"id":%d,"title":"Book %d","foreignBookId":"f%d","mediaType":"ebook","statistics":{"bookFileCount":1}}`, i, i, i)
	}
	backend.setQueue("[" + joinComma(records) + "]")
	hubA := NewHub(nil, nil, nil, database, &recordingContent{}, nil)
	hubA.pollChaptarrInstance("books-a", client)

	backend.setQueue(`[]`)
	content := &recordingContent{}
	hubB := NewHub(nil, nil, nil, database, content, nil)
	hubB.restoreQueueWitness()
	askedBefore := len(backend.asked())
	hubB.pollChaptarrInstance("books-a", client)

	if calls := content.calls(); len(calls) != 0 {
		t.Errorf("over-cap batch notified %d alert(s), want the whole batch dropped: %v", len(calls), calls)
	}
	if got := len(backend.asked()); got != askedBefore {
		t.Errorf("over-cap batch cost %d arr lookups, want 0", got-askedBefore)
	}

	// The cap is one-shot and per-instance: steady state is never capped.
	backend.setQueue(`[{"id":900,"bookId":7,"status":"downloading","sizeleft":1}]`)
	hubB.pollChaptarrInstance("books-a", client)
	backend.setQueue(`[]`)
	hubB.pollChaptarrInstance("books-a", client)
	if calls := content.calls(); len(calls) != 1 {
		t.Errorf("steady-state departure after a capped batch = %v, want exactly one alert", calls)
	}
}

// TestQueueWitnessIgnoresServiceTypeMismatch guards against a row written for
// one service being replayed into another poller's map.
func TestQueueWitnessIgnoresServiceTypeMismatch(t *testing.T) {
	database := witnessDB(t)
	if _, err := database.Exec(
		`INSERT INTO arr_queue_witness (instance_id, service_type, observed_at, media_ids) VALUES ('books-a','radarr',?,'[7]')`,
		time.Now().UTC(),
	); err != nil {
		t.Fatalf("seed row: %v", err)
	}

	backend := newBookBackend()
	srv := httptest.NewServer(backend.handler())
	t.Cleanup(srv.Close)
	client := chaptarr.NewClient(srv.URL, "test-key")

	content := &recordingContent{}
	hub := NewHub(nil, nil, nil, database, content, nil)
	hub.restoreQueueWitness()
	backend.setQueue(`[]`)
	hub.pollChaptarrInstance("books-a", client)

	if calls := content.calls(); len(calls) != 0 {
		t.Errorf("mismatched service_type notified %v, want silence", calls)
	}
}

// TestSaveQueueWitnessUpsertsSingleRow pins bounded growth and sort determinism.
func TestSaveQueueWitnessUpsertsSingleRow(t *testing.T) {
	database := witnessDB(t)
	backend := newBookBackend()
	backend.books[9] = `{"id":9,"title":"Other","foreignBookId":"f9","mediaType":"ebook","statistics":{"bookFileCount":1}}`
	srv := httptest.NewServer(backend.handler())
	t.Cleanup(srv.Close)
	client := chaptarr.NewClient(srv.URL, "test-key")

	hub := NewHub(nil, nil, nil, database, &recordingContent{}, nil)
	backend.setQueue(`[{"id":100,"bookId":7,"status":"downloading","sizeleft":1}]`)
	hub.pollChaptarrInstance("books-a", client)
	backend.setQueue(`[{"id":101,"bookId":9,"status":"downloading","sizeleft":1},{"id":100,"bookId":7,"status":"downloading","sizeleft":1}]`)
	hub.pollChaptarrInstance("books-a", client)

	var count int
	if err := database.QueryRow(`SELECT COUNT(*) FROM arr_queue_witness`).Scan(&count); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if count != 1 {
		t.Errorf("witness rows = %d, want exactly 1 per instance", count)
	}
	serviceType, ids, _ := witnessRowFor(t, database, "books-a")
	if serviceType != "chaptarr" {
		t.Errorf("service_type = %q, want chaptarr", serviceType)
	}
	if ids != "[7,9]" {
		t.Errorf("media_ids = %q, want sorted [7,9]", ids)
	}
}

// TestPollChaptarrIgnoresDeletedBook pins the crash fix: Chaptarr answers 404
// with (nil, nil), and dereferencing that on the poll goroutine — which has no
// recover() — took the whole server down.
func TestPollChaptarrIgnoresDeletedBook(t *testing.T) {
	backend := &chaptarrBackend{books: map[int]string{}} // every lookup 404s
	srv := httptest.NewServer(backend.handler())
	t.Cleanup(srv.Close)
	client := chaptarr.NewClient(srv.URL, "test-key")

	content := &recordingContent{}
	hub := NewHub(nil, nil, nil, nil, content, nil)
	backend.setQueue(`[{"id":100,"bookId":7,"status":"downloading","sizeleft":1}]`)
	hub.pollChaptarrInstance("books-a", client)
	backend.setQueue(`[]`)
	hub.pollChaptarrInstance("books-a", client) // must not panic

	if calls := content.calls(); len(calls) != 0 {
		t.Errorf("deleted book notified %v, want silence", calls)
	}
}

// TestNilDBHubBehavesAsBefore guards every existing construction path and any
// push-disabled deployment: with no database the poller must behave exactly as
// it did before persistence existed.
func TestNilDBHubBehavesAsBefore(t *testing.T) {
	backend := newBookBackend()
	srv := httptest.NewServer(backend.handler())
	t.Cleanup(srv.Close)
	client := chaptarr.NewClient(srv.URL, "test-key")

	content := &recordingContent{}
	hub := NewHub(nil, nil, nil, nil, content, nil)
	hub.restoreQueueWitness() // must be a no-op, not a panic

	backend.setQueue(`[{"id":100,"bookId":7,"status":"downloading","sizeleft":1}]`)
	hub.pollChaptarrInstance("books-a", client)
	if len(content.calls()) != 0 {
		t.Fatal("seed poll notified")
	}
	backend.setQueue(`[]`)
	hub.pollChaptarrInstance("books-a", client)
	if calls := content.calls(); len(calls) != 1 {
		t.Errorf("in-memory witness calls = %v, want exactly one", calls)
	}
}

// TestQueueWitnessLoadFiltersUnusableRows pins the load-level filtering on its
// own. The poller has a second staleness check, so without this test a
// regression in either layer would stay hidden behind the other.
func TestQueueWitnessLoadFiltersUnusableRows(t *testing.T) {
	database := witnessDB(t)
	now := time.Now()
	seed := func(id, service string, observedAt time.Time, ids string) {
		t.Helper()
		if _, err := database.Exec(
			`INSERT INTO arr_queue_witness (instance_id, service_type, observed_at, media_ids) VALUES (?,?,?,?)`,
			id, service, observedAt.UTC(), ids,
		); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}
	seed("fresh", "chaptarr", now.Add(-time.Minute), `[3,1,2]`)
	seed("stale", "chaptarr", now.Add(-queueWitnessStaleAfter-time.Minute), `[9]`)
	seed("future", "chaptarr", now.Add(time.Hour), `[9]`)
	seed("corrupt", "chaptarr", now.Add(-time.Minute), `not json`)

	w := newQueueWitness(database)
	rows, err := w.load(now, queueWitnessStaleAfter)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("loaded %d row(s) (%v), want only the fresh one", len(rows), rows)
	}
	row, ok := rows["fresh"]
	if !ok {
		t.Fatal("the fresh row was dropped")
	}
	if row.serviceType != "chaptarr" || fmt.Sprint(row.ids) != "[3 1 2]" {
		t.Errorf("fresh row = %+v, want chaptarr ids [3 1 2]", row)
	}
}

// probeContent runs a callback at the moment a book alert fires.
type probeContent struct {
	onBook func()
}

func (p *probeContent) NotifyNewMovie(title string, tmdbID int)         {}
func (p *probeContent) NotifyNewEpisode(seriesTitle string, tmdbID int) {}
func (p *probeContent) NotifyNewBook(title, foreignID, instanceID, format string) {
	if p.onBook != nil {
		p.onBook()
	}
}
func (p *probeContent) NotifyUpgradedMovie(title string, tmdbID int)                   {}
func (p *probeContent) NotifyUpgradedEpisode(seriesTitle string, tmdbID int)           {}
func (p *probeContent) NotifyUpgradedBook(title, foreignID, instanceID, format string) {}

func joinComma(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += ","
		}
		out += p
	}
	return out
}
