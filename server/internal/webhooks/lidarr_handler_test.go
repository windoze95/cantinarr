package webhooks

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/instance"
)

// albumBackend is a minimal Lidarr API double: a fixed album table that 404s
// anything else, recording which records were looked up.
type albumBackend struct {
	albums map[int]string
	asked  []int
}

func (b *albumBackend) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var id int
		if _, err := fmt.Sscanf(r.URL.Path, "/api/v1/album/%d", &id); err == nil {
			b.asked = append(b.asked, id)
			if body, ok := b.albums[id]; ok {
				_, _ = w.Write([]byte(body))
				return
			}
		}
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// addLidarr attaches a Lidarr instance to an existing fixture and returns its
// id and webhook credential.
func (f *fixture) addLidarr(t *testing.T, url string) (string, string) {
	t.Helper()
	inst := &instance.Instance{ServiceType: "lidarr", Name: "Music", URL: url, APIKey: "key"}
	if err := f.store.Create(inst); err != nil {
		t.Fatalf("create lidarr: %v", err)
	}
	token, err := f.store.WebhookToken(inst.ID)
	if err != nil {
		t.Fatalf("lidarr token: %v", err)
	}
	return inst.ID, token
}

// newMusicFixture wires a fixture whose Lidarr instance serves record 9 with
// files on disk and record 10 with none.
func newMusicFixture(t *testing.T) (*fixture, *albumBackend, string, string) {
	t.Helper()
	backend := &albumBackend{albums: map[int]string{
		9:  `{"id":9,"title":"Fear Inoculum","foreignAlbumId":"1f4a9e6b-63d5-4c0c-8ee6-a6a9155e6bd2","artist":{"artistName":"Tool"},"statistics":{"trackFileCount":10,"trackCount":10}}`,
		10: `{"id":10,"title":"Lateralus","foreignAlbumId":"e4306baa-75e3-3a0d-9c9c-b04d80e1a269","artist":{"artistName":"Tool"},"statistics":{"trackFileCount":0,"trackCount":13}}`,
	}}
	srv := backend.server(t)
	f := newFixture(t, "http://radarr.invalid", "http://sonarr.invalid")
	id, token := f.addLidarr(t, srv.URL)
	return f, backend, id, token
}

func (f *fixture) postMusic(t *testing.T, id, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	return f.post(t, "/api/webhooks/arr/"+id, body, func(r *http.Request) {
		r.SetBasicAuth("cantinarr", token)
	})
}

