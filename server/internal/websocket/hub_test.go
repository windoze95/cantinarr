package websocket

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/windoze95/cantinarr-server/internal/arr"
	"github.com/windoze95/cantinarr-server/internal/radarr"
	"github.com/windoze95/cantinarr-server/internal/sonarr"
)

// fakeOpener records complete queue snapshots so tests can pin the hub's
// transport-only contract without involving remediation policy.
type fakeOpener struct {
	mu        sync.Mutex
	snapshots []snapshotCall
}

type snapshotCall struct {
	serviceType string
	instanceID  string
	items       []arr.QueueObservation
}

func (f *fakeOpener) ObserveQueueSnapshot(serviceType, instanceID string, items []arr.QueueObservation) {
	f.mu.Lock()
	defer f.mu.Unlock()
	copyItems := append([]arr.QueueObservation(nil), items...)
	if items != nil && copyItems == nil {
		copyItems = make([]arr.QueueObservation, 0)
	}
	f.snapshots = append(f.snapshots, snapshotCall{serviceType, instanceID, copyItems})
}

func (f *fakeOpener) calls() []snapshotCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]snapshotCall(nil), f.snapshots...)
}

func newTestHub(opener IssueOpener) *Hub {
	return NewHub(nil, nil, nil, nil, opener)
}

func stuckItem(downloadID string) arr.QueueObservation {
	return arr.QueueObservation{
		DownloadID: downloadID,
		Signal: arr.QueueSignal{
			Status:                "warning",
			TrackedDownloadStatus: "error",
			ErrorMessage:          "The download is stalled with no connections",
		},
	}
}

// TestDispatchDetailedItemsForwardsFullSnapshot proves the hub diagnoses every
// item and sends exactly one unfiltered snapshot. Healthy entries and entries
// without a download id are observations too; the remediation layer decides
// whether and when any of them warrant an issue.
func TestDispatchDetailedItemsForwardsFullSnapshot(t *testing.T) {
	opener := &fakeOpener{}
	h := newTestHub(opener)

	items := []arr.QueueObservation{
		stuckItem("stalled"),
		{
			DownloadID: "healthy",
			Signal: arr.QueueSignal{
				Status:                "downloading",
				TrackedDownloadStatus: "ok",
				Size:                  1000,
				SizeLeft:              400,
			},
		},
		stuckItem(""),
	}
	h.dispatchDetailedItems("sonarr", "sonarr-1", items)

	calls := opener.calls()
	if len(calls) != 1 {
		t.Fatalf("snapshot calls = %d, want exactly 1", len(calls))
	}
	got := calls[0]
	if got.serviceType != "sonarr" || got.instanceID != "sonarr-1" {
		t.Fatalf("snapshot scope = %s/%s, want sonarr/sonarr-1", got.serviceType, got.instanceID)
	}
	if len(got.items) != 3 {
		t.Fatalf("snapshot items = %d, want all 3", len(got.items))
	}
	if got.items[0].Diagnosis.Severity != arr.SeverityError {
		t.Fatalf("stalled diagnosis = %+v, want error", got.items[0].Diagnosis)
	}
	if got.items[1].Diagnosis.Severity != arr.SeverityOK {
		t.Fatalf("healthy diagnosis = %+v, want ok", got.items[1].Diagnosis)
	}
	if got.items[1].Signal.Size != 1000 || got.items[1].Signal.SizeLeft != 400 {
		t.Fatalf("healthy progress signal = %+v, want size 1000/left 400", got.items[1].Signal)
	}
	if got.items[2].DownloadID != "" || got.items[2].Diagnosis.Severity != arr.SeverityError {
		t.Fatalf("no-id observation was filtered or undiagnosed: %+v", got.items[2])
	}

	// dispatchDetailedItems works on a copy rather than mutating its caller's
	// raw observations as a side effect.
	for i, item := range items {
		if item.Diagnosis.Severity != "" || item.Diagnosis.Problem != "" ||
			item.Diagnosis.Transparency != "" || len(item.Diagnosis.SuggestedActions) != 0 {
			t.Fatalf("input item %d diagnosis mutated: %+v", i, item.Diagnosis)
		}
	}
}

// TestDispatchDetailedItemsForwardsEmptySnapshot pins the distinction between
// a successful empty read (one empty observation) and a failed read (none).
func TestDispatchDetailedItemsForwardsEmptySnapshot(t *testing.T) {
	opener := &fakeOpener{}
	h := newTestHub(opener)

	h.dispatchDetailedItems("radarr", "radarr-1", nil)
	calls := opener.calls()
	if len(calls) != 1 {
		t.Fatalf("snapshot calls = %d, want 1", len(calls))
	}
	if calls[0].items == nil || len(calls[0].items) != 0 {
		t.Fatalf("empty successful snapshot = %#v, want non-nil empty slice", calls[0].items)
	}
}

func TestDispatchDetailedItemsNilObserverNoPanic(t *testing.T) {
	h := newTestHub(nil)
	h.dispatchDetailedItems("radarr", "radarr-1", []arr.QueueObservation{stuckItem("download")})
}

