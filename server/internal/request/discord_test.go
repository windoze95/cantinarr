package request

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/contentpolicy"
)

func seedDiscordRequest(t *testing.T, s *Service, userID int64, instanceID, mediaType, foreignID, format string, recordID int) int64 {
	t.Helper()
	result, err := s.db.Exec(`INSERT INTO request_log(user_id,instance_id,tmdb_id,media_type,title,foreign_id,book_format,book_record_id,status) VALUES(?,?,550,?,'Requested title',?,?,?,'requested')`, userID, instanceID, mediaType, foreignID, format, recordID)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := result.LastInsertId()
	return id
}

func TestDiscordMovieAvailabilityRequiresOwningLibraryAndCurrentGrant(t *testing.T) {
	var available, offline atomic.Bool
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if offline.Load() {
			w.WriteHeader(503)
			return
		}
		fmt.Fprintf(w, `[{"id":7,"tmdbId":550,"hasFile":%t}]`, available.Load())
	}))
	defer primary.Close()
	sibling := newFakeRadarrServer(t, &fakeRadarr{libraryJSON: `[{"id":8,"tmdbId":550,"hasFile":true}]`})
	s, uid, store, primaryID, siblingID := newTwoRadarrTestService(t, primary.URL, sibling.URL)
	id := seedDiscordRequest(t, s, uid, primaryID, "movie", "", "", 0)
	view, err := s.DiscordAvailability(context.Background(), id)
	if err != nil || len(view.Units) != 0 {
		t.Fatalf("sibling satisfied primary: %+v %v", view, err)
	}
	available.Store(true)
	s.InvalidateAvailabilityDigests(primaryID)
	view, err = s.DiscordAvailability(context.Background(), id)
	if err != nil || len(view.Units) != 1 || view.Subject.InstanceID != primaryID {
		t.Fatalf("import: %+v %v", view, err)
	}
	if ok, err := s.DiscordAuthorize(context.Background(), uid, view.Subject); err != nil || !ok {
		t.Fatalf("grant: %v %v", ok, err)
	}
	if err = store.SetUserGrants(uid, map[string][]string{"radarr": {siblingID}}); err != nil {
		t.Fatal(err)
	}
	if err = store.SetUserDefault(uid, "radarr", siblingID); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.DiscordAuthorize(context.Background(), uid, view.Subject); err != nil || ok {
		t.Fatalf("revoked grant: %v %v", ok, err)
	}
	offline.Store(true)
	s.InvalidateAvailabilityDigests(primaryID)
	if _, err = s.DiscordAvailability(context.Background(), id); err == nil {
		t.Fatal("outage was read as absence")
	}
}

func TestDiscordBookFormatsFollowBoundRecordAfterRekey(t *testing.T) {
	var audio atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/book" {
			count := 0
			if audio.Load() {
				count = 1
			}
			fmt.Fprintf(w, `[{"id":7,"foreignBookId":"canonical","mediaType":"ebook","statistics":{"bookFileCount":1}},{"id":8,"foreignBookId":"canonical","mediaType":"audiobook","statistics":{"bookFileCount":%d}}]`, count)
			return
		}
		if r.URL.Path == "/api/v1/queue" {
			fmt.Fprint(w, `{"records":[],"totalRecords":0}`)
			return
		}
		t.Errorf("bound record triggered an unrelated lookup: %s", r.URL.Path)
		w.WriteHeader(503)
	}))
	defer upstream.Close()
	s, uid := newChaptarrBookTestService(t, upstream.URL)
	var instanceID string
	if err := s.db.QueryRow(`SELECT id FROM service_instances`).Scan(&instanceID); err != nil {
		t.Fatal(err)
	}
	id := seedDiscordRequest(t, s, uid, instanceID, "book", "old-lookup", "both", 0)
	if _, err := s.db.Exec(`INSERT INTO request_dispatch(request_id,format,state,book_record_id) VALUES(?,'ebook','complete',7),(?,'audiobook','complete',8)`, id, id); err != nil {
		t.Fatal(err)
	}
	view, err := s.DiscordAvailability(context.Background(), id)
	if err != nil || len(view.Units) != 1 || view.Units[0].Format != "ebook" || view.Subject.ForeignID != "canonical" {
		t.Fatalf("ebook: %+v %v", view, err)
	}
	audio.Store(true)
	s.InvalidateBookDigests(instanceID)
	view, err = s.DiscordAvailability(context.Background(), id)
	if err != nil || len(view.Units) != 2 {
		t.Fatalf("both: %+v %v", view, err)
	}
	if _, err = s.db.Exec(`UPDATE request_dispatch SET state='approval' WHERE request_id=? AND format='audiobook'`, id); err != nil {
		t.Fatal(err)
	}
	view, err = s.DiscordAvailability(context.Background(), id)
	if err != nil || len(view.Units) != 1 {
		t.Fatalf("unapproved format leaked: %+v %v", view, err)
	}
}

