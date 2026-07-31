package webhooks

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/instance"
)

// bookBackend is a minimal Chaptarr API double: a fixed book table that 404s
// anything else, recording which records were looked up.
type bookBackend struct {
	books map[int]string
	asked []int
}

func (b *bookBackend) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var id int
		if _, err := fmt.Sscanf(r.URL.Path, "/api/v1/book/%d", &id); err == nil {
			b.asked = append(b.asked, id)
			if body, ok := b.books[id]; ok {
				_, _ = w.Write([]byte(body))
				return
			}
		}
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// addChaptarr attaches a Chaptarr instance to an existing fixture and returns
// its id and webhook credential.
func (f *fixture) addChaptarr(t *testing.T, url string) (string, string) {
	t.Helper()
	inst := &instance.Instance{ServiceType: "chaptarr", Name: "Books", URL: url, APIKey: "key"}
	if err := f.store.Create(inst); err != nil {
		t.Fatalf("create chaptarr: %v", err)
	}
	token, err := f.store.WebhookToken(inst.ID)
	if err != nil {
		t.Fatalf("chaptarr token: %v", err)
	}
	return inst.ID, token
}

// newBookFixture wires a fixture whose Chaptarr instance serves record 7 with a
// file on disk and record 8 with none.
func newBookFixture(t *testing.T) (*fixture, *bookBackend, string, string) {
	t.Helper()
	backend := &bookBackend{books: map[int]string{
		7: `{"id":7,"title":"Ahsoka (Star Wars)","foreignBookId":"29749107","mediaType":"ebook","statistics":{"bookFileCount":1}}`,
		8: `{"id":8,"title":"Flock","foreignBookId":"55520734","mediaType":"audiobook","statistics":{"bookFileCount":0}}`,
	}}
	srv := backend.server(t)
	f := newFixture(t, "http://radarr.invalid", "http://sonarr.invalid")
	id, token := f.addChaptarr(t, srv.URL)
	return f, backend, id, token
}

func (f *fixture) postBook(t *testing.T, id, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	return f.post(t, "/api/webhooks/arr/"+id, body, func(r *http.Request) {
		r.SetBasicAuth("cantinarr", token)
	})
}

