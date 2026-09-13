package request

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/requestquota"
)

func quotaLimit(t *testing.T, s *Service, admin, user int64, media, format string, count int) {
	t.Helper()
	if err := s.Quotas.Save(admin, user, []requestquota.Rule{{Key: requestquota.Key{MediaType: media, BookFormat: format}, Count: &count, WindowDays: 7}}); err != nil {
		t.Fatal(err)
	}
}
func quotaUsed(t *testing.T, s *Service, uid int64, media, format string) int {
	t.Helper()
	v, err := s.Quotas.Read(s.db, uid)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range v.Allowances {
		if a.MediaType == media && a.BookFormat == format {
			return a.Used
		}
	}
	t.Fatal("missing allowance")
	return -1
}
func quotaMustExceed(t *testing.T, err error) *requestquota.Exceeded {
	t.Helper()
	var e *requestquota.Exceeded
	if !errors.As(err, &e) {
		t.Fatalf("expected structured refusal, got %v", err)
	}
	return e
}
func quotaRows(t *testing.T, s *Service, table string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
func quotaHTTP(h http.HandlerFunc, uid int64, method, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "/api/requests", strings.NewReader(body))
	r = r.WithContext(context.WithValue(r.Context(), auth.ClaimsKey, &auth.Claims{UserID: uid, Role: "user"}))
	w := httptest.NewRecorder()
	h(w, r)
	return w
}

func TestQuotaRESTPreviewRefusalAndDuplicateAcceptance(t *testing.T) {
	s, uid := newHistoryTestService(t, "", "", "")
	requireApproval(t, s)
	admin := createTestAdmin(t, s)
	quotaLimit(t, s, admin, 0, "movie", "", 1)
	recorder := &recordingNotifier{}
	s.notifier = recorder
	h := NewHandler(s)
	body := `{"tmdb_id":550,"media_type":"movie","title":"Selected movie"}`
	preview := quotaHTTP(h.Preview, uid, "POST", body)
	if preview.Code != 200 || !strings.Contains(preview.Body.String(), `"requested_units":1`) {
		t.Fatal(preview.Body.String())
	}
	if quotaRows(t, s, "request_log") != 0 || quotaRows(t, s, "request_quota_items") != 0 {
		t.Fatal("preview reserved work")
	}
	first := quotaHTTP(h.Create, uid, "POST", body)
	if first.Code != 200 {
		t.Fatalf("%d %s", first.Code, first.Body.String())
	}
	duplicate := quotaHTTP(h.Create, uid, "POST", body)
	if duplicate.Code != 200 {
		t.Fatal(duplicate.Body.String())
	}
	before := len(recorder.quotaEvents) + len(recorder.adminEvents) + len(recorder.userEvents)
	refused := quotaHTTP(h.Create, uid, "POST", `{"tmdb_id":551,"media_type":"movie","title":"Other"}`)
	var failure requestquota.Exceeded
	if refused.Code != 429 || json.Unmarshal(refused.Body.Bytes(), &failure) != nil || failure.Code != "request_quota_exceeded" || failure.EarliestFitsAt == nil {
		t.Fatalf("%d %s", refused.Code, refused.Body.String())
	}
	if quotaRows(t, s, "request_log") != 1 || quotaRows(t, s, "request_dispatch") != 1 || quotaRows(t, s, "request_quota_charges") != 1 {
		t.Fatal("duplicate or refusal saved extra work")
	}
	if before != len(recorder.quotaEvents)+len(recorder.adminEvents)+len(recorder.userEvents) {
		t.Fatal("refusal notified creation/change")
	}
	// Current SQL authority overrides stale token claims and old clients still obey the limit.
	s.db.Exec(`UPDATE users SET role='admin' WHERE id=?`, uid)
	if w := quotaHTTP(h.Create, uid, "POST", `{"tmdb_id":552,"media_type":"movie","title":"Admin"}`); w.Code == 429 {
		t.Fatal("admin not exempt")
	}
}

