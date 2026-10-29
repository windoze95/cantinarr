package request

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/contentnotify"
)

func TestRequestedMovieUsesFreshOwningLibraryAndCurrentDecision(t *testing.T) {
	var available atomic.Bool
	var calls atomic.Int32
	var duringRead atomic.Pointer[func()]
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if callback := duringRead.Load(); callback != nil {
			(*callback)()
		}
		fmt.Fprintf(w, `[{"id":7,"tmdbId":550,"hasFile":%t}]`, available.Load())
	}))
	defer upstream.Close()
	sibling := newFakeRadarrServer(t, &fakeRadarr{libraryJSON: `[{"id":8,"tmdbId":550,"hasFile":true}]`})
	s, uid, _, instance, _ := newTwoRadarrTestService(t, upstream.URL, sibling.URL)
	id := seedDiscordRequest(t, s, uid, instance, "movie", "", "", 0)
	seedDiscordRequest(t, s, uid, instance, "movie", "", "", 0)
	if _, ok := s.movieAvailabilityDigestForInstance(instance); !ok {
		t.Fatal("seed cache")
	}
	available.Store(true)
	before := calls.Load()
	event := contentnotify.Import{MediaType: "movie", InstanceID: instance, TmdbID: 550}
	got, err := s.RequestedContentRecipients(context.Background(), event, []int64{uid})
	if err != nil || fmt.Sprint(got) != fmt.Sprint([]int64{uid}) {
		t.Fatalf("fresh import: %v %v", got, err)
	}
	if calls.Load()-before != 1 {
		t.Fatalf("fresh reads per batch=%d", calls.Load()-before)
	}
	// A final provider response must not carry a request that was denied while
	// it was in flight. Both duplicate rows are changed to exercise the batch.
	callback := func() {
		if _, err := s.db.Exec(`UPDATE request_log SET status='denied' WHERE user_id=?`, uid); err != nil {
			t.Error(err)
		}
	}
	duringRead.Store(&callback)
	got, err = s.RequestedContentRecipients(context.Background(), event, []int64{uid})
	if err != nil || len(got) != 0 {
		t.Fatalf("denied during read (%d): %v %v", id, got, err)
	}
}