// TestLidarrImportAlertsFromLiveRecord is the reason this path exists: a small
// album can be grabbed and imported inside one 30s poll interval, so the queue
// witness never sees it and the alert would otherwise never be sent. Lidarr
// serializes the import event as "Download" with a plural albums list.
func TestLidarrImportAlertsFromLiveRecord(t *testing.T) {
	f, _, id, token := newMusicFixture(t)

	rec := f.postMusic(t, id, token, `{"eventType":"Download","albums":[{"id":9,"mbId":"mb-1"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	// The identity must come from the live record, not the payload, so the
	// claim key matches the queue witness's exactly.
	want := "Fear Inoculum|Tool|1f4a9e6b-63d5-4c0c-8ee6-a6a9155e6bd2|" + id
	if len(f.content.music) != 1 || f.content.music[0] != want {
		t.Errorf("music alerts = %v, want exactly [%s]", f.content.music, want)
	}
	if len(f.requests.musicIDs) != 1 || f.requests.musicIDs[0] != id {
		t.Fatalf("music invalidations = %v", f.requests.musicIDs)
	}
	if len(f.hub.events) == 0 || f.hub.events[0].Type != "arr_queue_changed" {
		t.Fatalf("broadcasts = %v, want an arr_queue_changed invalidation ping", f.hub.events)
	}
	if data := f.hub.events[0].Data; data["service_type"] != "lidarr" || data["instance_id"] != id {
		t.Fatalf("ping payload = %#v", data)
	}
	// Music has no TMDB id; request_status_changed would collide across every
	// album at tmdb 0.
	for _, e := range f.hub.events {
		if e.Type == "request_status_changed" {
			t.Error("a music event emitted request_status_changed")
		}
	}
	// A payload without isUpgrade IS the fail-open pin: no flag, no upgrade
	// rerouting.
	if len(f.content.upgradedMusic) != 0 {
		t.Errorf("upgrade alerts = %v, want none without isUpgrade", f.content.upgradedMusic)
	}
}

// TestLidarrImportUpgradeReroutesToAdmins pins that isUpgrade:true swaps the
// audience — the assigned-listener broadcast stays silent, the admin upgrade
// alert carries the identical live-record identity — while cache invalidation
// and the queue ping are unchanged.
func TestLidarrImportUpgradeReroutesToAdmins(t *testing.T) {
	f, _, id, token := newMusicFixture(t)

	rec := f.postMusic(t, id, token, `{"eventType":"Download","isUpgrade":true,"albums":[{"id":9}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if len(f.content.music) != 0 {
		t.Errorf("broadcast alerts = %v, want none for a proven upgrade", f.content.music)
	}
	want := "Fear Inoculum|Tool|1f4a9e6b-63d5-4c0c-8ee6-a6a9155e6bd2|" + id
	if len(f.content.upgradedMusic) != 1 || f.content.upgradedMusic[0] != want {
		t.Errorf("upgrade alerts = %v, want exactly [%s]", f.content.upgradedMusic, want)
	}
	if len(f.requests.musicIDs) == 0 {
		t.Error("music availability caches were not invalidated, so the app would show stale state")
	}
	if len(f.hub.events) == 0 || f.hub.events[0].Type != "arr_queue_changed" {
		t.Errorf("broadcasts = %v, want an arr_queue_changed invalidation ping", f.hub.events)
	}
}

// TestLidarrImportPayloadIsOnlyATrigger pins that a forged or drifted payload
// cannot fabricate an alert. An arr-origin body is never trusted for identity,
// whoever wrote it.
func TestLidarrImportPayloadIsOnlyATrigger(t *testing.T) {
	f, _, id, token := newMusicFixture(t)

	f.postMusic(t, id, token,
		`{"eventType":"Download","albums":[{"id":9,"title":"Attacker Title","foreignAlbumId":"666"}]}`)

	want := "Fear Inoculum|Tool|1f4a9e6b-63d5-4c0c-8ee6-a6a9155e6bd2|" + id
	if len(f.content.music) != 1 || f.content.music[0] != want {
		t.Errorf("music alerts = %v, want the live record's identity [%s]", f.content.music, want)
	}
}

// TestLidarrImportEventVocabularyIsTolerant covers the main external unknown:
// the import event name. Every plausible spelling must alert, because a missed
// name means the alert is never sent.
func TestLidarrImportEventVocabularyIsTolerant(t *testing.T) {
	for _, event := range []string{"Download", "download", "ReleaseImport", "release_import", "trackFileImported", "AlbumImported", "DownloadImported"} {
		t.Run(event, func(t *testing.T) {
			f, _, id, token := newMusicFixture(t)
			f.postMusic(t, id, token, fmt.Sprintf(`{"eventType":%q,"albums":[{"id":9}]}`, event))
			if len(f.content.music) != 1 {
				t.Errorf("event %q produced %v, want exactly one alert", event, f.content.music)
			}
		})
	}
}

// TestLidarrNonImportEventsNeverAlert pins the other side of that tolerance: a
// rename, retag, delete or grab must never claim an album is ready — but each
// still changed the library, so caches must drop.
func TestLidarrNonImportEventsNeverAlert(t *testing.T) {
	for _, event := range []string{"Rename", "Retag", "AlbumDelete", "ArtistDelete", "ArtistAdd", "Grab"} {
		t.Run(event, func(t *testing.T) {
			f, backend, id, token := newMusicFixture(t)
			rec := f.postMusic(t, id, token, fmt.Sprintf(`{"eventType":%q,"albums":[{"id":9}]}`, event))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			if len(f.content.music) != 0 {
				t.Errorf("event %q alerted %v, want silence", event, f.content.music)
			}
			if len(backend.asked) != 0 {
				t.Errorf("event %q cost %d live lookups, want 0", event, len(backend.asked))
			}
			if len(f.requests.musicIDs) == 0 {
				t.Errorf("event %q did not invalidate music caches", event)
			}
		})
	}
}

// TestLidarrTestEventHasNoSideEffects keeps the save-time Test button from
// pushing anything or churning caches.
func TestLidarrTestEventHasNoSideEffects(t *testing.T) {
	f, backend, id, token := newMusicFixture(t)

	rec := f.postMusic(t, id, token, `{"eventType":"Test"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if len(f.content.music) != 0 || len(f.requests.musicIDs) != 0 || len(f.hub.events) != 0 || len(backend.asked) != 0 {
		t.Errorf("Test caused side effects: music=%v caches=%v events=%v lookups=%v",
			f.content.music, f.requests.musicIDs, f.hub.events, backend.asked)
	}
}

// TestLidarrImportWithoutFileStaysSilent pins the guard shared with the queue
// witness: a record with no track file means the import ghosted.
func TestLidarrImportWithoutFileStaysSilent(t *testing.T) {
	f, _, id, token := newMusicFixture(t)

	f.postMusic(t, id, token, `{"eventType":"Download","albums":[{"id":10}]}`)
	if len(f.content.music) != 0 {
		t.Errorf("a record with no file alerted %v, want silence", f.content.music)
	}
}

// TestLidarrImportOfDeletedRecordDoesNotPanic pins the (nil, nil) 404 contract.
// Dereferencing it would panic the request goroutine.
func TestLidarrImportOfDeletedRecordDoesNotPanic(t *testing.T) {
	f, _, id, token := newMusicFixture(t)

	rec := f.postMusic(t, id, token, `{"eventType":"Download","albums":[{"id":999}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if len(f.content.music) != 0 {
		t.Errorf("a deleted record alerted %v, want silence", f.content.music)
	}
}

// TestLidarrImportWithoutAlbumIDFallsBackToPoller pins the degrade path: an
// import we cannot resolve must still refresh the app and stay quiet, leaving
// the 30s witness to alert.
func TestLidarrImportWithoutAlbumIDFallsBackToPoller(t *testing.T) {
	f, backend, id, token := newMusicFixture(t)

	rec := f.postMusic(t, id, token, `{"eventType":"Download","albums":[{"id":0}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if len(f.content.music) != 0 {
		t.Errorf("an unresolvable import alerted %v, want silence", f.content.music)
	}
	if len(backend.asked) != 0 {
		t.Errorf("an unresolvable import cost %d lookups, want 0", len(backend.asked))
	}
	if len(f.requests.musicIDs) == 0 {
		t.Error("an unresolvable import must still invalidate music caches")
	}
}

// TestLidarrMultipleAlbumIDsAlertOnce covers the plural payload shape: distinct
// records each alert, and the singular album field deduplicates against the
// list.
func TestLidarrMultipleAlbumIDsAlertOnce(t *testing.T) {
	f, backend, id, token := newMusicFixture(t)
	backend.albums[11] = `{"id":11,"title":"Undertow","foreignAlbumId":"aa97c5d2-1a8d-3b3c-9dcb-1b3b1e6b3f52","artist":{"artistName":"Tool"},"statistics":{"trackFileCount":10,"trackCount":10}}`

	f.postMusic(t, id, token, `{"eventType":"Download","album":{"id":9},"albums":[{"id":9},{"id":11}]}`)

	if len(f.content.music) != 2 {
		t.Fatalf("alerts = %v, want one per distinct record", f.content.music)
	}
	if f.content.music[0] != "Fear Inoculum|Tool|1f4a9e6b-63d5-4c0c-8ee6-a6a9155e6bd2|"+id ||
		f.content.music[1] != "Undertow|Tool|aa97c5d2-1a8d-3b3c-9dcb-1b3b1e6b3f52|"+id {
		t.Errorf("alerts = %v, want the two records distinctly", f.content.music)
	}
}

// TestLidarrUnknownEventIsAcknowledged keeps an unmodelled event from looking
// like an error on the Lidarr side (a 4xx would make Lidarr disable the
// webhook).
func TestLidarrUnknownEventIsAcknowledged(t *testing.T) {
	f, _, id, token := newMusicFixture(t)

	for _, body := range []string{`{"eventType":"Health"}`, `{"eventType":"SomethingNew"}`} {
		rec := f.postMusic(t, id, token, body)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d for %s", rec.Code, body)
		}
	}
	if len(f.content.music) != 0 || len(f.requests.musicIDs) != 0 || len(f.hub.events) != 0 {
		t.Error("an unknown event had side effects")
	}
}

// TestNonLidarrInstanceIgnoresMusicPayload proves the branch is service-scoped:
// an albums body sent to a Radarr instance must not reach the music path.
func TestNonLidarrInstanceIgnoresMusicPayload(t *testing.T) {
	f, _, _, _ := newMusicFixture(t)

	rec := f.post(t, "/api/webhooks/arr/"+f.radarrID, `{"eventType":"Download","albums":[{"id":9}]}`,
		func(r *http.Request) { r.SetBasicAuth("cantinarr", f.radarrTok) })
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if len(f.content.music) != 0 {
		t.Errorf("a Radarr callback produced music alerts %v", f.content.music)
	}
	if len(f.requests.musicIDs) != 0 {
		t.Error("a Radarr callback invalidated music caches")
	}
}

func TestLidarrWebhookRejectsBadToken(t *testing.T) {
	f, _, id, _ := newMusicFixture(t)

	rec := f.post(t, "/api/webhooks/arr/"+id, `{"eventType":"Download"}`, func(r *http.Request) {
		r.SetBasicAuth("cantinarr", "wrong")
	})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if len(f.requests.musicIDs) != 0 {
		t.Fatalf("unauthenticated call invalidated caches: %v", f.requests.musicIDs)
	}
}