func TestQuotaTVExplicitScopesOverlapAndAtomicApprovalEdit(t *testing.T) {
	f := &fakeSonarrTV{lookupJSON: `[{"tvdbId":81189,"title":"Series","seasons":[{"seasonNumber":0},{"seasonNumber":1},{"seasonNumber":2},{"seasonNumber":3}]}]`}
	upstream := newFakeSonarrServer(t, f)
	s, uid := newHistoryTestService(t, "", upstream.URL, "")
	installTVFixture(t, s, f, 1396)
	requireApproval(t, s)
	admin := createTestAdmin(t, s)
	quotaLimit(t, s, admin, 0, "tv", "", 2)
	for scope, want := range map[string][]int{SeasonScopeAll: {1, 2, 3}, SeasonScopeFirst: {1}, SeasonScopeLatest: {3}, SeasonScopePilot: {1}} {
		p, e := s.PreviewRequest(uid, &CreateRequest{MediaType: "tv", TmdbID: 1396, Title: "Series", SeasonScope: scope})
		if e != nil {
			t.Fatal(e)
		}
		if !reflect.DeepEqual(p.Seasons, want) || p.Allowances[1].RequestedUnits != len(want) || p.Fits != (len(want) <= 2) {
			t.Fatalf("%s: %+v", scope, p)
		}
	}
	request := func(seasons ...int) (*CreateResponse, error) {
		return s.CreateMediaRequest(uid, &CreateRequest{MediaType: "tv", TmdbID: 1396, Title: "Series", Seasons: seasons})
	}
	first, e := request(1, 1)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = request(1); e != nil {
		t.Fatal(e)
	}
	overlap, e := request(2, 1, 2)
	if e != nil {
		t.Fatal(e)
	}
	if quotaUsed(t, s, uid, "tv", "") != 2 {
		t.Fatal("overlapping seasons charged twice")
	}
	_, e = request(3)
	quotaMustExceed(t, e)
	_, e = s.ApproveRequest(admin, first.RequestID, &DecisionOverride{Seasons: []int{1, 3}})
	quotaMustExceed(t, e)
	target, _, _, e := s.loadTVTarget(first.RequestID)
	if e != nil || !reflect.DeepEqual(target.SourceSeasons, []int{1}) {
		t.Fatal("refused approval changed scope")
	}
	var gate string
	s.db.QueryRow(`SELECT state FROM request_dispatch WHERE request_id=?`, first.RequestID).Scan(&gate)
	if gate != "approval" || f.addBody != nil {
		t.Fatal("refused approval started delivery")
	}
	if e = s.DenyRequest(admin, overlap.RequestID, "Reduce scope"); e != nil {
		t.Fatal(e)
	}
	if quotaUsed(t, s, uid, "tv", "") != 1 {
		t.Fatal("overlap refund removed still accepted season")
	}
	if _, e = s.ApproveRequest(admin, first.RequestID, &DecisionOverride{Seasons: []int{1, 3}}); e != nil {
		t.Fatal(e)
	}
	if quotaUsed(t, s, uid, "tv", "") != 2 || quotaUsed(t, s, admin, "tv", "") != 0 {
		t.Fatal("approval additions not charged to original requester")
	}
}

func TestQuotaBundledPilotExpansionWithinAndOutsideWindow(t *testing.T) {
	for _, age := range []time.Duration{0, 7 * 24 * time.Hour} {
		t.Run(age.String(), func(t *testing.T) {
			s, uid, admin, l := newCorrectionLab(t)
			quotaLimit(t, s, admin, 0, "tv", "", 1)
			now := time.Now()
			s.Quotas.Now = func() time.Time { return now }
			out, e := s.CreateMediaRequest(uid, tvRequest(225634, SeasonScopePilot))
			if e != nil {
				t.Fatal(e)
			}
			if out.Status != StatusRequested || quotaUsed(t, s, uid, "tv", "") != 1 {
				t.Fatalf("pilot: %+v", out)
			}
			now = now.Add(age)
			p, e := s.PreviewRequest(uid, tvRequest(225634, SeasonScopeAll))
			if e != nil {
				t.Fatal(e)
			}
			want := 0
			if age > 0 {
				want = 1
			}
			if p.Allowances[1].RequestedUnits != want || !reflect.DeepEqual(p.Seasons, []int{1}) {
				t.Fatalf("expansion price: %+v", p)
			}
			if _, e = s.CreateMediaRequest(uid, tvRequest(225634, SeasonScopeAll)); e != nil {
				t.Fatal(e)
			}
			if quotaUsed(t, s, uid, "tv", "") != 1 {
				t.Fatal("expansion usage")
			}
			assertOnlySeasons(t, l, 2)
			if quotaRows(t, s, "request_quota_charges") != 1+want {
				t.Fatal("pilot expansion charge count")
			}
		})
	}
}

