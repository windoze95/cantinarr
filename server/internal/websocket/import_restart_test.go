package websocket

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/windoze95/cantinarr-server/internal/chaptarr"
	"github.com/windoze95/cantinarr-server/internal/lidarr"
	"github.com/windoze95/cantinarr-server/internal/radarr"
	"github.com/windoze95/cantinarr-server/internal/sonarr"
)

// restartBackend models an arr losing its tracked queue during a restart while
// its library still contains an older file. Import history is independent of
// queue membership, just as it is on the real providers.
type restartBackend struct {
	mu                         sync.Mutex
	service, queue, history    string
	deleteHistory              string
	queueFailed, historyFailed bool
	beforeHistory              func()
	afterHistory               func()
}

func (b *restartBackend) handler(w http.ResponseWriter, r *http.Request) {
	b.mu.Lock()
	defer b.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/api/v3/queue", "/api/v1/queue":
		if b.queueFailed {
			http.Error(w, "restarting", 503)
			return
		}
		fmt.Fprint(w, queueEnvelope(b.queue))
	case "/api/v3/history", "/api/v1/history":
		if b.historyFailed {
			http.Error(w, "history unavailable", 503)
			return
		}
		if b.beforeHistory != nil {
			f := b.beforeHistory
			b.beforeHistory = nil
			f()
		}
		records := b.history
		if r.URL.Query().Get("eventType") != "3" {
			records = b.deleteHistory
		}
		if records == "" {
			records = "[]"
		}
		total := 0
		if records != "[]" {
			total = 1
		}
		fmt.Fprintf(w, `{"page":1,"pageSize":200,"totalRecords":%d,"records":%s}`, total, records)
		if b.afterHistory != nil {
			f := b.afterHistory
			b.afterHistory = nil
			f()
		}
	case "/api/v3/movie/7":
		fmt.Fprint(w, `{"id":7,"tmdbId":70,"title":"Existing movie","hasFile":true}`)
	case "/api/v3/series/7":
		fmt.Fprint(w, `{"id":7,"tmdbId":70,"title":"Existing series","statistics":{"episodeFileCount":1}}`)
	case "/api/v3/episode":
		fmt.Fprint(w, `[{"id":71,"seriesId":7,"seasonNumber":1,"episodeNumber":1,"hasFile":true,"airDateUtc":"2020-01-01T00:00:00Z"}]`)
	case "/api/v1/book/7":
		fmt.Fprint(w, `{"id":7,"foreignBookId":"book-7","title":"Existing book","mediaType":"ebook","statistics":{"bookFileCount":1}}`)
	case "/api/v1/album/7":
		fmt.Fprint(w, `{"id":7,"foreignAlbumId":"album-7","title":"Existing album","statistics":{"trackFileCount":1}}`)
	default:
		http.NotFound(w, r)
	}
}

func (b *restartBackend) receipt(at time.Time) string {
	field, event := "movieId", "downloadFolderImported"
	switch b.service {
	case "sonarr":
		field = "seriesId"
	case "chaptarr":
		field, event = "bookId", "bookFileImported"
	case "lidarr":
		field, event = "albumId", "trackFileImported"
	}
	return fmt.Sprintf(`[{"id":900,"%s":7,"episodeId":71,"eventType":"%s","date":"%s"}]`, field, event, at.UTC().Format(time.RFC3339Nano))
}

func restartFixture(t *testing.T, service string) (*restartBackend, *Hub, *recordingContent, func(*Hub)) {
	t.Helper()
	b := &restartBackend{service: service}
	field := map[string]string{"radarr": "movieId", "sonarr": "seriesId", "chaptarr": "bookId", "lidarr": "albumId"}[service]
	b.queue = fmt.Sprintf(`[{"id":1,"%s":7,"status":"downloading","size":100,"sizeleft":50}]`, field)
	b.history = b.receipt(time.Now().Add(-time.Hour))
	srv := httptest.NewServer(http.HandlerFunc(b.handler))
	t.Cleanup(srv.Close)
	content := &recordingContent{}
	hub := NewHub(nil, nil, nil, witnessDB(t), content, nil)
	poll := func(h *Hub) {
		switch service {
		case "radarr":
			h.pollRadarrInstance(service, radarr.NewClient(srv.URL, "test-key"))
		case "sonarr":
			h.pollSonarrInstance(service, sonarr.NewClient(srv.URL, "test-key"))
		case "chaptarr":
			h.pollChaptarrInstance(service, chaptarr.NewClient(srv.URL, "test-key"))
		case "lidarr":
			h.pollLidarrInstance(service, lidarr.NewClient(srv.URL, "test-key"))
		}
	}
	return b, hub, content, poll
}