func TestDiscordAlbumRequiresVerifiedCompleteAlbum(t *testing.T) {
	for _, tc := range []struct {
		files, total int
		want         bool
	}{{0, 10, false}, {3, 10, false}, {10, 10, true}, {1, 0, false}} {
		t.Run(fmt.Sprintf("%d-of-%d", tc.files, tc.total), func(t *testing.T) {
			fake := &fakeLidarr{albums: fmt.Sprintf(`[{"id":7,"foreignAlbumId":"rekeyed","statistics":{"trackFileCount":%d,"trackCount":%d}}]`, tc.files, tc.total)}
			upstream := httptest.NewServer(fake.handler(t))
			defer upstream.Close()
			s, uid, instanceID := newLidarrMusicTestService(t, upstream.URL)
			id := seedDiscordRequest(t, s, uid, instanceID, "music", "old-id", "", 7)
			view, err := s.DiscordAvailability(context.Background(), id)
			if err != nil || (len(view.Units) > 0) != tc.want {
				t.Fatalf("album: %+v %v", view, err)
			}
		})
	}
}

func TestDiscordTVUsesCorrectedStoryPilotAndCurrentContentPolicy(t *testing.T) {
	s, uid, admin, l := newCorrectionLab(t)
	response, err := s.CreateMediaRequest(uid, tvRequest(286801, SeasonScopePilot))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`UPDATE request_dispatch SET next_attempt_at=0 WHERE request_id=?`, response.RequestID); err != nil {
		t.Fatal(err)
	}
	s.SweepDispatch(context.Background())
	l.mu.Lock()
	for _, ep := range l.episodes {
		if ep["seasonNumber"] != 3 {
			ep["hasFile"] = true
		}
	}
	l.mu.Unlock()
	view, err := s.DiscordAvailability(context.Background(), response.RequestID)
	if err != nil || len(view.Units) != 0 {
		t.Fatalf("another story leaked: %+v %v", view, err)
	}
	l.mu.Lock()
	for _, ep := range l.episodes {
		ep["hasFile"] = true
	}
	l.mu.Unlock()
	s.InvalidateAvailabilityDigests(response.InstanceID)
	view, err = s.DiscordAvailability(context.Background(), response.RequestID)
	if err != nil || len(view.Units) != 1 || view.Units[0].Label != "S01E01" {
		t.Fatalf("pilot: %+v %v", view, err)
	}
	policies := contentpolicy.New(s.db, func() contentpolicy.RawGetter { return &ratingsTMDB{} }, nil)
	s.SetContentPolicy(policies)
	if err = policies.Store.Set(uid, contentpolicy.Policy{MaxMovieRating: "PG", MaxTVRating: "TV-PG", RatingRegion: "US", BlockUnrated: true}); err != nil {
		t.Fatal(err)
	}
	if allowed, err := s.DiscordAuthorize(context.Background(), uid, view.Subject); err != nil || allowed {
		t.Fatalf("kids gate: %v %v", allowed, err)
	}
	match, err := s.TVMatchDetail(admin, 286801, response.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveTVMatch(admin, 286801, TVMatchEdit{Revision: match.Match.Revision, Mode: "paused", InstanceID: response.InstanceID}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DiscordAvailability(context.Background(), response.RequestID); err == nil {
		t.Fatal("paused mapping was announced")
	}
}

func TestDiscordTVAnnouncesOnlySelectedSeasonOfMultiSeasonShow(t *testing.T) {
	s, uid, _, l := newCorrectionLab(t)
	l.extraSource = true
	l.lookupTVDB = 999999
	req := tvRequest(12345, "")
	req.Seasons = []int{2}
	response, err := s.CreateMediaRequest(uid, req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`UPDATE request_dispatch SET next_attempt_at=0 WHERE request_id=?`, response.RequestID); err != nil {
		t.Fatal(err)
	}
	s.SweepDispatch(context.Background())
	l.mu.Lock()
	l.parent["tmdbId"] = req.TmdbID
	for _, ep := range l.episodes {
		ep["hasFile"] = true
	}
	l.mu.Unlock()
	s.InvalidateAvailabilityDigests(response.InstanceID)
	view, err := s.DiscordAvailability(context.Background(), response.RequestID)
	if err != nil || len(view.Units) != 2 {
		t.Fatalf("selected season: %+v %v", view, err)
	}
	for i, label := range []string{"S02E01", "S02E02"} {
		if view.Units[i].Label != label {
			t.Fatalf("unrequested episode: %+v", view.Units)
		}
	}
}