func TestQuotaBooksAtomicSubscribersOwnershipAndSelectiveRefund(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer upstream.Close()
	s, uid := newChaptarrBookTestService(t, upstream.URL)
	admin := createTestAdmin(t, s)
	quotaLimit(t, s, admin, 0, "book", "ebook", 1)
	quotaLimit(t, s, admin, 0, "book", "audiobook", 0)
	req := func(format string) *CreateRequest {
		return &CreateRequest{MediaType: "book", ForeignID: "shared", Title: "Shared", BookFormat: format}
	}
	_, e := s.CreateMediaRequest(uid, req("both"))
	quotaMustExceed(t, e)
	if quotaRows(t, s, "request_dispatch") != 0 || quotaRows(t, s, "book_request_waiters") != 0 {
		t.Fatal("both partially saved")
	}
	quotaLimit(t, s, admin, 0, "book", "audiobook", 1)
	first, e := s.CreateMediaRequest(uid, req("both"))
	if e != nil {
		t.Fatal(e)
	}
	res, e := s.db.Exec(`INSERT INTO users(username,password_hash,role) VALUES('subscriber','','user')`)
	if e != nil {
		t.Fatal(e)
	}
	subscriber, _ := res.LastInsertId()
	s.db.Exec(`INSERT INTO user_instance_grants(user_id,instance_id) VALUES(?,?)`, subscriber, first.InstanceID)
	quotaLimit(t, s, admin, subscriber, "book", "audiobook", 0)
	_, e = s.CreateMediaRequest(subscriber, req("both"))
	quotaMustExceed(t, e)
	if quotaUsed(t, s, subscriber, "book", "ebook") != 0 || quotaRows(t, s, "book_request_waiters") != 1 {
		t.Fatal("subscription rejection was not atomic")
	}
	if _, e = s.CreateMediaRequest(subscriber, req("ebook")); e != nil {
		t.Fatal(e)
	}
	if quotaUsed(t, s, subscriber, "book", "ebook") != 1 {
		t.Fatal("subscriber was not charged")
	}
	// Only the ebook write has become consequential; reads/retries of audio have not.
	token, _, ok := s.claimDelivery(first.RequestID, "ebook")
	if !ok {
		t.Fatal("claim")
	}
	if e = s.startDelivery(context.Background(), first.RequestID, "ebook", token); e != nil {
		t.Fatal(e)
	}
	s.finishDelivery(first.RequestID, "ebook", token, "attention", "library_unavailable", nil)
	if _, e = s.DeliveryAction(context.Background(), uid, first.RequestID, "cancel", ""); e != nil {
		t.Fatal(e)
	}
	if quotaUsed(t, s, uid, "book", "ebook") != 1 || quotaUsed(t, s, uid, "book", "audiobook") != 0 {
		t.Fatal("selective refund ignored delivery boundary")
	}
	var owner int64
	s.db.QueryRow(`SELECT user_id FROM request_log WHERE id=?`, first.RequestID).Scan(&owner)
	if owner != subscriber {
		t.Fatal("ownership was not transferred")
	}
	if _, e = s.CreateMediaRequest(subscriber, req("ebook")); e != nil {
		t.Fatal(e)
	}
	if quotaUsed(t, s, subscriber, "book", "ebook") != 1 {
		t.Fatal("transfer changed the subscriber's charge")
	}
	if _, e = s.DeliveryAction(context.Background(), subscriber, first.RequestID, "cancel", ""); e != nil {
		t.Fatal(e)
	}
	if quotaUsed(t, s, subscriber, "book", "ebook") != 1 {
		t.Fatal("subscriber refunded a started delivery")
	}
}