func assertImportCalls(t *testing.T, content *recordingContent, want int) {
	t.Helper()
	got := len(content.movieCalls()) + len(content.episodeCalls()) + len(content.calls()) + len(content.musicCalls())
	if got != want {
		t.Fatalf("content alerts = %d, want %d", got, want)
	}
}

func TestArrRestartRequiresFreshImportAndPreservesLaterCompletion(t *testing.T) {
	for _, service := range []string{"radarr", "sonarr", "chaptarr", "lidarr"} {
		for _, reappears := range []bool{false, true} {
			for _, queueFailure := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/reappears=%t/queue-failure=%t", service, reappears, queueFailure), func(t *testing.T) {
					b, h, content, poll := restartFixture(t, service)
					poll(h) // seed; old library files and old imports are not news
					initial := h.lastPollAt[service]
					b.mu.Lock()
					downloading := b.queue
					b.queueFailed = queueFailure
					b.mu.Unlock()
					if queueFailure {
						poll(h) // arr is unavailable during restart
						if !h.lastPollAt[service].Equal(initial) {
							t.Fatal("failed queue advanced cursor")
						}
					}
					b.mu.Lock()
					b.queueFailed = false
					b.queue = "[]"
					b.mu.Unlock()
					poll(h) // successful, transiently empty queue; old file still exists
					assertImportCalls(t, content, 0)
					poll(h) // another empty response must not manufacture evidence
					assertImportCalls(t, content, 0)
					if reappears {
						b.mu.Lock()
						b.queue = downloading
						b.mu.Unlock()
						poll(h)
					}
					b.mu.Lock()
					b.queue = "[]"
					b.history = b.receipt(time.Now())
					b.mu.Unlock()
					poll(h) // genuine later import, including when the queue never reappeared
					assertImportCalls(t, content, 1)
					poll(h)
					assertImportCalls(t, content, 1)
					// The resolved cursor is durable across a subsequent Cantinarr restart.
					resumed := NewHub(nil, nil, nil, h.witness.db, content, nil)
					resumed.restoreQueueWitness()
					poll(resumed)
					assertImportCalls(t, content, 1)
				})
			}
		}
	}
}

func TestImportHistoryFailureRetriesDurableWindow(t *testing.T) {
	for _, service := range []string{"radarr", "sonarr", "chaptarr", "lidarr"} {
		t.Run(service, func(t *testing.T) {
			b, h, content, poll := restartFixture(t, service)
			poll(h)
			drainBroadcasts(t, h)
			initial := h.lastPollAt[service]
			b.mu.Lock()
			b.queue = "[]"
			b.historyFailed = true
			b.history = b.receipt(time.Now())
			b.mu.Unlock()
			poll(h)
			assertImportCalls(t, content, 0)
			queueChanges := 0
			for _, event := range drainBroadcasts(t, h) {
				if event.Type == "arr_queue_changed" {
					queueChanges++
				}
			}
			if queueChanges != 1 {
				t.Fatalf("history failure suppressed queue invalidation: got %d events", queueChanges)
			}
			if !h.lastPollAt[service].Equal(initial) {
				t.Fatal("unreadable history advanced cursor")
			}
			// Retry must survive process restart, not just remain pending in RAM.
			resumed := NewHub(nil, nil, nil, h.witness.db, content, nil)
			resumed.restoreQueueWitness()
			if !resumed.lastPollAt[service].Equal(initial) {
				t.Fatal("unreadable history changed durable cursor")
			}
			b.mu.Lock()
			b.historyFailed = false
			b.mu.Unlock()
			poll(resumed)
			assertImportCalls(t, content, 1)
			poll(resumed)
			assertImportCalls(t, content, 1)
		})
	}
}

func TestImportWhileStillQueuedAndDuringHistoryRead(t *testing.T) {
	for _, service := range []string{"radarr", "sonarr", "chaptarr", "lidarr"} {
		t.Run(service, func(t *testing.T) {
			b, h, content, poll := restartFixture(t, service)
			poll(h)
			b.mu.Lock()
			// The import occurs after history is read but before that poll completes.
			// A poll-end cursor would step over it permanently.
			b.afterHistory = func() { b.history = b.receipt(time.Now()) }
			b.mu.Unlock()
			poll(h)
			assertImportCalls(t, content, 0)
			poll(h) // title remains queued for another episode/file
			assertImportCalls(t, content, 1)
			poll(h)
			assertImportCalls(t, content, 1)
			b.mu.Lock()
			b.queue = "[]"
			b.mu.Unlock()
			poll(h)
			assertImportCalls(t, content, 1)
		})
	}
}

