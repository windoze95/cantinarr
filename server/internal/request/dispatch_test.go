package request

import (
	"context"
	"fmt"
	"github.com/windoze95/cantinarr-server/internal/instance"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestDeliverySavedBeforeAttemptAndRecoveredAfterRestart(t *testing.T) {
	var service *Service
	var unavailable atomic.Bool
	unavailable.Store(true)
	var reads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads.Add(1)
		var count int
		if err := service.db.QueryRow(`SELECT COUNT(*) FROM request_dispatch`).Scan(&count); err != nil || count != 1 {
			t.Errorf("network preceded durable job: %d %v", count, err)
		}
		if unavailable.Load() {
			w.Header().Set("Retry-After", "600")
			w.WriteHeader(503)
			return
		}
		if r.URL.Path == "/api/v1/book" {
			fmt.Fprint(w, `[{"id":7,"foreignBookId":"saved","title":"Saved","mediaType":"ebook","monitored":true,"statistics":{"bookFileCount":1}}]`)
			return
		}
		if r.URL.Path == "/api/v1/queue" {
			fmt.Fprint(w, `{"records":[],"totalRecords":0}`)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	var userID int64
	service, userID = newChaptarrBookTestService(t, server.URL)
	out, err := service.createAndDispatchForTest(userID, &CreateRequest{MediaType: "book", ForeignID: "saved", Title: "Saved", BookFormat: "ebook"})
	if err != nil || len(out.Delivery) != 1 || out.Delivery[0].State != "retry" {
		t.Fatalf("save: %+v %v", out, err)
	}
	job := out.Delivery[0]
	if job.NextAttemptAt == nil || time.Until(*job.NextAttemptAt) < 590*time.Second {
		t.Fatalf("Retry-After ignored: %+v", job)
	}
	if n, e := service.PendingCount(); e != nil || n != 0 {
		t.Fatalf("retry entered approvals: %d %v", n, e)
	}
	before := reads.Load()
	restarted := NewService(service.db, service.registry, nil, nil)
	restarted.SweepDispatch(context.Background())
	if reads.Load() != before {
		t.Fatal("restart ignored scheduled retry time")
	}
	unavailable.Store(false)
	service.db.Exec(`UPDATE request_dispatch SET next_attempt_at=0`)
	restarted.SweepDispatch(context.Background())
	states, e := restarted.deliveryStates(out.RequestID)
	if e != nil || states[0].State != "complete" || states[0].Attempts != 2 {
		t.Fatalf("recovery: %+v %v", states, e)
	}
}

func TestApprovalAndCancellationSurviveWorkers(t *testing.T) {
	var mutations atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			mutations.Add(1)
		}
		w.WriteHeader(503)
	}))
	defer server.Close()
	s, userID := newChaptarrBookTestService(t, server.URL)
	requireApproval(t, s)
	out, err := s.CreateMediaRequest(userID, &CreateRequest{MediaType: "book", ForeignID: "approved", Title: "Approval", BookFormat: "ebook"})
	if err != nil || out.Status != StatusPending {
		t.Fatalf("intake: %+v %v", out, err)
	}
	s.SweepDispatch(context.Background())
	states, _ := s.deliveryStates(out.RequestID)
	if states[0].Attempts != 0 {
		t.Fatal("worker bypassed approval")
	}
	if _, err = s.DeliveryAction(context.Background(), userID, out.RequestID, "retry", ""); err != nil {
		t.Fatal(err)
	}
	s.SweepDispatch(context.Background())
	states, _ = s.deliveryStates(out.RequestID)
	if states[0].Attempts != 0 {
		t.Fatal("manual retry bypassed approval")
	}
	if _, err = s.DeliveryAction(context.Background(), userID, out.RequestID, "cancel", ""); err != nil {
		t.Fatal(err)
	}
	s.SweepDispatch(context.Background())
	states, _ = s.deliveryStates(out.RequestID)
	if states[0].State != "cancelled" || mutations.Load() != 0 {
		t.Fatalf("cancelled request dispatched: %+v", states)
	}
	if _, err = s.ApproveRequest(createTestAdmin(t, s), out.RequestID, nil); err == nil {
		t.Fatal("cancelled request approved")
	}
}

