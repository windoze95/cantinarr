package websocket

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/windoze95/cantinarr-server/internal/db"
	"github.com/windoze95/cantinarr-server/internal/lidarr"
)

// lidarrBackend is a minimal Lidarr API double: a mutable queue plus a fixed
// album table, recording every album lookup it serves. history holds the
// records array for the import query (eventType=3); deleteHistory the
// trackFileDeleted (eventType=5) query.
type lidarrBackend struct {
	mu            sync.Mutex
	queue         string
	history       string
	deleteHistory string
	albums        map[int]string
	albumAsked    []int
}

func (b *lidarrBackend) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b.mu.Lock()
		defer b.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/queue":
			fmt.Fprint(w, queueEnvelope(b.queue))
		case r.URL.Path == "/api/v1/history":
			records := b.history
			if r.URL.Query().Get("eventType") == "5" {
				records = b.deleteHistory
			}
			if records == "" {
				records = "[]"
			}
			count := strings.Count(records, `"id":`)
			fmt.Fprintf(w, `{"page":1,"pageSize":200,"totalRecords":%d,"records":%s}`, count, records)
		default:
			var id int
			if _, err := fmt.Sscanf(r.URL.Path, "/api/v1/album/%d", &id); err == nil {
				b.albumAsked = append(b.albumAsked, id)
				if body, ok := b.albums[id]; ok {
					fmt.Fprint(w, body)
					return
				}
			}
			http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
		}
	}
}

func (b *lidarrBackend) setQueue(records string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.queue = records
}

func (b *lidarrBackend) setHistory(records string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.history = records
}

func (b *lidarrBackend) setDeleteHistory(records string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.deleteHistory = records
}

func (b *lidarrBackend) asked() []int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]int(nil), b.albumAsked...)
}

// newAlbumBackend builds the standard Lidarr double: record 9 imported (files
// on disk), record 10 fileless.
func newAlbumBackend() *lidarrBackend {
	return &lidarrBackend{
		albums: map[int]string{
			9:  `{"id":9,"title":"Fear Inoculum","foreignAlbumId":"1f4a9e6b-63d5-4c0c-8ee6-a6a9155e6bd2","artist":{"artistName":"Tool"},"statistics":{"trackFileCount":10,"trackCount":10}}`,
			10: `{"id":10,"title":"Lateralus","foreignAlbumId":"e4306baa-75e3-3a0d-9c9c-b04d80e1a269","artist":{"artistName":"Tool"},"statistics":{"trackFileCount":0,"trackCount":13}}`,
		},
	}
}

func drainBroadcasts(t *testing.T, h *Hub) []Event {
	t.Helper()
	var events []Event
	for {
		select {
		case msg := <-h.broadcast:
			var e Event
			if err := json.Unmarshal(msg.data, &e); err != nil {
				t.Fatalf("decode broadcast: %v", err)
			}
			events = append(events, e)
		default:
			return events
		}
	}
}

// TestPollLidarrWitnessesImportedDepartures pins the music completion witness:
// a queue whose composition changes broadcasts the arr_queue_changed
// invalidation ping, a departed album with files on disk announces new_music
// from the live record, a departed album without files stays silent, and an
// unmapped queue row is never looked up.
func TestPollLidarrWitnessesImportedDepartures(t *testing.T) {
	backend := newAlbumBackend()
	srv := httptest.NewServer(backend.handler())
	t.Cleanup(srv.Close)
	client := lidarr.NewClient(srv.URL, "test-key")

	content := &recordingContent{}
	hub := NewHub(nil, nil, nil, nil, content, nil)

	// First poll: three tracked downloads (one unmapped). No previous state,
	// so nothing can have departed yet.
	backend.setQueue(`[
		{"id":100,"albumId":9,"status":"downloading","sizeleft":1000},
		{"id":101,"albumId":10,"status":"downloading","sizeleft":2000},
		{"id":102,"albumId":0,"status":"queued","sizeleft":3000}
	]`)
	hub.pollLidarrInstance("music-a", client)
	if events := drainBroadcasts(t, hub); len(events) != 0 {
		t.Fatalf("first poll broadcast %v, want none (no previous composition)", events)
	}
	if calls := content.musicCalls(); len(calls) != 0 {
		t.Fatalf("first poll notified %v, want none (no previous state)", calls)
	}
	if _, tracked := hub.prevLidarrQueue["music-a"][0]; tracked {
		t.Fatal("unmapped queue row (albumId 0) entered the membership")
	}

	// Second poll: everything left the queue. Only the record with files may
	// alert, and the unmapped id must not be fetched at all.
	backend.setQueue(`[]`)
	hub.pollLidarrInstance("music-a", client)

	events := drainBroadcasts(t, hub)
	if len(events) != 1 || events[0].Type != "arr_queue_changed" {
		t.Fatalf("broadcasts = %v, want exactly one arr_queue_changed", events)
	}
	if data := events[0].Data; data["service_type"] != "lidarr" || data["instance_id"] != "music-a" {
		t.Fatalf("ping payload = %#v", data)
	}
	calls := content.musicCalls()
	if len(calls) != 1 || calls[0] != "Fear Inoculum|Tool|1f4a9e6b-63d5-4c0c-8ee6-a6a9155e6bd2|music-a" {
		t.Errorf("witness calls = %v, want exactly the imported album", calls)
	}
	for _, id := range backend.asked() {
		if id == 0 {
			t.Error("unmapped queue row (albumId 0) was looked up")
		}
	}

	// Third poll: an empty queue again — no repeat alerts for old departures.
	hub.pollLidarrInstance("music-a", client)
	if calls := content.musicCalls(); len(calls) != 1 {
		t.Errorf("witness re-alerted on an unchanged empty queue: %v", calls)
	}
	if calls := content.upgradedMusicCalls(); len(calls) != 0 {
		t.Errorf("departures without delete proof rerouted to upgrades: %v", calls)
	}
}