// TestChaptarrImportAlertsFromLiveRecord is the reason this path exists: a small
// ebook can be grabbed and imported inside one 30s poll interval, so the queue
// witness never sees it and the alert would otherwise never be sent.
func TestChaptarrImportAlertsFromLiveRecord(t *testing.T) {
	f, _, id, token := newBookFixture(t)

	rec := f.postBook(t, id, token, `{"eventType":"Download","book":{"id":7}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	// The identity must come from the live record, not the payload, so the claim
	// key matches the queue witness's exactly.
	want := "Ahsoka (Star Wars)|29749107|" + id + "|ebook"
	if len(f.content.books) != 1 || f.content.books[0] != want {
		t.Errorf("book alerts = %v, want exactly [%s]", f.content.books, want)
	}
	if len(f.requests.bookIDs) == 0 {
		t.Error("book availability caches were not invalidated, so the app would show stale state")
	}
	if len(f.hub.events) == 0 || f.hub.events[0].Type != "arr_queue_changed" {
		t.Errorf("broadcasts = %v, want an arr_queue_changed invalidation ping", f.hub.events)
	}
	// Books carry no TMDB id; a request_status_changed would collide across
	// every book at tmdb 0.
	for _, e := range f.hub.events {
		if e.Type == "request_status_changed" {
			t.Error("a book event emitted request_status_changed")
		}
	}
}

// TestChaptarrImportPayloadIsOnlyATrigger pins that a forged or drifted payload
// cannot fabricate an alert. Chaptarr is closed source, so the body is never
// trusted for identity.
func TestChaptarrImportPayloadIsOnlyATrigger(t *testing.T) {
	f, _, id, token := newBookFixture(t)

	f.postBook(t, id, token,
		`{"eventType":"Download","book":{"id":7,"title":"Attacker Title","foreignBookId":"666"}}`)

	want := "Ahsoka (Star Wars)|29749107|" + id + "|ebook"
	if len(f.content.books) != 1 || f.content.books[0] != want {
		t.Errorf("book alerts = %v, want the live record's identity [%s]", f.content.books, want)
	}
}

// TestChaptarrImportEventVocabularyIsTolerant covers the main external unknown:
// the fork's import event name. Every plausible spelling must alert, because a
// missed name means the alert is never sent.
func TestChaptarrImportEventVocabularyIsTolerant(t *testing.T) {
	for _, event := range []string{"Download", "download", "ReleaseImport", "release_import", "bookFileImported", "BookImported"} {
		t.Run(event, func(t *testing.T) {
			f, _, id, token := newBookFixture(t)
			f.postBook(t, id, token, fmt.Sprintf(`{"eventType":%q,"book":{"id":7}}`, event))
			if len(f.content.books) != 1 {
				t.Errorf("event %q produced %v, want exactly one alert", event, f.content.books)
			}
		})
	}
}

// TestChaptarrNonImportEventsNeverAlert pins the other side of that tolerance: a
// rename or delete must never claim a book is ready.
func TestChaptarrNonImportEventsNeverAlert(t *testing.T) {
	for _, event := range []string{"Rename", "Retag", "BookDelete", "AuthorDelete", "BookFileDelete", "Grab"} {
		t.Run(event, func(t *testing.T) {
			f, backend, id, token := newBookFixture(t)
			rec := f.postBook(t, id, token, fmt.Sprintf(`{"eventType":%q,"book":{"id":7}}`, event))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			if len(f.content.books) != 0 {
				t.Errorf("event %q alerted %v, want silence", event, f.content.books)
			}
			if len(backend.asked) != 0 {
				t.Errorf("event %q cost %d live lookups, want 0", event, len(backend.asked))
			}
			// It still changed the library, so caches must drop.
			if len(f.requests.bookIDs) == 0 {
				t.Errorf("event %q did not invalidate book caches", event)
			}
		})
	}
}

// TestChaptarrTestEventHasNoSideEffects keeps the save-time Test button from
// pushing anything or churning caches.
func TestChaptarrTestEventHasNoSideEffects(t *testing.T) {
	f, backend, id, token := newBookFixture(t)

	rec := f.postBook(t, id, token, `{"eventType":"Test"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if len(f.content.books) != 0 || len(f.requests.bookIDs) != 0 || len(f.hub.events) != 0 || len(backend.asked) != 0 {
		t.Errorf("Test caused side effects: books=%v caches=%v events=%v lookups=%v",
			f.content.books, f.requests.bookIDs, f.hub.events, backend.asked)
	}
}

// TestChaptarrImportWithoutFileStaysSilent pins the guard shared with the queue
// witness: a departure with no file means the import ghosted.
func TestChaptarrImportWithoutFileStaysSilent(t *testing.T) {
	f, _, id, token := newBookFixture(t)

	f.postBook(t, id, token, `{"eventType":"Download","book":{"id":8}}`)
	if len(f.content.books) != 0 {
		t.Errorf("a record with no file alerted %v, want silence", f.content.books)
	}
}

// TestChaptarrImportOfDeletedRecordDoesNotPanic pins the (nil, nil) 404 contract.
// Dereferencing it would panic the request goroutine.
func TestChaptarrImportOfDeletedRecordDoesNotPanic(t *testing.T) {
	f, _, id, token := newBookFixture(t)

	rec := f.postBook(t, id, token, `{"eventType":"Download","book":{"id":999}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if len(f.content.books) != 0 {
		t.Errorf("a deleted record alerted %v, want silence", f.content.books)
	}
}

// TestChaptarrImportWithoutBookIDFallsBackToPoller pins the degrade path: an
// import we cannot resolve must still refresh the app and stay quiet, leaving
// the 30s witness to alert.
func TestChaptarrImportWithoutBookIDFallsBackToPoller(t *testing.T) {
	f, backend, id, token := newBookFixture(t)

	rec := f.postBook(t, id, token, `{"eventType":"Download","book":{"id":0}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if len(f.content.books) != 0 {
		t.Errorf("an unresolvable import alerted %v, want silence", f.content.books)
	}
	if len(backend.asked) != 0 {
		t.Errorf("an unresolvable import cost %d lookups, want 0", len(backend.asked))
	}
	if len(f.requests.bookIDs) == 0 {
		t.Error("an unresolvable import must still invalidate book caches")
	}
}

// TestChaptarrUnknownEventIsAcknowledged keeps a fork's unmodelled event from
// looking like an error on the Chaptarr side.
func TestChaptarrUnknownEventIsAcknowledged(t *testing.T) {
	f, _, id, token := newBookFixture(t)

	rec := f.postBook(t, id, token, `{"eventType":"HealthIssue","book":{"id":7}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if len(f.content.books) != 0 || len(f.requests.bookIDs) != 0 {
		t.Error("an unknown event had side effects")
	}
}

// TestChaptarrWebhookRequiresItsOwnCredential pins that book callbacks are
// authenticated exactly like the video ones.
func TestChaptarrWebhookRequiresItsOwnCredential(t *testing.T) {
	f, _, id, _ := newBookFixture(t)

	rec := f.postBook(t, id, "wrong-token", `{"eventType":"Download","book":{"id":7}}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if len(f.content.books) != 0 {
		t.Error("an unauthenticated callback produced an alert")
	}
}

// TestChaptarrGrabListAlertsPerRecord covers the plural payload shape: ebook and
// audiobook are separate records and each alerts on its own import.
func TestChaptarrMultipleBookIDsAlertOnce(t *testing.T) {
	f, backend, id, token := newBookFixture(t)
	backend.books[9] = `{"id":9,"title":"Ahsoka (Star Wars)","foreignBookId":"29749107","mediaType":"audiobook","statistics":{"bookFileCount":1}}`

	f.postBook(t, id, token, `{"eventType":"Download","book":{"id":7},"books":[{"id":7},{"id":9}]}`)

	if len(f.content.books) != 2 {
		t.Fatalf("alerts = %v, want one per distinct record", f.content.books)
	}
	if f.content.books[0] != "Ahsoka (Star Wars)|29749107|"+id+"|ebook" ||
		f.content.books[1] != "Ahsoka (Star Wars)|29749107|"+id+"|audiobook" {
		t.Errorf("alerts = %v, want the ebook and audiobook records distinctly", f.content.books)
	}
}

// TestNonChaptarrInstanceIgnoresBookPayload proves the branch is service-scoped:
// a book body sent to a Radarr instance must not reach the book path.
func TestNonChaptarrInstanceIgnoresBookPayload(t *testing.T) {
	f, _, _, _ := newBookFixture(t)

	rec := f.post(t, "/api/webhooks/arr/"+f.radarrID, `{"eventType":"Download","book":{"id":7}}`,
		func(r *http.Request) { r.SetBasicAuth("cantinarr", f.radarrTok) })
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if len(f.content.books) != 0 {
		t.Errorf("a Radarr callback produced book alerts %v", f.content.books)
	}
	if len(f.requests.bookIDs) != 0 {
		t.Error("a Radarr callback invalidated book caches")
	}
}