func TestDispatchLeaseAndPartialProgress(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer server.Close()
	s, userID := newChaptarrBookTestService(t, server.URL)
	out, err := s.CreateMediaRequest(userID, &CreateRequest{MediaType: "book", Title: "Source", BookFormat: "both", ForeignID: "native-choice"})
	if err != nil {
		t.Fatal(err)
	}
	// A first format may already have completed before the process crashed.
	s.db.Exec(`UPDATE request_dispatch SET state='complete',code='requested' WHERE request_id=? AND format='ebook'`, out.RequestID)
	other := NewService(s.db, s.registry, nil, nil)
	var wins atomic.Int32
	var wg sync.WaitGroup
	for _, service := range []*Service{s, other} {
		wg.Add(1)
		go func(svc *Service) {
			defer wg.Done()
			if _, _, ok := svc.claimDelivery(out.RequestID, "audiobook"); ok {
				wins.Add(1)
			}
		}(service)
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("concurrent workers acquired %d leases", wins.Load())
	}
	if _, _, ok := s.claimDelivery(out.RequestID, "ebook"); ok {
		t.Fatal("completed format was replayed")
	}
	s.db.Exec(`UPDATE request_dispatch SET lease_until=0 WHERE request_id=?`, out.RequestID)
	s.db.Exec(`UPDATE request_dispatch_locks SET lease_until=0`)
	if _, _, ok := other.claimDelivery(out.RequestID, "audiobook"); !ok {
		t.Fatal("crashed worker lease could not be reclaimed")
	}
}

func TestNativeDedupAndRevokedAccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("intake or revoked worker reached the service")
		w.WriteHeader(503)
	}))
	defer server.Close()
	s, userID := newChaptarrBookTestService(t, server.URL)
	makeRequest := func() *CreateRequest {
		return &CreateRequest{MediaType: "book", Title: "Source", BookFormat: "ebook", ForeignID: "native-choice"}
	}
	first, err := s.CreateMediaRequest(userID, makeRequest())
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.CreateMediaRequest(userID, makeRequest())
	if err != nil || second.RequestID != first.RequestID {
		t.Fatalf("source binding broke dedup: %+v %v", second, err)
	}
	s.db.Exec(`DELETE FROM user_default_instances WHERE user_id=?`, userID)
	s.SweepDispatch(context.Background())
	states, _ := s.deliveryStates(first.RequestID)
	if states[0].State != "attention" || states[0].Code != "access_unavailable" {
		t.Fatalf("revoked request: %+v", states)
	}
	if _, err = s.DeliveryAction(context.Background(), userID, first.RequestID, "cancel", ""); err != nil {
		t.Fatalf("owner cannot cancel after revocation: %v", err)
	}
}

func addDeliverySubscriber(t *testing.T, s *Service, instanceID string) int64 {
	t.Helper()
	res, err := s.db.Exec(`INSERT INTO users(username,password_hash,role) VALUES('subscriber','','user')`)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	if _, err = s.db.Exec(`INSERT INTO user_default_instances(user_id,service_type,instance_id) VALUES(?,'chaptarr',?)`, id, instanceID); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestCancellationPreservesOtherSubscribersAndTheirFormats(t *testing.T) {
	for _, ownerCancels := range []bool{true, false} {
		t.Run(fmt.Sprint(ownerCancels), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Error("cancellation contacted service")
				w.WriteHeader(503)
			}))
			defer server.Close()
			s, owner := newChaptarrBookTestService(t, server.URL)
			var ref *CatalogRef
			out, err := s.CreateMediaRequest(owner, &CreateRequest{MediaType: "book", Title: "Shared", BookFormat: "both", ForeignID: "native-choice"})
			if err != nil {
				t.Fatal(err)
			}
			subscriber := addDeliverySubscriber(t, s, out.InstanceID)
			other, err := s.CreateMediaRequest(subscriber, &CreateRequest{MediaType: "book", Title: "Shared", BookFormat: "audiobook", ForeignID: "native-choice"})
			if err != nil || other.RequestID != out.RequestID {
				t.Fatalf("subscription: %+v %v", other, err)
			}
			if len(other.Delivery) != 1 || other.Delivery[0].Format != "audiobook" || other.Delivery[0].CanManage || !other.Delivery[0].CanCancel {
				t.Fatalf("subscriber scope: %+v", other)
			}
			cancelUser, remaining := subscriber, owner
			if ownerCancels {
				cancelUser, remaining = owner, subscriber
			}
			cancelled, err := s.DeliveryAction(context.Background(), cancelUser, out.RequestID, "cancel", "")
			if err != nil {
				t.Fatal(err)
			}
			if cancelled.RequestID == out.RequestID {
				t.Fatal("shared request cancelled globally")
			}
			var currentOwner int64
			if err = s.db.QueryRow(`SELECT user_id FROM request_log WHERE id=?`, out.RequestID).Scan(&currentOwner); err != nil || currentOwner != remaining {
				t.Fatalf("owner transfer: %d %v", currentOwner, err)
			}
			status, err := s.DeliveryStatus(remaining, "book", "native-choice", out.InstanceID, ref, false)
			if err != nil {
				t.Fatal(err)
			}
			if status.Delivery[len(status.Delivery)-1].State != "queued" {
				t.Fatalf("remaining request lost: %+v", status)
			}
			if ownerCancels && len(status.Delivery) != 1 {
				t.Fatalf("new owner inherited unwanted format: %+v", status)
			}
		})
	}
}

