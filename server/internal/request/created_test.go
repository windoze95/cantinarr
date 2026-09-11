package request

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/discordnotify"
	"github.com/windoze95/cantinarr-server/internal/secrets"
)

type creation struct {
	id       int64
	approval bool
}
type creationRecorder struct {
	mu     sync.Mutex
	events []creation
}

func (r *creationRecorder) RequestCreated(id int64, approval bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, creation{id, approval})
}

func TestCreationPendingAndDecisions(t *testing.T) {
	for _, kind := range []string{"movie", "tv"} {
		t.Run(kind, func(t *testing.T) {
			s, uid := newHistoryTestService(t, "", "", "")
			requireApproval(t, s)
			rec := &creationRecorder{}
			s.SetCreationObserver(rec)
			for i := 0; i < 2; i++ {
				if _, err := s.CreateMediaRequest(uid, &CreateRequest{TmdbID: 550, MediaType: kind, Title: "A title"}); err != nil {
					t.Fatal(err)
				}
			}
			if len(rec.events) != 1 || !rec.events[0].approval {
				t.Fatalf("creation: %+v", rec.events)
			}
			admin := createTestAdmin(t, s)
			if err := s.DenyRequest(admin, rec.events[0].id, "later"); err != nil {
				t.Fatal(err)
			}
			if len(rec.events) != 1 {
				t.Fatal("decision emitted new request")
			}
			if _, err := s.CreateMediaRequest(uid, &CreateRequest{TmdbID: 550, MediaType: kind, Title: "A title"}); err != nil {
				t.Fatal(err)
			}
			if len(rec.events) != 2 || rec.events[0].id == rec.events[1].id {
				t.Fatal("new request after denial was suppressed")
			}
		})
	}
}