func TestQuotaCatalogNoOpRefundAndExhaustedAvailableWork(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != "GET" {
			t.Errorf("no-op wrote %s", r.URL.Path)
		}
		switch r.URL.Path {
		case "/api/v1/book":
			fmt.Fprint(w, `[{"id":7,"foreignBookId":"owned","title":"Owned","mediaType":"ebook","monitored":true,"statistics":{"bookFileCount":1}}]`)
		case "/api/v1/queue":
			fmt.Fprint(w, `{"records":[],"totalRecords":0}`)
		default:
			fmt.Fprint(w, `[]`)
		}
	}))
	defer upstream.Close()
	s, uid := newChaptarrBookTestService(t, upstream.URL)
	admin := createTestAdmin(t, s)
	quotaLimit(t, s, admin, 0, "book", "ebook", 1)
	request := func() *CreateRequest {
		return &CreateRequest{MediaType: "book", ForeignID: "owned", Title: "Owned", BookFormat: "ebook"}
	}
	out, e := s.CreateMediaRequest(uid, request())
	if e != nil {
		t.Fatal(e)
	}
	if quotaUsed(t, s, uid, "book", "ebook") != 1 {
		t.Fatal("unresolved intake did not reserve")
	}
	restarted := NewService(s.db, s.registry, nil, nil)
	restarted.SweepDispatch(context.Background())
	if quotaUsed(t, s, uid, "book", "ebook") != 0 {
		t.Fatal("read-only verified no-op not refunded")
	}
	var started int64
	s.db.QueryRow(`SELECT delivery_started_at FROM request_dispatch WHERE request_id=?`, out.RequestID).Scan(&started)
	if started != 0 {
		t.Fatal("read-only lookup started delivery")
	}
	quotaLimit(t, s, admin, 0, "book", "ebook", 0)
	if _, e = s.CreateMediaRequest(uid, request()); e != nil {
		t.Fatalf("available work required allowance: %v", e)
	}
	if quotaUsed(t, s, uid, "book", "ebook") != 0 {
		t.Fatal("available work charged")
	}
}