func TestOldCancellationDoesNotReplaceNewRequest(t *testing.T) {
	s, uid := newChaptarrBookTestService(t, "http://unused")
	var ref *CatalogRef
	create := func() *CreateResponse {
		out, err := s.CreateMediaRequest(uid, &CreateRequest{MediaType: "book", Title: "Again", BookFormat: "ebook", ForeignID: "native-choice"})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	first := create()
	if _, err := s.DeliveryAction(context.Background(), uid, first.RequestID, "cancel", ""); err != nil {
		t.Fatal(err)
	}
	second := create()
	out, err := s.DeliveryStatus(uid, "book", "native-choice", second.InstanceID, ref, false)
	if err != nil || len(out.Delivery) != 1 || out.Delivery[0].State != "queued" || out.RequestID != second.RequestID {
		t.Fatalf("old history masked new intent: %+v %v", out, err)
	}
}

func TestPendingDeliveryDoesNotHideLiveLibraryChanges(t *testing.T) {
	var live atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !live.Load() {
			w.WriteHeader(503)
			return
		}
		if r.URL.Path == "/api/v1/book" {
			fmt.Fprint(w, `[{"id":7,"foreignBookId":"external","title":"External","mediaType":"ebook","statistics":{"bookFileCount":1}}]`)
		} else {
			fmt.Fprint(w, `{"records":[],"totalRecords":0}`)
		}
	}))
	defer server.Close()
	s, uid := newChaptarrBookTestService(t, server.URL)
	out, err := s.CreateMediaRequest(uid, &CreateRequest{MediaType: "book", ForeignID: "external", Title: "External", BookFormat: "ebook"})
	if err != nil {
		t.Fatal(err)
	}
	unknown, err := s.GetUserBookStatusForInstance(uid, "external", out.InstanceID)
	if err != nil || unknown.StatusKnown == nil || *unknown.StatusKnown || len(unknown.BookFormatWaits) != 1 {
		t.Fatalf("outage became absence: %+v %v", unknown, err)
	}
	live.Store(true)
	s.InvalidateBookDigests(out.InstanceID)
	status, err := s.GetUserBookStatusForInstance(uid, "external", out.InstanceID)
	if err != nil || status.BookFormats["ebook"] != StatusAvailable || len(status.BookFormatWaits) != 0 {
		t.Fatalf("live import masked: %+v %v", status, err)
	}
	states, _ := s.deliveryStates(out.RequestID)
	if states[0].State != "queued" {
		t.Fatal("read mutated delivery")
	}
}

func TestRetryBackoffAndLimit(t *testing.T) {
	s, uid := newChaptarrBookTestService(t, "http://unused")
	out, err := s.CreateMediaRequest(uid, &CreateRequest{MediaType: "book", Title: "Retry", BookFormat: "ebook", ForeignID: "native-choice"})
	if err != nil {
		t.Fatal(err)
	}
	for _, attempt := range []int{1, 2, 20, 50} {
		if _, err = s.db.Exec(`UPDATE request_dispatch SET state='queued',attempts=?,next_attempt_at=0 WHERE request_id=?`, attempt-1, out.RequestID); err != nil {
			t.Fatal(err)
		}
		token, _, ok := s.claimDelivery(out.RequestID, "ebook")
		if !ok {
			t.Fatal("could not claim")
		}
		before := time.Now()
		s.finishDelivery(out.RequestID, "ebook", token, "retry", "catalog_unavailable", nil)
		after := time.Now()
		s.db.Exec(`DELETE FROM request_dispatch_locks`)
		states, _ := s.deliveryStates(out.RequestID)
		d := states[0]
		if attempt == 50 {
			if d.State != "attention" || d.Code != "retry_limit" {
				t.Fatalf("limit: %+v", d)
			}
			continue
		}
		want := time.Minute
		if attempt == 2 {
			want = 2 * time.Minute
		}
		if attempt == 20 {
			want = 6 * time.Hour
		}
		// Scheduling stores whole seconds. Bracket the scheduling call so
		// rounding and a slow database read cannot shorten the expected delay.
		if d.NextAttemptAt == nil || d.NextAttemptAt.Unix() < before.Add(want).Unix() || d.NextAttemptAt.Unix() > after.Add(want).Unix() {
			t.Fatalf("attempt %d delay: %+v", attempt, d)
		}
	}
}