func TestAutomaticMovieCreationDoesNotRepeatForExistingWork(t *testing.T) {
	// Existing monitored media still counts as this user's first submission.
	f := &fakeRadarr{libraryJSON: `[{"id":42,"tmdbId":550,"title":"Canonical title","monitored":true,"hasFile":false}]`}
	upstream := newFakeRadarrServer(t, f)
	s, uid := newHistoryTestService(t, upstream.URL, "", "")
	rec := &creationRecorder{}
	s.SetCreationObserver(rec)
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.CreateMediaRequest(uid, &CreateRequest{TmdbID: 550, MediaType: "movie", Title: "typed title"}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if len(rec.events) != 1 || rec.events[0].approval {
		t.Fatalf("automatic duplicate: %+v", rec.events)
	}
	var title string
	s.db.QueryRow(`SELECT title FROM request_log WHERE id=?`, rec.events[0].id).Scan(&title)
	if title != "Canonical title" {
		t.Fatal("alert lost canonical title")
	}
	other := createTestAdmin(t, s)
	if _, err := s.CreateMediaRequest(other, &CreateRequest{TmdbID: 550, MediaType: "movie"}); err != nil {
		t.Fatal(err)
	}
	if len(rec.events) != 2 {
		t.Fatal("another requester was deduped")
	}
	// Live monitoring was removed outside Cantinarr; reviving it is new work.
	f.libraryJSON = `[{"id":42,"tmdbId":550,"title":"Canonical title","monitored":false,"hasFile":false}]`
	if _, err := s.CreateMediaRequest(uid, &CreateRequest{TmdbID: 550, MediaType: "movie"}); err != nil {
		t.Fatal(err)
	}
	if len(rec.events) != 3 {
		t.Fatal("revived request was suppressed")
	}
}

func TestAutomaticNewMovieAndFailedRequest(t *testing.T) {
	f := &fakeRadarr{lookupJSON: `[{"title":"New movie","tmdbId":550,"year":1999}]`}
	srv := newFakeRadarrServer(t, f)
	s, uid := newHistoryTestService(t, srv.URL, "", "")
	rec := &creationRecorder{}
	s.SetCreationObserver(rec)
	if _, err := s.CreateMediaRequest(uid, &CreateRequest{TmdbID: 550, MediaType: "movie"}); err != nil {
		t.Fatal(err)
	}
	if len(rec.events) != 1 || rec.events[0].approval {
		t.Fatalf("creation: %+v", rec.events)
	}
	if _, err := s.CreateMediaRequest(uid, &CreateRequest{MediaType: "wrong"}); err == nil {
		t.Fatal("invalid request accepted")
	}
	if len(rec.events) != 1 {
		t.Fatal("failure emitted creation")
	}
}

func TestAutomaticTVCreationAndNewScope(t *testing.T) {
	f := &fakeSonarrTV{lookupJSON: `[{"title":"Andor","tvdbId":121361,"year":2022,"seasons":[{"seasonNumber":1},{"seasonNumber":2}]}]`}
	srv := newFakeSonarrServer(t, f)
	s, uid := newHistoryTestService(t, "", srv.URL, "")
	g := s.GetGlobalSettings()
	g.AllowSeasonChoice = true
	if err := s.SetGlobalSettings(g); err != nil {
		t.Fatal(err)
	}
	rec := &creationRecorder{}
	s.SetCreationObserver(rec)
	makeReq := func(scope string) *CreateRequest {
		return &CreateRequest{TmdbID: 1399, TvdbID: 121361, MediaType: "tv", Title: "Andor", SeasonScope: scope}
	}
	if _, err := s.CreateMediaRequest(uid, makeReq(SeasonScopeAll)); err != nil {
		t.Fatal(err)
	}
	f.libraryJSON = `[{"id":42,"tvdbId":121361,"title":"Andor","monitored":true,"seasons":[{"seasonNumber":1,"monitored":true,"statistics":{"episodeFileCount":12,"totalEpisodeCount":12,"episodeCount":12}},{"seasonNumber":2,"monitored":true,"statistics":{"episodeFileCount":12,"totalEpisodeCount":12,"episodeCount":12}}]}]`
	if _, err := s.CreateMediaRequest(uid, makeReq(SeasonScopeAll)); err != nil {
		t.Fatal(err)
	}
	if len(rec.events) != 1 || rec.events[0].approval {
		t.Fatalf("automatic TV duplicate: %+v", rec.events)
	}
	if _, err := s.CreateMediaRequest(uid, makeReq(SeasonScopeFirst)); err != nil {
		t.Fatal(err)
	}
	if len(rec.events) != 2 {
		t.Fatal("new TV scope suppressed")
	}
}

func TestCatalogCreationGroupsFormatsAndSkipsSubscriptions(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("intake reached upstream"); w.WriteHeader(503) }))
	defer upstream.Close()
	for _, approval := range []bool{false, true} {
		for _, kind := range []string{"book", "music"} {
			t.Run(kind+map[bool]string{true: "pending", false: "automatic"}[approval], func(t *testing.T) {
				var s *Service
				var uid int64
				if kind == "book" {
					s, uid = newChaptarrBookTestService(t, upstream.URL)
				} else {
					s, uid, _ = newLidarrMusicTestService(t, upstream.URL)
				}
				if approval {
					requireApproval(t, s)
				}
				rec := &creationRecorder{}
				s.SetCreationObserver(rec)
				makeReq := func() *CreateRequest {
					r := &CreateRequest{MediaType: kind, ForeignID: "native-id", Title: "Both formats"}
					if kind == "book" {
						r.BookFormat = "both"
					}
					return r
				}
				var response *CreateResponse
				for i := 0; i < 2; i++ {
					var err error
					response, err = s.CreateMediaRequest(uid, makeReq())
					if err != nil {
						t.Fatal(err)
					}
				}
				if len(rec.events) != 1 || rec.events[0].approval != approval {
					t.Fatalf("catalog creation: %+v", rec.events)
				}
				if kind == "book" {
					other := addDeliverySubscriber(t, s, response.InstanceID)
					if _, err := s.CreateMediaRequest(other, makeReq()); err != nil {
						t.Fatal(err)
					}
					if len(rec.events) != 1 {
						t.Fatal("shared subscription emitted new request")
					}
				}
				if _, err := s.DeliveryAction(context.Background(), uid, response.RequestID, "cancel", ""); err != nil {
					t.Fatal(err)
				}
				if len(rec.events) != 1 {
					t.Fatal("cancellation emitted new request")
				}
			})
		}
	}
}

func TestDiscordEnqueueFailureCannotRejectSavedRequest(t *testing.T) {
	s, uid := newHistoryTestService(t, "", "", "")
	requireApproval(t, s)
	cipher, _ := secrets.NewCipher(bytes.Repeat([]byte{42}, 32))
	discord := discordnotify.NewService(s.db, cipher, nil)
	if err := discord.Save(true, "https://discord.com/api/webhooks/123/test_token", false); err != nil {
		t.Fatal(err)
	}
	s.SetCreationObserver(discord)
	if _, err := s.db.Exec(`DROP TABLE discord_notifications`); err != nil {
		t.Fatal(err)
	}
	if out, err := s.CreateMediaRequest(uid, &CreateRequest{TmdbID: 550, MediaType: "movie", Title: "Saved"}); err != nil || !out.Success {
		t.Fatalf("enqueue failure changed request: %+v %v", out, err)
	}
	var count int
	s.db.QueryRow(`SELECT COUNT(*) FROM request_log`).Scan(&count)
	if count != 1 {
		t.Fatal("request rolled back")
	}
}
