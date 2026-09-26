package request

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/contentpolicy"
	"github.com/windoze95/cantinarr-server/internal/discordnotify"
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
	// A pause is an administrator's decision, not a library outage.
	if _, err = s.DiscordAvailability(context.Background(), response.RequestID); !errors.Is(err, discordnotify.ErrUnverifiable) {
		t.Fatalf("paused mapping: %v", err)
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

// deliveredSeasonOne requests season 1 of an uncorrected show while TMDB lists
// only that season, then imports both of its episodes.
func deliveredSeasonOne(t *testing.T) (*Service, int64, *CreateResponse, *correctionLab) {
	t.Helper()
	s, uid, _, l := newCorrectionLab(t)
	l.lookupTVDB = 999999
	req := tvRequest(12345, "")
	req.Seasons = []int{1}
	response, err := s.CreateMediaRequest(uid, req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`UPDATE request_dispatch SET next_attempt_at=0 WHERE request_id=?`, response.RequestID); err != nil {
		t.Fatal(err)
	}
	s.SweepDispatch(context.Background())
	l.mu.Lock()
	for _, ep := range l.episodes {
		if ep["seasonNumber"] == 1 {
			ep["hasFile"] = true
		}
	}
	l.mu.Unlock()
	s.InvalidateAvailabilityDigests(response.InstanceID)
	return s, uid, response, l
}

func TestDiscordTVSurvivesANewlyListedSeason(t *testing.T) {
	s, _, response, l := deliveredSeasonOne(t)
	l.mu.Lock()
	l.extraSource = true // TMDB announces season 2 after the request
	l.mu.Unlock()
	view, err := s.DiscordAvailability(context.Background(), response.RequestID)
	if err != nil || len(view.Units) != 2 || view.Units[0].Label != "S01E01" {
		t.Fatalf("new season stopped availability: %+v %v", view, err)
	}
	// A different series identity is still a stale match.
	if _, err = s.db.Exec(`UPDATE tmdb_tvdb_cache SET tvdb_id=888888 WHERE tmdb_id=12345`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DiscordAvailability(context.Background(), response.RequestID); !errors.Is(err, ErrTVMatchStale) {
		t.Fatalf("changed series: %v", err)
	}
}

func TestDiscordTVOwnershipWithoutSonarrTMDBIDs(t *testing.T) {
	s, _, response, l := deliveredSeasonOne(t)
	// Sonarr v3 and v4 before 4.0.6 report no series TMDB id.
	l.mu.Lock()
	l.parent["tmdbId"] = 0
	l.mu.Unlock()
	s.InvalidateAvailabilityDigests(response.InstanceID)
	if view, err := s.DiscordAvailability(context.Background(), response.RequestID); err != nil || len(view.Units) != 2 {
		t.Fatalf("missing Sonarr TMDB id: %+v %v", view, err)
	}
	// A different id is a split show that needs a TV match.
	l.mu.Lock()
	l.parent["tmdbId"] = 424242
	l.mu.Unlock()
	s.InvalidateAvailabilityDigests(response.InstanceID)
	if _, err := s.DiscordAvailability(context.Background(), response.RequestID); !errors.Is(err, ErrTVMatchStale) {
		t.Fatalf("other title: %v", err)
	}
}

func TestDiscordLegacyTVSelectionsAreUnverifiable(t *testing.T) {
	s, uid, response, _ := deliveredSeasonOne(t)
	for _, scope := range []string{SeasonScopeAll, "[0,1]"} {
		id := seedDiscordRequest(t, s, uid, response.InstanceID, "tv", "", "", 0)
		if _, err := s.db.Exec(`UPDATE request_log SET tmdb_id=12345,season_scope=? WHERE id=?`, scope, id); err != nil {
			t.Fatal(err)
		}
		if _, err := s.DiscordAvailability(context.Background(), id); !errors.Is(err, discordnotify.ErrUnverifiable) {
			t.Fatalf("%s: %v", scope, err)
		}
	}
	// An explicit legacy selection of real seasons can still be proved.
	id := seedDiscordRequest(t, s, uid, response.InstanceID, "tv", "", "", 0)
	if _, err := s.db.Exec(`UPDATE request_log SET tmdb_id=12345,season_scope='[1]' WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	if view, err := s.DiscordAvailability(context.Background(), id); err != nil || len(view.Units) != 2 {
		t.Fatalf("legacy explicit: %+v %v", view, err)
	}
}

func TestDiscordTVRepairBaselinesAtDelivery(t *testing.T) {
	s, uid, response, _ := deliveredSeasonOne(t)
	original := seedDiscordRequest(t, s, uid, response.InstanceID, "tv", "", "", 0)
	if _, err := s.db.Exec(`UPDATE request_tv_targets SET repair_of=? WHERE request_id=?`, original, response.RequestID); err != nil {
		t.Fatal(err)
	}
	view, err := s.DiscordAvailability(context.Background(), response.RequestID)
	if err != nil || !view.Baseline || len(view.Units) != 2 {
		t.Fatalf("delivered repair: %+v %v", view, err)
	}
	// Before delivery an empty read must not become the repair's baseline.
	if _, err = s.db.Exec(`UPDATE request_dispatch SET state='approval' WHERE request_id=?`, response.RequestID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DiscordAvailability(context.Background(), response.RequestID); !errors.Is(err, discordnotify.ErrUnverifiable) {
		t.Fatalf("undelivered repair: %v", err)
	}
}

func TestDiscordObservationKeyFollowsSelectedSeasonFiles(t *testing.T) {
	s, uid, response, l := deliveredSeasonOne(t)
	ctx := context.Background()
	stats := func(files int, size int64) {
		t.Helper()
		l.mu.Lock()
		for _, v := range l.parent["seasons"].([]any) {
			season := v.(map[string]any)
			season["statistics"] = map[string]any{"episodeFileCount": files, "totalEpisodeCount": 2, "sizeOnDisk": size}
		}
		l.mu.Unlock()
		s.InvalidateAvailabilityDigests(response.InstanceID)
	}
	key := func() string {
		t.Helper()
		k, err := s.DiscordObservationKey(ctx, response.RequestID)
		if err != nil {
			t.Fatal(err)
		}
		return k
	}
	stats(2, 100)
	first := key()
	if first == "" || key() != first {
		t.Fatalf("unstable key %q", first)
	}
	stats(2, 150) // a replaced file keeps the count but not the size
	second := key()
	if second == first {
		t.Fatal("a changed file set kept its key")
	}
	stats(1, 150)
	if key() == second {
		t.Fatal("a deleted file kept its key")
	}
	// Missing season statistics are not a stable reading.
	l.mu.Lock()
	for _, v := range l.parent["seasons"].([]any) {
		delete(v.(map[string]any), "statistics")
	}
	l.mu.Unlock()
	s.InvalidateAvailabilityDigests(response.InstanceID)
	if k := key(); k != "" {
		t.Fatalf("missing statistics produced key %q", k)
	}
	movie := seedDiscordRequest(t, s, uid, response.InstanceID, "movie", "", "", 0)
	if k, err := s.DiscordObservationKey(ctx, movie); err != nil || k != "" {
		t.Fatalf("movie key %q %v", k, err)
	}
}