func TestManualDeliveryRetryResumesFailedNativeImport(t *testing.T) {
	stub := newPendingImportAPIStub(t)
	s, uid := newChaptarrBookTestService(t, stub.server.URL)
	out, err := s.createAndDispatchForTest(uid, &CreateRequest{MediaType: "book", ForeignID: "gr:253739298", Title: "Waiting Book", BookFormat: "ebook"})
	if err != nil {
		t.Fatal(err)
	}
	states, _ := s.deliveryStates(out.RequestID)
	if states[0].State != "waiting_library" {
		t.Fatalf("initial: %+v", states)
	}
	stub.existsJSON.Store(`{"exists":false,"pending":true,"pendingId":3,"status":"Failed"}`)
	s.sweepDeliveryImports()
	states, _ = s.deliveryStates(out.RequestID)
	if states[0].State != "attention" {
		t.Fatalf("failed: %+v", states)
	}
	adds := stub.addAttempts.Load()
	if _, err = s.DeliveryAction(context.Background(), uid, out.RequestID, "retry", ""); err != nil {
		t.Fatal(err)
	}
	restarted := NewService(s.db, s.registry, nil, nil)
	restarted.SweepDispatch(context.Background())
	states, _ = restarted.deliveryStates(out.RequestID)
	if states[0].State != "waiting_library" || stub.retryHits.Load() != 1 || stub.addAttempts.Load() != adds {
		t.Fatalf("retry replaced native import: %+v retry=%d adds=%d", states, stub.retryHits.Load(), stub.addAttempts.Load())
	}
}

func TestLostAddResponseReconcilesNativeImportBeforeReplay(t *testing.T) {
	stub := newPendingImportAPIStub(t)
	s, uid := newChaptarrBookTestService(t, stub.server.URL)
	req := &CreateRequest{MediaType: "book", ForeignID: "gr:253739298", Title: "Waiting Book", BookFormat: "ebook"}
	ids, _, err := s.saveDelivery(uid, req, testInstanceID(t, s), req.ForeignID, "", "", []string{"ebook"}, false)
	if err != nil {
		t.Fatal(err)
	}
	// The service accepted an import but the caller lost its POST response.
	s.db.Exec(`UPDATE request_dispatch SET state='retry', attempts=1`)
	restarted := NewService(s.db, s.registry, nil, nil)
	restarted.SweepDispatch(context.Background())
	states, _ := restarted.deliveryStates(ids[0])
	if states[0].State != "waiting_library" || stub.addAttempts.Load() != 0 || stub.retryHits.Load() != 0 {
		t.Fatalf("accepted native work was replayed: %+v", states)
	}
	restarted.sweepDeliveryImports()
	next, _ := restarted.deliveryStates(ids[0])
	if next[0].Attempts != states[0].Attempts || next[0].State != "waiting_library" {
		t.Fatalf("native observation consumed delivery retries: %+v", next)
	}
}

func TestAdminCanInspectAndCancelDeliveryAfterInstanceRemoval(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("removed instance should never be contacted") }))
	defer server.Close()
	s, uid, instanceID := newLidarrMusicTestService(t, server.URL)
	adminID := createTestAdmin(t, s)
	out, err := s.CreateMediaRequest(uid, &CreateRequest{MediaType: "music", Title: "Saved album", CatalogRef: &CatalogRef{Provider: "musicbrainz", ID: "11111111-1111-4111-8111-111111111111"}})
	if err != nil {
		t.Fatal(err)
	}
	if err = instance.NewStore(s.db, nil).Delete(instanceID); err != nil {
		t.Fatal(err)
	}
	s.registry.InvalidateClient(instanceID)
	s.SweepDispatch(context.Background())
	detail, err := s.DeliveryByID(adminID, out.RequestID)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Delivery) != 1 || detail.Delivery[0].State != "attention" || !detail.Delivery[0].CanManage || !detail.Delivery[0].CanCancel || detail.StatusKnown == nil || *detail.StatusKnown {
		t.Fatalf("saved admin controls disappeared or claimed library truth: %+v", detail)
	}
	if _, err = s.DeliveryByID(uid, out.RequestID); err == nil {
		t.Fatal("requester read bypassed a removed grant")
	}
	if _, err = s.DeliveryAction(context.Background(), adminID, out.RequestID, "retry", ""); err == nil {
		t.Fatal("retry bypassed the missing instance")
	}
	cancelled, err := s.DeliveryAction(context.Background(), adminID, out.RequestID, "cancel", "")
	if err != nil || cancelled.Delivery[0].State != "cancelled" {
		t.Fatalf("admin could not cancel saved intent: %+v %v", cancelled, err)
	}
}