// TestAutoDispatchFailedReadDeliversNothing verifies that a failed detailed
// queue request cannot be mistaken for a successful empty queue and therefore
// cannot trigger lifecycle decisions in the observer.
func TestAutoDispatchFailedReadDeliversNothing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "upstream unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	opener := &fakeOpener{}
	h := newTestHub(opener)
	h.autoDispatchRadarr("radarr-1", radarr.NewClient(server.URL, "test-key"))

	if calls := opener.calls(); len(calls) != 0 {
		t.Fatalf("failed queue read delivered %d snapshot(s), want none", len(calls))
	}
}

// TestAutoDispatchSuccessfulEmptyReadDeliversSnapshot complements the failure
// test with the real detailed-client path: an empty queue is still a complete,
// authoritative observation.
func TestAutoDispatchSuccessfulEmptyReadDeliversSnapshot(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"totalRecords":0,"records":[]}`))
	}))
	defer server.Close()

	opener := &fakeOpener{}
	h := newTestHub(opener)
	h.autoDispatchSonarr("sonarr-1", sonarr.NewClient(server.URL, "test-key"))

	calls := opener.calls()
	if len(calls) != 1 {
		t.Fatalf("successful empty queue delivered %d snapshot(s), want 1", len(calls))
	}
	if calls[0].serviceType != "sonarr" || calls[0].instanceID != "sonarr-1" || len(calls[0].items) != 0 {
		t.Fatalf("empty queue snapshot = %+v", calls[0])
	}
}

// TestQueueSignalMappers confirms the real Radarr/Sonarr detailed queue types
// preserve stable media identity, classification input, and byte progress.
func TestQueueSignalMappers(t *testing.T) {
	added := time.Date(2026, 7, 12, 15, 0, 0, 0, time.UTC)
	noFile, hasFile := 0, 12
	trueValue := true
	r := radarrQueueSignal(radarr.DetailedQueueItem{
		ID:                    41,
		MovieID:               7,
		DownloadID:            "rdl",
		Status:                "completed",
		TrackedDownloadState:  "importPending",
		TrackedDownloadStatus: "warning",
		Protocol:              "usenet",
		Added:                 &added,
		Size:                  800,
		Sizeleft:              120,
		Movie:                 &radarr.MovieContext{ID: 7, Title: "Movie", TmdbID: 101, MovieFileID: &noFile},
	})
	if r.DownloadID != "rdl" || r.Signal.TrackedDownloadState != "importPending" {
		t.Fatalf("radarr mapping = %+v, want downloadID=rdl state=importPending", r)
	}
	if r.AddedAt == nil || !r.AddedAt.Equal(added) {
		t.Fatalf("radarr attempt boundary = %v, want %v", r.AddedAt, added)
	}
	if r.FileIDAtSnapshot == nil || *r.FileIDAtSnapshot != 0 {
		t.Fatalf("radarr queue file ID = %v, want known absent", r.FileIDAtSnapshot)
	}
	if r.Media.QueueID != 41 || r.Media.TmdbID != 101 || r.Media.Title != "Movie" {
		t.Fatalf("radarr media context = %+v", r.Media)
	}
	if r.Signal.Size != 800 || r.Signal.SizeLeft != 120 {
		t.Fatalf("radarr progress = size %.0f left %.0f", r.Signal.Size, r.Signal.SizeLeft)
	}
	if d := arr.Diagnose(r.Signal); d.Severity != arr.SeverityWarning {
		t.Fatalf("radarr importPending severity = %q, want warning", d.Severity)
	}

	s := sonarrQueueSignal(sonarr.DetailedQueueItem{
		ID:                    42,
		SeriesID:              6,
		EpisodeID:             8,
		DownloadID:            "sdl",
		Added:                 &added,
		EpisodeHasFile:        &trueValue,
		TrackedDownloadStatus: "error",
		ErrorMessage:          "The download is stalled with no connections",
		Size:                  900,
		Sizeleft:              300,
		Series:                &sonarr.SeriesContext{ID: 6, Title: "Show", TvdbID: 202, TmdbID: 303},
		Episode:               &sonarr.EpisodeContext{ID: 8, SeriesID: 6, SeasonNumber: 1, EpisodeNumber: 2, EpisodeFileID: &hasFile, HasFile: &trueValue},
	})
	if s.DownloadID != "sdl" {
		t.Fatalf("sonarr mapping downloadID = %q, want sdl", s.DownloadID)
	}
	if s.AddedAt == nil || !s.AddedAt.Equal(added) {
		t.Fatalf("sonarr attempt boundary = %v, want %v", s.AddedAt, added)
	}
	if s.FileIDAtSnapshot == nil || *s.FileIDAtSnapshot != int64(hasFile) {
		t.Fatalf("sonarr queue file ID = %v, want %d", s.FileIDAtSnapshot, hasFile)
	}
	if s.Media.QueueID != 42 || s.Media.TmdbID != 303 || s.Media.TvdbID != 202 ||
		s.Media.SeasonNumber != 1 || s.Media.EpisodeNumber != 2 || s.Media.Title != "Show" {
		t.Fatalf("sonarr media context = %+v", s.Media)
	}
	if s.Signal.Size != 900 || s.Signal.SizeLeft != 300 {
		t.Fatalf("sonarr progress = size %.0f left %.0f", s.Signal.Size, s.Signal.SizeLeft)
	}
	if d := arr.Diagnose(s.Signal); d.Severity != arr.SeverityError {
		t.Fatalf("sonarr stalled severity = %q, want error", d.Severity)
	}
}