func TestRequestedBookWaitersAndFormatSpecificRekey(t *testing.T) {
	var count atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/book" {
			fmt.Fprintf(w, `[{"id":7,"foreignBookId":"ebook-key","mediaType":"ebook","statistics":{"bookFileCount":%d}},{"id":8,"foreignBookId":"audio-key","mediaType":"audiobook","statistics":{"bookFileCount":%d}}]`, count.Load(), count.Load())
			return
		}
		if r.URL.Path == "/api/v1/queue" {
			fmt.Fprint(w, `{"records":[],"totalRecords":0}`)
			return
		}
		w.WriteHeader(503)
	}))
	defer upstream.Close()
	s, owner := newChaptarrBookTestService(t, upstream.URL)
	var instance string
	if err := s.db.QueryRow(`SELECT id FROM service_instances`).Scan(&instance); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int{991, 992} {
		if _, err := s.db.Exec(`INSERT INTO users(id,username,password_hash,role) VALUES(?,?,'','user')`, id, fmt.Sprint(id)); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Exec(`INSERT INTO user_instance_grants(user_id,instance_id) VALUES(?,?)`, id, instance); err != nil {
			t.Fatal(err)
		}
	}
	id := seedDiscordRequest(t, s, owner, instance, "book", "old-key", "both", 0)
	if _, err := s.db.Exec(`INSERT INTO request_dispatch(request_id,format,state,book_record_id) VALUES(?,'ebook','complete',7),(?,'audiobook','complete',8)`, id, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO book_request_waiters(request_id,user_id,book_format) VALUES(?,991,'ebook'),(?,992,'audiobook')`, id, id); err != nil {
		t.Fatal(err)
	}
	// This old unresolvable request must not poison the valid bound request.
	seedDiscordRequest(t, s, owner, instance, "book", "unresolvable", "ebook", 0)
	if _, err := s.DiscordAvailability(context.Background(), id, false); err != nil {
		t.Fatal(err)
	}
	count.Store(1)
	for _, tc := range []struct {
		format, key string
		want        int64
	}{{"ebook", "ebook-key", 991}, {"audiobook", "audio-key", 992}} {
		event := contentnotify.Import{MediaType: "book", InstanceID: instance, ForeignID: tc.key, Format: tc.format}
		got, _ := s.RequestedContentRecipients(context.Background(), event, []int64{owner, 991, 992})
		if fmt.Sprint(got) != fmt.Sprint([]int64{owner, tc.want}) {
			t.Fatalf("%s recipients: %v", tc.format, got)
		}
	}
	if _, err := s.db.Exec(`INSERT INTO book_request_waiters(request_id,user_id,book_format) VALUES(?,?,'ebook')`, id, owner); err != nil {
		t.Fatal(err)
	}
	allowed, err := s.currentRequestRecipient(context.Background(), id, owner, contentnotify.Import{MediaType: "book", InstanceID: instance, ForeignID: "audio-key", Format: "audiobook"})
	if err != nil || allowed {
		t.Fatalf("owner waiter precedence: %v %v", allowed, err)
	}

}

func TestRequestedTVUsesImportedEpisodeAndSavedSeason(t *testing.T) {
	s, uid, response, l := deliveredSeasonOne(t)
	event := contentnotify.Import{MediaType: "tv", InstanceID: response.InstanceID, TmdbID: 12345, EpisodeIDs: []int{21}}
	got, err := s.RequestedContentRecipients(context.Background(), event, []int64{uid})
	if err != nil || len(got) != 0 {
		t.Fatalf("unrequested season: %v %v", got, err)
	}
	event.EpisodeIDs = []int{11}
	got, err = s.RequestedContentRecipients(context.Background(), event, []int64{uid})
	if err != nil || len(got) != 1 {
		t.Fatalf("requested season: %v %v", got, err)
	}
	// Cache a false value, then import the second requested episode. Request
	// matching must bypass the pre-import snapshot without webhook invalidation.
	l.mu.Lock()
	for _, ep := range l.episodes {
		ep["hasFile"] = false
	}
	l.mu.Unlock()
	if _, err = s.DiscordAvailability(context.Background(), response.RequestID, true); err != nil {
		t.Fatal(err)
	}
	l.mu.Lock()
	for _, ep := range l.episodes {
		if ep["id"] == 12 {
			ep["hasFile"] = true
		}
	}
	l.mu.Unlock()
	event.EpisodeIDs = []int{12}
	got, err = s.RequestedContentRecipients(context.Background(), event, []int64{uid})
	if err != nil || len(got) != 1 {
		t.Fatalf("fresh requested episode: %v %v", got, err)
	}
	// An independently unverifiable old request must not poison this one.
	stale := seedDiscordRequest(t, s, uid, response.InstanceID, "tv", "", "", 0)
	if _, err = s.db.Exec(`UPDATE request_log SET tmdb_id=12345 WHERE id=?`, stale); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`INSERT INTO request_tv_targets(request_id,snapshot) VALUES(?,'{')`, stale); err != nil {
		t.Fatal(err)
	}
	got, err = s.RequestedContentRecipients(context.Background(), event, []int64{uid})
	if err == nil || len(got) != 1 {
		t.Fatalf("partial verified match: %v %v", got, err)
	}
	event.EpisodeIDs = nil
	got, err = s.RequestedContentRecipients(context.Background(), event, []int64{uid})
	if err != nil || len(got) != 0 {
		t.Fatalf("title-only event: %v %v", got, err)
	}
}

func TestRequestedMissingTVFollowsOnlySubmittedUnits(t *testing.T) {
	s, uid, f, _ := newMissingTVFixture(t)
	out := createMissingTV(t, s, uid)
	retryMissingTV(t, s)
	f.mu.Lock()
	f.files[1] = true
	f.files[5] = true
	f.count = 7
	f.mu.Unlock()
	event := contentnotify.Import{MediaType: "tv", InstanceID: out.InstanceID, TmdbID: 200}
	for _, tc := range []struct {
		id   int
		want bool
	}{{101, false}, {105, true}, {107, false}} {
		event.EpisodeIDs = []int{tc.id}
		got, err := s.RequestedContentRecipients(context.Background(), event, []int64{uid})
		if err != nil || (len(got) > 0) != tc.want {
			t.Fatalf("episode %d: %v %v", tc.id, got, err)
		}
	}
	followMissingTV(t, s)
	if _, err := s.StopTVFollow(uid, out.RequestID); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.files[7] = true
	f.count = 8
	f.files[8] = true
	f.mu.Unlock()
	for _, tc := range []struct {
		id   int
		want bool
	}{{107, true}, {108, false}} {
		event.EpisodeIDs = []int{tc.id}
		got, err := s.RequestedContentRecipients(context.Background(), event, []int64{uid})
		if err != nil || (len(got) > 0) != tc.want {
			t.Fatalf("stopped episode %d: %v %v", tc.id, got, err)
		}
	}
}