func TestImportDuringReadWaitsForDurableBoundary(t *testing.T) {
	for _, service := range []string{"radarr", "sonarr", "chaptarr", "lidarr"} {
		t.Run(service, func(t *testing.T) {
			b, h, content, poll := restartFixture(t, service)
			poll(h)
			b.mu.Lock()
			// This receipt appears in the response, but is newer than the
			// cursor this poll can persist. Announcing it now would replay it.
			b.beforeHistory = func() { b.history = b.receipt(time.Now()) }
			b.mu.Unlock()
			poll(h)
			assertImportCalls(t, content, 0)
			// Restart before the next poll to exercise the saved boundary too.
			resumed := NewHub(nil, nil, nil, h.witness.db, content, nil)
			resumed.restoreQueueWitness()
			poll(resumed)
			assertImportCalls(t, content, 1)
			poll(resumed)
			assertImportCalls(t, content, 1)
		})
	}
}

func TestUpgradeEvidenceUsesSamePollBoundary(t *testing.T) {
	for _, service := range []string{"radarr", "sonarr", "chaptarr", "lidarr"} {
		t.Run(service, func(t *testing.T) {
			b, h, content, poll := restartFixture(t, service)
			poll(h)
			b.mu.Lock()
			b.history = b.receipt(time.Now())
			// A deletion for a subsequent upgrade happens during the read.
			// It cannot reclassify the earlier import in this closed window.
			b.beforeHistory = func() {
				field, event, id := "movieId", "movieFileDeleted", 7
				switch service {
				case "sonarr":
					field, event, id = "episodeId", "episodeFileDeleted", 71
				case "chaptarr":
					field, event = "bookId", "bookFileDeleted"
				case "lidarr":
					field, event = "albumId", "trackFileDeleted"
				}
				b.deleteHistory = fmt.Sprintf(`[{"id":901,%q:%d,"eventType":%q,"date":%q,"data":{"reason":"Upgrade"}}]`, field, id, event, time.Now().UTC().Format(time.RFC3339Nano))
			}
			b.mu.Unlock()
			poll(h)
			assertImportCalls(t, content, 1)
			upgrades := len(content.upgradedMovieCalls()) + len(content.upgradedEpisodeCalls()) + len(content.upgradedBookCalls()) + len(content.upgradedMusicCalls())
			if upgrades != 0 {
				t.Fatalf("later delete rerouted earlier import to %d upgrade alerts", upgrades)
			}
		})
	}
}

func TestImportEvidenceWindowBoundaries(t *testing.T) {
	since := time.Now().Add(-time.Minute)
	until := since.Add(30 * time.Second)
	for _, service := range []string{"radarr", "sonarr", "chaptarr", "lidarr"} {
		for _, tc := range []struct {
			name string
			at   time.Time
			want int
		}{
			{"at prior cursor", since, 0},
			{"at poll start", until, 1},
			{"after poll start", until.Add(time.Nanosecond), 0},
		} {
			t.Run(service+"/"+tc.name, func(t *testing.T) {
				b := &restartBackend{service: service}
				b.history = b.receipt(tc.at)
				srv := httptest.NewServer(http.HandlerFunc(b.handler))
				t.Cleanup(srv.Close)
				var fresh, upgrades []int
				var err error
				switch service {
				case "radarr":
					fresh, upgrades, err = radarrImportsSince(radarr.NewClient(srv.URL, "fixture"), since, until)
				case "sonarr":
					var imports map[int][]sonarr.ImportedEpisode
					imports, err = sonarrImportEvidence(sonarr.NewClient(srv.URL, "fixture"), since, until)
					for id := range imports {
						fresh = append(fresh, id)
					}
				case "chaptarr":
					fresh, upgrades, err = chaptarrImportsSince(chaptarr.NewClient(srv.URL, "fixture"), since, until)
				case "lidarr":
					fresh, upgrades, err = lidarrImportsSince(lidarr.NewClient(srv.URL, "fixture"), since, until)
				}
				if err != nil {
					t.Fatal(err)
				}
				if len(fresh) != tc.want || len(upgrades) != 0 {
					t.Fatalf("window evidence = fresh %v, upgraded %v; want %d fresh", fresh, upgrades, tc.want)
				}
			})
		}
	}
}