// TestLidarrQueueWitnessSurvivesRestart pins the durable-membership story the
// music push category builds on: the membership one process saves is restored
// by the next, keyed under service type lidarr.
func TestLidarrQueueWitnessSurvivesRestart(t *testing.T) {
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	backend := newAlbumBackend()
	srv := httptest.NewServer(backend.handler())
	t.Cleanup(srv.Close)
	client := lidarr.NewClient(srv.URL, "test-key")

	first := NewHub(nil, nil, nil, database, &recordingContent{}, nil)
	backend.setQueue(`[{"id":100,"albumId":9,"status":"downloading","sizeleft":1000}]`)
	first.pollLidarrInstance("music-a", client)
	drainBroadcasts(t, first)

	second := NewHub(nil, nil, nil, database, &recordingContent{}, nil)
	second.restoreQueueWitness()
	if _, tracked := second.prevLidarrQueue["music-a"][9]; !tracked {
		t.Fatalf("restored membership = %v, want album 9", second.prevLidarrQueue["music-a"])
	}
	if second.restoredWitness["music-a"].IsZero() || time.Since(second.restoredWitness["music-a"]) > time.Minute {
		t.Fatalf("restored observation time = %v", second.restoredWitness["music-a"])
	}
}

// TestLidarrCatchUpAppliesImportGuards pins the music shape: only recognized
// import events with a dated record and files on disk announce; rename-class
// events, undated records, and fileless records stay silent — the same guards
// the departure witness applies.
func TestLidarrCatchUpAppliesImportGuards(t *testing.T) {
	database := witnessDB(t)
	backend := newAlbumBackend()
	srv := httptest.NewServer(backend.handler())
	t.Cleanup(srv.Close)
	client := lidarr.NewClient(srv.URL, "test-key")

	backend.setQueue(`[]`)
	hubA := NewHub(nil, nil, nil, database, &recordingContent{}, nil)
	hubA.pollLidarrInstance("music-a", client)

	if _, err := database.Exec(
		`UPDATE arr_queue_witness SET observed_at = ? WHERE instance_id = 'music-a'`,
		time.Now().Add(-30*time.Minute).UTC(),
	); err != nil {
		t.Fatalf("backdate witness: %v", err)
	}
	backend.setHistory(fmt.Sprintf(
		`[{"id":903,"albumId":9,"eventType":"trackFileImported","date":"%s"},
		  {"id":902,"albumId":10,"eventType":"trackFileImported","date":"%s"},
		  {"id":901,"albumId":11,"eventType":"Rename","date":"%s"},
		  {"id":900,"albumId":12,"eventType":"trackFileImported"}]`,
		historyDate(5*time.Minute), historyDate(6*time.Minute), historyDate(7*time.Minute)))

	after := &recordingContent{}
	hubB := NewHub(nil, nil, nil, database, after, nil)
	hubB.restoreQueueWitness()
	hubB.pollLidarrInstance("music-a", client)

	if got := after.musicCalls(); len(got) != 1 || got[0] != "Fear Inoculum|Tool|1f4a9e6b-63d5-4c0c-8ee6-a6a9155e6bd2|music-a" {
		t.Fatalf("lidarr catch-up = %v, want exactly the imported album", got)
	}
	for _, id := range backend.asked() {
		if id == 11 || id == 12 {
			t.Errorf("excluded history record %d was looked up", id)
		}
	}
}

// TestLidarrCatchUpReroutesProvenUpgrades pins the upgrade split for music: an
// import whose window pairs it with a trackFileDeleted record carrying reason
// Upgrade goes to the admin upgrade alert, never the household broadcast.
// Lidarr has no trackFileDelete webhook toggle, so this history pairing is the
// only upgrade proof music ever gets.
func TestLidarrCatchUpReroutesProvenUpgrades(t *testing.T) {
	database := witnessDB(t)
	backend := newAlbumBackend()
	srv := httptest.NewServer(backend.handler())
	t.Cleanup(srv.Close)
	client := lidarr.NewClient(srv.URL, "test-key")

	backend.setQueue(`[]`)
	hubA := NewHub(nil, nil, nil, database, &recordingContent{}, nil)
	hubA.pollLidarrInstance("music-a", client)

	if _, err := database.Exec(
		`UPDATE arr_queue_witness SET observed_at = ? WHERE instance_id = 'music-a'`,
		time.Now().Add(-30*time.Minute).UTC(),
	); err != nil {
		t.Fatalf("backdate witness: %v", err)
	}
	backend.setHistory(fmt.Sprintf(
		`[{"id":903,"albumId":9,"eventType":"trackFileImported","date":"%s"}]`,
		historyDate(5*time.Minute)))
	backend.setDeleteHistory(fmt.Sprintf(
		`[{"id":902,"albumId":9,"eventType":"trackFileDeleted","date":"%s","data":{"reason":"Upgrade"}}]`,
		historyDate(5*time.Minute)))

	after := &recordingContent{}
	hubB := NewHub(nil, nil, nil, database, after, nil)
	hubB.restoreQueueWitness()
	hubB.pollLidarrInstance("music-a", client)

	if got := after.musicCalls(); len(got) != 0 {
		t.Errorf("broadcast alerts = %v, want none for a proven upgrade", got)
	}
	if got := after.upgradedMusicCalls(); len(got) != 1 || got[0] != "Fear Inoculum|Tool|1f4a9e6b-63d5-4c0c-8ee6-a6a9155e6bd2|music-a" {
		t.Errorf("upgrade alerts = %v, want exactly the upgraded album", got)
	}
}
