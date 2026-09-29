package request

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/contentpolicy"
	"github.com/windoze95/cantinarr-server/internal/instance"
)

type requesterTagLab struct {
	mu                               sync.Mutex
	movies                           map[int]map[string]any
	tags                             []map[string]any
	tagFailure                       int
	tagReads, creates, updates, adds int
	onTagRead                        func()
}

func newRequesterTagLab(t *testing.T) (*requesterTagLab, string) {
	t.Helper()
	l := &requesterTagLab{movies: map[int]map[string]any{}, tags: []map[string]any{}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		l.mu.Lock()
		defer l.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		write := func(value any) { json.NewEncoder(w).Encode(value) }
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v3/movie":
			n, _ := strconv.Atoi(r.URL.Query().Get("tmdbId"))
			if movie := l.movies[n]; movie != nil {
				write([]any{movie})
			} else {
				write([]any{})
			}
		case "GET /api/v3/movie/lookup":
			n, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Query().Get("term"), "tmdb:"))
			write([]any{map[string]any{"tmdbId": n, "title": "Movie", "year": 2020}})
		case "GET /api/v3/qualityprofile":
			write([]any{map[string]any{"id": 1, "name": "Any"}})
		case "GET /api/v3/rootfolder":
			write([]any{map[string]any{"id": 1, "path": "/movies"}})
		case "GET /api/v3/queue":
			write(map[string]any{"totalRecords": 0, "records": []any{}})
		case "POST /api/v3/movie":
			var movie map[string]any
			json.NewDecoder(r.Body).Decode(&movie)
			n := int(movie["tmdbId"].(float64))
			movie["id"], movie["tags"] = n, []int{7}
			l.movies[n] = movie
			l.adds++
			write(movie)
		case "GET /api/v3/tag":
			l.tagReads++
			if l.onTagRead != nil {
				l.onTagRead()
			}
			if l.tagFailure != 0 {
				w.Header().Set("Retry-After", "900")
				w.WriteHeader(l.tagFailure)
				write(map[string]any{"error": "secret http://internal-arr/private"})
				return
			}
			write(l.tags)
		case "POST /api/v3/tag":
			var tag map[string]any
			json.NewDecoder(r.Body).Decode(&tag)
			tag["id"] = 100 + len(l.tags)
			l.tags = append(l.tags, tag)
			l.creates++
			write(tag)
		case "PUT /api/v3/movie/editor":
			var body struct {
				IDs   []int  `json:"movieIds"`
				Tags  []int  `json:"tags"`
				Apply string `json:"applyTags"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			if body.Apply != "add" || len(body.IDs) != 1 || len(body.Tags) != 1 {
				t.Errorf("unexpected editor payload: %+v", body)
				w.WriteHeader(400)
				return
			}
			m := l.movies[body.IDs[0]]
			m["tags"] = append(m["tags"].([]int), body.Tags...)
			l.updates++
			write([]any{m})
		default:
			if r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/api/v3/movie/") {
				n, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/api/v3/movie/"))
				write(l.movies[n])
				return
			}
			t.Errorf("unexpected provider request %s %s", r.Method, r.URL)
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(server.Close)
	return l, server.URL
}

func setRequesterTagging(t *testing.T, store *instance.Store, instanceID string, enabled bool) {
	t.Helper()
	i, err := store.Get(instanceID)
	if err != nil {
		t.Fatal(err)
	}
	i.TagRequests = enabled
	if err := store.Update(i); err != nil {
		t.Fatal(err)
	}
}

func tagMovieRequest(t *testing.T, s *Service, user int64, instanceID string, tmdbID int) int64 {
	t.Helper()
	if _, err := s.CreateMediaRequest(user, &CreateRequest{TmdbID: tmdbID, Title: "Movie", MediaType: "movie", InstanceID: instanceID}); err != nil {
		t.Fatal(err)
	}
	var id int64
	if err := s.db.QueryRow(`SELECT MAX(id) FROM request_log WHERE user_id=? AND tmdb_id=? AND instance_id=?`, user, tmdbID, instanceID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func assertTagState(t *testing.T, s *Service, id int64, want string) {
	t.Helper()
	var state string
	err := s.db.QueryRow(`SELECT state FROM request_tag_jobs WHERE request_id=?`, id).Scan(&state)
	if err != nil || state != want {
		t.Fatalf("request %d tag state=%s err=%v, want %s", id, state, err, want)
	}
}

func retryTagRequest(s *Service, user, id int64) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", fmt.Sprintf("/api/admin/requests/%d/tags/retry", id), nil)
	if user > 0 {
		r = r.WithContext(context.WithValue(r.Context(), auth.ClaimsKey, &auth.Claims{UserID: user, Role: "admin"}))
	}
	route := chi.NewRouteContext()
	route.URLParams.Add("requestID", strconv.FormatInt(id, 10))
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, route))
	w := httptest.NewRecorder()
	NewHandler(s).RetryRequesterTag(w, r)
	return w
}

func TestRequesterTagAdmissionNoBackfillAndMultipleRequesters(t *testing.T) {
	l, url := newRequesterTagLab(t)
	s, uid, store, primary, sibling := newTwoRadarrTestService(t, url, url)
	old := tagMovieRequest(t, s, uid, primary, 1)
	setRequesterTagging(t, store, primary, true)
	// A deduped revisit cannot retroactively make the old request eligible.
	tagMovieRequest(t, s, uid, primary, 1)
	var count int
	s.db.QueryRow(`SELECT COUNT(*) FROM request_tag_jobs WHERE request_id=?`, old).Scan(&count)
	if count != 0 {
		t.Fatal("old request gained a job")
	}
	id := tagMovieRequest(t, s, uid, primary, 2)
	s.SweepRequesterTags(context.Background())
	assertTagState(t, s, id, "applied")
	res, err := s.db.Exec(`INSERT INTO users(username,password_hash,role) VALUES ('another','','user')`)
	if err != nil {
		t.Fatal(err)
	}
	other, _ := res.LastInsertId()
	if err = store.SetUserGrants(other, map[string][]string{"radarr": {primary}}); err != nil {
		t.Fatal(err)
	}
	// Existing monitored media takes the free/no-op path but still gets the
	// new requester's tag, without another media add or replacing other tags.
	otherID := tagMovieRequest(t, s, other, primary, 2)
	s.SweepRequesterTags(context.Background())
	assertTagState(t, s, otherID, "applied")
	if !reflect.DeepEqual(l.movies[2]["tags"], []int{7, 100, 101}) || l.adds != 2 {
		t.Fatalf("native state = %+v adds=%d", l.movies, l.adds)
	}
	if _, err = s.db.Exec(`UPDATE users SET username='renamed' WHERE id=?`, uid); err != nil {
		t.Fatal(err)
	}
	renamed := tagMovieRequest(t, s, uid, primary, 3)
	s.SweepRequesterTags(context.Background())
	assertTagState(t, s, renamed, "applied")
	if l.creates != 2 {
		t.Fatal("username change made a duplicate tag")
	}
	// Destination-scoped enablement: a request to the disabled sibling has
	// no tag job even when the other instance is enabled.
	disabled := tagMovieRequest(t, s, uid, sibling, 3)
	s.db.QueryRow(`SELECT COUNT(*) FROM request_tag_jobs WHERE request_id=?`, disabled).Scan(&count)
	if count != 0 {
		t.Fatal("sibling setting leaked")
	}
}

func TestRequesterTagApprovalUsesOriginalRequester(t *testing.T) {
	l, url := newRequesterTagLab(t)
	s, uid, store, primary, _ := newTwoRadarrTestService(t, url, url)
	setRequesterTagging(t, store, primary, true)
	requireApproval(t, s)
	id := tagMovieRequest(t, s, uid, primary, 1)
	s.SweepRequesterTags(context.Background())
	assertTagState(t, s, id, "waiting")
	if l.tagReads != 0 || l.adds != 0 {
		t.Fatal("unapproved request touched tags or media")
	}
	admin := createTestAdmin(t, s)
	if _, err := s.ApproveRequest(admin, id, nil); err != nil {
		t.Fatal(err)
	}
	s.SweepRequesterTags(context.Background())
	assertTagState(t, s, id, "applied")
	if l.tags[0]["label"] != fmt.Sprintf("cantinarr-%d-requester", uid) {
		t.Fatalf("tag = %v", l.tags)
	}
	page, err := s.adminHistory(context.Background(), adminHistoryFilter{Limit: 50})
	if err != nil || page.Requests[0].RequesterTagging.Status != "applied" || page.Requests[0].Decision != "approved" {
		t.Fatalf("history=%+v err=%v", page, err)
	}
}

func TestRequesterTagFailureRetryAndLeaseRecoveryDoNotResubmitMedia(t *testing.T) {
	l, url := newRequesterTagLab(t)
	s, uid, store, primary, _ := newTwoRadarrTestService(t, url, url)
	setRequesterTagging(t, store, primary, true)
	id := tagMovieRequest(t, s, uid, primary, 1)
	l.tagFailure = 429
	s.SweepRequesterTags(context.Background())
	assertTagState(t, s, id, "retrying")
	var status, message string
	var next int64
	s.db.QueryRow(`SELECT r.status,j.message,j.next_attempt_at FROM request_log r JOIN request_tag_jobs j ON j.request_id=r.id WHERE r.id=?`, id).Scan(&status, &message, &next)
	if status != StatusRequested || strings.Contains(message, "secret") || strings.Contains(message, "internal-arr") || next < time.Now().Add(899*time.Second).Unix() {
		t.Fatalf("status=%s message=%s next=%d", status, message, next)
	}
	s.SweepRequesterTags(context.Background())
	if l.tagReads != 1 {
		t.Fatal("retry ignored backoff")
	}
	admin := createTestAdmin(t, s)
	for _, user := range []int64{0, uid} {
		if w := retryTagRequest(s, user, id); w.Code != 401 && w.Code != 403 {
			t.Fatalf("user %d retry=%d", user, w.Code)
		}
	}
	page, err := s.adminHistory(context.Background(), adminHistoryFilter{Limit: 50})
	if err != nil || !page.Requests[0].RequesterTagging.CanRetry {
		t.Fatalf("history retry = %+v %v", page, err)
	}
	if w := retryTagRequest(s, admin, id); w.Code != 202 {
		t.Fatalf("retry=%d %s", w.Code, w.Body.String())
	}
	assertTagState(t, s, id, "pending")
	// Simulate a dead worker with an expired durable lease; a new Service
	// must recover solely from SQLite, without the old process's wakeups.
	s.db.Exec(`UPDATE request_tag_jobs SET state='processing',lease_token='dead',lease_until=1 WHERE request_id=?`, id)
	s.db.Exec(`INSERT OR REPLACE INTO request_dispatch_locks VALUES (?,'dead',1)`, primary)
	l.tagFailure = 0
	restarted := NewService(s.db, instance.NewRegistry(store), nil, nil)
	restarted.SweepRequesterTags(context.Background())
	assertTagState(t, s, id, "applied")
	if l.adds != 1 || l.creates != 1 || l.updates != 1 {
		t.Fatalf("adds=%d creates=%d updates=%d", l.adds, l.creates, l.updates)
	}
	if w := retryTagRequest(s, admin, id); w.Code != 409 {
		t.Fatalf("applied retry=%d", w.Code)
	}
	if w := retryTagRequest(s, admin, 9999); w.Code != 409 {
		t.Fatalf("historical retry=%d", w.Code)
	}
}

func TestRequesterTagDisableCancelsInFlightAndCannotRevive(t *testing.T) {
	l, url := newRequesterTagLab(t)
	s, uid, store, primary, _ := newTwoRadarrTestService(t, url, url)
	setRequesterTagging(t, store, primary, true)
	applied := tagMovieRequest(t, s, uid, primary, 1)
	s.SweepRequesterTags(context.Background())
	id := tagMovieRequest(t, s, uid, primary, 2)
	l.onTagRead = func() {
		setRequesterTagging(t, store, primary, false)
		setRequesterTagging(t, store, primary, true)
	}
	s.SweepRequesterTags(context.Background())
	assertTagState(t, s, id, "cancelled")
	assertTagState(t, s, applied, "applied")
	l.onTagRead = nil
	s.SweepRequesterTags(context.Background())
	if l.updates != 1 {
		t.Fatal("disabled in-flight job wrote or revived")
	}
	future := tagMovieRequest(t, s, uid, primary, 3)
	s.SweepRequesterTags(context.Background())
	assertTagState(t, s, future, "applied")
}

func TestRequesterTagRechecksAccessAfterProviderReads(t *testing.T) {
	l, url := newRequesterTagLab(t)
	s, uid, store, primary, sibling := newTwoRadarrTestService(t, url, url)
	setRequesterTagging(t, store, sibling, true)
	id := tagMovieRequest(t, s, uid, sibling, 1)
	l.onTagRead = func() {
		if err := store.SetUserGrants(uid, map[string][]string{"radarr": {primary}}); err != nil {
			t.Error(err)
		}
	}
	s.SweepRequesterTags(context.Background())
	assertTagState(t, s, id, "failed")
	if l.creates != 0 || l.updates != 0 {
		t.Fatal("revoked grant allowed tagging")
	}
}

func TestRequesterTagConcurrentWorkersAndRetryExhaustion(t *testing.T) {
	l, url := newRequesterTagLab(t)
	s, uid, store, primary, _ := newTwoRadarrTestService(t, url, url)
	setRequesterTagging(t, store, primary, true)
	id := tagMovieRequest(t, s, uid, primary, 1)
	other := NewService(s.db, instance.NewRegistry(store), nil, nil)
	var wg sync.WaitGroup
	for _, worker := range []*Service{s, other} {
		wg.Go(func() { worker.SweepRequesterTags(context.Background()) })
	}
	wg.Wait()
	assertTagState(t, s, id, "applied")
	if l.creates != 1 || l.updates != 1 {
		t.Fatal("workers duplicated writes")
	}
	failed := tagMovieRequest(t, s, uid, primary, 2)
	s.db.Exec(`UPDATE request_tag_jobs SET attempts=49 WHERE request_id=?`, failed)
	l.tagFailure = 503
	s.SweepRequesterTags(context.Background())
	assertTagState(t, s, failed, "failed")
	var message string
	s.db.QueryRow(`SELECT message FROM request_tag_jobs WHERE request_id=?`, failed).Scan(&message)
	if !strings.Contains(message, "Automatic retries exhausted") {
		t.Fatal(message)
	}
}

func TestRequesterTagLabel(t *testing.T) {
	for _, tc := range []struct{ name, want string }{{"John.Doe @ Home", "john-doe-home"}, {"✨李", "user"}, {"---A__B---", "a-b"}} {
		prefix, got := requesterTagLabel(42, tc.name)
		if prefix != "cantinarr-42-" || got != prefix+tc.want {
			t.Fatalf("label %q = %s", tc.name, got)
		}
	}
}

func TestRequesterTagTVUsesSavedSeriesAndKeepsSeasonScope(t *testing.T) {
	s, uid, _, lab := newCorrectionLab(t)
	if _, err := s.db.Exec(`UPDATE service_instances SET tag_requests=1 WHERE service_type='sonarr'`); err != nil {
		t.Fatal(err)
	}
	resp, err := s.CreateMediaRequest(uid, tvRequest(225634, SeasonScopeAll))
	if err != nil {
		t.Fatal(err)
	}
	lab.parent["tags"] = []int{7}
	s.SweepRequesterTags(context.Background())
	assertTagState(t, s, resp.RequestID, "applied")
	assertOnlySeasons(t, lab, 2)
	if !reflect.DeepEqual(lab.parent["tags"], []int{7, 100}) || lab.tagEdits != 1 || len(lab.adds) != 1 {
		t.Fatalf("tagging changed native scope: %v", lab.parent)
	}
	target, _, _, err := s.loadTVTarget(resp.RequestID)
	if err != nil || !reflect.DeepEqual(target.SourceSeasons, []int{1}) || !reflect.DeepEqual(target.TargetSeasons, []int{2}) {
		t.Fatalf("scope=%+v err=%v", target, err)
	}
	// The free/already monitored TV path also captures eligibility.
	free, err := s.CreateMediaRequest(uid, tvRequest(225634, SeasonScopeAll))
	if err != nil {
		t.Fatal(err)
	}
	s.SweepRequesterTags(context.Background())
	assertTagState(t, s, free.RequestID, "applied")
	if lab.tagEdits != 1 {
		t.Fatal("already applied TV tag was written again")
	}
}

func TestRequesterTagTVMatchAndContentPolicyChangesFailClosed(t *testing.T) {
	for _, change := range []string{"match", "policy", "metadata"} {
		t.Run(change, func(t *testing.T) {
			s, uid, admin, lab := newCorrectionLab(t)
			s.db.Exec(`UPDATE service_instances SET tag_requests=1 WHERE service_type='sonarr'`)
			resp, err := s.CreateMediaRequest(uid, tvRequest(225634, SeasonScopeAll))
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "match":
				_, err = s.SaveTVMatch(admin, 225634, TVMatchEdit{Revision: resp.Match.Revision, Mode: "custom", TVDBID: 389492, SeasonMap: map[int]int{1: 3}, InstanceID: resp.InstanceID})
				if err != nil {
					t.Fatal(err)
				}
			case "policy":
				policies := contentpolicy.New(s.db, func() contentpolicy.RawGetter { return &ratingsTMDB{} }, nil)
				s.SetContentPolicy(policies)
				if err = policies.Store.Set(uid, contentpolicy.Policy{MaxMovieRating: "PG", MaxTVRating: "TV-PG", RatingRegion: "US", BlockUnrated: true}); err != nil {
					t.Fatal(err)
				}
			case "metadata":
				lab.metadataDown = true
			}
			s.SweepRequesterTags(context.Background())
			want := "failed"
			if change == "metadata" {
				want = "retrying"
			}
			assertTagState(t, s, resp.RequestID, want)
			if lab.tagEdits != 0 || len(lab.requesterTags) != 0 {
				t.Fatal("unsafe TV tag mutation")
			}
		})
	}
}