func TestQuotaMovieLostResponseDurableRecoveryAndRetry(t *testing.T) {
	var s *Service
	var uid int64
	var writes atomic.Int32
	var added atomic.Bool
	var available atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "GET" && r.URL.Path == "/api/v3/movie":
			if available.Load() {
				fmt.Fprint(w, `[{"id":7,"tmdbId":550,"title":"Movie","monitored":true,"hasFile":true}]`)
			} else if added.Load() {
				fmt.Fprint(w, `[{"id":7,"tmdbId":550,"title":"Movie","monitored":true}]`)
			} else {
				fmt.Fprint(w, `[]`)
			}
		case r.URL.Path == "/api/v3/queue":
			fmt.Fprint(w, `{"records":[],"totalRecords":0}`)
		case r.URL.Path == "/api/v3/movie/lookup":
			fmt.Fprint(w, `[{"tmdbId":550,"title":"Movie"}]`)
		case r.URL.Path == "/api/v3/qualityprofile":
			fmt.Fprint(w, `[{"id":1,"name":"Any"}]`)
		case r.URL.Path == "/api/v3/rootfolder":
			fmt.Fprint(w, `[{"id":1,"path":"/movies"}]`)
		case r.Method == "POST" && r.URL.Path == "/api/v3/movie":
			writes.Add(1)
			var saved, started int
			if e := s.db.QueryRow(`SELECT COUNT(*),COALESCE(MAX(delivery_started_at),0) FROM request_dispatch`).Scan(&saved, &started); e != nil || saved != 1 || started == 0 || quotaUsed(t, s, uid, "movie", "") != 1 {
				t.Errorf("write preceded durable charge/marker: %d %d %v", saved, started, e)
			}
			added.Store(true)
			w.WriteHeader(502) // Radarr committed, but its response was lost.
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer upstream.Close()
	s, uid = newHistoryTestService(t, upstream.URL, "", "")
	admin := createTestAdmin(t, s)
	quotaLimit(t, s, admin, 0, "movie", "", 1)
	out, e := s.CreateMediaRequest(uid, &CreateRequest{MediaType: "movie", TmdbID: 550, Title: "Movie"})
	if e != nil {
		t.Fatal(e)
	}
	if len(out.Delivery) != 1 || out.Delivery[0].State != "retry" || out.Status != StatusRequested {
		t.Fatalf("lost response: %+v", out)
	}
	st, e := s.GetUserStatus(uid, 550, "movie", out.InstanceID)
	if e != nil || st.Status != StatusRequested || len(st.Delivery) != 1 {
		t.Fatalf("saved movie status: %+v %v", st, e)
	}
	available.Store(true)
	st, e = s.GetUserStatus(uid, 550, "movie", out.InstanceID)
	if e != nil || st.Status != StatusAvailable || len(st.Delivery) != 1 {
		t.Fatalf("saved delivery hid playback availability: %+v %v", st, e)
	}
	var marker int64
	s.db.QueryRow(`SELECT delivery_started_at FROM request_dispatch WHERE request_id=?`, out.RequestID).Scan(&marker)
	if _, e = s.DeliveryAction(context.Background(), uid, out.RequestID, "retry", ""); e != nil {
		t.Fatal(e)
	}
	restarted := NewService(s.db, s.registry, nil, nil)
	restarted.SweepDispatch(context.Background())
	states, e := restarted.deliveryStates(out.RequestID)
	if e != nil || states[0].State != "complete" || writes.Load() != 1 {
		t.Fatalf("recovery: %+v writes=%d %v", states, writes.Load(), e)
	}
	var after int64
	s.db.QueryRow(`SELECT delivery_started_at FROM request_dispatch WHERE request_id=?`, out.RequestID).Scan(&after)
	if after != marker || quotaUsed(t, s, uid, "movie", "") != 1 || quotaRows(t, s, "request_quota_charges") != 1 {
		t.Fatal("retry cleared marker or changed charge")
	}
}

func TestQuotaPilotPendingDuplicateAndApprovalExpansionAfterWindow(t *testing.T) {
	s, uid, admin, l := newCorrectionLab(t)
	l.delayRefresh = true
	quotaLimit(t, s, admin, 0, "tv", "", 1)
	first, e := s.CreateMediaRequest(uid, tvRequest(225634, SeasonScopePilot))
	if e != nil {
		t.Fatal(e)
	}
	again, e := s.CreateMediaRequest(uid, tvRequest(225634, SeasonScopePilot))
	if e != nil {
		t.Fatal(e)
	}
	if again.RequestID != first.RequestID || quotaRows(t, s, "request_dispatch") != 1 || quotaUsed(t, s, uid, "tv", "") != 1 {
		t.Fatal("metadata refresh defeated duplicate detection")
	}
	s2, uid2, admin2, _ := newCorrectionLab(t)
	requireApproval(t, s2)
	quotaLimit(t, s2, admin2, 0, "tv", "", 1)
	now := time.Now()
	s2.Quotas.Now = func() time.Time { return now }
	pending, e := s2.CreateMediaRequest(uid2, tvRequest(225634, SeasonScopePilot))
	if e != nil {
		t.Fatal(e)
	}
	now = now.Add(7 * 24 * time.Hour)
	quotaLimit(t, s2, admin2, 0, "tv", "", 0)
	_, e = s2.ApproveRequest(admin2, pending.RequestID, &DecisionOverride{SeasonScope: SeasonScopeAll})
	quotaMustExceed(t, e)
	target, _, _, e := s2.loadTVTarget(pending.RequestID)
	if e != nil || !target.Pilot {
		t.Fatal("refused expansion changed pilot")
	}
	if _, e = s2.ApproveRequest(admin2, pending.RequestID, nil); e != nil {
		t.Fatalf("unchanged aged approval was charged: %v", e)
	}
	if quotaRows(t, s2, "request_quota_charges") != 1 {
		t.Fatal("unchanged approval charged again")
	}
}
