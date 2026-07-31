package push

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeHealthSink records every transition the Notifier reports.
type fakeHealthSink struct {
	mu      sync.Mutex
	healthy []bool
	details []string
}

func (f *fakeHealthSink) RecordPushDeliveryHealth(healthy bool, detail string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.healthy = append(f.healthy, healthy)
	f.details = append(f.details, detail)
	return nil
}

func (f *fakeHealthSink) transitions() []bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]bool(nil), f.healthy...)
}

func (f *fakeHealthSink) lastDetail() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.details) == 0 {
		return ""
	}
	return f.details[len(f.details)-1]
}

// newFailingGateway stands up a gateway that rejects every send, so the send
// path fails the way an unreachable or broken gateway does.
func newFailingGateway(t *testing.T, database *sql.DB) *Manager {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	t.Cleanup(srv.Close)
	mgr := NewManager(database, nil, srv.URL, "pgk_test", "", "Cantinarr", nil)
	if mgr.Ensure(context.Background()) == nil {
		t.Fatal("Ensure returned nil client for an explicit-key manager")
	}
	return mgr
}

// waitFor polls until cond holds, so a test never depends on the timing of the
// fire-and-forget send goroutine.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// TestDeliveryFailuresRaiseHealthOnlyAfterARun is the whole point of the
// threshold: a single failed send is a blip, and the notification it lost is
// already gone whatever we do. Two in a row means the gateway, which is the
// thing an admin can act on.
func TestDeliveryFailuresRaiseHealthOnlyAfterARun(t *testing.T) {
	database, err := dbOpen(t)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	sink := &fakeHealthSink{}
	n := NewNotifier(database, newFailingGateway(t, database), nil)
	n.SetDeliveryHealthSink(sink)

	n.send(n.client(), []int64{1}, "first", "body", nil)
	waitFor(t, "the first send to fail", func() bool {
		n.healthMu.Lock()
		defer n.healthMu.Unlock()
		return n.consecutiveFails == 1
	})
	if got := sink.transitions(); len(got) != 0 {
		t.Fatalf("transitions after one failure = %v, want none", got)
	}

	n.send(n.client(), []int64{1}, "second", "body", nil)
	waitFor(t, "the health sink to be told", func() bool {
		return len(sink.transitions()) > 0
	})
	if got := sink.transitions(); got[0] {
		t.Errorf("first transition = healthy, want a failure")
	}
	// The detail has to carry how many alerts the outage swallowed; that count
	// is what an admin needs when they come back to it.
	if detail := sink.lastDetail(); !strings.Contains(detail, "2 push notifications") {
		t.Errorf("detail = %q, want the run length in it", detail)
	}
}

// TestDeliverySuccessAlwaysReportsHealth pins the restart-safety choice: the
// counter is in-memory but the issue is durable, so a success reports even
// when this process never saw the failures that opened it.
func TestDeliverySuccessAlwaysReportsHealth(t *testing.T) {
	database, err := dbOpen(t)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	sink := &fakeHealthSink{}
	mgr, _ := newNotifierTestGateway(t, database)
	n := NewNotifier(database, mgr, nil)
	n.SetDeliveryHealthSink(sink)

	n.send(n.client(), []int64{1}, "hello", "body", nil)
	waitFor(t, "the health sink to be told", func() bool {
		return len(sink.transitions()) > 0
	})
	if got := sink.transitions(); !got[0] {
		t.Errorf("transition = failure, want healthy")
	}
}

// TestDeliveryRunResetsOnSuccess keeps an outage from being reported off the
// back of failures that were never consecutive.
func TestDeliveryRunResetsOnSuccess(t *testing.T) {
	database, err := dbOpen(t)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	n := NewNotifier(database, nil, nil)
	sink := &fakeHealthSink{}
	n.SetDeliveryHealthSink(sink)

	n.recordDeliveryFailure(errFake)
	n.recordDeliverySuccess()
	n.recordDeliveryFailure(errFake)

	for _, healthy := range sink.transitions() {
		if !healthy {
			t.Fatal("two failures either side of a success were reported as an outage")
		}
	}
}

var errFake = &fakeError{}

type fakeError struct{}

func (*fakeError) Error() string { return "gateway unreachable" }
